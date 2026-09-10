package httpserver

import (
	"bufio"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/ekk1/mygo/utils/logutil"
)

// Middleware 包装标准 http.Handler；同一组参数按从左到右的顺序进入。
type Middleware func(http.Handler) http.Handler

func chain(h http.Handler, middleware ...Middleware) http.Handler {
	for i := len(middleware) - 1; i >= 0; i-- {
		if middleware[i] != nil {
			h = middleware[i](h)
		}
	}
	return h
}

// Methods 仅允许指定方法；方法转为大写，GET 同时允许 HEAD。
// 拒绝时返回 405 和 Allow；未指定方法则拒绝全部请求。
func Methods(methods ...string) Middleware {
	allowed := make(map[string]bool, len(methods))
	for _, method := range methods {
		allowed[strings.ToUpper(method)] = true
	}
	if allowed[http.MethodGet] {
		allowed[http.MethodHead] = true
	}
	names := make([]string, 0, len(allowed))
	for method := range allowed {
		names = append(names, method)
	}
	slices.Sort(names)
	allow := strings.Join(names, ", ")
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !allowed[r.Method] {
				w.Header().Set("Allow", allow)
				ServerError(w, r, http.StatusMethodNotAllowed, "")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// BasicAuth 使用固定用户名和密码校验，失败返回 401 及 Basic challenge。
// 凭据摘要使用恒定时间比较；跨网络使用时应配合 TLS。
func BasicAuth(username, password string) Middleware {
	expectedUser, expectedPass := sha256.Sum256([]byte(username)), sha256.Sum256([]byte(password))
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, pass, ok := r.BasicAuth()
			userHash, passHash := sha256.Sum256([]byte(user)), sha256.Sum256([]byte(pass))
			matches := subtle.ConstantTimeCompare(userHash[:], expectedUser[:]) & subtle.ConstantTimeCompare(passHash[:], expectedPass[:])
			if !ok || matches != 1 {
				w.Header().Set("WWW-Authenticate", `Basic realm="restricted", charset="UTF-8"`)
				ServerError(w, r, http.StatusUnauthorized, "")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// CookieAuth 将指定 cookie 的值交给 check；缺失、nil check 或校验失败时以 303 重定向。
// redirectURL 为空时使用 "/"，应由应用提供可信地址；check 必须支持并发调用。
func CookieAuth(name string, check func(string) bool, redirectURL string) Middleware {
	if redirectURL == "" {
		redirectURL = "/"
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(name)
			if err != nil || check == nil || !check(cookie.Value) {
				w.Header().Set("Cache-Control", "no-store")
				http.Redirect(w, r, redirectURL, http.StatusSeeOther)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func validPath(p string) bool {
	if !strings.HasPrefix(p, "/") || strings.Contains(p, "\\") || strings.Contains(p, "//") {
		return false
	}
	for _, c := range p {
		if c < 32 || c == 127 {
			return false
		}
	}
	for _, part := range strings.Split(p, "/") {
		if part == "." || part == ".." {
			return false
		}
	}
	return true
}

func validatePath(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !validPath(r.URL.Path) {
			ServerError(w, r, http.StatusBadRequest, "Invalid request path.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// responseWriter 记录最终状态，并让 ResponseController 穿透包装。
type responseWriter struct {
	http.ResponseWriter
	status   int
	hijacked bool
}

func (w *responseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *responseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	if status >= 200 || status == 101 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *responseWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(p)
}
func (w *responseWriter) FlushError() error {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return http.NewResponseController(w.ResponseWriter).Flush()
}
func (w *responseWriter) Flush() { _ = w.FlushError() }
func (w *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err == nil {
		w.hijacked = true
	}
	return conn, rw, err
}
func (w *responseWriter) Push(target string, opts *http.PushOptions) error {
	if p, ok := w.ResponseWriter.(http.Pusher); ok {
		return p.Push(target, opts)
	}
	return http.ErrNotSupported
}

func requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w}
		defer func() {
			status := rw.status
			if status == 0 {
				status = 200
			}
			logutil.Info(fmt.Sprintf("HTTP method=%q path=%q status=%d duration=%s remote=%q", r.Method, r.URL.Path, status, time.Since(start), r.RemoteAddr))
		}()
		next.ServeHTTP(rw, r)
	})
}

func recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				logutil.Error(fmt.Sprintf("HTTP panic path=%q error=%q", r.URL.Path, fmt.Sprint(v)))
				if rw, ok := w.(*responseWriter); ok && (rw.status != 0 || rw.hijacked) {
					panic(http.ErrAbortHandler)
				}
				ServerError(w, r, http.StatusInternalServerError, "")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
