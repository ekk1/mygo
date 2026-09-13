package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ekk1/mygo/utils/kv"
)

var validID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,100}$`)
var errConflict = errors.New("内容已更新，请刷新后重试")
var errNotFound = errors.New("未找到记录")
var errBusy = errors.New("此会话正在生成，请等待或停止后重试")

type provider struct {
	Kind      string   `json:"kind"`
	Protocol  string   `json:"protocol,omitempty"`
	Resources []string `json:"resources,omitempty"`
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	BaseURL   string   `json:"base_url"`
	ProxyURL  string   `json:"proxy_url"`
	APIKey    string   `json:"api_key,omitempty"`
	HasKey    bool     `json:"has_key"`
	ClearKey  bool     `json:"clear_key,omitempty"`
	Models    []string `json:"models"`
}
type configuration struct {
	Revision      int64      `json:"revision"`
	Providers     []provider `json:"providers"`
	SaveResponses bool       `json:"save_responses"`
}
type message struct {
	Usage       *usageInfo      `json:"usage"`
	Request     json.RawMessage `json:"request,omitempty"`
	ID          string          `json:"id"`
	ParentID    string          `json:"parent_id"`
	Role        string          `json:"role"`
	Text        string          `json:"text"`
	Status      string          `json:"status"`
	ProviderID  string          `json:"provider_id,omitempty"`
	ActualModel string          `json:"actual_model,omitempty"`
	Protocol    string          `json:"protocol,omitempty"`
	CreatedAt   string          `json:"created_at"`
	Output      json.RawMessage `json:"output,omitempty"`
	Error       string          `json:"error,omitempty"`
	Files       []string        `json:"files,omitempty"`
	TaskID      string          `json:"task_id,omitempty"`
	AssetIDs    []string        `json:"asset_ids,omitempty"`
}
type session struct {
	Usage     usageSummary `json:"usage"`
	ProfileID string       `json:"profile_id,omitempty"`
	Operation string       `json:"operation,omitempty"`
	ID        string       `json:"id"`
	Title     string       `json:"title"`
	HeadID    string       `json:"head_id"`
	UpdatedAt string       `json:"updated_at"`
	Messages  []message    `json:"messages"`
}
type sessionEntry struct {
	mu      sync.Mutex
	data    session
	busy    bool
	deleted bool
}
type store struct {
	mu       sync.RWMutex
	dir      string
	config   configuration
	sessions map[string]*sessionEntry
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func clone[T any](v T) T {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	var out T
	if err = json.Unmarshal(b, &out); err != nil {
		panic(err)
	}
	return out
}
func loadKV(path string, out any) error {
	var db kv.DB
	if err := db.Load(path); err != nil {
		return err
	}
	v, err := db.Get("value")
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(v), out)
}
func saveKV(path string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var db kv.DB
	db.Set("value", string(b))
	return db.Save(path)
}
func openStore(dir string) (*store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, "sessions"), 0700); err != nil {
		return nil, err
	}
	s := &store{dir: dir, config: configuration{Providers: []provider{}}, sessions: map[string]*sessionEntry{}}
	if err := loadKV(filepath.Join(dir, "config.json"), &s.config); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("load config: %w", err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "sessions"))
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		var v session
		if err := loadKV(filepath.Join(dir, "sessions", e.Name()), &v); err != nil {
			return nil, fmt.Errorf("load %s: %w", e.Name(), err)
		}
		if !validID.MatchString(v.ID) || e.Name() != v.ID+".json" {
			return nil, fmt.Errorf("invalid session file %s", e.Name())
		}
		interrupted := false
		for i := range v.Messages {
			if v.Messages[i].Status == "pending" {
				v.Messages[i].Status = "error"
				if v.Messages[i].TaskID != "" {
					v.Messages[i].Status = "interrupted"
				}
				v.Messages[i].Error = "上次生成因进程退出而中断"
				interrupted = true
			}
		}
		if interrupted {
			v.UpdatedAt = now()
			if err := saveKV(filepath.Join(dir, "sessions", e.Name()), v); err != nil {
				return nil, err
			}
		}
		sessionUsage(&v)
		s.sessions[v.ID] = &sessionEntry{data: v}
	}
	return s, nil
}
func (s *store) configSnapshot(redact bool) configuration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := clone(s.config)
	if c.Providers == nil {
		c.Providers = []provider{}
	}
	for i := range c.Providers {
		c.Providers[i].HasKey = c.Providers[i].APIKey != ""
		if redact {
			c.Providers[i].APIKey = ""
		}
	}
	return c
}
func (s *store) entry(id string) (*sessionEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.sessions[id]
	if !ok {
		return nil, errNotFound
	}
	return e, nil
}
func (s *store) getSession(id string) (session, error) {
	e, err := s.entry(id)
	if err != nil {
		return session{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.deleted {
		return session{}, errNotFound
	}
	return clone(e.data), nil
}

// persistSession requires e.mu. Publish only after the independent KV save succeeds.
func (s *store) persistSession(e *sessionEntry, v session) error {
	sessionUsage(&v)
	if e.deleted {
		return errNotFound
	}
	v.UpdatedAt = now()
	if err := saveKV(filepath.Join(s.dir, "sessions", v.ID+".json"), v); err != nil {
		return err
	}
	e.data = clone(v)
	return nil
}
func (s *store) createSession(title, profileID, operation string) (session, error) {
	v := session{ID: newID(), ProfileID: profileID, Operation: operation, Title: strings.TrimSpace(title), UpdatedAt: now(), Messages: []message{}}
	if v.Title == "" {
		v.Title = "新会话"
	}
	if len(v.Title) > 500 {
		return session{}, fmt.Errorf("标题过长")
	}
	e := &sessionEntry{}
	if err := s.persistSession(e, v); err != nil {
		return session{}, err
	}
	s.mu.Lock()
	s.sessions[v.ID] = e
	s.mu.Unlock()
	return clone(e.data), nil
}
func (s *store) listSessions() []session {
	s.mu.RLock()
	es := make([]*sessionEntry, 0, len(s.sessions))
	for _, e := range s.sessions {
		es = append(es, e)
	}
	s.mu.RUnlock()
	out := make([]session, 0, len(es))
	for _, e := range es {
		e.mu.Lock()
		v := e.data
		v.Messages = nil
		out = append(out, v)
		e.mu.Unlock()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out
}
func (s *store) deleteSession(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.sessions[id]
	if !ok {
		return errNotFound
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.busy {
		return errBusy
	}
	if err := os.Remove(filepath.Join(s.dir, "sessions", id+".json")); err != nil {
		return err
	}
	e.deleted = true
	delete(s.sessions, id)
	return nil
}
func ancestors(v session, id string) ([]message, error) {
	byID := make(map[string]message, len(v.Messages))
	for _, m := range v.Messages {
		byID[m.ID] = m
	}
	path := []message{}
	seen := map[string]bool{}
	for id != "" {
		m, ok := byID[id]
		if !ok || seen[id] {
			return nil, fmt.Errorf("无效的分支节点")
		}
		seen[id] = true
		path = append(path, m)
		id = m.ParentID
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path, nil
}
func (s *store) forkSession(id, node, title string) (session, error) {
	v, err := s.getSession(id)
	if err != nil {
		return session{}, err
	}
	path, err := ancestors(v, node)
	if err != nil {
		return session{}, err
	}
	for _, m := range path {
		if m.Status == "pending" {
			return session{}, errBusy
		}
	}
	if title == "" {
		title = v.Title + " · 分支"
	}
	if len(title) > 500 {
		return session{}, fmt.Errorf("标题过长")
	}
	v = session{ID: newID(), Title: title, HeadID: node, Messages: path, ProfileID: v.ProfileID, Operation: v.Operation}
	e := &sessionEntry{}
	if err = s.persistSession(e, v); err != nil {
		return session{}, err
	}
	s.mu.Lock()
	s.sessions[v.ID] = e
	s.mu.Unlock()
	return clone(e.data), nil
}
