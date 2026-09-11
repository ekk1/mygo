package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestServeWaitsForActiveHandlersOnShutdown(t *testing.T) {
	a, s, err := newApp(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	entered := make(chan struct{})
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	if err = s.HandleFunc("GET /slow", func(w http.ResponseWriter, r *http.Request) { close(entered); <-release; io.WriteString(w, "saved") }); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- serveWorkbench(ctx, a, s, l) }()
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		res, err := http.Get("http://" + l.Addr().String() + "/slow")
		if err == nil {
			io.Copy(io.Discard, res.Body)
			res.Body.Close()
		}
	}()
	<-entered
	cancel()
	select {
	case err := <-done:
		t.Fatalf("server exited before active handler persisted: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown did not finish")
	}
	<-requestDone
}
