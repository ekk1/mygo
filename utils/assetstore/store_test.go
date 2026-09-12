package assetstore

import (
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestPersistenceAndFavorites(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.Add(`../../unsafe\name.txt`, "text/plain", Source{"kind": "upload", "session_id": "s1"}, strings.NewReader("original bytes\x00"), 100)
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "name.txt" || a.Favorite || a.Size != 15 || a.Source["session_id"] != "s1" {
		t.Fatalf("unexpected metadata: %+v", a)
	}
	if len(s.List(true)) != 0 {
		t.Fatal("uploads must not start favorited")
	}
	name, favorite := "renamed.txt", true
	if _, err = s.Update(a.ID, &name, &favorite); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	items := s.List(true)
	if len(items) != 1 || items[0].Name != name {
		t.Fatalf("persisted favorites: %+v", items)
	}
	_, f, err := s.OpenContent(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(f)
	f.Close()
	if err != nil || string(b) != "original bytes\x00" {
		t.Fatalf("content: %q %v", b, err)
	}
	if err = s.Delete(a.ID); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted asset: %v", err)
	}
}

func TestRejectOversizeTraversalAndUnsafeMedia(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Add("large", "", Source{}, strings.NewReader("12345"), 4); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("size error: %v", err)
	}
	if len(s.List(false)) != 0 {
		t.Fatal("oversized upload was published")
	}
	for _, id := range []string{"../outside", "/etc/passwd", "", strings.Repeat("a", 33)} {
		if _, _, err = s.OpenContent(id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("invalid ID %q: %v", id, err)
		}
		if err = s.Delete(id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("invalid delete %q: %v", id, err)
		}
	}
	a, err := s.Add("pretend.png", "image/png", Source{}, strings.NewReader("<!doctype html><script>alert(1)</script>"), 100)
	if err != nil {
		t.Fatal(err)
	}
	if a.ContentType != "text/html; charset=utf-8" {
		t.Fatalf("claimed type overrode detected HTML: %q", a.ContentType)
	}
	if _, err = s.Update(a.ID, new(string), nil); err == nil {
		t.Fatal("empty name accepted")
	}
}

func TestConcurrentIngestUpdateReadDelete(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a, err := s.Add("asset.txt", "text/plain", Source{}, strings.NewReader("payload"), 100)
			if err != nil {
				t.Error(err)
				return
			}
			var readers sync.WaitGroup
			for range 4 {
				readers.Add(1)
				go func() {
					defer readers.Done()
					favorite := true
					_, updateErr := s.Update(a.ID, nil, &favorite)
					if updateErr != nil && !errors.Is(updateErr, ErrNotFound) {
						t.Error(updateErr)
					}
					_, file, openErr := s.OpenContent(a.ID)
					if errors.Is(openErr, ErrNotFound) {
						return
					}
					if openErr != nil {
						t.Error(openErr)
						return
					}
					defer file.Close()
					b, readErr := io.ReadAll(file)
					if readErr != nil || string(b) != "payload" {
						t.Errorf("concurrent content %q: %v", b, readErr)
					}
					_ = s.List(true)
				}()
			}
			if err = s.Delete(a.ID); err != nil {
				t.Error(err)
			}
			readers.Wait()
		}()
	}
	wg.Wait()
	if len(s.List(false)) != 0 {
		t.Fatal("deleted metadata remained")
	}
}

func TestFailedWriteDoesNotPublishAsset(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Add("bad", "", Source{}, errorReader{}, 100)
	if !errors.Is(err, io.ErrUnexpectedEOF) || len(s.List(false)) != 0 {
		t.Fatalf("failed read published asset: %v", err)
	}
	if info, err := os.Stat(s.dir); err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("directory permissions: %v %v", info, err)
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestOriginSnapshotsAreIndependent(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source := Source{"origin": "original"}
	a, err := s.Add("file.txt", "text/plain", source, strings.NewReader("hello"), 10)
	if err != nil {
		t.Fatal(err)
	}
	source["origin"] = "caller mutation"
	a.Source["origin"] = "return mutation"
	item, err := s.Get(a.ID)
	if err != nil || item.Source["origin"] != "original" {
		t.Fatalf("aliased origin: %+v %v", item, err)
	}
	item.Source["origin"] = "get mutation"
	list := s.List(false)
	list[0].Source["origin"] = "list mutation"
	item, file, err := s.OpenContent(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
	if item.Source["origin"] != "original" {
		t.Fatalf("snapshot mutation changed origin: %+v", item)
	}
}

func TestOggKeepsAudioAndVideoClassification(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range []string{"audio/ogg", "video/ogg"} {
		a, err := s.Add("recording.ogg", typ, nil, strings.NewReader("OggS\x00container-bytes"), 100)
		if err != nil || a.ContentType != typ {
			t.Fatalf("Ogg classification = %q, %v; want %q", a.ContentType, err, typ)
		}
	}
}
