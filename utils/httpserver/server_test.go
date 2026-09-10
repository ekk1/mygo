package httpserver

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func request(h http.Handler, method, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(method, path, nil))
	return w
}

func TestMiddlewareAndRegistration(t *testing.T) {
	var order []string
	mark := func(name string) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { order = append(order, name); next.ServeHTTP(w, r) })
		}
	}
	s := New(mark("global"))
	defer s.Close()
	if err := s.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") }, mark("route"), Methods("GET"), BasicAuth("admin", "secret")); err != nil {
		t.Fatal(err)
	}
	if err := s.HandleFunc("/hello", func(http.ResponseWriter, *http.Request) {}); err == nil {
		t.Fatal("duplicate should return error")
	}
	if err := s.HandleFunc("/{bad", func(http.ResponseWriter, *http.Request) {}); err == nil {
		t.Fatal("invalid pattern should return error")
	}
	if err := s.Handle("/nil", nil); err == nil {
		t.Fatal("nil handler accepted")
	}
	w := request(s, "POST", "/hello")
	if w.Code != 405 || w.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("method: %d %v", w.Code, w.Header())
	}
	w = request(s, "GET", "/hello")
	if w.Code != 401 || w.Header().Get("WWW-Authenticate") == "" {
		t.Fatal("missing basic challenge")
	}
	for _, password := range []string{"wrong", "secret"} {
		r := httptest.NewRequest("GET", "/hello", nil)
		r.SetBasicAuth("admin", password)
		w = httptest.NewRecorder()
		s.ServeHTTP(w, r)
		want := 401
		if password == "secret" {
			want = 200
		}
		if w.Code != want {
			t.Errorf("auth status %d want %d", w.Code, want)
		}
	}
	if strings.Join(order, ",") != "global,route,global,route,global,route,global,route" {
		t.Fatalf("middleware order: %v", order)
	}
}

func TestCookieAuth(t *testing.T) {
	called := 0
	h := CookieAuth("session", func(v string) bool { called++; return v == "valid" }, "/login")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	for _, tc := range []struct {
		value string
		want  int
	}{{"", 303}, {"wrong", 303}, {"valid", 204}} {
		r := httptest.NewRequest("POST", "/private", nil)
		if tc.value != "" {
			r.AddCookie(&http.Cookie{Name: "session", Value: tc.value})
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("%q status %d", tc.value, w.Code)
		}
		if tc.want == 303 && w.Header().Get("Location") != "/login" {
			t.Fatal("missing redirect")
		}
	}
	if called != 2 {
		t.Fatalf("checker calls %d", called)
	}
	h = CookieAuth("session", nil, "")(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("nil validator allowed request") }))
	if w := request(h, "GET", "/private"); w.Code != 303 || w.Header().Get("Location") != "/" {
		t.Fatal("default redirect")
	}
}

func TestPathValidationAndRecovery(t *testing.T) {
	s := New()
	defer s.Close()
	if err := s.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") }); err != nil {
		t.Fatal(err)
	}
	if err := s.HandleFunc("/panic", func(http.ResponseWriter, *http.Request) { panic("private secret") }); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/a/../b", "/a/%2e%2e/b", "//host/a", "/a\\b", "/a/%00b", "/a/./b"} {
		if w := request(s, "GET", p); w.Code != 400 {
			t.Errorf("path %q got %d", p, w.Code)
		}
	}
	if w := request(s, "GET", "/中文?next=../"); w.Code != 200 {
		t.Errorf("valid path: %d", w.Code)
	}
	w := request(s, "GET", "/panic")
	if w.Code != 500 || strings.Contains(w.Body.String(), "private secret") || !strings.Contains(w.Body.String(), `href="/"`) {
		t.Fatalf("panic response: %d %s", w.Code, w.Body)
	}
}

func TestServerErrorEscapesAndHead(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	ServerError(w, r, 400, "<script>alert(1)</script>")
	if w.Code != 400 || strings.Contains(w.Body.String(), "<script>") || !strings.Contains(w.Body.String(), "&lt;script&gt;") || !strings.Contains(w.Body.String(), `href="/"`) {
		t.Fatalf("error page: %d %s", w.Code, w.Body)
	}
	w = httptest.NewRecorder()
	ServerError(w, httptest.NewRequest("HEAD", "/", nil), 500, "oops")
	if w.Code != 500 || w.Body.Len() != 0 {
		t.Fatal("HEAD error body")
	}
	w = httptest.NewRecorder()
	ServerError(w, r, 200, "")
	if w.Code != 500 {
		t.Fatal("invalid error status")
	}
}

func TestStaticConfinementAndMethods(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	for name, value := range map[string]string{"index.html": "home", "a.txt": "asset"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("outside-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "escape")); err != nil {
		t.Fatal(err)
	}
	s := New()
	defer s.Close()
	if err := s.Static("/files", dir); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path string
		want         int
		body         string
	}{
		{"GET", "/files/", 200, "home"}, {"GET", "/files/a.txt", 200, "asset"}, {"GET", "/files", 301, ""},
		{"GET", "/files/missing", 404, ""}, {"GET", "/files/escape/secret", 403, ""}, {"POST", "/files/a.txt", 405, ""}, {"HEAD", "/files/a.txt", 200, ""},
	} {
		w := request(s, tc.method, tc.path)
		if w.Code != tc.want || (tc.body != "" && w.Body.String() != tc.body) || strings.Contains(w.Body.String(), "outside-secret") {
			t.Errorf("%s %s: %d %q", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
	if err := s.Static("/bad", filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing directory accepted")
	}
	if err := s.Static("/wild/{name}", dir); err == nil {
		t.Fatal("wildcard static prefix accepted")
	}
	if err := s.Static("/files", dir); err == nil {
		t.Fatal("duplicate static path accepted")
	}
}

func TestServeShutdownAndConcurrentRegistration(t *testing.T) {
	s := New()
	defer s.Close()
	if err := s.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "hello") }); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Serve(l) }()
	c := &http.Client{Timeout: time.Second}
	defer c.CloseIdleConnections()
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := c.Get("http://" + l.Addr().String() + "/hello")
			if err != nil {
				t.Error(err)
				return
			}
			defer res.Body.Close()
			b, _ := io.ReadAll(res.Body)
			if string(b) != "hello" {
				t.Errorf("body %q", b)
			}
		}()
	}
	if err := s.HandleFunc("/later", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("Serve result %v", err)
	}
	if err := s.Static("/after", t.TempDir()); err == nil {
		t.Fatal("registered resource after close")
	}
}
