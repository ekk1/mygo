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
	"testing"
	"time"
)

func TestNativeConversationPreservesProviderHistory(t *testing.T) {
	cases := []struct{ operation, current, previous, output, want string }{
		{"responses.create", `{"model":"m","input":"next","service_tier":"flex"}`, `{"model":"m","input":"first"}`, `{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]}]}`, "output_text"},
		{"messages.create", `{"model":"m","messages":[{"role":"user","content":"next"}],"max_tokens":100}`, `{"model":"m","messages":[{"role":"user","content":"first"}]}`, `{"content":[{"type":"text","text":"answer"}]}`, "answer"},
		{"content.generate", `{"model":"m","contents":[{"role":"user","parts":[{"text":"next"}]}]}`, `{"model":"m","contents":[{"role":"user","parts":[{"text":"first"}]}]}`, `{"candidates":[{"content":{"role":"model","parts":[{"text":"answer","thoughtSignature":"keep-signature"}]}}]}`, "keep-signature"},
	}
	for _, tc := range cases {
		t.Run(tc.operation, func(t *testing.T) {
			path := []message{{Role: "user", Text: "first", Status: "complete", ActualModel: "m", Request: json.RawMessage(tc.previous)}, {Role: "assistant", Text: "answer", ActualModel: "m", Status: "complete", Output: json.RawMessage(tc.output)}}
			got, err := conversationRequest(tc.operation, json.RawMessage(tc.current), path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(got), tc.want) || !strings.Contains(string(got), "first") || !strings.Contains(string(got), "next") {
				t.Fatalf("lost history: %s", got)
			}
			if tc.operation == "responses.create" && !strings.Contains(string(got), `"service_tier":"flex"`) {
				t.Fatal("lost tier")
			}
		})
	}
}
func TestConversationRejectsStreamBeforeDispatch(t *testing.T) {
	if _, err := conversationRequest("responses.create", json.RawMessage(`{"model":"m","input":"hello","stream":true}`), nil); err == nil {
		t.Fatal("stream request accepted by buffered conversation")
	}
}

func TestConversationPreviewDoesNotPersistAndIsBoundToProfile(t *testing.T) {
	a, s, err := newApp(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg := a.store.configSnapshot(false)
	cfg.Providers = []provider{{ID: "p", Name: "p", Kind: "openai", APIKey: "key", BaseURL: "https://example.invalid/v1", ProxyURL: "-"}, {ID: "q", Name: "q", Kind: "openai", APIKey: "other", BaseURL: "https://example.invalid/v1", ProxyURL: "-"}}
	cfg, err = a.store.saveConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"provider_id": "p", "operation": "responses.create", "params": map[string]any{"model": "native-model", "input": strings.Repeat("x", 6000), "service_tier": "flex"}, "revision": cfg.Revision, "expected_head": "", "parent_id": "", "text": "question"}
	invoke := func(path string) *httptest.ResponseRecorder {
		b, _ := json.Marshal(payload)
		r := httptest.NewRequest("POST", "http://localhost"+path, bytes.NewReader(b))
		r.Header.Set("X-Workbench-Request", "1")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	w := invoke("/api/sessions/new/native?preview=1")
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if len(a.store.listSessions()) != 0 {
		t.Fatal("preview created a session")
	}
	if !strings.Contains(w.Body.String(), strings.Repeat("x", 6000)) {
		t.Fatal("preview truncated transmitted data")
	}
	v, _ := a.store.createSession("test", "", "")
	e, _ := a.store.entry(v.ID)
	e.mu.Lock()
	v.ProfileID = "q"
	v.Operation = "responses.create"
	a.store.persistSession(e, v)
	e.mu.Unlock()
	if w = invoke("/api/sessions/" + v.ID + "/native?preview=1"); w.Code != 400 {
		t.Fatalf("cross-profile session accepted %d", w.Code)
	}
	payload["revision"] = cfg.Revision - 1
	if w = invoke("/api/sessions/new/native?preview=1"); w.Code != 409 {
		t.Fatalf("stale settings accepted %d", w.Code)
	}
}

func TestConversationCancelRetainsErrorAndBlocksDelete(t *testing.T) {
	started := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
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
	cfg.Providers = []provider{{ID: "p", Name: "p", Kind: "openai", APIKey: "key", BaseURL: upstream.URL + "/v1", ProxyURL: "-"}}
	cfg, err = a.store.saveConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	v, _ := a.store.createSession("cancel", "", "")
	e, _ := a.store.entry(v.ID)
	e.mu.Lock()
	v.ProfileID = "p"
	v.Operation = "responses.create"
	a.store.persistSession(e, v)
	e.mu.Unlock()
	payload := map[string]any{"provider_id": "p", "operation": "responses.create", "params": map[string]any{"model": "m", "input": "wait"}, "revision": cfg.Revision, "expected_head": "", "parent_id": "", "text": "wait"}
	b, _ := json.Marshal(payload)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := httptest.NewRequest("POST", "http://localhost/api/sessions/"+v.ID+"/native", bytes.NewReader(b)).WithContext(ctx)
	r.Header.Set("X-Workbench-Request", "1")
	done := make(chan struct{})
	go func() { defer close(done); s.ServeHTTP(httptest.NewRecorder(), r) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not start")
	}
	if err := a.store.deleteSession(v.ID); !errors.Is(err, errBusy) {
		t.Fatalf("delete during send: %v", err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not finish")
	}
	restored, err := openStore(a.store.dir)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := restored.getSession(v.ID)
	last := got.Messages[len(got.Messages)-1]
	if last.Status != "cancelled" || last.Error == "" {
		t.Fatalf("cancel not persisted: %+v", last)
	}
}

func TestConversationModelChangeKeepsUserAttachmentsOnly(t *testing.T) {
	path := []message{
		{Role: "user", Status: "complete", ActualModel: "old", Request: json.RawMessage(`{"input":[{"role":"user","content":[{"type":"input_file","file_id":"file-owned"}]}]}`)},
		{Role: "assistant", Status: "complete", ActualModel: "old", Text: "answer", Output: json.RawMessage(`{"output":[{"type":"function_call","call_id":"old-tool"}]}`)},
	}
	got, err := conversationRequest("responses.create", json.RawMessage(`{"model":"new","input":"next"}`), path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "file-owned") || strings.Contains(string(got), "old-tool") || !strings.Contains(string(got), "answer") {
		t.Fatalf("incorrect model transition: %s", got)
	}
}
