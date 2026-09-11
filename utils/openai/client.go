// Package openai provides native OpenAI generation, media and resource APIs.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/ekk1/mygo/utils/httpclient"
	"github.com/ekk1/mygo/utils/internal/aihttp"
)

// Config configures an independent, reusable OpenAI client.
// Zero timeout is 10 minutes; zero response limit is 256 MiB per buffered body or SSE event.
type Config struct {
	APIKey, BaseURL, ProxyURL string
	Timeout                   time.Duration
	Headers                   http.Header
	Debug                     bool
	DebugDir                  string
	DebugOmitResponseBody     bool
	MaxResponseBytes          int64
	CAFile, CertFile, KeyFile string
}

// Client owns its transport. Construct with New and reuse concurrently without mutating requests.
type Client struct{ transport *aihttp.Client }

// HTTPResponse preserves status, headers, and original response bytes (empty for streamed downloads).
type HTTPResponse = httpclient.Response

// Event is an unmodified native SSE event; Data contains raw JSON, or the Chat [DONE] sentinel.
type Event = aihttp.Event

// Upload supplies one multipart file. The caller owns Reader; the client does not close it.
type Upload struct {
	Field, Filename, ContentType string
	Reader                       io.Reader
}

// Ptr returns a pointer, allowing an optional field to explicitly send false or zero.
func Ptr[T any](v T) *T { return &v }

// New validates configuration. An explicit Authorization header can replace Bearer authentication.
func New(cfg Config) (*Client, error) {
	h := make(http.Header)
	for k, v := range cfg.Headers {
		h[http.CanonicalHeaderKey(k)] = append([]string(nil), v...)
	}
	if h.Get("Authorization") == "" {
		if strings.TrimSpace(cfg.APIKey) == "" {
			return nil, fmt.Errorf("openai: APIKey or Authorization header required")
		}
		h.Set("Authorization", "Bearer "+cfg.APIKey)
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.openai.com/v1"
	}
	transport, err := aihttp.New(aihttp.Config{BaseURL: cfg.BaseURL, ProxyURL: cfg.ProxyURL, Timeout: cfg.Timeout, Headers: h, Debug: cfg.Debug, DebugDir: cfg.DebugDir, DebugOmitResponseBody: cfg.DebugOmitResponseBody, MaxResponseBytes: cfg.MaxResponseBytes, CAFile: cfg.CAFile, CertFile: cfg.CertFile, KeyFile: cfg.KeyFile})
	if err != nil {
		return nil, err
	}
	return &Client{transport}, nil
}

