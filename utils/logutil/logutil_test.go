package logutil_test

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/ekk1/mygo/utils/logutil"
)

func captureOutput(t *testing.T, run func()) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stdout
	os.Stdout = f
	defer func() {
		os.Stdout = original
		f.Close()
		logutil.SetLevel(logutil.LevelInfo)
	}()
	run()
	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestDefaultLevel(t *testing.T) {
	output := captureOutput(t, func() {
		logutil.Debug("hidden")
		logutil.Info("visible")
	})
	if strings.Contains(output, "hidden") || !strings.Contains(output, "[INFO] visible\n") {
		t.Fatalf("unexpected default output: %q", output)
	}
}

func TestLevelsAndFormat(t *testing.T) {
	for _, tt := range []struct {
		name  string
		level logutil.Level
		want  string
	}{
		{"debug", logutil.LevelDebug, "DEBUG,INFO,WARN,ERROR"},
		{"info", logutil.LevelInfo, "INFO,WARN,ERROR"},
		{"warn", logutil.LevelWarn, "WARN,ERROR"},
		{"error", logutil.LevelError, "ERROR"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			output := captureOutput(t, func() {
				logutil.SetLevel(tt.level)
				logutil.Debug("count=", 3)
				logutil.Info("count=", 3)
				logutil.Warn("count=", 3)
				logutil.Error("count=", 3)
			})
			pattern := regexp.MustCompile(`^\[\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\] \[(DEBUG|INFO|WARN|ERROR)\] count=3$`)
			var levels []string
			for _, line := range strings.Split(strings.TrimSuffix(output, "\n"), "\n") {
				match := pattern.FindStringSubmatch(line)
				if match == nil {
					t.Fatalf("unexpected format: %q", line)
				}
				levels = append(levels, match[1])
			}
			if got := strings.Join(levels, ","); got != tt.want {
				t.Fatalf("levels = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestConcurrentLoggingAndSetLevel(t *testing.T) {
	const workers, entries = 16, 50
	output := captureOutput(t, func() {
		var wg sync.WaitGroup
		for worker := range workers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for entry := range entries {
					if entry%2 == 0 {
						logutil.SetLevel(logutil.LevelDebug)
					} else {
						logutil.SetLevel(logutil.LevelInfo)
					}
					logutil.Info("worker=", worker, " entry=", entry)
				}
			}()
		}
		wg.Wait()
	})
	lines := strings.Split(strings.TrimSuffix(output, "\n"), "\n")
	if len(lines) != workers*entries {
		t.Fatalf("got %d lines, want %d", len(lines), workers*entries)
	}
	seen := make(map[string]int)
	for _, line := range lines {
		_, message, ok := strings.Cut(line, "] [INFO] ")
		if !ok {
			t.Fatalf("broken record: %q", line)
		}
		seen[message]++
	}
	for worker := range workers {
		for entry := range entries {
			want := fmt.Sprintf("worker=%d entry=%d", worker, entry)
			if seen[want] != 1 {
				t.Errorf("record %q appeared %d times, want once", want, seen[want])
			}
		}
	}
}
