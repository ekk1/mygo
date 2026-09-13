package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestUsageLedgerSurvivesDeletion(t *testing.T) {
	a, h, _ := backgroundApp(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"id": "resp-usage", "model": "actual-model", "output": []any{}, "usage": map[string]any{"input_tokens": 100, "output_tokens": 30, "total_tokens": 130, "input_tokens_details": map[string]any{"cached_tokens": 80}, "output_tokens_details": map[string]any{"reasoning_tokens": 10}}})
	}))
	id := mediaSubmit(t, h, "/api/native/openai/responses.create?background=1&feature=chat", map[string]any{"provider_id": "p", "params": map[string]any{"model": "requested-model", "input": "hello"}})
	mediaWait(t, a, id, "complete")
	w := backgroundRequest(t, h, "GET", "/api/usage", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"input":100`) || !strings.Contains(w.Body.String(), `"cache_read":80`) {
		t.Fatalf("usage: %d %s", w.Code, w.Body.String())
	}
	if w := backgroundRequest(t, h, "DELETE", "/api/tasks/"+id, nil); w.Code != 200 {
		t.Fatal(w.Code)
	}
	b, s, err := newApp(a.store.dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_ = b
	w = backgroundRequest(t, s, "GET", "/api/usage", nil)
	var result struct {
		Records []json.RawMessage `json:"records"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Records) != 1 || !strings.Contains(w.Body.String(), "actual-model") {
		t.Fatalf("ledger after restart: %s", w.Body.String())
	}
}

