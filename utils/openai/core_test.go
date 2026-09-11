package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func newCoreTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := New(Config{APIKey: "test-key", BaseURL: server.URL + "/v1"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.CloseIdleConnections)
	return client
}

func TestCreateChatCompletionPreservesNativeFieldsAndRawHTTP(t *testing.T) {
	client := newCoreTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["store"] != false || body["service_tier"] != "flex" || body["custom_native"] != "kept" {
			t.Fatalf("body = %#v", body)
		}
		if !reflect.DeepEqual(body["modalities"], []any{"text", "audio"}) || !reflect.DeepEqual(body["audio"], map[string]any{"voice": "alloy", "format": "wav"}) {
			t.Fatalf("audio configuration = %#v", body)
		}
		messages := body["messages"].([]any)
		content := messages[0].(map[string]any)["content"].([]any)
		if !reflect.DeepEqual(content[0], map[string]any{"type": "input_audio", "input_audio": map[string]any{"data": "UklGRg==", "format": "wav"}}) {
			t.Fatalf("audio input = %#v", content)
		}
		if _, present := body["stream"]; present {
			t.Fatalf("non-stream request injected stream: %#v", body)
		}
		w.Header().Set("X-Request-ID", "req_chat")
		io.WriteString(w, `{"id":"chat_1","object":"chat.completion","model":"gpt-test","choices":[{"index":0,"message":{"role":"assistant","content":"hello","audio":{"id":"audio_1","data":"UklGRg==","transcript":"hello","expires_at":123}},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5},"future":"raw"}`)
	})

	store := false
	got, err := client.CreateChatCompletion(context.Background(), ChatCompletionRequest{
		Model:      "gpt-test",
		Messages:   []ChatMessage{{Role: "user", Content: []any{map[string]any{"type": "input_audio", "input_audio": map[string]any{"data": "UklGRg==", "format": "wav"}}}}},
		Modalities: []string{"text", "audio"},
		Audio:      map[string]any{"voice": "alloy", "format": "wav"},
		Store:      &store, ServiceTier: "flex",
		Extra: map[string]any{"custom_native": "kept"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "chat_1" || got.Choices[0].Message.Role != "assistant" || got.Usage.TotalTokens != 5 {
		t.Fatalf("completion = %#v", got)
	}
	if !reflect.DeepEqual(got.Choices[0].Message.Audio, map[string]any{"id": "audio_1", "data": "UklGRg==", "transcript": "hello", "expires_at": float64(123)}) {
		t.Fatalf("audio result = %#v", got.Choices[0].Message.Audio)
	}
	if got.HTTP == nil || got.HTTP.Header.Get("X-Request-ID") != "req_chat" || !strings.Contains(string(got.HTTP.Body), `"future":"raw"`) {
		t.Fatalf("HTTP = %#v", got.HTTP)
	}
}

func TestNonStreamingCreateRejectsStreamTrueBeforeRequest(t *testing.T) {
	requests := 0
	client := newCoreTestClient(t, func(http.ResponseWriter, *http.Request) { requests++ })
	for name, call := range map[string]func() error{
		"chat": func() error {
			_, err := client.CreateChatCompletion(context.Background(), ChatCompletionRequest{Model: "gpt-test", Messages: []ChatMessage{{Role: "user", Content: "hi"}}, Stream: Ptr(true)})
			return err
		},
		"response": func() error {
			_, err := client.CreateResponse(context.Background(), ResponseRequest{Model: "gpt-test", Input: "hi", Stream: Ptr(true)})
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); err == nil || !strings.Contains(err.Error(), "stream") {
				t.Fatalf("error = %v", err)
			}
		})
	}
	if requests != 0 {
		t.Fatalf("sent %d requests", requests)
	}
}

func TestRequestExtraCannotOverrideTypedField(t *testing.T) {
	_, err := json.Marshal(ResponseRequest{Model: "gpt-test", Extra: map[string]any{"model": "other"}})
	if err == nil || !strings.Contains(err.Error(), "model") {
		t.Fatalf("error = %v", err)
	}
}

