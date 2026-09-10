package httpserver

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestListeners(t *testing.T) {
	fixture := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer fixture.Close()
	cert := fixture.TLS.Certificates[0]
	key, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	for file, data := range map[string][]byte{certFile: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]}), keyFile: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key})} {
		if err := os.WriteFile(file, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, network := range []string{"http", "tls", "unix"} {
		t.Run(network, func(t *testing.T) {
			s := New()
			defer s.Close()
			if err := s.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "listening") }); err != nil {
				t.Fatal(err)
			}
			transport := http.DefaultTransport.(*http.Transport).Clone()
			transport.Proxy = nil
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 200 * time.Millisecond}
			done := make(chan error, 1)
			rawURL := ""
			socket := filepath.Join(t.TempDir(), "server.sock")
			if network == "unix" {
				transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, "unix", socket)
				}
				rawURL = "http://unix/"
				go func() { done <- s.ListenUnix(socket) }()
			} else {
				l, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				addr := l.Addr().String()
				l.Close()
				if network == "tls" {
					roots := x509.NewCertPool()
					roots.AddCert(fixture.Certificate())
					transport.TLSClientConfig = &tls.Config{RootCAs: roots}
					rawURL = "https://" + addr
					go func() { done <- s.ListenTLS(addr, certFile, keyFile) }()
				} else {
					rawURL = "http://" + addr
					go func() { done <- s.ListenHTTP(addr) }()
				}
			}
			deadline := time.Now().Add(3 * time.Second)
			for {
				res, err := client.Get(rawURL)
				if err == nil {
					b, readErr := io.ReadAll(res.Body)
					res.Body.Close()
					if readErr != nil || string(b) != "listening" {
						t.Fatalf("response %q %v", b, readErr)
					}
					break
				}
				select {
				case serveErr := <-done:
					t.Fatalf("listener failed: %v", serveErr)
				default:
				}
				if time.Now().After(deadline) {
					t.Fatal(err)
				}
				time.Sleep(5 * time.Millisecond)
			}
			if network == "unix" {
				info, err := os.Stat(socket)
				if err != nil {
					t.Fatal(err)
				}
				if info.Mode().Perm() != 0600 {
					t.Errorf("socket permissions %v", info.Mode())
				}
			}
			client.CloseIdleConnections()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := s.Shutdown(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if !errors.Is(err, http.ErrServerClosed) {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("listener did not stop")
			}
			if network == "unix" {
				if _, err := os.Stat(socket); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("socket not removed: %v", err)
				}
			}
		})
	}
}

func TestUnixDoesNotDeleteExistingPath(t *testing.T) {
	name := filepath.Join(t.TempDir(), "existing")
	if err := os.WriteFile(name, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	s := New()
	defer s.Close()
	if err := s.ListenUnix(name); err == nil {
		t.Fatal("existing file accepted")
	}
	data, err := os.ReadFile(name)
	if err != nil || string(data) != "keep" {
		t.Fatalf("existing file changed: %q %v", data, err)
	}
	if err := s.ListenTLS("127.0.0.1:0", "missing-cert", "missing-key"); err == nil {
		t.Fatal("missing TLS certificate accepted")
	}
}

func TestShutdownTimeoutKeepsStaticResources(t *testing.T) {
	s := New()
	defer s.Close()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("asset"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Static("/files", dir); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	if err := s.HandleFunc("/hold", func(w http.ResponseWriter, r *http.Request) { close(entered); <-release }); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go s.Serve(l)
	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	go func() {
		res, err := client.Get("http://" + l.Addr().String() + "/hold")
		if err == nil {
			res.Body.Close()
		}
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("request did not arrive")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := s.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown %v", err)
	}
	if w := request(s, "GET", "/files/a.txt"); w.Code != 200 || w.Body.String() != "asset" {
		t.Fatalf("resource closed too early: %d %s", w.Code, w.Body)
	}
}

func TestLoggingAndStreaming(t *testing.T) {
	// 默认日志不能破坏 Flush、Hijack 或 ResponseController。
	s := New()
	defer s.Close()
	if err := s.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(103)
		io.WriteString(w, "first\n")
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Error(err)
		}
		io.WriteString(w, "last\n")
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.HandleFunc("/hijack", func(w http.ResponseWriter, r *http.Request) {
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		rw.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 6\r\n\r\nhijack")
		rw.Flush()
	}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	defer ts.Close()
	for path, want := range map[string]string{"/stream": "first\nlast\n", "/hijack": "hijack"} {
		res, err := ts.Client().Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil || res.StatusCode != 200 || string(b) != want {
			t.Fatalf("%s: %d %q %v", path, res.StatusCode, b, err)
		}
	}
}

func TestPanicAfterWriteAbortsConnection(t *testing.T) {
	s := New()
	defer s.Close()
	if err := s.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "partial")
		w.(http.Flusher).Flush()
		panic("boom")
	}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	defer ts.Close()
	res, err := ts.Client().Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err == nil || string(b) != "partial" {
		t.Fatalf("partial panic: %q %v", b, err)
	}
}

func TestRequestLogOmitsSecretsAndEscapesPath(t *testing.T) {
	if os.Getenv("HTTP_SERVER_LOG_TEST") == "1" {
		s := New()
		defer s.Close()
		r := httptest.NewRequest("GET", "/bad/%0apath?password=query-secret", nil)
		r.Header.Set("Authorization", "Bearer auth-secret")
		r.AddCookie(&http.Cookie{Name: "session", Value: "cookie-secret"})
		s.ServeHTTP(httptest.NewRecorder(), r)
		return
	}
	// 用独立进程收集 stdout，避免与其他请求日志竞争全局 os.Stdout。
	cmd := exec.Command(os.Args[0], "-test.run=^TestRequestLogOmitsSecretsAndEscapesPath$")
	cmd.Env = append(os.Environ(), "HTTP_SERVER_LOG_TEST=1")
	log, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(strings.NewReader(string(log)))
	lines := 0
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), "HTTP ") {
			lines++
		}
	}
	if lines != 1 || !strings.Contains(string(log), "status=400") || strings.Contains(string(log), "secret") {
		t.Fatalf("unsafe log: %q", log)
	}
}

func TestErrorPageReplacesEntityHeaders(t *testing.T) {
	for _, recoverPanic := range []bool{false, true} {
		s := New()
		if err := s.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", "1")
			w.Header().Set("Content-Encoding", "gzip")
			if recoverPanic {
				panic("failed before body")
			}
			ServerError(w, r, 500, "")
		}); err != nil {
			t.Fatal(err)
		}
		ts := httptest.NewServer(s)
		res, err := ts.Client().Get(ts.URL)
		if err != nil {
			ts.Close()
			s.Close()
			t.Fatal(err)
		}
		body, err := io.ReadAll(res.Body)
		res.Body.Close()
		ts.Close()
		s.Close()
		if err != nil || res.StatusCode != 500 || !strings.Contains(string(body), `href="/"`) {
			t.Fatalf("panic=%v: body=%q err=%v", recoverPanic, body, err)
		}
	}
}
