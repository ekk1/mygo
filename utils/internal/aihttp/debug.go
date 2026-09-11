package aihttp

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ekk1/mygo/utils/httpclient"
)

// recordSink serializes writes and close; transport may finish closing request bodies asynchronously.
type recordSink struct {
	mu       sync.Mutex
	file     *os.File
	complete bool
	err      error
	closed   bool
	bytes    int64
}

func (s *recordSink) write(p []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return os.ErrClosed
	}
	n, e := s.file.Write(p)
	s.bytes += int64(n)
	if e == nil && n != len(p) {
		e = io.ErrShortWrite
	}
	s.err = errors.Join(s.err, e)
	return e
}
func (s *recordSink) eof() { s.mu.Lock(); s.complete = true; s.mu.Unlock() }
func (s *recordSink) close() (int64, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.err = errors.Join(s.err, s.file.Close())
		s.closed = true
	}
	return s.bytes, s.complete, s.err
}

type recordReader struct {
	reader io.Reader
	record *recordSink
}

func (r *recordReader) Read(p []byte) (int, error) {
	n, e := r.reader.Read(p)
	if e == io.EOF {
		r.record.eof()
	}
	if n > 0 {
		if werr := r.record.write(p[:n]); werr != nil {
			e = errors.Join(e, werr)
		}
	}
	return n, e
}

// recordBody synchronizes finalization with reads. Close interrupts an active
// pipe read before taking readMu; subsequent transport reads cannot touch logs.
type recordBody struct {
	io.ReadCloser
	record    *recordSink
	expected  int64
	readMu    sync.Mutex
	closeOnce sync.Once
	closed    bool
	closeErr  error
}

func (r *recordBody) Read(p []byte) (int, error) {
	r.readMu.Lock()
	defer r.readMu.Unlock()
	if r.closed {
		return 0, io.ErrClosedPipe
	}
	return (&recordReader{r.ReadCloser, r.record}).Read(p)
}
func (r *recordBody) Close() error {
	r.closeOnce.Do(func() {
		r.closeErr = r.ReadCloser.Close()
		r.readMu.Lock()
		r.closed = true
		r.record.mu.Lock()
		if r.expected > 0 && r.record.bytes == r.expected && r.record.err == nil {
			r.record.complete = true
		}
		r.record.mu.Unlock()
		r.readMu.Unlock()
	})
	return r.closeErr
}
func (r *recordBody) finish() error {
	e := r.Close()
	r.record.mu.Lock()
	complete := r.record.complete
	r.record.mu.Unlock()
	if !complete {
		e = errors.Join(e, io.ErrUnexpectedEOF)
	}
	return e
}

type record struct {
	dir               string
	request, response *recordSink
	started           time.Time
}

func startRecord(dir string, req *http.Request) (*record, error) {
	if dir == "" {
		return nil, nil
	}
	p, e := os.MkdirTemp(dir, time.Now().UTC().Format("20060102T150405.000000000")+"-")
	if e != nil {
		return nil, e
	}
	r := &record{dir: p, started: time.Now()}
	f, e := os.OpenFile(filepath.Join(p, "request.body"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return nil, e
	}
	r.request = &recordSink{file: f, complete: req.Body == nil || req.Body == http.NoBody}
	f, e = os.OpenFile(filepath.Join(p, "response.body"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		r.request.close()
		return nil, e
	}
	r.response = &recordSink{file: f}
	e = writeMeta(filepath.Join(p, "request.json"), map[string]any{"method": req.Method, "url": redactURL(req.URL), "headers": redactHeaders(req.Header), "started_at": r.started})
	if e != nil {
		r.request.close()
		r.response.close()
		return nil, e
	}
	return r, nil
}
func (r *record) finish(res *httpclient.Response, requestErr error) error {
	rn, rc, re := r.request.close()
	sn, sc, se := r.response.close()
	m := map[string]any{"finished_at": time.Now(), "elapsed_ms": time.Since(r.started).Milliseconds(), "request_bytes": rn, "request_complete": rc, "response_bytes": sn, "response_complete": sc, "received_response": res != nil}
	if res != nil {
		m["status_code"] = res.StatusCode
		m["headers"] = redactHeaders(res.Header)
	}
	// Do not copy arbitrary error strings: net/url and TLS errors may contain credentials.
	if requestErr != nil {
		m["error"] = "request or response processing failed"
	}
	return errors.Join(re, se, writeMeta(filepath.Join(r.dir, "response.json"), m))
}
func writeMeta(path string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	return os.WriteFile(path, b, 0600)
}
func redactHeaders(h http.Header) http.Header {
	out := h.Clone()
	for k := range out {
		low := strings.ToLower(k)
		if strings.Contains(low, "auth") || strings.Contains(low, "api-key") || strings.Contains(low, "apikey") || strings.Contains(low, "cookie") || strings.Contains(low, "token") || strings.Contains(low, "secret") {
			out[k] = []string{"[REDACTED]"}
		} else if low == "location" || low == "content-location" || low == "referer" {
			for i, value := range out[k] {
				u, e := url.Parse(value)
				if e != nil {
					out[k][i] = "[REDACTED]"
				} else {
					out[k][i] = redactURL(u)
				}
			}
		} else if low == "link" {
			// Link can contain multiple URLs and quoted parameters; retain no credentials.
			out[k] = []string{"[REDACTED]"}
		}
	}
	return out
}
func redactURL(u *url.URL) string {
	v := *u
	v.User = nil
	q := v.Query()
	for k := range q {
		low := strings.ToLower(k)
		if strings.Contains(low, "key") || strings.Contains(low, "token") || strings.Contains(low, "signature") || strings.Contains(low, "credential") || low == "sig" || strings.Contains(low, "auth") || strings.Contains(low, "password") || strings.Contains(low, "secret") {
			q.Set(k, "[REDACTED]")
		}
	}
	v.RawQuery = q.Encode()
	return v.String()
}
