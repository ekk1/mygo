package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// assetTransport resolves untrusted media hosts once and uses that validated IP
// for both direct dials and proxy CONNECT/SOCKS targets. Host, TLS server name,
// signed paths and the response URL remain the original provider result values.
type assetTransport struct {
	base        *http.Transport
	providerURL *url.URL
}

func (t *assetTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	u := r.URL
	sameOrigin := strings.EqualFold(u.Scheme, t.providerURL.Scheme) && strings.EqualFold(u.Host, t.providerURL.Host)
	if u.User != nil || u.Host == "" || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && sameOrigin)) {
		return nil, fmt.Errorf("unsupported generated media URL")
	}
	// The explicitly configured provider origin may intentionally be local.
	if sameOrigin {
		return t.base.RoundTrip(r)
	}
	ips, err := net.DefaultResolver.LookupIPAddr(r.Context(), u.Hostname())
	if err != nil || len(ips) == 0 {
		return nil, fmt.Errorf("generated media host could not be resolved")
	}
	for _, ip := range ips {
		if !ip.IP.IsGlobalUnicast() || ip.IP.IsPrivate() || ip.IP.IsLoopback() || ip.IP.IsLinkLocalUnicast() {
			return nil, fmt.Errorf("generated media host is not public")
		}
	}
	transport := t.base.Clone()
	// Each media request has a dedicated pinned destination and TLS server name.
	// Disabling keep-alives also closes its connection when the body is closed.
	transport.DisableKeepAlives = true
	defer transport.CloseIdleConnections()
	targetTLS := &tls.Config{MinVersion: tls.VersionTLS12}
	if transport.TLSClientConfig != nil {
		targetTLS = transport.TLSClientConfig.Clone()
	}
	targetTLS.ServerName = u.Hostname()
	transport.TLSClientConfig = targetTLS
	if transport.Proxy != nil {
		// Resolve environment/NO_PROXY against the original hostname, not its IP.
		proxyURL, proxyErr := transport.Proxy(r)
		if proxyErr != nil {
			return nil, proxyErr
		}
		transport.Proxy = nil
		if proxyURL != nil {
			proxyCopy := *proxyURL
			if proxyCopy.Scheme == "https" {
				// Transport shares TLSClientConfig between proxy and origin TLS.
				// Establish the proxy TLS layer here with the proxy's own name,
				// then let Transport tunnel and authenticate the media origin.
				proxyTLS := targetTLS.Clone()
				proxyTLS.ServerName = proxyCopy.Hostname()
				dial := transport.DialContext
				if dial == nil {
					dial = (&net.Dialer{}).DialContext
				}
				transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
					conn, dialErr := dial(ctx, network, address)
					if dialErr != nil {
						return nil, dialErr
					}
					secured := tls.Client(conn, proxyTLS)
					if timeout := transport.TLSHandshakeTimeout; timeout > 0 {
						var cancel context.CancelFunc
						ctx, cancel = context.WithTimeout(ctx, timeout)
						defer cancel()
					}
					if handshakeErr := secured.HandshakeContext(ctx); handshakeErr != nil {
						conn.Close()
						return nil, handshakeErr
					}
					return secured, nil
				}
				proxyCopy.Scheme = "http"
				// An omitted HTTPS proxy port still means 443 after changing
				// the internal scheme used for the already secured connection.
				if proxyCopy.Port() == "" {
					proxyCopy.Host = net.JoinHostPort(proxyCopy.Hostname(), "443")
				}
			}
			transport.Proxy = http.ProxyURL(&proxyCopy)
		}
	}
	pinned := r.Clone(r.Context())
	port := u.Port()
	if port == "" {
		port = "443"
	}
	pinned.URL.Host = net.JoinHostPort(ips[0].IP.String(), port)
	if pinned.Host == "" {
		pinned.Host = u.Host
	}
	response, err := transport.RoundTrip(pinned)
	if response != nil {
		response.Request = r
	}
	return response, err
}

func (t *assetTransport) CloseIdleConnections() { t.base.CloseIdleConnections() }
