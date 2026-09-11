package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestChatUsesMappedRouteAndOnlyAncestorPath(t *testing.T) {
	var sent map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Errorf("path=%s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing auth")
		}
		json.NewDecoder(r.Body).Decode(&sent)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"Hello\"}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"Hello\"}]}]}}\n\n")
	}))
	defer upstream.Close()
	a, s, err := newApp(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg := a.store.configSnapshot(false)
	cfg.Providers = []provider{{ID: "p", Name: "P", APIKey: "secret", BaseURL: upstream.URL + "/v1", ProxyURL: "-"}}
	cfg.Models = []model{{ID: "alias", Name: "Friendly", Featured: true, Routes: []route{{ProviderID: "p", Model: "actual", Protocol: "responses"}}}}
	if _, err = a.store.saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	v, _ := a.store.createSession("Chat")
	e, _ := a.store.entry(v.ID)
	e.mu.Lock()
	v.Messages = []message{{ID: "root", Role: "user", Text: "ancestor", Status: "complete"}, {ID: "sibling", ParentID: "root", Role: "assistant", Text: "DO_NOT_SEND", Status: "complete"}}
	a.store.persistSession(e, v)
	e.mu.Unlock()
	req := httptest.NewRequest("POST", "http://localhost/api/sessions/"+v.ID+"/messages", strings.NewReader(`{"parent_id":"root","model_id":"alias","text":"question","tools":{"web":true},"options":{"temperature":0}}`))
	req.Header.Set("X-Workbench-Request", "1")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"type":"done"`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	b, _ := json.Marshal(sent)
	if sent["model"] != "actual" || bytes.Contains(b, []byte("DO_NOT_SEND")) || !bytes.Contains(b, []byte("ancestor")) {
		t.Fatalf("sent=%s", b)
	}
	if sent["temperature"] != float64(0) {
		t.Fatal("explicit zero lost")
	}
	got, _ := a.store.getSession(v.ID)
	if got.Messages[len(got.Messages)-1].Text != "Hello" || got.Messages[len(got.Messages)-1].Status != "complete" {
		t.Fatalf("session=%+v", got)
	}
	requestLog := httptest.NewRecorder()
	s.ServeHTTP(requestLog, httptest.NewRequest("GET", "http://localhost/api/logs", nil))
	if !strings.Contains(requestLog.Body.String(), "responses.stream") {
		t.Fatal(requestLog.Body.String())
	}
}
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

func TestChatCancellationPersistsPartialAndBlocksConcurrentMutation(t *testing.T) {
	started := make(chan struct{})
	flushed := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n")
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	}))
	defer upstream.Close()
	a, s, err := newApp(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg := a.store.configSnapshot(false)
	cfg.Providers = []provider{{ID: "p", Name: "p", APIKey: "fake", BaseURL: upstream.URL + "/v1", ProxyURL: "-"}}
	cfg.Models = []model{{ID: "m", Name: "m", Featured: true, Routes: []route{{ProviderID: "p", Model: "m", Protocol: "responses"}}}}
	if _, err = a.store.saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	v, _ := a.store.createSession("cancel")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest("POST", "http://localhost/api/sessions/"+v.ID+"/messages", strings.NewReader(`{"model_id":"m","text":"hello"}`)).WithContext(ctx)
	req.Header.Set("X-Workbench-Request", "1")
	w := &signalRecorder{ResponseRecorder: httptest.NewRecorder(), flushed: flushed}
	done := make(chan struct{})
	go func() { s.ServeHTTP(w, req); close(done) }()
	<-started
	<-flushed
	if err = a.store.deleteSession(v.ID); !errors.Is(err, errBusy) {
		t.Fatalf("delete during generation = %v", err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not finish")
	}
	got, _ := a.store.getSession(v.ID)
	m := got.Messages[len(got.Messages)-1]
	if m.Status != "cancelled" || m.Text != "partial" {
		t.Fatalf("cancelled message=%+v", m)
	}
	reloaded, err := openStore(a.store.dir)
	if err != nil {
		t.Fatal(err)
	}
	got, _ = reloaded.getSession(v.ID)
	if got.Messages[len(got.Messages)-1].Text != "partial" {
		t.Fatal("partial not saved")
	}
}

// Signal after the delta reaches the actual NDJSON writer, not merely after upstream Flush.
type signalRecorder struct {
	*httptest.ResponseRecorder
	flushed chan struct{}
	once    sync.Once
}

func (w *signalRecorder) Flush() {
	w.ResponseRecorder.Flush()
	if strings.Contains(w.Body.String(), `"type":"delta"`) {
		w.once.Do(func() { close(w.flushed) })
	}
}

func TestOptionsCannotBypassModelMapping(t *testing.T) {
	a, s, err := newApp(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	v, _ := a.store.createSession("bypass")
	r := httptest.NewRequest("POST", "http://localhost/api/sessions/"+v.ID+"/messages", strings.NewReader(`{"model_id":"bad","text":"hello","options":{"model":"hidden"}}`))
	r.Header.Set("X-Workbench-Request", "1")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
	got, _ := a.store.getSession(v.ID)
	if len(got.Messages) != 0 {
		t.Fatal("invalid request changed session")
	}
}

func TestResponseContextSwitchesModelAndKeepsPartialText(t *testing.T) {
	path := []message{{ID: "a", Role: "assistant", Text: "earlier answer", ProviderID: "p", ActualModel: "old-model", Protocol: "responses", Output: json.RawMessage(`[{"type":"code_interpreter_call","id":"old-tool"}]`)}, {ID: "b", Role: "assistant", Text: "partial answer", ProviderID: "p", ActualModel: "new-model", Protocol: "responses", Status: "cancelled", Output: json.RawMessage(`[]`)}}
	req, err := buildResponseRequest(chatInput{Text: "continue"}, path, route{ProviderID: "p", Model: "new-model", Protocol: "responses"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(req)
	if strings.Contains(string(b), "old-tool") || !strings.Contains(string(b), "earlier answer") || !strings.Contains(string(b), "partial answer") {
		t.Fatalf("context=%s", b)
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
