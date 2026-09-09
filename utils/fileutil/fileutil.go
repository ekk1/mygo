// Package fileutil provides file helpers that panic on I/O errors.
package fileutil

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ekk1/mygo/utils/strutil"
)

// Files returns lexically sorted regular file paths directly inside dir.
// It excludes directories, symbolic links and special files; errors panic.
func Files(dir string) []string { return list(dir, false) }

// Dirs returns lexically sorted directory paths directly inside dir.
// It does not follow symbolic links; errors panic.
func Dirs(dir string) []string { return list(dir, true) }

func list(dir string, directories bool) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		panic(err)
	}
	paths := make([]string, 0)
	for _, entry := range entries {
		if directories {
			if entry.IsDir() {
				paths = append(paths, filepath.Join(dir, entry.Name()))
			}
		} else {
			info, err := entry.Info()
			if err != nil {
				panic(err)
			}
			if info.Mode().IsRegular() {
				paths = append(paths, filepath.Join(dir, entry.Name()))
			}
		}
	}
	return paths
}

// Read reads the entire file as a string, panicking on errors.
func Read(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	return string(data)
}

// Write creates or truncates a file using mode 0644 (before umask).
// Parent directories must exist. Existing permissions are preserved. Errors panic.
func Write(path, content string) {
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		panic(err)
	}
}

// Latest returns the most recently modified regular file directly inside dir.
// Ties use lexical path order. No regular files or any I/O error causes a panic.
func Latest(dir string) string {
	var latest string
	var modified time.Time
	for _, path := range Files(dir) {
		info, err := os.Stat(path)
		if err != nil {
			panic(err)
		}
		if latest == "" || info.ModTime().After(modified) {
			latest, modified = path, info.ModTime()
		}
	}
	if latest == "" {
		panic(fmt.Errorf("fileutil.Latest %q: %w", dir, os.ErrNotExist))
	}
	return latest
}

// Grep returns lines containing any case-sensitive literal keyword.
// once stops matching after the first matching line. Empty keywords match nothing;
// an empty keyword string matches every line. The whole file is read; errors panic.
func Grep(path string, keywords []string, once bool) []string {
	lines := strutil.Lines(Read(path))
	matches := make([]string, 0)
	for _, line := range lines {
		for _, keyword := range keywords {
			if strings.Contains(line, keyword) {
				matches = append(matches, line)
				if once {
					return matches
				}
				break
			}
		}
	}
	return matches
}
