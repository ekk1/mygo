package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A local DNS server returns a public address once and then a private one. A
// transport that validates DNS separately from the actual dial fails this test.
func assetTestDNS(t *testing.T) *atomic.Int32 {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	queries := &atomic.Int32{}
	go func() {
		defer close(done)
		buffer := make([]byte, 2048)
		for {
			n, addr, err := conn.ReadFrom(buffer)
			if err != nil {
				return
			}
			if n < 17 {
				continue
			}
			end := 12
			for end < n && buffer[end] != 0 {
				end += int(buffer[end]) + 1
			}
			end += 5
			if end > n {
				continue
			}
			kind := binary.BigEndian.Uint16(buffer[end-4 : end-2])
			response := append([]byte(nil), buffer[:end]...)
			binary.BigEndian.PutUint16(response[2:4], 0x8180)
			for i := 6; i < 12; i++ {
				response[i] = 0
			}
			if kind == 1 {
				ip := []byte{93, 184, 216, 34}
				if queries.Add(1) > 1 {
					ip = []byte{127, 0, 0, 1}
				}
				binary.BigEndian.PutUint16(response[6:8], 1)
				response = append(response, 0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 0, 0, 4)
				response = append(response, ip...)
			}
			_, _ = conn.WriteTo(response, addr)
		}
	}()
	original := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "udp", conn.LocalAddr().String())
	}}
	t.Cleanup(func() { net.DefaultResolver = original; conn.Close(); <-done })
	return queries
}

func assetTestTLS(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "asset transport test"}, DNSNames: []string{"media.test", "proxy.test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(parsed)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, roots
}

func TestAssetTransportPinsPublicDestinationAndPreservesSignedRequest(t *testing.T) {
	for _, proxyScheme := range []string{"", "http", "https"} {
		t.Run("proxy="+proxyScheme, func(t *testing.T) {
			queries := assetTestDNS(t)
			cert, roots := assetTestTLS(t)
			var received atomic.Int32
			upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				received.Add(1)
				if r.Host != "media.test" || r.TLS.ServerName != "media.test" || r.RequestURI != "/file%2Bname?sig=a%2Bb&expires=7" {
					t.Errorf("request changed: host=%q SNI=%q URI=%q", r.Host, r.TLS.ServerName, r.RequestURI)
				}
				io.WriteString(w, "saved media")
			}))
			upstream.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
			upstream.StartTLS()
			defer upstream.Close()
			base := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				if address != "93.184.216.34:443" {
					return nil, fmt.Errorf("unsafe/unpinned destination: %s", address)
				}
				return (&net.Dialer{}).DialContext(ctx, network, upstream.Listener.Addr().String())
			}}
			if proxyScheme != "" {
				proxy := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != "CONNECT" || r.Host != "93.184.216.34:443" {
						t.Errorf("unpinned CONNECT: %s %s", r.Method, r.Host)
						w.WriteHeader(400)
						return
					}
					if proxyScheme == "https" && r.TLS.ServerName != "proxy.test" {
						t.Errorf("proxy SNI=%q", r.TLS.ServerName)
					}
					remote, err := net.Dial("tcp", upstream.Listener.Addr().String())
					if err != nil {
						t.Error(err)
						w.WriteHeader(502)
						return
					}
					client, buffered, err := w.(http.Hijacker).Hijack()
					if err != nil {
						remote.Close()
						t.Error(err)
						return
					}
					defer client.Close()
					defer remote.Close()
					io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n")
					copied := make(chan struct{})
					go func() { io.Copy(remote, buffered); remote.Close(); close(copied) }()
					io.Copy(client, remote)
					client.Close()
					<-copied
				}))
				if proxyScheme == "https" {
					proxy.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
					proxy.StartTLS()
				} else {
					proxy.Start()
				}
				defer proxy.Close()
				proxyURL, _ := url.Parse(proxy.URL)
				proxyURL.Host = net.JoinHostPort("proxy.test", proxyURL.Port())
				base.Proxy = http.ProxyURL(proxyURL)
				base.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
					if address != proxyURL.Host {
						return nil, fmt.Errorf("wrong proxy destination: %s", address)
					}
					return (&net.Dialer{}).DialContext(ctx, network, proxy.Listener.Addr().String())
				}
			}
			providerURL, _ := url.Parse("https://provider.test")
			transport := &assetTransport{base: base, providerURL: providerURL}
			defer transport.CloseIdleConnections()
			req, _ := http.NewRequest("GET", "https://media.test/file%2Bname?sig=a%2Bb&expires=7", nil)
			response, err := (&http.Client{Transport: transport, Timeout: 3 * time.Second}).Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil || string(body) != "saved media" || received.Load() != 1 || queries.Load() != 1 {
				t.Fatalf("body=%q err=%v requests=%d DNS queries=%d", body, err, received.Load(), queries.Load())
			}
			if req.URL.Host != "media.test" || response.Request.URL.Host != "media.test" {
				t.Fatal("public URL was replaced by transport address")
			}
		})
	}
}

func TestAssetTransportRejectsPrivateRedirectAndAllowsProviderOrigin(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/relative", http.StatusFound)
			return
		}
		if r.URL.Path == "/relative" {
			http.Redirect(w, r, "https://127.0.0.1/private", http.StatusFound)
			return
		}
		io.WriteString(w, "provider media")
	}))
	defer server.Close()
	providerURL, _ := url.Parse(server.URL)
	transport := &assetTransport{base: &http.Transport{}, providerURL: providerURL}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	response, err := client.Get(server.URL + "/file")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	_, err = client.Get(server.URL + "/start")
	if err == nil || !strings.Contains(err.Error(), "public") {
		t.Fatalf("private redirect error: %v", err)
	}
	if hits.Load() != 3 {
		t.Fatalf("redirect requests=%d", hits.Load())
	}
}

func TestAssetTransportStillVerifiesMediaCertificate(t *testing.T) {
	assetTestDNS(t)
	cert, roots := assetTestTLS(t)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("media with mismatched certificate reached the HTTP handler")
	}))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	server.StartTLS()
	defer server.Close()
	providerURL, _ := url.Parse("https://provider.test")
	transport := &assetTransport{providerURL: providerURL, base: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: roots},
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if address != "93.184.216.34:443" {
				return nil, fmt.Errorf("unsafe/unpinned destination: %s", address)
			}
			return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
		},
	}}
	defer transport.CloseIdleConnections()
	_, err := (&http.Client{Transport: transport, Timeout: 3 * time.Second}).Get("https://wrong-name.test/media")
	if err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("certificate mismatch error: %v", err)
	}
}
