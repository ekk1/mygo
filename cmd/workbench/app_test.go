package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCrossOriginRejected(t *testing.T) {
	_, s, err := newApp(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := httptest.NewRequest("POST", "http://localhost/api/sessions", strings.NewReader(`{"title":"bad"}`))
	r.Header.Set("Origin", "https://attacker.example")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("status=%d", w.Code)
	}
}

func TestLocalHostGuardRejectsRebinding(t *testing.T) {
	_, s, err := newApp(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "http://attacker.example/api/config", nil))
	if w.Code != 403 {
		t.Fatalf("status=%d", w.Code)
	}
}
func TestJSONEncodingFailureDoesNotReturnSuccess(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, 200, map[string]any{"bad": json.RawMessage(`[DONE]`)})
	if w.Code != 500 || !json.Valid(w.Body.Bytes()) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}