// CloseIdleConnections releases idle connections without interrupting active requests.
func (c *Client) CloseIdleConnections() { c.transport.CloseIdleConnections() }
func (c *Client) decode(output any) func(*HTTPResponse, io.Reader) error {
	return func(res *HTTPResponse, r io.Reader) error {
		b, err := aihttp.ReadAll(r, c.transport.Limit())
		res.Body = b
		if err != nil {
			return err
		}
		if output != nil && len(bytes.TrimSpace(b)) > 0 {
			return json.Unmarshal(b, output)
		}
		return nil
	}
}
func (c *Client) json(ctx context.Context, method, path string, input, output any) (*HTTPResponse, error) {
	var r io.Reader
	if input != nil {
		b, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	return c.transport.Do(ctx, method, path, "application/json", r, c.decode(output))
}
func (c *Client) download(ctx context.Context, path string, dst io.Writer) (*HTTPResponse, error) {
	return c.jsonDownload(ctx, http.MethodGet, path, nil, dst)
}
func (c *Client) jsonDownload(ctx context.Context, method, path string, input any, dst io.Writer) (*HTTPResponse, error) {
	if dst == nil {
		return nil, fmt.Errorf("openai: nil destination")
	}
	var r io.Reader
	if input != nil {
		b, e := json.Marshal(input)
		if e != nil {
			return nil, e
		}
		r = bytes.NewReader(b)
	}
	return c.transport.Do(ctx, method, path, "application/json", r, func(_ *HTTPResponse, r io.Reader) error { _, e := io.Copy(dst, r); return e })
}
func (c *Client) stream(ctx context.Context, path string, input any, handle func(Event) error) (*HTTPResponse, error) {
	if handle == nil {
		return nil, fmt.Errorf("openai: nil stream callback")
	}
	b, e := json.Marshal(input)
	if e != nil {
		return nil, e
	}
	return c.transport.Do(ctx, "POST", path, "application/json", bytes.NewReader(b), c.events(handle))
}
func (c *Client) events(handle func(Event) error) func(*HTTPResponse, io.Reader) error {
	return func(res *HTTPResponse, r io.Reader) error {
		ct, _, err := mime.ParseMediaType(res.Header.Get("Content-Type"))
		if err != nil || ct != "text/event-stream" {
			res.Body, err = aihttp.ReadAll(r, c.transport.Limit())
			return errors.Join(fmt.Errorf("openai: expected text/event-stream"), err)
		}
		return aihttp.ReadSSE(r, c.transport.Limit(), handle)
	}
}
func (c *Client) multipart(ctx context.Context, method, path string, fields map[string]string, files []Upload, output any) (*HTTPResponse, error) {
	return c.multipartValues(ctx, method, path, expandValues(fields), files, output)
}
func (c *Client) multipartValues(ctx context.Context, method, path string, fields map[string][]string, files []Upload, output any) (*HTTPResponse, error) {
	return c.form(ctx, method, path, fields, files, c.decode(output))
}
func (c *Client) streamMultipart(ctx context.Context, path string, fields map[string]string, files []Upload, handle func(Event) error) (*HTTPResponse, error) {
	return c.streamMultipartValues(ctx, path, expandValues(fields), files, handle)
}
func (c *Client) streamMultipartValues(ctx context.Context, path string, fields map[string][]string, files []Upload, handle func(Event) error) (*HTTPResponse, error) {
	if handle == nil {
		return nil, fmt.Errorf("openai: nil stream callback")
	}
	return c.form(ctx, "POST", path, fields, files, c.events(handle))
}
func expandValues(fields map[string]string) map[string][]string {
	out := make(map[string][]string, len(fields))
	for k, v := range fields {
		out[k] = []string{v}
	}
	return out
}
func (c *Client) form(ctx context.Context, method, path string, fields map[string][]string, files []Upload, consume func(*HTTPResponse, io.Reader) error) (*HTTPResponse, error) {
	for _, f := range files {
		if f.Reader == nil || f.Field == "" || f.Filename == "" {
			return nil, fmt.Errorf("openai: upload requires field, filename and reader")
		}
		if strings.ContainsAny(f.ContentType, "\r\n") {
			return nil, fmt.Errorf("openai: invalid upload content type")
		}
	}
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	done := make(chan error, 1)
	go func() {
		err := writeForm(mw, fields, files)
		if err == nil {
			err = mw.Close()
		}
		pw.CloseWithError(err)
		done <- err
	}()
	res, err := c.transport.Do(ctx, method, path, mw.FormDataContentType(), pr, consume)
	pr.Close()
	writeErr := <-done
	return res, errors.Join(err, writeErr)
}
func writeForm(mw *multipart.Writer, fields map[string][]string, files []Upload) error {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, v := range fields[k] {
			if e := mw.WriteField(k, v); e != nil {
				return e
			}
		}
	}
	for _, f := range files {
		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": f.Field, "filename": f.Filename}))
		ct := f.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		h.Set("Content-Type", ct)
		w, e := mw.CreatePart(h)
		if e != nil {
			return e
		}
		if _, e = io.Copy(w, f.Reader); e != nil {
			return e
		}
	}
	return nil
}
func marshalFields(base any, extra map[string]any) ([]byte, error) {
	b, e := json.Marshal(base)
	if e != nil {
		return nil, e
	}
	if len(extra) == 0 {
		return b, nil
	}
	var fields map[string]json.RawMessage
	if e = json.Unmarshal(b, &fields); e != nil {
		return nil, e
	}
	for k, v := range extra {
		if _, ok := fields[k]; ok {
			return nil, fmt.Errorf("openai: duplicate field %q", k)
		}
		raw, e := json.Marshal(v)
		if e != nil {
			return nil, e
		}
		fields[k] = raw
	}
	return json.Marshal(fields)
}
func pathID(id string) (string, error) {
	if strings.TrimSpace(id) == "" || id == "." || id == ".." || strings.ContainsAny(id, "/\\?#\r\n\x00") {
		return "", fmt.Errorf("openai: invalid resource ID")
	}
	return url.PathEscape(id), nil
}

// DownloadMedia downloads a returned media URL using this client's proxy and debug settings.
// No API authentication or custom default headers are forwarded. Redirects are not followed.
// For authenticated Files/Containers endpoints, use their dedicated download methods.
func (c *Client) DownloadMedia(ctx context.Context, rawURL string, dst io.Writer) (*HTTPResponse, error) {
	return c.transport.DownloadURL(ctx, rawURL, dst)
}
