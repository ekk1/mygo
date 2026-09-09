package fileutil

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestFilesDirsLatest(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	sub := filepath.Join(dir, "sub")
	Write(a, "first")
	Write(b, "second")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatal(err)
	}
	Write(filepath.Join(sub, "nested"), "nested")
	if got := Read(a); got != "first" {
		t.Fatal(got)
	}
	Write(a, "x")
	if got := Read(a); got != "x" {
		t.Fatal("write did not truncate")
	}
	if got := Files(dir); !reflect.DeepEqual(got, []string{a, b}) {
		t.Fatal(got)
	}
	if got := Dirs(dir); !reflect.DeepEqual(got, []string{sub}) {
		t.Fatal(got)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(a, old, old); err != nil {
		t.Fatal(err)
	}
	if got := Latest(dir); got != b {
		t.Fatal(got)
	}
	if err := os.Chtimes(b, old, old); err != nil {
		t.Fatal(err)
	}
	if got := Latest(dir); got != a {
		t.Fatalf("tie should use lexical order: %s", got)
	}
}
func TestGrep(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log")
	long := strings.Repeat("x", 100000) + "ERROR"
	Write(path, "fine\r\nERROR one\rWARN two\nERROR WARN\n"+long)
	if got := Grep(path, []string{"ERROR", "WARN"}, false); !reflect.DeepEqual(got, []string{"ERROR one", "WARN two", "ERROR WARN", long}) {
		t.Fatalf("unexpected matches: %d", len(got))
	}
	if got := Grep(path, []string{"ERROR", "WARN"}, true); !reflect.DeepEqual(got, []string{"ERROR one"}) {
		t.Fatal(got)
	}
	if got := Grep(path, nil, false); len(got) != 0 {
		t.Fatal(got)
	}
	if got := Grep(path, []string{"error"}, false); len(got) != 0 {
		t.Fatal("case insensitive")
	}
	if got := Grep(path, []string{""}, true); !reflect.DeepEqual(got, []string{"fine"}) {
		t.Fatal(got)
	}
}
func TestPanics(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing")
	for name, fn := range map[string]func(){"read": func() { Read(missing) }, "write": func() { Write(filepath.Join(missing, "x"), "") }, "files": func() { Files(missing) }, "dirs": func() { Dirs(missing) }, "latest empty": func() { Latest(dir) }, "grep": func() { Grep(missing, []string{"x"}, false) }} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("expected panic")
				}
			}()
			fn()
		})
	}
}

func TestEmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	if got := Files(dir); len(got) != 0 {
		t.Fatal(got)
	}
	if got := Dirs(dir); len(got) != 0 {
		t.Fatal(got)
	}
}

func TestListsExcludeSymlinks(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	sub := filepath.Join(dir, "sub")
	Write(file, "")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(file, filepath.Join(dir, "file-link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(sub, filepath.Join(dir, "dir-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "missing"), filepath.Join(dir, "broken-link")); err != nil {
		t.Fatal(err)
	}
	if got := Files(dir); !reflect.DeepEqual(got, []string{file}) {
		t.Fatal(got)
	}
	if got := Dirs(dir); !reflect.DeepEqual(got, []string{sub}) {
		t.Fatal(got)
	}
}
