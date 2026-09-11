package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProfileVendorDefaultsAndSecrets(t *testing.T) {
	s, err := openStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := s.configSnapshot(false)
	if err := json.Unmarshal([]byte(`[{"id":"claude","name":"Claude main","kind":"anthropic","api_key":"one"},{"id":"google","name":"Gemini main","kind":"gemini","api_key":"two"}]`), &cfg.Providers); err != nil {
		t.Fatal(err)
	}
	saved, err := s.saveConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Providers[0].BaseURL != "https://api.anthropic.com/v1" {
		t.Fatalf("wrong vendor default: %s", saved.Providers[0].BaseURL)
	}
	b, _ := json.Marshal(saved)
	if strings.Contains(string(b), `"api_key"`) {
		t.Fatal("saved response leaks credentials")
	}
	if !strings.Contains(string(b), `"kind":"anthropic"`) {
		t.Fatal("profile vendor lost")
	}
	saved.Providers[1].Name = "Updated"
	if _, err = s.saveConfig(saved); err != nil {
		t.Fatal(err)
	}
	if s.configSnapshot(false).Providers[0].APIKey != "one" || s.configSnapshot(false).Providers[1].APIKey != "two" {
		t.Fatal("profile keys mixed")
	}
}
func TestProfileRejectsUnknownVendor(t *testing.T) {
	s, _ := openStore(t.TempDir())
	cfg := s.configSnapshot(false)
	json.Unmarshal([]byte(`[{"id":"bad","name":"Bad","kind":"unknown","api_key":"key"}]`), &cfg.Providers)
	if _, err := s.saveConfig(cfg); err == nil {
		t.Fatal("unknown vendor accepted")
	}
}