func TestUsageLegacyRecovery(t *testing.T) {
	dir := t.TempDir()
	s, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	output := json.RawMessage(`{"model":"actual","id":"response-old","usage":{"input_tokens":12,"output_tokens":8}}`)
	v := session{ID: "original", ProfileID: "p", Operation: "responses.create", Messages: []message{{ID: "answer", Role: "assistant", Status: "complete", ActualModel: "alias", Output: output}}}
	if err := saveKV(filepath.Join(dir, "sessions", "original.json"), v); err != nil {
		t.Fatal(err)
	}
	_ = s
	a, server, err := newApp(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	got, err := a.store.getSession("original")
	if err != nil {
		t.Fatal(err)
	}
	if got.Usage.Tokens["total"] != 20 || got.Messages[0].Usage == nil {
		t.Errorf("legacy session usage: %+v", got.Usage)
	}
	r := a.usage["message-answer"]
	if r.Model != "actual" || r.RequestedModel != "alias" || r.ResponseID != "response-old" {
		t.Errorf("historical metadata: %+v", r)
	}
	r.Usage = nil
	r.Status = "pending"
	r.ProfileName = "original name"
	r.Historical = false
	if err := a.saveUsage(r); err != nil {
		t.Fatal(err)
	}
	v.ID = "fork"
	if err := saveKV(filepath.Join(dir, "sessions", "fork.json"), v); err != nil {
		t.Fatal(err)
	}
	b, server2, err := newApp(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer server2.Close()
	r = b.usage["message-answer"]
	if len(b.usage) != 1 || r.Usage == nil || r.Usage.Tokens["total"] != 20 || r.ProfileName != "original name" || r.Historical {
		t.Fatalf("recover without duplicate or metadata loss: %+v", r)
	}
}

func TestUsageNormalization(t *testing.T) {
	for _, tc := range []struct {
		name, op, raw string
		want          map[string]int64
		partial       bool
	}{
		{"responses", "responses.create", `{"usage":{"input_tokens":100,"output_tokens":30,"total_tokens":130,"input_tokens_details":{"cached_tokens":80},"output_tokens_details":{"reasoning_tokens":10}}}`, map[string]int64{"input": 100, "output": 30, "total": 130, "cache_read": 80, "reasoning": 10}, false},
		{"chat", "chat.create", `{"usage":{"prompt_tokens":10,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":4},"completion_tokens_details":{"reasoning_tokens":2}}}`, map[string]int64{"input": 10, "output": 5, "total": 15, "cache_read": 4, "reasoning": 2}, false},
		{"anthropic", "messages.create", `{"usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":80,"cache_creation_input_tokens":20,"cache_creation":{"ephemeral_5m_input_tokens":12,"ephemeral_1h_input_tokens":8}}}`, map[string]int64{"input": 110, "output": 5, "total": 115, "cache_read": 80, "cache_write": 20, "cache_write_5m": 12, "cache_write_1h": 8}, false},
		{"gemini", "content.generate", `{"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":20,"thoughtsTokenCount":10,"totalTokenCount":130,"cachedContentTokenCount":70,"toolUsePromptTokenCount":2}}`, map[string]int64{"input": 100, "output": 30, "total": 130, "reasoning": 10, "cache_read": 70, "tool_input": 2}, false},
		{"missing output", "responses.create", `{"usage":{"input_tokens":12}}`, map[string]int64{"input": 12}, true},
		{"zero", "responses.create", `{"usage":{"input_tokens":0,"output_tokens":0}}`, map[string]int64{"input": 0, "output": 0, "total": 0}, false},
		{"invalid counts", "responses.create", `{"usage":{"input_tokens":-1,"output_tokens":2.5,"total_tokens":"5"}}`, nil, false},
		{"missing", "responses.create", `{"usage":null}`, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := parseUsage(tc.op, json.RawMessage(tc.raw), "complete")
			if tc.want == nil {
				if got != nil {
					t.Fatal(got)
				}
				return
			}
			if got == nil || !reflect.DeepEqual(got.Tokens, tc.want) || got.Partial != tc.partial {
				t.Fatalf("got %+v want %v partial %v", got, tc.want, tc.partial)
			}
			if !parseUsage(tc.op, json.RawMessage(tc.raw), "error").Partial {
				t.Fatal("failed request must indicate partial accounting")
			}
		})
	}
}

func TestUsageStreamingSnapshots(t *testing.T) {
	raw := json.RawMessage(`{"events":[{"event":"message_start","data":{"message":{"model":"claude","usage":{"input_tokens":10,"output_tokens":1,"cache_read_input_tokens":80}}}},{"event":"message_delta","data":{"usage":{"output_tokens":5}}},{"event":"message_delta","data":{"usage":{"output_tokens":8}}}]}`)
	u := parseUsage("messages.create", raw, "complete")
	if u == nil || u.Tokens["input"] != 90 || u.Tokens["output"] != 8 || u.Tokens["total"] != 98 {
		t.Fatalf("snapshot counters must not be summed: %+v", u)
	}
	raw = json.RawMessage(`{"events":[{"data":{"choices":[{"finish_reason":"stop"}],"usage":null}},{"data":{"choices":[],"usage":{"prompt_tokens":20,"completion_tokens":7,"total_tokens":27}}},{"data":"[DONE]"}]}`)
	u = parseUsage("chat.create", raw, "complete")
	if u == nil || u.Tokens["total"] != 27 {
		t.Fatalf("final usage-only chunk: %+v", u)
	}
	for _, tc := range []struct {
		raw  string
		want bool
	}{
		{`{"stream":true,"stream_options":{"other":123}}`, true},
		{`{"stream":true,"stream_options":{"include_usage":false}}`, false},
	} {
		var result struct {
			StreamOptions struct {
				IncludeUsage bool `json:"include_usage"`
			} `json:"stream_options"`
		}
		if err := json.Unmarshal(usageStreamParams("chat.create", json.RawMessage(tc.raw)), &result); err != nil {
			t.Fatal(err)
		}
		if result.StreamOptions.IncludeUsage != tc.want {
			t.Fatal(result)
		}
	}
}

func TestUsageFiltersAndUnreported(t *testing.T) {
	a, h, _ := backgroundApp(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("no upstream request expected") }))
	for _, r := range []usageRecord{
		{ID: "one", ProfileID: "p", Model: "m", CreatedAt: "2026-09-01T00:00:00Z", Usage: &usageInfo{Tokens: map[string]int64{"input": 12, "output": 8, "total": 20}}},
		{ID: "two", ProfileID: "p", Model: "m", CreatedAt: "2026-09-01T23:59:59Z"},
		{ID: "three", ProfileID: "other", Model: "m", CreatedAt: "2026-09-02T00:00:00Z"},
	} {
		if err := a.saveUsage(r); err != nil {
			t.Fatal(err)
		}
	}
	w := backgroundRequest(t, h, "GET", "/api/usage?profile_id=p&model=m&from=2026-09-01&to=2026-09-01", nil)
	var v struct {
		Total   usageSummary
		Records []usageRecord
	}
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Records) != 2 || v.Total.Requests != 2 || v.Total.Reported != 1 || v.Total.Tokens["total"] != 20 {
		t.Fatalf("%s", w.Body.String())
	}
	for _, q := range []string{"from=oops", "from=2026-09-02&to=2026-09-01"} {
		if w := backgroundRequest(t, h, "GET", "/api/usage?"+q, nil); w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
}

