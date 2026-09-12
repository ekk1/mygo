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
		{"responses.create", `{"model":"m","input":"next","service_tier":"flex"}`, `{"model":"m","input":"first"}`, `{"output":[{"type":"reasoning","id":"rs_1","encrypted_content":"encrypted-reasoning","summary":[]},{"type":"image_generation_call","id":"ig_1","result":"image-bytes"},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]}]}`, "encrypted-reasoning"},
		{"messages.create", `{"model":"m","messages":[{"role":"user","content":"next"}],"max_tokens":100}`, `{"model":"m","messages":[{"role":"user","content":"first"}]}`, `{"content":[{"type":"thinking","thinking":"reason","signature":"signed-thinking"},{"type":"server_tool_use","id":"srvtoolu_1","name":"web_search","input":{"query":"q"}},{"type":"web_search_tool_result","tool_use_id":"srvtoolu_1","content":[{"type":"web_search_result","encrypted_content":"encrypted-result"}]},{"type":"text","text":"answer"}]}`, "encrypted-result"},
		{"content.generate", `{"model":"m","contents":[{"role":"user","parts":[{"text":"next"}]}]}`, `{"model":"m","contents":[{"role":"user","parts":[{"text":"first"}]}]}`, `{"candidates":[{"content":{"role":"model","parts":[{"text":"answer","thoughtSignature":"keep-signature"},{"inlineData":{"mimeType":"image/png","data":"image-data"},"thoughtSignature":"image-signature"}]}}]}`, "image-signature"},
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

func TestNativeTextExcludesGeminiThoughtParts(t *testing.T) {
	var response any
	if err := json.Unmarshal([]byte(`{"candidates":[{"content":{"role":"model","parts":[{"thought":true,"text":"private reasoning"},{"text":"visible answer","thoughtSignature":"signed"}]}}]}`), &response); err != nil {
		t.Fatal(err)
	}
	if got := nativeText(response); got != "visible answer" {
		t.Fatalf("nativeText = %q", got)
	}
}

func TestConversationPreservesChatAssistantAndToolMessages(t *testing.T) {
	path := []message{
		{Role: "user", Status: "complete", ActualModel: "m", Request: json.RawMessage(`{"messages":[{"role":"user","content":"use the tool"}]}`)},
		{Role: "assistant", Status: "complete", ActualModel: "m", Text: "", Output: json.RawMessage(`{"choices":[{"message":{"role":"assistant","content":null,"reasoning_content":"encrypted-reasoning","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q\":1}"}}]}}]}`)},
		{Role: "user", Status: "complete", ActualModel: "m", Request: json.RawMessage(`{"messages":[{"role":"tool","tool_call_id":"call_1","content":"tool result"}]}`)},
	}
	got, err := conversationRequest("chat.create", json.RawMessage(`{"model":"m","messages":[{"role":"user","content":"continue"}]}`), path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"encrypted-reasoning", "tool_calls", "call_1", "tool result"} {
		if !strings.Contains(string(got), want) {
			t.Fatalf("lost %q in chat history: %s", want, got)
		}
	}
}

