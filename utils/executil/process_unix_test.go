//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package executil

import (
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestStopProcessGroup(t *testing.T) {
	requireBash(t)
	ready := make(chan struct{})
	var once sync.Once
	// The child inherits the output pipe; killing only Bash leaves Wait blocked.
	p, err := Start("sleep 30 & printf ready; wait", func(string) { once.Do(func() { close(ready) }) })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-p.PID(), syscall.SIGKILL); _ = p.Wait() })
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("no output")
	}
	exited := make(chan error, 1)
	go func() { exited <- p.Wait() }()
	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := p.Stop(); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	select {
	case err := <-exited:
		if err == nil {
			t.Fatal("expected killed process error")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("child still holds output pipe after Stop")
	}
}
