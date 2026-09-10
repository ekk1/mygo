// Package httpserver 提供路由、中间件、静态目录及 HTTP/TLS/Unix 监听的轻量封装。
package httpserver

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"
)

// Server 持有路由、监听器及静态目录资源；必须通过 New 创建，不可复制。
// 注册路由、处理请求、Shutdown 和 Close 可并发调用。
type Server struct {
	mux     *http.ServeMux
	handler http.Handler
	server  *http.Server
	mu      sync.Mutex
	closed  bool
	roots   []*os.Root
}

// New 创建默认包含请求日志、panic 恢复和路径校验的服务。
// middleware 按参数顺序从外到内执行，位于默认保护层内。
func New(middleware ...Middleware) *Server {
	s := &Server{mux: http.NewServeMux()}
	s.handler = requestLog(recoverPanics(validatePath(chain(s.mux, middleware...))))
	s.server = &http.Server{Handler: s, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	return s
}

// ServeHTTP 实现 http.Handler，可用于 httptest 或自定义 http.Server。
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.handler.ServeHTTP(w, r) }

// Handle 注册标准 ServeMux 模式；非法、冲突或关闭后注册返回 error。
func (s *Server) Handle(pattern string, handler http.Handler, middleware ...Middleware) error {
	if handler == nil {
		return fmt.Errorf("httpserver: nil handler")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return http.ErrServerClosed
	}
	return s.register(pattern, chain(handler, middleware...))
}

// HandleFunc 注册处理函数和可选的路由中间件。
func (s *Server) HandleFunc(pattern string, handler http.HandlerFunc, middleware ...Middleware) error {
	if handler == nil {
		return fmt.Errorf("httpserver: nil handler")
	}
	return s.Handle(pattern, handler, middleware...)
}

func (s *Server) register(pattern string, h http.Handler) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("httpserver: register %q: %v", pattern, v)
		}
	}()
	s.mux.Handle(pattern, h)
	return nil
}

// Serve 在已有监听器上提供 HTTP；接管并关闭监听器，关闭服务时返回 http.ErrServerClosed。
func (s *Server) Serve(listener net.Listener) error { return s.server.Serve(listener) }

// ListenHTTP 在 TCP 地址上阻塞提供 HTTP。
func (s *Server) ListenHTTP(addr string) error {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return s.Serve(l)
}

// ListenTLS 在 TCP 地址上阻塞提供 HTTPS，使用 PEM 服务端证书和私钥。
func (s *Server) ListenTLS(addr, certFile, keyFile string) error {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer l.Close()
	return s.server.ServeTLS(l, certFile, keyFile)
}

// ListenUnix 在 Unix socket 上阻塞提供 HTTP，socket 权限为 0600。
// 不删除已有路径；关闭监听器时由 net.UnixListener 清理新建的 socket。
func (s *Server) ListenUnix(socketPath string) error {
	l, err := net.Listen("unix", socketPath)
	if err != nil {
		return err
	}
	defer l.Close()
	if err := os.Chmod(socketPath, 0600); err != nil {
		return err
	}
	return s.Serve(l)
}

// Shutdown 停止接收请求，等待现有请求结束并释放静态目录。
// 超时返回 ctx 的错误并保留目录，可再次 Shutdown 或 Close。
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	if err := s.server.Shutdown(ctx); err != nil {
		return err
	}
	return s.closeRoots()
}

// Close 立即关闭连接及静态目录；正常使用优先 Shutdown。可重复调用。
func (s *Server) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return errors.Join(s.server.Close(), s.closeRoots())
}

func (s *Server) closeRoots() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var errs []error
	for _, root := range s.roots {
		errs = append(errs, root.Close())
	}
	s.roots = nil
	return errors.Join(errs...)
}