func TestResponseTypedInputMessageUsesNativeShape(t *testing.T) {
	body, err := json.Marshal(ResponseRequest{
		Model: "gpt-test",
		Input: []ResponseInputMessage{{
			Role: "user",
			Content: []ResponseInputContent{
				{Type: "input_text", Text: "describe"},
				{Type: "input_image", ImageURL: "https://example.test/image.png", Detail: "high"},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"input":[{"role":"user","content":[{"type":"input_text","text":"describe"},{"type":"input_image","image_url":"https://example.test/image.png","detail":"high"}]}],"model":"gpt-test"}`
	if string(body) != want {
		t.Fatalf("body = %s", body)
	}
}

func TestResponseLifecyclePathsAndPartialHTTPError(t *testing.T) {
	var requests []string
	client := newCoreTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		if strings.Contains(r.URL.Path, "bad") {
			w.Header().Set("X-Request-ID", "req_bad")
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"error":{"message":"bad"}}`)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/input_items"):
			io.WriteString(w, `{"object":"list","data":[{"id":"msg_1","type":"message","role":"user","content":[]}],"has_more":false}`)
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			io.WriteString(w, `{"id":"resp_1","object":"response","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}`)
		}
	})

	if _, err := client.GetResponse(context.Background(), "resp/unsafe"); err == nil {
		t.Fatal("unsafe response ID accepted")
	}
	if _, err := client.GetResponse(context.Background(), "bad"); err == nil {
		t.Fatal("HTTP error missing")
	} else {
		got, _ := client.GetResponse(context.Background(), "bad")
		if got == nil || got.HTTP == nil || got.HTTP.StatusCode != 400 || got.HTTP.Header.Get("X-Request-ID") != "req_bad" {
			t.Fatalf("partial response = %#v", got)
		}
	}
	includeObfuscation := false
	if _, err := client.GetResponse(context.Background(), "resp_1", ResponseGetOptions{Include: []string{"reasoning.encrypted_content"}, IncludeObfuscation: &includeObfuscation, StartingAfter: Ptr(int64(4))}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CancelResponse(context.Background(), "resp_1"); err != nil {
		t.Fatal(err)
	}
	items, err := client.ListResponseInputItems(context.Background(), "resp_1", ResponseInputItemsOptions{After: "msg_0", Limit: 10, Order: "asc", Include: []string{"reasoning.encrypted_content"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(items.Data) != 1 || items.Data[0].ID != "msg_1" {
		t.Fatalf("items = %#v", items)
	}
	if _, err := client.DeleteResponse(context.Background(), "resp_1"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"GET /v1/responses/bad", "GET /v1/responses/bad", "GET /v1/responses/resp_1?include=reasoning.encrypted_content&include_obfuscation=false&starting_after=4",
		"POST /v1/responses/resp_1/cancel",
		"GET /v1/responses/resp_1/input_items?after=msg_0&include=reasoning.encrypted_content&limit=10&order=asc",
		"DELETE /v1/responses/resp_1",
	}
	if !reflect.DeepEqual(requests, want) {
		t.Fatalf("requests = %#v", requests)
	}
}

func TestResponseItemsRoundTripUnknownNativeFields(t *testing.T) {
	rawItems := []byte(`[{"id":"rs_1","type":"reasoning","summary":[{"type":"summary_text","text":"thought"}],"encrypted_content":"secret"},{"id":"img_1","type":"image_generation_call","status":"completed","result":"base64","revised_prompt":"cat"},{"id":"sh_1","type":"shell_call","status":"completed","action":{"commands":["pwd"],"timeout_ms":1000}}]`)
	var items []ResponseOutputItem
	if err := json.Unmarshal(rawItems, &items); err != nil {
		t.Fatal(err)
	}
	roundTrip, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	if string(roundTrip) != string(rawItems) {
		t.Fatalf("items = %s", roundTrip)
	}

	rawContent := []byte(`{"type":"output_text","text":"hello","annotations":[{"type":"url_citation","url":"https://example.test","title":"Example","start_index":0,"end_index":5}],"future":{"nested":true}}`)
	var content ResponseContent
	if err := json.Unmarshal(rawContent, &content); err != nil {
		t.Fatal(err)
	}
	roundTrip, err = json.Marshal(content)
	if err != nil {
		t.Fatal(err)
	}
	if string(roundTrip) != string(rawContent) {
		t.Fatalf("content = %s", roundTrip)
	}
	content.Raw = nil
	roundTrip, err = json.Marshal(content)
	if err != nil || strings.Contains(string(roundTrip), "future") {
		t.Fatalf("edited content = %s, err = %v", roundTrip, err)
	}
}

func TestResponseCountTokensAndCompact(t *testing.T) {
	client := newCoreTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/responses/input_tokens":
			io.WriteString(w, `{"object":"response.input_tokens","input_tokens":17}`)
		case "/v1/responses/compact":
			io.WriteString(w, `{"id":"cmp_1","object":"response.compaction","created_at":1,"output":[{"type":"compaction","encrypted_content":"abc"}],"usage":{"input_tokens":17,"output_tokens":4,"total_tokens":21}}`)
		default:
			t.Fatalf("path = %s", r.URL.Path)
		}
	})
	count, err := client.CountResponseInputTokens(context.Background(), ResponseInputTokensRequest{Model: "gpt-test", Input: "hello"})
	if err != nil || count.InputTokens != 17 || count.HTTP == nil {
		t.Fatalf("count = %#v, err = %v", count, err)
	}
	compact, err := client.CompactResponse(context.Background(), ResponseCompactRequest{Model: "gpt-test", Input: "hello"})
	if err != nil || compact.ID != "cmp_1" || compact.Usage.TotalTokens != 21 || compact.HTTP == nil {
		t.Fatalf("compact = %#v, err = %v", compact, err)
	}
}

func TestNativeToolConstructors(t *testing.T) {
	strict := true
	async := false
	tools := []any{
		WebSearch(WebSearchOptions{}),
		WebSearch(WebSearchOptions{Type: "web_search_preview", SearchContextSize: "high"}),
		ImageGeneration(ImageGenerationOptions{}),
		ImageGeneration(ImageGenerationOptions{Model: "custom-image", Action: "edit"}),
		CodeInterpreter(CodeInterpreterOptions{Container: AutoContainer{MemoryLimit: "4g", FileIDs: []string{"file_1"}, NetworkPolicy: map[string]any{"type": "disabled"}}, AllowedCallers: []string{"direct"}}),
		CodeInterpreter(CodeInterpreterOptions{Container: "cntr_1"}),
		CodeInterpreter(CodeInterpreterOptions{}),
		HostedShell(HostedShellOptions{Environment: ShellEnvironment{Type: "container_reference", ContainerID: "cntr_1"}}),
		func() FunctionTool {
			tool := Function("lookup", "Look up a value", json.RawMessage(`{"type":"object"}`), &strict)
			tool.AllowedCallers = []string{"direct"}
			tool.Async = &async
			tool.DeferLoading = Ptr(true)
			tool.OutputSchema = json.RawMessage(`{"type":"string"}`)
			return tool
		}(),
	}
	b, err := json.Marshal(tools)
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"type":"web_search"},{"type":"web_search_preview","search_context_size":"high"},{"type":"image_generation","model":"gpt-image-2.5-flare"},{"type":"image_generation","model":"custom-image","action":"edit"},{"type":"code_interpreter","container":{"type":"auto","memory_limit":"4g","file_ids":["file_1"],"network_policy":{"type":"disabled"}},"allowed_callers":["direct"]},{"type":"code_interpreter","container":"cntr_1"},{"type":"code_interpreter","container":{"type":"auto"}},{"type":"shell","environment":{"type":"container_reference","container_id":"cntr_1"}},{"type":"function","name":"lookup","description":"Look up a value","parameters":{"type":"object"},"strict":true,"allowed_callers":["direct"],"async":false,"defer_loading":true,"output_schema":{"type":"string"}}]`
	if string(b) != want {
		t.Fatalf("tools = %s", b)
	}
}

func TestChatStreamRequiresDone(t *testing.T) {
	for _, tc := range []struct {
		name, stream string
		want         error
		streamError  bool
	}{
		{name: "done", stream: "data: {\"id\":\"chat_1\"}\n\ndata: [DONE]\n\n"},
		{name: "truncated", stream: "data: {\"id\":\"chat_1\"}\n\n", want: io.ErrUnexpectedEOF},
		{name: "server error", stream: "data: {\"error\":{\"message\":\"bad\"}}\n\n", streamError: true},
		{name: "malformed", stream: "data: {bad json}\n\n", streamError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := newCoreTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, tc.stream)
			})
			var events []Event
			_, err := client.StreamChatCompletion(context.Background(), ChatCompletionRequest{Model: "gpt-test", Messages: []ChatMessage{{Role: "user", Content: "hi"}}}, func(event Event) error { events = append(events, event); return nil })
			if (!tc.streamError && !errors.Is(err, tc.want)) || (tc.streamError && err == nil) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			var streamErr *StreamError
			if errors.As(err, &streamErr) != tc.streamError {
				t.Fatalf("stream error = %v", err)
			}
			if tc.streamError && len(streamErr.Event.Data) == 0 {
				t.Fatal("stream error lost raw event")
			}
			if len(events) == 0 {
				t.Fatal("callback received no events")
			}
		})
	}
}

func TestResponseStreamRequiresTerminalAndPropagatesCallback(t *testing.T) {
	callbackErr := errors.New("stop")
	for _, tc := range []struct {
		name, stream string
		callback     error
		want         error
		streamError  bool
	}{
		{name: "completed", stream: "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\"}}\n\n"},
		{name: "incomplete terminal", stream: "event: response.incomplete\ndata: {\"type\":\"response.incomplete\"}\n\n"},
		{name: "failed terminal", stream: "event: response.failed\ndata: {\"type\":\"response.failed\"}\n\n", streamError: true},
		{name: "error event", stream: "event: error\ndata: {\"type\":\"error\",\"message\":\"bad\"}\n\n", streamError: true},
		{name: "malformed", stream: "data: {bad json}\n\n", streamError: true},
		{name: "truncated", stream: "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\"}\n\n", want: io.ErrUnexpectedEOF},
		{name: "callback", stream: "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\"}\n\n", callback: callbackErr, want: callbackErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := newCoreTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, tc.stream)
			})
			_, err := client.StreamResponse(context.Background(), ResponseRequest{Model: "gpt-test", Input: "hi"}, func(Event) error { return tc.callback })
			if (!tc.streamError && !errors.Is(err, tc.want)) || (tc.streamError && err == nil) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			var streamErr *StreamError
			if errors.As(err, &streamErr) != tc.streamError {
				t.Fatalf("stream error = %v", err)
			}
		})
	}
}

func TestStreamsRejectNilCallbackBeforeRequest(t *testing.T) {
	requests := 0
	client := newCoreTestClient(t, func(http.ResponseWriter, *http.Request) { requests++ })
	if _, err := client.StreamChatCompletion(context.Background(), ChatCompletionRequest{}, nil); err == nil {
		t.Fatal("chat accepted nil callback")
	}
	if _, err := client.StreamResponse(context.Background(), ResponseRequest{}, nil); err == nil {
		t.Fatal("responses accepted nil callback")
	}
	if requests != 0 {
		t.Fatalf("sent %d requests", requests)
	}
}
