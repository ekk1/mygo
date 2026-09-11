// Package aihttp contains transport details shared by native AI clients.
package aihttp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/ekk1/mygo/utils/httpclient"
)

// Config configures an immutable transport. Headers are copied.
type Config struct {
	BaseURL, ProxyURL         string
	Headers                   http.Header
	Timeout                   time.Duration
	MaxResponseBytes          int64
	CAFile, CertFile, KeyFile string
	Debug                     bool
	DebugDir                  string
	DebugOmitResponseBody     bool
}

// Client owns a connection pool and immutable request defaults.
type Client struct {
	http                  *http.Client
	base                  *url.URL
	headers               http.Header
	max                   int64
	debugDir              string
	debugOmitResponseBody bool
}

// New builds a transport without making network requests.
func New(cfg Config) (*Client, error) {
	base, err := url.Parse(cfg.BaseURL)
	if err != nil || base == nil || (base.Scheme != "https" && base.Scheme != "http") || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, fmt.Errorf("aihttp: invalid base URL")
	}
	if cfg.Timeout < 0 || cfg.MaxResponseBytes < 0 {
		return nil, fmt.Errorf("aihttp: negative timeout or response limit")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Minute
	}
	if cfg.MaxResponseBytes == 0 {
		cfg.MaxResponseBytes = 256 << 20
	}
	var dir string
	if cfg.Debug {
		dir = cfg.DebugDir
		if dir == "" {
			dir = "./ai-debug"
		}
		if err = os.MkdirAll(dir, 0700); err != nil {
			return nil, fmt.Errorf("aihttp: debug directory: %w", err)
		}
	}
	hc, err := httpclient.New(httpclient.Config{ProxyURL: cfg.ProxyURL, Timeout: cfg.Timeout, CAFile: cfg.CAFile, CertFile: cfg.CertFile, KeyFile: cfg.KeyFile})
	if err != nil {
		return nil, err
	}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	headers := make(http.Header)
	for k, v := range cfg.Headers {
		headers[http.CanonicalHeaderKey(k)] = append([]string(nil), v...)
	}
	return &Client{http: hc.Client, base: base, headers: headers, max: cfg.MaxResponseBytes, debugDir: dir, debugOmitResponseBody: cfg.DebugOmitResponseBody}, nil
}

// CloseIdleConnections releases idle connections, without interrupting requests.
func (c *Client) CloseIdleConnections() { c.http.CloseIdleConnections() }

// Limit returns the buffered response and SSE event size limit.
func (c *Client) Limit() int64 { return c.max }

// Do sends one request and invokes consume for successful responses. Body is always closed.
// Non-2xx responses return retained bytes and httpclient.StatusError. Requests never follow redirects.
func (c *Client) Do(ctx context.Context, method, path, contentType string, body io.Reader, consume func(*httpclient.Response, io.Reader) error) (out *httpclient.Response, err error) {
	relative, e := url.Parse(path)
	if e != nil || relative.IsAbs() || relative.Host != "" || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return nil, fmt.Errorf("aihttp: invalid relative endpoint")
	}
	target := strings.TrimRight(c.base.String(), "/") + path
	req, e := http.NewRequestWithContext(ctx, method, target, body)
	if e != nil {
		return nil, e
	}
	req.Header = c.headers.Clone()
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec, e := startRecord(c.debugDir, req, c.debugOmitResponseBody)
	if e != nil {
		if req.Body != nil {
			req.Body.Close()
		}
		return nil, e
	}
	if rec != nil {
		defer func() { err = errors.Join(err, rec.finish(out, err)) }()
		if req.Body != nil {
			loggedBody := &recordBody{ReadCloser: req.Body, record: rec.request, expected: req.ContentLength}
			req.Body = loggedBody
			defer func() { err = errors.Join(err, loggedBody.finish()) }()
		}
	}
	res, e := c.http.Do(req)
	if e != nil {
		return nil, e
	}
	defer func() { err = errors.Join(err, res.Body.Close()) }()
	out = &httpclient.Response{StatusCode: res.StatusCode, Header: res.Header.Clone()}
	var reader io.Reader = res.Body
	if rec != nil {
		reader = &recordReader{reader: reader, record: rec.response}
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		out.Body, e = ReadAll(reader, c.max)
		return out, errors.Join(&httpclient.StatusError{Response: out}, e)
	}
	if consume != nil {
		err = consume(out, reader)
	} else {
		out.Body, err = ReadAll(reader, c.max)
	}
	return out, err
}

// ReadAll reads at most limit bytes and reports over-limit and partial-read errors.
func ReadAll(r io.Reader, limit int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit))
	if err != nil {
		return b, err
	}
	var tail [1]byte
	n, e := r.Read(tail[:])
	if n > 0 {
		return b, fmt.Errorf("aihttp: response exceeds %d bytes", limit)
	}
	if e != nil && e != io.EOF {
		return b, e
	}
	if e == nil {
		_, e = io.ReadFull(r, tail[:])
		if e == nil {
			return b, fmt.Errorf("aihttp: response exceeds %d bytes", limit)
		}
		if e != io.EOF {
			return b, e
		}
	}
	return b, nil
}

// DownloadURL reads an explicit media URL with the same transport, but without API headers.
func (c *Client) DownloadURL(ctx context.Context, rawURL string, dst io.Writer) (*httpclient.Response, error) {
	if dst == nil {
		return nil, fmt.Errorf("aihttp: nil destination")
	}
	u, e := url.Parse(rawURL)
	if e != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Fragment != "" {
		return nil, fmt.Errorf("aihttp: invalid media URL")
	}
	origin := &url.URL{Scheme: u.Scheme, Host: u.Host}
	media := *c
	media.base = origin
	media.headers = make(http.Header)
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	if u.RawQuery != "" {
		path += "?" + u.RawQuery
	}
	return media.Do(ctx, "GET", path, "", nil, func(_ *httpclient.Response, r io.Reader) error { _, e := io.Copy(dst, r); return e })
}
