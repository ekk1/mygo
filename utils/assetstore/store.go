// Package assetstore persists binary assets and their editable metadata.
package assetstore

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ekk1/mygo/utils/kv"
)

// DefaultMaxBytes is the per-asset limit used when Add receives zero.
const DefaultMaxBytes int64 = 300 << 20

// ErrNotFound means the ID is invalid, absent, or deleted.
var ErrNotFound = errors.New("asset not found")

// ErrTooLarge means ingestion exceeded its byte limit; no asset was published.
var ErrTooLarge = errors.New("asset exceeds byte limit")

// Source records optional application-defined origin attributes, without secrets.
type Source map[string]string

func copyAsset(a Asset) Asset {
	source := make(Source, len(a.Source))
	for key, value := range a.Source {
		source[key] = value
	}
	a.Source = source
	return a
}

// Asset describes immutable content and its editable display name and favorite flag.
type Asset struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	Favorite    bool   `json:"favorite"`
	CreatedAt   string `json:"created_at"`
	Source      Source `json:"source"`
}

// Store owns one directory. Methods are concurrent-safe; do not copy a Store.
// A directory must be used by only one Store/process at a time.
type Store struct {
	mu    sync.RWMutex
	dir   string
	items map[string]Asset
}

// Open creates the directory if needed and loads committed asset metadata.
// Incomplete staging directories are ignored. Corrupt committed assets return an error.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	s := &Store{dir: dir, items: make(map[string]Asset)}
	for _, entry := range entries {
		if !validID(entry.Name()) {
			continue
		}
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("invalid asset directory %q", entry.Name())
		}
		var db kv.DB
		if err = db.Load(filepath.Join(dir, entry.Name(), "metadata.json")); err != nil {
			return nil, err
		}
		raw, loadErr := db.Get("value")
		if loadErr != nil {
			return nil, loadErr
		}
		var a Asset
		if err = json.Unmarshal([]byte(raw), &a); err != nil {
			return nil, err
		}
		if a.ID != entry.Name() || a.Size < 0 || cleanName(a.Name) != a.Name || a.Name == "" {
			return nil, fmt.Errorf("invalid asset metadata %q", entry.Name())
		}
		info, statErr := os.Lstat(filepath.Join(dir, a.ID, "content"))
		if statErr != nil {
			return nil, statErr
		}
		if !info.Mode().IsRegular() || info.Size() != a.Size {
			return nil, fmt.Errorf("invalid asset content %q", a.ID)
		}
		s.items[a.ID] = a
	}
	return s, nil
}