func TestConversationResponsesReplaysFullImageGenerationOutput(t *testing.T) {
	path := []message{
		{Role: "user", Status: "complete", ActualModel: "m", Request: json.RawMessage(`{"input":"draw it"}`)},
		{Role: "assistant", Status: "complete", ActualModel: "m", Output: json.RawMessage(`{"output":[{"type":"image_generation_call","id":"ig_1","status":"completed","prompt":"a lighthouse","result":"large-base64-result"}]}`)},
	}
	got, err := conversationRequest("responses.create", json.RawMessage(`{"model":"m","input":"make it realistic"}`), path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"id":"ig_1"`, `"status":"completed"`, `"prompt":"a lighthouse"`, `"result":"large-base64-result"`} {
		if !strings.Contains(string(got), want) {
			t.Fatalf("image output lost %s: %s", want, got)
		}
	}
}

func TestConversationProjectsChatResponseMessageToAssistantInput(t *testing.T) {
	path := []message{{
		Role:        "assistant",
		Status:      "complete",
		ActualModel: "m",
		Output:      json.RawMessage(`{"choices":[{"message":{"role":"assistant","content":"answer","refusal":null,"reasoning_content":"opaque reasoning","annotations":[{"type":"url_citation"}],"audio":{"id":"audio_1","data":"large-audio-output","expires_at":123,"transcript":"spoken answer"},"tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}],"response_only":"drop me"}}]}`),
	}}
	got, err := conversationRequest("chat.create", json.RawMessage(`{"model":"m","messages":[{"role":"user","content":"next"}]}`), path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"answer", "opaque reasoning", `"audio":{"id":"audio_1"}`, "call_1"} {
		if !strings.Contains(string(got), want) {
			t.Fatalf("assistant input lost %q: %s", want, got)
		}
	}
	for _, forbidden := range []string{"annotations", "large-audio-output", "expires_at", "spoken answer", "response_only"} {
		if strings.Contains(string(got), forbidden) {
			t.Fatalf("assistant input retained output-only %q: %s", forbidden, got)
		}
	}
}

func TestConversationXAIRequestsEncryptedReasoningForLocalHistory(t *testing.T) {
	for name, raw := range map[string]string{
		"missing include": `{"model":"m","input":"hello"}`,
		"keeps include":   `{"model":"m","input":"hello","include":["web_search_call.action.sources"]}`,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := conversationProviderRequest("xai", "responses.create", json.RawMessage(raw), nil)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(got), "reasoning.encrypted_content") {
				t.Fatalf("xAI encrypted reasoning was not requested: %s", got)
			}
			if name == "keeps include" && !strings.Contains(string(got), "web_search_call.action.sources") {
				t.Fatalf("existing include was removed: %s", got)
			}
		})
	}
	openAI, err := conversationProviderRequest("openai", "responses.create", json.RawMessage(`{"model":"m","input":"hello"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(openAI), "reasoning.encrypted_content") {
		t.Fatalf("OpenAI legacy include was added unnecessarily: %s", openAI)
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

func TestConversationStatefulResponsesSendOnlyCurrentInput(t *testing.T) {
	path := []message{
		{Role: "user", Status: "complete", ActualModel: "m", Request: json.RawMessage(`{"input":"old question"}`)},
		{Role: "assistant", Status: "complete", ActualModel: "m", Text: "old answer", Output: json.RawMessage(`{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"old answer"}]}]}`)},
	}
	for name, state := range map[string]string{
		"previous response": `"previous_response_id":"resp_1"`,
		"conversation":      `"conversation":{"id":"conv_1"}`,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := conversationRequest("responses.create", json.RawMessage(`{"model":"m","input":"new question",`+state+`}`), path)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(got), "old question") || strings.Contains(string(got), "old answer") || !strings.Contains(string(got), "new question") {
				t.Fatalf("stateful request replayed local history: %s", got)
			}
		})
	}
}

func TestConversationModelChangeDropsOrphanToolResults(t *testing.T) {
	tests := []struct {
		operation string
		request   string
		current   string
		orphan    string
		keep      string
	}{
		{"responses.create", `{"input":[{"role":"user","content":[{"type":"input_file","file_id":"file-keep"}]},{"type":"function_call_output","call_id":"old-call","output":"orphan-response"}]}`, `{"model":"new","input":"next"}`, "orphan-response", "file-keep"},
		{"chat.create", `{"messages":[{"role":"user","content":"keep-chat"},{"role":"tool","tool_call_id":"old-call","content":"orphan-chat"}]}`, `{"model":"new","messages":[{"role":"user","content":"next"}]}`, "orphan-chat", "keep-chat"},
		{"messages.create", `{"messages":[{"role":"user","content":[{"type":"text","text":"keep-anthropic"},{"type":"tool_result","tool_use_id":"old-call","content":"orphan-anthropic"}]}]}`, `{"model":"new","max_tokens":10,"messages":[{"role":"user","content":"next"}]}`, "orphan-anthropic", "keep-anthropic"},
		{"content.generate", `{"contents":[{"role":"user","parts":[{"text":"keep-gemini"},{"functionResponse":{"name":"old","response":{"value":"orphan-gemini"}}}]}]}`, `{"model":"new","contents":[{"role":"user","parts":[{"text":"next"}]}]}`, "orphan-gemini", "keep-gemini"},
	}
	for _, tc := range tests {
		t.Run(tc.operation, func(t *testing.T) {
			path := []message{{Role: "user", Status: "complete", ActualModel: "old", Request: json.RawMessage(tc.request)}}
			got, err := conversationRequest(tc.operation, json.RawMessage(tc.current), path)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(got), tc.orphan) || !strings.Contains(string(got), tc.keep) {
				t.Fatalf("incorrect cross-model history: %s", got)
			}
		})
	}
}

func TestConversationFailedAssistantReplaysOnlyPartialText(t *testing.T) {
	tests := []struct {
		operation string
		request   string
		output    string
		current   string
		secret    string
	}{
		{"responses.create", `{"input":"first"}`, `{"output":[{"type":"reasoning","encrypted_content":"unfinished-response-reasoning"},{"type":"function_call","call_id":"unfinished-response-call"}]}`, `{"model":"m","input":"next"}`, "unfinished-response-call"},
		{"chat.create", `{"messages":[{"role":"user","content":"first"}]}`, `{"choices":[{"message":{"role":"assistant","reasoning_content":"unfinished-chat-reasoning","tool_calls":[{"id":"unfinished-chat-call","type":"function","function":{"name":"lookup","arguments":"{}"}}]}}]}`, `{"model":"m","messages":[{"role":"user","content":"next"}]}`, "unfinished-chat-call"},
		{"messages.create", `{"messages":[{"role":"user","content":"first"}]}`, `{"content":[{"type":"thinking","thinking":"unfinished","signature":"unfinished-anthropic-signature"},{"type":"tool_use","id":"unfinished-anthropic-call","name":"lookup","input":{}}]}`, `{"model":"m","max_tokens":10,"messages":[{"role":"user","content":"next"}]}`, "unfinished-anthropic-call"},
		{"content.generate", `{"contents":[{"role":"user","parts":[{"text":"first"}]}]}`, `{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"lookup","args":{}},"thoughtSignature":"unfinished-gemini-signature"}]}}]}`, `{"model":"m","contents":[{"role":"user","parts":[{"text":"next"}]}]}`, "unfinished-gemini-signature"},
	}
	for _, tc := range tests {
		t.Run(tc.operation, func(t *testing.T) {
			path := []message{
				{Role: "user", Status: "complete", ActualModel: "m", Request: json.RawMessage(tc.request)},
				{Role: "assistant", Status: "error", ActualModel: "m", Text: "safe partial text", Output: json.RawMessage(tc.output)},
			}
			got, err := conversationRequest(tc.operation, json.RawMessage(tc.current), path)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(got), tc.secret) || !strings.Contains(string(got), "safe partial text") {
				t.Fatalf("failed assistant replayed unfinished native state: %s", got)
			}
		})
	}
}

func TestConversationModelChangeSkipsStructuredOnlyAssistant(t *testing.T) {
	tests := []struct {
		operation string
		output    string
		current   string
		field     string
	}{
		{"responses.create", `{"output":[{"type":"function_call","call_id":"old-call"}]}`, `{"model":"new","input":"next"}`, "input"},
		{"chat.create", `{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"old-call","type":"function","function":{"name":"lookup","arguments":"{}"}}]}}]}`, `{"model":"new","messages":[{"role":"user","content":"next"}]}`, "messages"},
		{"messages.create", `{"content":[{"type":"tool_use","id":"old-call","name":"lookup","input":{}}]}`, `{"model":"new","max_tokens":10,"messages":[{"role":"user","content":"next"}]}`, "messages"},
		{"content.generate", `{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"lookup","args":{}}}]}}]}`, `{"model":"new","contents":[{"role":"user","parts":[{"text":"next"}]}]}`, "contents"},
	}
	for _, tc := range tests {
		t.Run(tc.operation, func(t *testing.T) {
			path := []message{{Role: "assistant", Status: "complete", ActualModel: "old", Output: json.RawMessage(tc.output)}}
			got, err := conversationRequest(tc.operation, json.RawMessage(tc.current), path)
			if err != nil {
				t.Fatal(err)
			}
			var body map[string]any
			if err := json.Unmarshal(got, &body); err != nil {
				t.Fatal(err)
			}
			if list, _ := body[tc.field].([]any); len(list) != 1 {
				t.Fatalf("model transition emitted an empty assistant turn: %s", got)
			}
		})
	}
}
