package executil

import (
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

func requireBash(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash unavailable")
	}
}
func TestRun(t *testing.T) {
	requireBash(t)
	out, err := Run("printf '%s' hello | tr a-z A-Z; printf err >&2; exit 7")
	if err == nil || !strings.Contains(out, "HELLO") || !strings.Contains(out, "err") {
		t.Fatalf("out=%q err=%v", out, err)
	}
}
func TestRunEnv(t *testing.T) {
	requireBash(t)
	t.Setenv("MYGO_EXISTING", "parent")
	t.Setenv("MYGO_KEEP", "kept")
	out, err := RunEnv(`printf '%s:%s:%s' "$MYGO_EXISTING" "$MYGO_KEEP" "$MYGO_NEW"`, map[string]string{"MYGO_EXISTING": "child", "MYGO_NEW": "new"})
	if err != nil || out != "child:kept:new" {
		t.Fatalf("out=%q err=%v", out, err)
	}
	if _, err := RunEnv("true", map[string]string{"BAD=KEY": "x"}); err == nil {
		t.Error("invalid env accepted")
	}
}
func TestProcessStreamingAndStop(t *testing.T) {
	requireBash(t)
	chunks := make(chan string, 10)
	p, err := StartEnv(`printf '%s' "$MYGO_STREAM"; exec sleep 30`, map[string]string{"MYGO_STREAM": "ready"}, func(s string) { chunks <- s })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Stop(); _ = p.Wait() })
	if p.PID() <= 0 {
		t.Fatal("invalid pid")
	}
	select {
	case s := <-chunks:
		if s != "ready" {
			t.Fatal(s)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no live output")
	}
	if !p.Running() {
		t.Fatal("process not running")
	}
	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				_ = p.Running()
				_ = p.PID()
			}
		}()
	}
	if err := p.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := p.Wait(); err == nil {
		t.Fatal("killed process should fail")
	}
	wg.Wait()
	if p.Running() {
		t.Fatal("still running")
	}
	if err := p.Stop(); err != nil {
		t.Fatal(err)
	}
}
func TestProcessWait(t *testing.T) {
	requireBash(t)
	var output strings.Builder
	p, err := Start("printf out; printf err >&2", func(s string) { output.WriteString(s) })
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := p.Wait(); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if !strings.Contains(output.String(), "out") || !strings.Contains(output.String(), "err") {
		t.Fatal(output.String())
	}
	p, err = Start("exit 9", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = p.Wait(); err == nil {
		t.Fatal("missing exit error")
	}
}

func TestMissingBash(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := Run("true"); err == nil {
		t.Error("Run should report missing bash")
	}
	if p, err := Start("true", nil); err == nil || p != nil {
		t.Fatalf("Start = %v, %v", p, err)
	}
}
