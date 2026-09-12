// workbench runs a private personal workspace with local KV persistence.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/ekk1/mygo/utils/httpserver"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8090", "HTTP listen address")
	dir := flag.String("data-dir", "./workbench-data", "private data directory (one process only)")
	flag.Parse()
	if err := run(*addr, *dir, os.Getenv("WORKBENCH_PASSWORD")); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(addr, dir, password string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if password == "" && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("非回环监听须设置 WORKBENCH_PASSWORD，登录用户名为 workbench")
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	lock := filepath.Join(dir, ".lock")
	if err = os.Mkdir(lock, 0700); err != nil {
		return fmt.Errorf("数据目录已锁定（若上次异常退出，确认无运行实例后删除 %s）: %w", lock, err)
	}
	defer os.Remove(lock)
	a, s, err := newApp(dir, password)
	if err != nil {
		return err
	}
	defer s.Close()
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	fmt.Printf("workbench: http://%s\n", l.Addr())
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return serveWorkbench(ctx, a, s, l)
}

// Wait for both the listener and in-flight application persistence before exiting.
func serveWorkbench(ctx context.Context, a *app, s *httpserver.Server, l net.Listener) error {
	served := make(chan error, 1)
	go func() { served <- s.Serve(l) }()
	select {
	case err := <-served:
		a.stopAccepting()
		_ = s.Close()
		a.active.Wait()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		a.stopAccepting()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.Shutdown(shutdownCtx); err != nil {
			_ = s.Close()
		}
		err := <-served
		a.active.Wait()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
