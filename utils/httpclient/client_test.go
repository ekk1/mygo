package httpclient

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestJSONAndForm(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Test") != "yes" {
			t.Error("missing configured header")
		}
		switch r.URL.Path {
		case "/json":
			if r.Header.Get("Content-Type") != "application/json" || r.Method != "PATCH" {
				t.Error("incorrect JSON request")
			}
			var v map[string]string
			if err := json.NewDecoder(r.Body).Decode(&v); err != nil || v["name"] != "测试" {
				t.Errorf("JSON body: %v %v", v, err)
			}
		case "/form":
			if err := r.ParseForm(); err != nil || r.Form.Get("name") != "a+b &中" || len(r.Form["tag"]) != 2 {
				t.Errorf("form: %v %v", r.Form, err)
			}
		}
		w.Header().Set("X-Result", "ok")
		io.WriteString(w, `{"ok":true}`)
	}))
	defer s.Close()
	headers := http.Header{"X-Test": {"yes"}}
	c, err := New(Config{Headers: headers})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseIdleConnections()
	headers.Set("X-Test", "changed")
	var out struct{ OK bool }
	res, err := c.JSON(context.Background(), "PATCH", s.URL+"/json", map[string]string{"name": "测试"}, &out)
	if err != nil || !out.OK || res.Header.Get("X-Result") != "ok" || len(res.Body) == 0 {
		t.Fatalf("JSON: %+v %v", res, err)
	}
	out.OK = false
	_, err = c.Form(context.Background(), "POST", s.URL+"/form", url.Values{"name": {"a+b &中"}, "tag": {"one", "two"}}, &out)
	if err != nil || !out.OK {
		t.Fatalf("Form: %+v %v", out, err)
	}
}

func TestResponseErrors(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		limit      int64
		wantError  bool
	}{
		{"status", `{"error":"no"}`, 403, 0, true},
		{"invalid JSON", `broken`, 200, 0, true},
		{"trailing JSON", `{} {}`, 200, 0, true},
		{"empty", ``, 204, 0, false},
		{"too large", `12345`, 200, 4, true},
		{"exact limit", `1234`, 200, 4, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); io.WriteString(w, tc.body) }))
			defer s.Close()
			c, err := New(Config{MaxResponseBytes: tc.limit})
			if err != nil {
				t.Fatal(err)
			}
			defer c.CloseIdleConnections()
			var out any
			res, err := c.JSON(context.Background(), "GET", s.URL, nil, &out)
			if (err != nil) != tc.wantError {
				t.Fatalf("response=%+v err=%v", res, err)
			}
			if tc.status == 403 {
				var se *StatusError
				if !errors.As(err, &se) || se.Response.StatusCode != 403 || string(se.Response.Body) != tc.body {
					t.Fatalf("status error: %v", err)
				}
			}
		})
	}
}

func TestProxyAndCancellation(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Host != "example.invalid" {
			t.Errorf("proxy target: %s", r.URL)
		}
		io.WriteString(w, `{"proxied":true}`)
	}))
	defer proxy.Close()
	c, err := New(Config{ProxyURL: proxy.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseIdleConnections()
	var out map[string]bool
	if _, err = c.JSON(context.Background(), "GET", "http://example.invalid/test", nil, &out); err != nil || !out["proxied"] {
		t.Fatalf("proxy: %v %v", out, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = c.JSON(ctx, "GET", proxy.URL, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
}

func TestCustomCA(t *testing.T) {
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{}`) }))
	defer s.Close()
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := New(Config{CAFile: ca, ProxyURL: "-"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseIdleConnections()
	if _, err = c.JSON(context.Background(), "GET", s.URL, nil, nil); err != nil {
		t.Fatal(err)
	}
	plain, err := New(Config{ProxyURL: "-"})
	if err != nil {
		t.Fatal(err)
	}
	defer plain.CloseIdleConnections()
	if _, err = plain.JSON(context.Background(), "GET", s.URL, nil, nil); err == nil {
		t.Fatal("untrusted certificate accepted")
	}
}

func TestInvalidConfigAndEncoding(t *testing.T) {
	for _, cfg := range []Config{{ProxyURL: "://bad"}, {ProxyURL: "ftp://host"}, {ProxyURL: "http://"}, {CertFile: "x"}, {KeyFile: "x"}, {CAFile: "/nonexistent-ca"}, {Timeout: -time.Second}, {MaxResponseBytes: -1}} {
		if _, err := New(cfg); err == nil {
			t.Fatalf("accepted %+v", cfg)
		}
	}
	if _, err := JSON(context.Background(), "POST", "http://example.invalid", make(chan int), nil); err == nil || !strings.Contains(err.Error(), "json") {
		t.Fatalf("encode: %v", err)
	}
}

type wrappedTransport struct{ http.RoundTripper }

func TestNewWithReplacedDefaultTransport(t *testing.T) {
	old := http.DefaultTransport
	http.DefaultTransport = wrappedTransport{old}
	defer func() { http.DefaultTransport = old }()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{}`) }))
	defer s.Close()
	c, err := New(Config{ProxyURL: "-"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseIdleConnections()
	if _, err := c.JSON(context.Background(), "GET", s.URL, nil, nil); err != nil {
		t.Fatal(err)
	}
}
