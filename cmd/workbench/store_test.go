package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreSeparatePersistenceAndFork(t *testing.T) {
	dir := t.TempDir()
	s, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := s.configSnapshot(false)
	cfg.Providers = []provider{{ID: "p", Name: "Local", APIKey: "secret", BaseURL: "http://localhost:1234/v1"}}
	cfg.Models = []model{{ID: "m", Name: "Alias", Featured: true, Routes: []route{{ProviderID: "p", Model: "native", Protocol: "responses"}}}}
	if _, err = s.saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	session, err := s.createSession("Test")
	if err != nil {
		t.Fatal(err)
	}
	e, _ := s.entry(session.ID)
	e.mu.Lock()
	session.Messages = []message{{ID: "one", Role: "user", Text: "first"}, {ID: "two", ParentID: "one", Role: "assistant", Text: "answer"}, {ID: "other", ParentID: "one", Role: "assistant", Text: "excluded"}}
	session.HeadID = "other"
	err = s.persistSession(e, session)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	fork, err := s.forkSession(session.ID, "two", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(fork.Messages) != 2 || fork.HeadID != "two" {
		t.Fatalf("fork=%+v", fork)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	if string(before) != string(after) {
		t.Fatal("session write changed config")
	}
	restored, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := restored.getSession(fork.ID)
	if err != nil || len(got.Messages) != 2 {
		t.Fatalf("restore=%+v %v", got, err)
	}
	redacted := restored.configSnapshot(true)
	if redacted.Providers[0].APIKey != "" || !redacted.Providers[0].HasKey {
		t.Fatal("key leaked or lost")
	}
	redacted.Providers[0].Name = "changed"
	if _, err = restored.saveConfig(redacted); err != nil {
		t.Fatal(err)
	}
	if restored.configSnapshot(false).Providers[0].APIKey != "secret" {
		t.Fatal("empty edit cleared key")
	}
}

func TestConfigRejectsInvalidMappingAndStaleRevision(t *testing.T) {
	s, err := openStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := s.configSnapshot(false)
	bad := cfg
	bad.Models = []model{{ID: "m", Name: "Model", Featured: true, Routes: []route{{ProviderID: "missing", Model: "x", Protocol: "responses"}}}}
	if _, err = s.saveConfig(bad); err == nil {
		t.Fatal("missing provider accepted")
	}
	if _, err = s.saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err = s.saveConfig(cfg); err == nil {
		t.Fatal("stale config accepted")
	}
}

func TestStoreFailedSaveDoesNotPublish(t *testing.T) {
	s, err := openStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	old := s.configSnapshot(false)
	if err = os.Mkdir(filepath.Join(s.dir, "config.json"), 0700); err != nil {
		t.Fatal(err)
	}
	next := old
	next.SaveResponses = true
	if _, err = s.saveConfig(next); err == nil {
		t.Fatal("expected filesystem failure")
	}
	if s.configSnapshot(false).SaveResponses {
		t.Fatal("failed state published")
	}
}

func TestDeletedSessionCannotBeResurrectedByStaleEntry(t *testing.T) {
	s, err := openStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.createSession("remove")
	if err != nil {
		t.Fatal(err)
	}
	e, _ := s.entry(v.ID)
	if err = s.deleteSession(v.ID); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	err = s.persistSession(e, v)
	e.mu.Unlock()
	if err == nil {
		t.Fatal("stale entry resurrected deleted file")
	}
	if _, err = os.Stat(filepath.Join(s.dir, "sessions", v.ID+".json")); !os.IsNotExist(err) {
		t.Fatalf("deleted file exists: %v", err)
	}
}
func TestExistingStoreDirectoryIsPrivate(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := openStore(dir); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0700 {
		t.Fatalf("mode=%o", st.Mode().Perm())
	}
}