func validID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, r := range id {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func cleanName(name string) string {
	name = path.Base(strings.ReplaceAll(name, `\`, "/"))
	name = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name))
	if name == "." || name == "/" || !utf8.ValidString(name) {
		return ""
	}
	if len(name) > 240 {
		name = name[:240]
		for !utf8.ValidString(name) {
			name = name[:len(name)-1]
		}
	}
	return name
}

func saveMetadata(dir string, a Asset) error {
	b, err := json.Marshal(a)
	if err != nil {
		return err
	}
	var db kv.DB
	db.Set("value", string(b))
	return db.Save(filepath.Join(dir, "metadata.json"))
}

// Add streams and commits a new asset. Every call creates a distinct, unfavorited
// asset. Zero maxBytes uses DefaultMaxBytes; negative limits and nil readers fail.
// Display names are reduced to a safe basename and never used as filesystem paths.
func (s *Store) Add(name, contentType string, source Source, reader io.Reader, maxBytes int64) (Asset, error) {
	if reader == nil || maxBytes < 0 {
		return Asset{}, fmt.Errorf("reader and nonnegative byte limit are required")
	}
	if maxBytes == 0 {
		maxBytes = DefaultMaxBytes
	}
	// Keep limit+1 representable for the bounded read below.
	if maxBytes == 1<<63-1 {
		return Asset{}, fmt.Errorf("byte limit is too large")
	}
	name = cleanName(name)
	if name == "" {
		name = "asset"
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return Asset{}, err
	}
	a := copyAsset(Asset{ID: hex.EncodeToString(random[:]), Name: name, Source: source, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)})
	stage, err := os.MkdirTemp(s.dir, ".asset-")
	if err != nil {
		return Asset{}, err
	}
	defer os.RemoveAll(stage)
	f, err := os.OpenFile(filepath.Join(stage, "content"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return Asset{}, err
	}
	n, copyErr := io.Copy(f, io.LimitReader(reader, maxBytes+1))
	if copyErr != nil {
		f.Close()
		return Asset{}, copyErr
	}
	if n > maxBytes {
		f.Close()
		return Asset{}, ErrTooLarge
	}
	var prefix [512]byte
	read, readErr := f.ReadAt(prefix[:], 0)
	closeErr := f.Close()
	if readErr != nil && readErr != io.EOF {
		return Asset{}, readErr
	}
	if closeErr != nil {
		return Asset{}, closeErr
	}
	a.Size = n
	a.ContentType = http.DetectContentType(prefix[:read])
	// Preserve useful media hints for formats the standard sniffer cannot detect.
	// Detectable HTML/XML/plain text must never inherit an image or video claim.
	if typ, _, parseErr := mime.ParseMediaType(contentType); parseErr == nil {
		if a.ContentType == "application/octet-stream" || a.ContentType == "application/ogg" && (typ == "audio/ogg" || typ == "video/ogg") {
			a.ContentType = typ
		}
	}
	if err = saveMetadata(stage, a); err != nil {
		return Asset{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err = os.Rename(stage, filepath.Join(s.dir, a.ID)); err != nil {
		return Asset{}, err
	}
	s.items[a.ID] = a
	return copyAsset(a), nil
}

// List returns a snapshot ordered newest first, optionally restricted to favorites.
func (s *Store) List(favoritesOnly bool) []Asset {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]Asset, 0, len(s.items))
	for _, a := range s.items {
		if !favoritesOnly || a.Favorite {
			items = append(items, copyAsset(a))
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt == items[j].CreatedAt {
			return items[i].ID > items[j].ID
		}
		return items[i].CreatedAt > items[j].CreatedAt
	})
	return items
}

// Get returns a metadata snapshot; invalid or missing IDs return ErrNotFound.
func (s *Store) Get(id string) (Asset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.items[id]
	if !ok {
		return Asset{}, ErrNotFound
	}
	return copyAsset(a), nil
}

// OpenContent atomically reads metadata and opens the corresponding content file.
// The caller must close the file. Already open handles follow OS deletion semantics.
func (s *Store) OpenContent(id string) (Asset, *os.File, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.items[id]
	if !ok {
		return Asset{}, nil, ErrNotFound
	}
	f, err := os.Open(filepath.Join(s.dir, a.ID, "content"))
	if err != nil {
		return Asset{}, nil, err
	}
	return copyAsset(a), f, nil
}

// Update persists a new display name and/or favorite flag; nil fields are unchanged.
// Empty names are rejected. Failed writes leave the in-memory metadata unchanged.
func (s *Store) Update(id string, name *string, favorite *bool) (Asset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.items[id]
	if !ok {
		return Asset{}, ErrNotFound
	}
	if name != nil {
		a.Name = cleanName(*name)
		if a.Name == "" {
			return Asset{}, fmt.Errorf("asset name is required")
		}
	}
	if favorite != nil {
		a.Favorite = *favorite
	}
	if err := saveMetadata(filepath.Join(s.dir, a.ID), a); err != nil {
		return Asset{}, err
	}
	s.items[id] = a
	return copyAsset(a), nil
}

// Delete removes an asset from the index and deletes its content. Missing IDs return
// ErrNotFound. If final directory cleanup fails, the asset is already unpublished.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.items[id]
	if !ok {
		return ErrNotFound
	}
	tombstone := filepath.Join(s.dir, ".deleted-"+a.ID)
	if err := os.Rename(filepath.Join(s.dir, a.ID), tombstone); err != nil {
		return err
	}
	delete(s.items, id)
	return os.RemoveAll(tombstone)
}