func TestUsageChatStreamEndToEnd(t *testing.T) {
	a, h, cfg := backgroundApp(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if usageMap(body["stream_options"])["include_usage"] != true {
			t.Error("stream did not request final usage")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"chat-count\",\"model\":\"actual\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}],\"usage\":null}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":15,\"completion_tokens\":5,\"total_tokens\":20}}\n\ndata: [DONE]\n\n")
	}))
	v, err := a.store.createSession("count", "p", "chat.create")
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"provider_id": "p", "operation": "chat.create", "params": map[string]any{"model": "alias", "messages": []any{map[string]any{"role": "user", "content": "hello"}}}, "revision": cfg.Revision, "stream": true, "background": true}
	w := backgroundRequest(t, h, "POST", "/api/sessions/"+v.ID+"/native", payload)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var started struct{ Task generationTask }
	if err := json.NewDecoder(w.Body).Decode(&started); err != nil {
		t.Fatal(err)
	}
	id := started.Task.ID
	mediaWait(t, a, id, "complete")
	s, err := a.store.getSession(v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if s.Usage.Tokens["total"] != 20 || s.Messages[1].Usage.Tokens["total"] != 20 {
		t.Fatalf("session usage: %+v", s)
	}
	a.usageMu.RLock()
	defer a.usageMu.RUnlock()
	if len(a.usage) != 1 {
		t.Fatal(a.usage)
	}
	for _, r := range a.usage {
		if r.Model != "actual" || r.RequestedModel != "alias" || r.Usage.Tokens["total"] != 20 || r.SessionID != s.ID || r.MessageID != s.Messages[1].ID || r.TaskID != id {
			t.Fatalf("%+v", r)
		}
	}
}

func TestUsageWriteFailurePreventsSending(t *testing.T) {
	a, _, _ := backgroundApp(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("request sent without durable ledger") }))
	if err := os.Remove(filepath.Join(a.store.dir, "usage")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.store.dir, "usage"), []byte("block"), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := a.store.getProvider("p")
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.executeNative(context.Background(), p, "responses.create", json.RawMessage(`{"model":"m","input":"hello"}`), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "请求尚未发送") {
		t.Fatal(err)
	}
}

func TestUsageHistoricalStreamMetadata(t *testing.T) {
	v := usageRecord{Operation: "chat.create", Model: "alias", RequestedModel: "alias"}
	raw := json.RawMessage(`{"usage":{"prompt_tokens":2,"completion_tokens":3},"native_events":[{"data":{"model":"actual","id":"chat-final","choices":[],"usage":null}}]}`)
	usageMetadata(&v, raw)
	if v.Model != "actual" || v.ResponseID != "chat-final" {
		t.Fatalf("reduced stream lost metadata: %+v", v)
	}
	u := parseUsage(v.Operation, raw, "complete")
	if u == nil || u.Tokens["total"] != 5 {
		t.Fatal(u)
	}
}
