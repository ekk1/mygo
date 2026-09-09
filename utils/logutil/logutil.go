// Package logutil provides simple, concurrency-safe leveled output to stdout.
package logutil

import (
	"fmt"
	"sync"
	"time"
)

// Level is the minimum severity to print.
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

var (
	mu    sync.Mutex
	level = LevelInfo
)

// SetLevel sets the minimum output level. The default is LevelInfo.
// It is safe to call concurrently with logging functions.
func SetLevel(l Level) {
	mu.Lock()
	defer mu.Unlock()
	level = l
}

// Debug prints args using fmt.Print semantics and appends a newline.
func Debug(args ...any) { printLog(LevelDebug, "DEBUG", args...) }

// Info prints args using fmt.Print semantics and appends a newline.
func Info(args ...any) { printLog(LevelInfo, "INFO", args...) }

// Warn prints args using fmt.Print semantics and appends a newline.
func Warn(args ...any) { printLog(LevelWarn, "WARN", args...) }

// Error prints args using fmt.Print semantics and appends a newline.
func Error(args ...any) { printLog(LevelError, "ERROR", args...) }

func printLog(l Level, name string, args ...any) {
	mu.Lock()
	defer mu.Unlock()
	if l < level {
		return
	}
	fmt.Printf("[%s] [%s] %s\n", time.Now().Format("2006-01-02 15:04:05"), name, fmt.Sprint(args...))
}
