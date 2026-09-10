package kv

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

func TestStringsAndTypes(t *testing.T) {
	var db DB
	if _, err := db.Get("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	db.Set("", "")
	if v, err := db.Get(""); v != "" || err != nil {
		t.Fatal(v, err)
	}
	if err := db.HSet("h", "f", "v"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Get("h"); !errors.Is(err, ErrWrongType) {
		t.Fatal(err)
	}
	if err := db.HSet("", "f", "v"); !errors.Is(err, ErrWrongType) {
		t.Fatal(err)
	}
	if _, err := db.RPush("h", "v"); !errors.Is(err, ErrWrongType) {
		t.Fatal(err)
	}
	if _, err := db.LPop("h"); !errors.Is(err, ErrWrongType) {
		t.Fatal(err)
	}
	if _, err := db.LRange("h", 0, -1); !errors.Is(err, ErrWrongType) {
		t.Fatal(err)
	}
	if _, err := db.HGet("", "f"); !errors.Is(err, ErrWrongType) {
		t.Fatal(err)
	}
	if _, err := db.HGetAll(""); !errors.Is(err, ErrWrongType) {
		t.Fatal(err)
	}
	if _, err := db.HKeys(""); !errors.Is(err, ErrWrongType) {
		t.Fatal(err)
	}
	db.Set("h", "replaced")
	if v, err := db.Get("h"); v != "replaced" || err != nil {
		t.Fatal(v, err)
	}
	if got := db.Keys(); !reflect.DeepEqual(got, []string{"", "h"}) {
		t.Fatal(got)
	}
	if n := db.Delete("h", "h", "absent", ""); n != 2 {
		t.Fatal(n)
	}
	if len(db.Keys()) != 0 {
		t.Fatal(db.Keys())
	}
	var other DB
	db.Set("x", "x")
	if len(other.Keys()) != 0 {
		t.Fatal("instances share state")
	}
}

func TestHashCopiesAndMissing(t *testing.T) {
	var db DB
	if _, err := db.HGet("h", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if m, err := db.HGetAll("h"); len(m) != 0 || err != nil {
		t.Fatal(m, err)
	}
	if ks, err := db.HKeys("h"); len(ks) != 0 || err != nil {
		t.Fatal(ks, err)
	}
	for _, f := range []string{"b", "a", "b"} {
		if err := db.HSet("h", f, f); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.HSet("h", "b", "new"); err != nil {
		t.Fatal(err)
	}
	m, err := db.HGetAll("h")
	if err != nil || !reflect.DeepEqual(m, map[string]string{"a": "a", "b": "new"}) {
		t.Fatal(m, err)
	}
	m["b"] = "changed"
	if v, err := db.HGet("h", "b"); v != "new" || err != nil {
		t.Fatal(v, err)
	}
	if _, err := db.HGet("h", "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if ks, err := db.HKeys("h"); err != nil || !reflect.DeepEqual(ks, []string{"a", "b"}) {
		t.Fatal(ks, err)
	}
	if db.Delete("h") != 1 {
		t.Fatal("delete hash")
	}
	if _, err := db.RPush("h", "ok"); err != nil {
		t.Fatal(err)
	}
}

func TestListRangesAndPop(t *testing.T) {
	var db DB
	if n, err := db.RPush("l"); n != 0 || err != nil || len(db.Keys()) != 0 {
		t.Fatal(n, err)
	}
	if _, err := db.LPop("l"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if v, err := db.LRange("l", 0, -1); len(v) != 0 || err != nil {
		t.Fatal(v, err)
	}
	values := []string{"a", "b", "c"}
	if n, err := db.RPush("l", values...); n != 3 || err != nil {
		t.Fatal(n, err)
	}
	values[0] = "changed"
	for _, tc := range []struct {
		start, stop int
		want        []string
	}{
		{0, -1, []string{"a", "b", "c"}}, {-2, -1, []string{"b", "c"}}, {-99, 99, []string{"a", "b", "c"}},
		{1, 1, []string{"b"}}, {2, 1, []string{}}, {3, 9, []string{}}, {0, -4, []string{}},
	} {
		got, err := db.LRange("l", tc.start, tc.stop)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%+v: %v %v", tc, got, err)
		}
		if len(got) > 0 {
			got[0] = "mutated"
		}
	}
	for _, want := range []string{"a", "b", "c"} {
		if v, err := db.LPop("l"); v != want || err != nil {
			t.Fatal(v, err)
		}
	}
	if len(db.Keys()) != 0 {
		t.Fatal("empty list key retained")
	}
	if _, err := db.RPush("l", "new"); err != nil {
		t.Fatal(err)
	}
	db.Set("l", "string")
	if v, err := db.Get("l"); v != "string" || err != nil {
		t.Fatal(v, err)
	}
}

func TestPersistence(t *testing.T) {
	var db DB
	db.Set("s", "你好\n")
	if err := db.HSet("h", "f", "v"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.RPush("l", "a", "b"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "db.json")
	if err := db.Save(path); err != nil {
		t.Fatal(err)
	}
	var loaded DB
	loaded.Set("old", "old")
	if err := loaded.Load(path); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded.Keys(), []string{"h", "l", "s"}) {
		t.Fatal(loaded.Keys())
	}
	if v, err := loaded.Get("s"); v != "你好\n" || err != nil {
		t.Fatal(v, err)
	}
	if v, err := loaded.HGet("h", "f"); v != "v" || err != nil {
		t.Fatal(v, err)
	}
	if v, err := loaded.LRange("l", 0, -1); !reflect.DeepEqual(v, []string{"a", "b"}) || err != nil {
		t.Fatal(v, err)
	}
	for _, bad := range []string{`{`, `null`, `{"types":{"x":"bogus"}}`, `{"types":{"x":"string"}}`, `{"strings":{"x":"v"}}`, `{"types":{"x":"hash"},"hashes":{"x":null}}`, `{"types":{"x":"list"},"lists":{"x":[]}}`, `{"types":{"x":"string"},"strings":{"x":"v"},"lists":{"x":["v"]}}`} {
		if err := os.WriteFile(path, []byte(bad), 0600); err != nil {
			t.Fatal(err)
		}
		if err := loaded.Load(path); err == nil {
			t.Fatalf("accepted %s", bad)
		}
		if v, err := loaded.Get("s"); v != "你好\n" || err != nil {
			t.Fatal("failed load changed data", v, err)
		}
	}
	if err := loaded.Load(path + "/missing"); err == nil {
		t.Fatal("missing load")
	}
	if err := loaded.Save(path + "/missing"); err == nil {
		t.Fatal("bad save")
	}
	if err := loaded.Save(filepath.Dir(path)); err == nil {
		t.Fatal("rename over directory")
	}
	var empty DB
	if err := empty.Save(path); err != nil {
		t.Fatal(err)
	}
	if err := loaded.Load(path); err != nil || len(loaded.Keys()) != 0 {
		t.Fatal(err, loaded.Keys())
	}
	if err := loaded.HSet("new", "f", "v"); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentOperations(t *testing.T) {
	var db DB
	path := filepath.Join(t.TempDir(), "db.json")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 80; j++ {
				key := fmt.Sprint(i, "/", j)
				db.Set(key, key)
				db.Get(key)
				db.Keys()
				db.Delete(key)
				if err := db.HSet("h", key, key); err != nil {
					t.Error(err)
				}
				db.HGet("h", key)
				db.HGetAll("h")
				db.HKeys("h")
				if _, err := db.RPush("l", key); err != nil {
					t.Error(err)
				}
				db.LRange("l", 0, -1)
			}
		}(i)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			if err := db.Save(path); err != nil {
				t.Error(err)
			}
			var copy DB
			if err := copy.Load(path); err != nil {
				t.Error(err)
			}
		}
	}()
	wg.Wait()
	m, err := db.HGetAll("h")
	if err != nil || len(m) != 640 {
		t.Fatal(len(m), err)
	}
	list, err := db.LRange("l", 0, -1)
	if err != nil || len(list) != 640 {
		t.Fatal(len(list), err)
	}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 80; j++ {
				if _, err := db.LPop("l"); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	if _, err := db.LPop("l"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}
