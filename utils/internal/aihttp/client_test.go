package aihttp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ekk1/mygo/utils/httpclient"
)

func TestProxyAndConcurrentDebug(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Host != "api.test" || r.URL.Path != "/prefix/responses" {
			t.Errorf("wrong proxy target: %s", r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("auth lost")
		}
		b, _ := io.ReadAll(r.Body)
		if string(b) != "payload" {
			t.Errorf("body: %s", b)
		}
		w.Header().Set("X-Request-ID", "req_123")
		io.WriteString(w, "answer")
	}))
	defer proxy.Close()
	dir := t.TempDir()
	c, err := New(Config{BaseURL: "http://api.test/prefix", ProxyURL: proxy.URL, Headers: http.Header{"Authorization": {"Bearer secret"}}, Debug: true, DebugDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseIdleConnections()
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := c.Do(context.Background(), "POST", "/responses", "text/plain", strings.NewReader("payload"), func(res *httpclient.Response, r io.Reader) error { var e error; res.Body, e = io.ReadAll(r); return e })
			if err != nil {
				t.Error(err)
			} else if string(res.Body) != "answer" {
				t.Error("bad response")
			}
		}()
	}
	wg.Wait()
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 8 {
		t.Fatalf("logs %d %v", len(entries), err)
	}
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		for name, want := range map[string]string{"request.body": "payload", "response.body": "answer"} {
			b, err := os.ReadFile(filepath.Join(p, name))
			if err != nil || string(b) != want {
				t.Fatalf("%s=%q %v", name, b, err)
			}
		}
		for _, name := range []string{"request.json", "response.json"} {
			b, err := os.ReadFile(filepath.Join(p, name))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(b), "secret") {
				t.Fatal("credential logged")
			}
		}
	}
}

func TestErrorCancelRedirectAndLoggingFailure(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bad":
			w.WriteHeader(429)
			io.WriteString(w, `{"error":{"message":"limited"}}`)
		case "/redirect":
			http.Redirect(w, r, "/bad", 302)
		case "/wait":
			<-r.Context().Done()
		}
	}))
	defer s.Close()
	c, _ := New(Config{BaseURL: s.URL, ProxyURL: "-"})
	defer c.CloseIdleConnections()
	for _, p := range []string{"/bad", "/redirect"} {
		res, err := c.Do(context.Background(), "GET", p, "", nil, nil)
		var se *httpclient.StatusError
		if !errors.As(err, &se) || res.StatusCode == 0 {
			t.Fatalf("%s: %v", p, err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := c.Do(ctx, "GET", "/wait", "", nil, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "file")
	os.WriteFile(path, []byte("x"), 0600)
	_, err = New(Config{BaseURL: s.URL, Debug: true, DebugDir: path})
	if err == nil {
		t.Fatal("accepted invalid log directory")
	}
}

func TestSSELongMultilineAndIncomplete(t *testing.T) {
	long := strings.Repeat("x", 70000)
	var events []Event
	err := ReadSSE(strings.NewReader(": heartbeat\r\nevent: delta\r\nid: 4\r\ndata: "+long+"\r\ndata: tail\r\n\r\ndata: [DONE]\n\n"), 100000, func(e Event) error { events = append(events, e); return nil })
	if err != nil || len(events) != 2 || events[0].Type != "delta" || events[0].ID != "4" || string(events[0].Data) != long+"\ntail" {
		t.Fatalf("events %d %v", len(events), err)
	}
	if err := ReadSSE(strings.NewReader("data: unfinished"), 1000, func(Event) error { return nil }); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("incomplete %v", err)
	}
	if err := ReadSSE(strings.NewReader("data: "+long+"\n\n"), 1000, func(Event) error { return nil }); err == nil {
		t.Fatal("missing event limit")
	}
	stop := errors.New("stop")
	if err := ReadSSE(strings.NewReader("data: ok\n\n"), 1000, func(Event) error { return stop }); !errors.Is(err, stop) {
		t.Fatal(err)
	}
}

// This catches silently accepting truncated network bodies and losing their debug record.
func TestTruncatedResponseAndResponseLimit(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/truncated" {
			w.Header().Set("Content-Length", "100")
			io.WriteString(w, "partial")
			return
		}
		io.WriteString(w, "12345")
	}))
	defer s.Close()
	dir := t.TempDir()
	c, err := New(Config{BaseURL: s.URL, ProxyURL: "-", Debug: true, DebugDir: dir, MaxResponseBytes: 10})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseIdleConnections()
	res, err := c.Do(context.Background(), "GET", "/truncated", "", nil, nil)
	if !errors.Is(err, io.ErrUnexpectedEOF) || string(res.Body) != "partial" {
		t.Fatalf("%q %v", res.Body, err)
	}
	entries, _ := os.ReadDir(dir)
	body, _ := os.ReadFile(filepath.Join(dir, entries[0].Name(), "response.body"))
	meta, _ := os.ReadFile(filepath.Join(dir, entries[0].Name(), "response.json"))
	if string(body) != "partial" || !strings.Contains(string(meta), `"response_complete": false`) {
		t.Fatalf("lost partial trace %s %s", body, meta)
	}
	c2, _ := New(Config{BaseURL: s.URL, ProxyURL: "-", MaxResponseBytes: 3})
	defer c2.CloseIdleConnections()
	res, err = c2.Do(context.Background(), "GET", "/large", "", nil, nil)
	if err == nil || string(res.Body) != "123" {
		t.Fatalf("limit %q %v", res.Body, err)
	}
}

type asyncBodyTransport func(*http.Request) (*http.Response, error)

func (f asyncBodyTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// A transport may still be consuming the upload when it returns the response.
func TestDebugFinalizationStopsLateRequestReads(t *testing.T) {
	dir := t.TempDir()
	c, err := New(Config{BaseURL: "http://example.test", Debug: true, DebugDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseIdleConnections()
	release := make(chan struct{})
	finished := make(chan error, 1)
	c.http.Transport = asyncBodyTransport(func(r *http.Request) (*http.Response, error) {
		go func() { <-release; _, err := io.ReadAll(r.Body); r.Body.Close(); finished <- err }()
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("{}")), Request: r}, nil
	})
	_, err = c.Do(context.Background(), "POST", "/test", "application/json", strings.NewReader("payload"), nil)
	close(release)
	lateErr := <-finished
	if err == nil {
		t.Fatalf("incomplete upload returned success; late read error: %v", lateErr)
	}
	entries, _ := os.ReadDir(dir)
	b, _ := os.ReadFile(filepath.Join(dir, entries[0].Name(), "request.body"))
	if len(b) != 0 {
		t.Fatalf("request log changed after completion: %q", b)
	}
}
func TestDebugRedactsSignedLocation(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://storage.example/image?sig=SECRET&auth=PRIVATE")
		w.WriteHeader(302)
	}))
	defer s.Close()
	dir := t.TempDir()
	c, err := New(Config{BaseURL: s.URL, Debug: true, DebugDir: dir, ProxyURL: "-"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseIdleConnections()
	c.Do(context.Background(), "GET", "/test", "", nil, nil)
	entries, _ := os.ReadDir(dir)
	b, _ := os.ReadFile(filepath.Join(dir, entries[0].Name(), "response.json"))
	if strings.Contains(string(b), "SECRET") || strings.Contains(string(b), "PRIVATE") {
		t.Fatalf("credential in redirect log: %s", b)
	}
}
