// Package executil executes Bash command strings and manages background processes.
package executil

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// Run executes command using bash -c and returns combined stdout and stderr,
// including output produced before an execution error.
func Run(command string) (string, error) { return RunEnv(command, nil) }

// RunEnv is Run with extra environment variables overriding inherited values.
func RunEnv(command string, env map[string]string) (string, error) {
	cmd, err := commandWithEnv(command, env)
	if err != nil {
		return "", err
	}
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func commandWithEnv(command string, env map[string]string) (*exec.Cmd, error) {
	cmd := exec.Command("bash", "-c", command)
	cmd.Env = os.Environ()
	for key, value := range env {
		if key == "" || strings.ContainsAny(key, "=\x00") || strings.ContainsRune(value, '\x00') {
			return nil, fmt.Errorf("executil: invalid environment variable name or value for %q", key)
		}
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	return cmd, nil
}

// Process is a started command. Its methods are safe for concurrent use.
// Use Start or StartEnv to construct it; the zero value is not usable.
type Process struct {
	cmd      *exec.Cmd
	mu       sync.Mutex
	done     chan struct{}
	err      error
	finished bool
}

type outputWriter struct{ callback func(string) }

func (w *outputWriter) Write(b []byte) (int, error) {
	w.callback(string(b))
	return len(b), nil
}

// Start starts command using bash -c and calls onOutput with combined output
// chunks as they arrive. A nil callback discards output. Callbacks are serialized.
func Start(command string, onOutput func(string)) (*Process, error) {
	return StartEnv(command, nil, onOutput)
}

// StartEnv is Start with extra environment variables overriding inherited values.
// The callback must return promptly and must not call Wait. Chunks need not be
// complete lines or UTF-8 sequences. Output is drained before Wait returns.
func StartEnv(command string, env map[string]string, onOutput func(string)) (*Process, error) {
	cmd, err := commandWithEnv(command, env)
	if err != nil {
		return nil, err
	}
	var writer io.Writer = io.Discard
	if onOutput != nil {
		writer = &outputWriter{callback: onOutput}
	}
	// Sharing the same comparable writer makes os/exec serialize both streams.
	cmd.Stdout, cmd.Stderr = writer, writer
	configureProcess(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	p := &Process{cmd: cmd, done: make(chan struct{})}
	go func() {
		err := cmd.Wait()
		p.mu.Lock()
		p.err, p.finished = err, true
		close(p.done)
		p.mu.Unlock()
	}()
	return p, nil
}

// PID returns the shell process ID, including after the process exits.
func (p *Process) PID() int { return p.cmd.Process.Pid }

// Running reports whether process exit and output draining are still pending.
func (p *Process) Running() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.finished
}

// Wait waits for exit and output draining, returning the execution error.
// It can be called repeatedly or concurrently.
func (p *Process) Wait() error {
	<-p.done
	return p.err
}

// Stop forcefully terminates a running process (its process group on Unix).
// It does not wait for exit; use Wait to reap completion. An already completed
// process is a no-op. Children that start a new process group are not stopped.
func (p *Process) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.finished {
		return nil
	}
	return stopProcess(p.cmd)
}
