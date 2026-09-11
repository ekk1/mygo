package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func previewObject(t *testing.T, value any) map[string]any {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err = json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestNativePreviewUsesVendorSpecificRoutesWithoutCallingUpstream(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer upstream.Close()
	cases := []struct {
		kind, operation, raw, method, path string
		bodyHas, queryHas                  map[string]any
	}{
		{"openai", "responses.create", `{"model":"gpt-test","input":"hello","store":false}`, "POST", "/v1/responses", map[string]any{"model": "gpt-test", "store": false}, nil},
		{"compatible", "chat.create", `{"model":"proxy-model","messages":[]}`, "POST", "/v1/chat/completions", map[string]any{"model": "proxy-model"}, nil},
		{"anthropic", "messages.create", `{"model":"claude-test","max_tokens":0,"messages":[]}`, "POST", "/v1/messages", map[string]any{"max_tokens": float64(0)}, nil},
		{"gemini", "content.generate", `{"model":"models/gemini-test","contents":[],"generationConfig":{"temperature":0}}`, "POST", "/v1beta/models/gemini-test:generateContent", map[string]any{"contents": []any{}}, nil},
		{"gemini", "files.list", `{"pageToken":"next token","pageSize":25}`, "GET", "/v1beta/files", nil, map[string]any{"pageToken": "next token", "pageSize": "25"}},
		{"xai", "videos.get", `{"video_id":"video/one"}`, "GET", "/v1/videos/video%2Fone", nil, nil},
		{"xai", "audio.speech", `{"text":"hello","voice_id":"eve","language":"en"}`, "POST", "/v1/tts", map[string]any{"text": "hello"}, nil},
		{"xai", "videos.create", `{"model":"grok-video","prompt":"hello"}`, "POST", "/v1/videos/generations", map[string]any{"model": "grok-video"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.kind+"/"+tc.operation, func(t *testing.T) {
			baseURL := upstream.URL + "/v1"
			if tc.kind == "gemini" {
				baseURL = upstream.URL
			}
			v, err := nativePreview(provider{Kind: tc.kind, BaseURL: baseURL}, tc.operation, json.RawMessage(tc.raw), nil)
			if err != nil {
				t.Fatal(err)
			}
			p := previewObject(t, v)
			if p["method"] != tc.method {
				t.Fatalf("method = %v", p["method"])
			}
			u := p["url"].(string)
			if !strings.Contains(u, tc.path) {
				t.Fatalf("url = %s, want path %s", u, tc.path)
			}
			if tc.bodyHas != nil {
				body := p["body"].(map[string]any)
				for key, want := range tc.bodyHas {
					if got, ok := body[key]; !ok || jsonValue(got) != jsonValue(want) {
						t.Fatalf("body[%s] = %#v, want %#v", key, got, want)
					}
				}
				if tc.kind == "gemini" {
					if _, ok := body["model"]; ok {
						t.Fatal("Gemini model leaked into body")
					}
				}
			}
			for key, want := range tc.queryHas {
				if !strings.Contains(u, key+"="+strings.ReplaceAll(want.(string), " ", "+")) {
					t.Fatalf("url = %s", u)
				}
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("preview made %d upstream calls", calls.Load())
	}
}

func jsonValue(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestExecuteNativeSendsAnthropicHeadersPreservesBodyAndWritesLogs(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "very-secret" || r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Fatalf("request = %s headers %#v", r.URL, r.Header)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"max_tokens":12,"messages":[],"model":"claude-test","service_tier":"standard_only","stream":false}` {
			t.Fatalf("body = %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"msg_1","content":[]}`)
	}))
	defer upstream.Close()
	dir := t.TempDir()
	a := &app{store: &store{dir: dir, config: configuration{SaveResponses: false}}}
	result, err := a.executeNative(context.Background(), provider{ID: "anth", Kind: "anthropic", BaseURL: upstream.URL + "/v1", ProxyURL: "-", APIKey: "very-secret"}, "messages.create", json.RawMessage(`{"model":"claude-test","max_tokens":12,"messages":[],"service_tier":"standard_only","stream":false}`), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if previewObject(t, result)["id"] != "msg_1" {
		t.Fatalf("result = %#v", result)
	}
	logs, err := os.ReadDir(filepath.Join(dir, "logs"))
	if err != nil || len(logs) != 1 {
		t.Fatalf("logs = %v, %v", logs, err)
	}
	children, _ := os.ReadDir(filepath.Join(dir, "logs", logs[0].Name()))
	var requestDir string
	for _, child := range children {
		if child.IsDir() {
			requestDir = filepath.Join(dir, "logs", logs[0].Name(), child.Name())
		}
	}
	requestMeta, _ := os.ReadFile(filepath.Join(requestDir, "request.json"))
	if bytes.Contains(requestMeta, []byte("very-secret")) || !bytes.Contains(requestMeta, []byte("[REDACTED]")) {
		t.Fatalf("request log = %s", requestMeta)
	}
	if _, err := os.Stat(filepath.Join(requestDir, "response.body")); !os.IsNotExist(err) {
		t.Fatalf("response body log exists: %v", err)
	}
}

func TestAnthropicMessageFileSourceSendsFilesBetaHeader(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("anthropic-beta"); got != "files-api-2025-04-14" {
			t.Fatalf("anthropic-beta = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"msg_file","content":[]}`)
	}))
	defer upstream.Close()
	a := &app{store: &store{dir: t.TempDir(), config: configuration{}}}
	params := json.RawMessage(`{"model":"claude-test","max_tokens":12,"messages":[{"role":"user","content":[{"type":"document","source":{"type":"file","file_id":"file_123"}}]}]}`)
	if _, err := a.executeNative(context.Background(), provider{ID: "anth", Kind: "anthropic", BaseURL: upstream.URL, ProxyURL: "-", APIKey: "secret"}, "messages.create", params, nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestAudioTranscriptionTextResponsesReturnStrings(t *testing.T) {
	operations := map[string]string{"audio.transcribe": "/audio/transcriptions", "audio.translate": "/audio/translations"}
	for operation, path := range operations {
		for _, responseFormat := range []string{"text", "srt", "vtt"} {
			t.Run(operation+"/"+responseFormat, func(t *testing.T) {
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != path {
						t.Fatalf("path = %s", r.URL.Path)
					}
					w.Header().Set("Content-Type", "text/plain; charset=utf-8")
					io.WriteString(w, "plain transcript")
				}))
				defer upstream.Close()
				a := &app{store: &store{dir: t.TempDir(), config: configuration{}}}
				result, err := a.executeNative(context.Background(), provider{ID: "openai", Kind: "openai", BaseURL: upstream.URL, ProxyURL: "-", APIKey: "secret"}, operation, json.RawMessage(`{"model":"whisper-1","response_format":"`+responseFormat+`"}`), resourceUploads{"file": {{Filename: "speech.wav", Reader: strings.NewReader("audio")}}}, nil)
				if err != nil || result != "plain transcript" {
					t.Fatalf("result=%#v error=%v", result, err)
				}
			})
		}
	}
}

func TestNativeMultipartPreviewAndExecutionShareFields(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/edits" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		if r.FormValue("prompt") != "keep me" || r.FormValue("n") != "0" {
			t.Fatalf("form = %#v", r.MultipartForm.Value)
		}
		f, h, err := r.FormFile("image")
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		b, _ := io.ReadAll(f)
		if h.Filename != "input.png" || string(b) != "PNG" {
			t.Fatalf("file = %s %q", h.Filename, b)
		}
		io.WriteString(w, `{"data":[]}`)
	}))
	defer upstream.Close()
	uploads := resourceUploads{"image": {{Filename: "input.png", ContentType: "image/png", Reader: strings.NewReader("PNG")}}}
	p := provider{ID: "x", Kind: "xai", BaseURL: upstream.URL + "/v1", ProxyURL: "-", APIKey: "key"}
	preview, err := nativePreview(p, "images.edit", json.RawMessage(`{"prompt":"keep me","n":0}`), uploads)
	if err != nil {
		t.Fatal(err)
	}
	view := previewObject(t, preview)
	if view["content_type"] != "multipart/form-data" {
		t.Fatalf("content type = %v", view["content_type"])
	}
	files := view["files"].([]any)
	if files[0].(map[string]any)["size"] != float64(3) {
		t.Fatalf("files = %#v", files)
	}
	a := &app{store: &store{dir: t.TempDir(), config: configuration{}}}
	if _, err = a.executeNative(context.Background(), p, "images.edit", json.RawMessage(`{"prompt":"keep me","n":0}`), uploads, nil); err != nil {
		t.Fatal(err)
	}
}

func TestNativeRejectsWrongKindAndUnknownOperation(t *testing.T) {
	if _, err := nativePreview(provider{Kind: "anthropic", BaseURL: "https://example.test/v1"}, "chat.create", json.RawMessage(`{}`), nil); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("wrong operation error = %v", err)
	}
	dir := t.TempDir()
	a := &app{store: &store{dir: dir, config: configuration{Providers: []provider{{ID: "p", Kind: "anthropic", BaseURL: "https://example.test/v1"}}}}}
	r := httptest.NewRequest(http.MethodPost, "/api/native/openai/models.list?preview=1", strings.NewReader(`{"provider_id":"p","params":{}}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.runNative(w, r)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "kind") {
		t.Fatalf("response = %d %s", w.Code, w.Body.String())
	}
	if _, err := nativePreview(provider{Kind: "compatible", BaseURL: "https://example.test/v1"}, "containers.list", json.RawMessage(`{}`), nil); err == nil || !strings.Contains(err.Error(), "enabled") {
		t.Fatalf("compatible capability error = %v", err)
	}
	if _, err := nativePreview(provider{Kind: "compatible", BaseURL: "https://example.test/v1", Resources: []string{"containers"}}, "containers.list", json.RawMessage(`{}`), nil); err != nil {
		t.Fatalf("enabled compatible container: %v", err)
	}
}

func TestNativeHandlerRejectsStaleRevisionBeforeUpstream(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer upstream.Close()
	a := &app{store: &store{dir: t.TempDir(), config: configuration{Revision: 7, Providers: []provider{{ID: "p", Kind: "openai", BaseURL: upstream.URL + "/v1", ProxyURL: "-"}}}}}
	r := httptest.NewRequest(http.MethodPost, "/api/native/openai/models.list?preview=1&revision=6", strings.NewReader(`{"provider_id":"p","params":{}}`))
	r.SetPathValue("vendor", "openai")
	r.SetPathValue("operation", "models.list")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.runNative(w, r)
	if w.Code != http.StatusConflict || calls.Load() != 0 {
		t.Fatalf("response = %d %s, calls = %d", w.Code, w.Body.String(), calls.Load())
	}
}

func TestNativeSupportedOperationMatrixBuilds(t *testing.T) {
	file := func() resourceUploads {
		return resourceUploads{"file": {{Filename: "input.bin", Reader: strings.NewReader("data")}}}
	}
	image := func() resourceUploads {
		return resourceUploads{"image": {{Filename: "input.png", Reader: strings.NewReader("png")}}}
	}
	tests := []struct {
		kind, operation, raw string
		uploads              resourceUploads
	}{
		{"openai", "models.list", `{}`, nil}, {"openai", "responses.create", `{"model":"m"}`, nil}, {"openai", "chat.create", `{"model":"m"}`, nil},
		{"openai", "images.generate", `{"model":"m"}`, nil}, {"openai", "images.edit", `{"model":"m"}`, image()}, {"openai", "audio.speech", `{"model":"m"}`, nil},
		{"openai", "audio.transcribe", `{"model":"m"}`, file()}, {"openai", "audio.translate", `{"model":"m"}`, file()}, {"openai", "videos.create", `{"model":"m"}`, nil}, {"openai", "videos.get", `{"video_id":"v"}`, nil},
		{"openai", "files.upload", `{"purpose":"assistants"}`, file()}, {"openai", "files.list", `{"after":"x"}`, nil}, {"openai", "files.get", `{"file_id":"f"}`, nil}, {"openai", "files.delete", `{"file_id":"f"}`, nil}, {"openai", "files.download", `{"file_id":"f"}`, nil},
		{"openai", "containers.create", `{"name":"c"}`, nil}, {"openai", "containers.list", `{}`, nil}, {"openai", "containers.get", `{"container_id":"c"}`, nil}, {"openai", "containers.delete", `{"container_id":"c"}`, nil},
		{"openai", "containers.files.add", `{"container_id":"c","file_id":"f"}`, nil}, {"openai", "containers.files.upload", `{"container_id":"c"}`, file()}, {"openai", "containers.files.list", `{"container_id":"c"}`, nil},
		{"openai", "containers.files.get", `{"container_id":"c","file_id":"f"}`, nil}, {"openai", "containers.files.delete", `{"container_id":"c","file_id":"f"}`, nil}, {"openai", "containers.files.download", `{"container_id":"c","file_id":"f"}`, nil},
		{"openai", "batches.create", `{"input_file_id":"f","endpoint":"/v1/responses","completion_window":"24h"}`, nil}, {"openai", "batches.list", `{}`, nil}, {"openai", "batches.get", `{"batch_id":"b"}`, nil}, {"openai", "batches.cancel", `{"batch_id":"b"}`, nil},
		{"compatible", "responses.create", `{"model":"m"}`, nil}, {"compatible", "chat.create", `{"model":"m"}`, nil}, {"compatible", "files.upload", `{}`, file()},
		{"anthropic", "models.list", `{}`, nil}, {"anthropic", "messages.create", `{"model":"m","max_tokens":1,"messages":[]}`, nil}, {"anthropic", "files.upload", `{}`, file()}, {"anthropic", "files.list", `{"after_id":"f"}`, nil}, {"anthropic", "files.get", `{"file_id":"f"}`, nil}, {"anthropic", "files.delete", `{"file_id":"f"}`, nil}, {"anthropic", "files.download", `{"file_id":"f"}`, nil}, {"anthropic", "batches.create", `{"requests":[]}`, nil}, {"anthropic", "batches.list", `{}`, nil}, {"anthropic", "batches.get", `{"batch_id":"b"}`, nil}, {"anthropic", "batches.cancel", `{"batch_id":"b"}`, nil}, {"anthropic", "batches.delete", `{"batch_id":"b"}`, nil}, {"anthropic", "batches.results", `{"batch_id":"b"}`, nil},
		{"gemini", "models.list", `{}`, nil}, {"gemini", "content.generate", `{"model":"m","contents":[]}`, nil}, {"gemini", "images.generate", `{"model":"m","contents":[]}`, nil}, {"gemini", "files.upload", `{"display_name":"f"}`, file()}, {"gemini", "files.list", `{}`, nil}, {"gemini", "files.get", `{"name":"files/f"}`, nil}, {"gemini", "files.delete", `{"name":"files/f"}`, nil}, {"gemini", "files.download", `{"name":"files/generated-video"}`, nil}, {"gemini", "videos.create", `{"model":"m","instances":[]}`, nil}, {"gemini", "videos.get", `{"name":"operations/o"}`, nil}, {"gemini", "batches.create", `{"model":"m","batch":{}}`, nil}, {"gemini", "batches.list", `{}`, nil}, {"gemini", "batches.get", `{"name":"batches/b"}`, nil}, {"gemini", "batches.cancel", `{"name":"batches/b"}`, nil}, {"gemini", "batches.delete", `{"name":"batches/b"}`, nil},
		{"xai", "models.list", `{}`, nil}, {"xai", "responses.create", `{"model":"m"}`, nil}, {"xai", "chat.create", `{"model":"m"}`, nil}, {"xai", "images.generate", `{"model":"m"}`, nil}, {"xai", "images.edit", `{"model":"m"}`, image()}, {"xai", "audio.speech", `{"model":"m"}`, nil}, {"xai", "audio.transcribe", `{"model":"m"}`, file()}, {"xai", "videos.create", `{"model":"m"}`, nil}, {"xai", "videos.get", `{"video_id":"v"}`, nil}, {"xai", "files.upload", `{}`, file()}, {"xai", "files.list", `{}`, nil}, {"xai", "files.get", `{"file_id":"f"}`, nil}, {"xai", "files.delete", `{"file_id":"f"}`, nil}, {"xai", "files.download", `{"file_id":"f"}`, nil}, {"xai", "batches.create", `{"name":"b"}`, nil}, {"xai", "batches.list", `{}`, nil}, {"xai", "batches.get", `{"batch_id":"b"}`, nil}, {"xai", "batches.cancel", `{"batch_id":"b"}`, nil}, {"xai", "batches.requests", `{"batch_id":"b","batch_requests":[]}`, nil}, {"xai", "batches.results", `{"batch_id":"b"}`, nil},
	}
	for _, tc := range tests {
		t.Run(tc.kind+"/"+tc.operation, func(t *testing.T) {
			base := "https://example.test/v1"
			if tc.kind == "gemini" {
				base = "https://example.test"
			}
			profile := provider{Kind: tc.kind, BaseURL: base}
			if tc.kind == "compatible" {
				profile.Resources = []string{"files", "containers", "batches", "images", "audio"}
			}
			if _, err := nativePreview(profile, tc.operation, json.RawMessage(tc.raw), tc.uploads); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestGeminiGeneratedFileDownloadUsesOnlyResourceName(t *testing.T) {
	value, err := nativePreview(provider{Kind: "gemini", BaseURL: "https://generativelanguage.googleapis.com"}, "files.download", json.RawMessage(`{"file_id":"generated-video","filename":"result.mp4"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	preview := value.(nativeRequestPreview)
	if preview.Method != http.MethodGet || preview.URL != "https://generativelanguage.googleapis.com/v1beta/files/generated-video:download?alt=media" {
		t.Fatalf("preview = %#v", preview)
	}
	if _, err = nativePreview(provider{Kind: "gemini", BaseURL: "https://generativelanguage.googleapis.com"}, "files.download", json.RawMessage(`{"name":"https://attacker.invalid/file"}`), nil); err == nil {
		t.Fatal("expected arbitrary URL to be rejected")
	}
}

func TestGeminiFileUploadUsesAuthenticatedSameOriginResumableFlow(t *testing.T) {
	var upstream *httptest.Server
	var calls atomic.Int32
	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch calls.Add(1) {
		case 1:
			if r.URL.Path != "/upload/v1beta/files" || r.Header.Get("x-goog-api-key") != "gemini-secret" || r.Header.Get("X-Goog-Upload-Protocol") != "resumable" || r.Header.Get("X-Goog-Upload-Header-Content-Length") != "4" {
				t.Fatalf("start request = %s %#v", r.URL, r.Header)
			}
			body, _ := io.ReadAll(r.Body)
			if string(body) != `{"file":{"display_name":"notes"}}` {
				t.Fatalf("metadata = %s", body)
			}
			w.Header().Set("X-Goog-Upload-URL", upstream.URL+"/upload-session?upload_id=private-token")
			w.WriteHeader(http.StatusOK)
		case 2:
			if r.URL.Path != "/upload-session" || r.Header.Get("x-goog-api-key") != "gemini-secret" || r.Header.Get("X-Goog-Upload-Command") != "upload, finalize" {
				t.Fatalf("upload request = %s %#v", r.URL, r.Header)
			}
			body, _ := io.ReadAll(r.Body)
			if string(body) != "DATA" {
				t.Fatalf("upload = %q", body)
			}
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"file":{"name":"files/one"}}`)
		}
	}))
	defer upstream.Close()
	dir := t.TempDir()
	a := &app{store: &store{dir: dir, config: configuration{}}}
	result, err := a.executeNative(context.Background(), provider{ID: "g", Kind: "gemini", BaseURL: upstream.URL, ProxyURL: "-", APIKey: "gemini-secret"}, "files.upload", json.RawMessage(`{"display_name":"notes"}`), resourceUploads{"file": {{Filename: "notes.txt", ContentType: "text/plain", Reader: strings.NewReader("DATA")}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || previewObject(t, result)["file"].(map[string]any)["name"] != "files/one" {
		t.Fatalf("result=%#v calls=%d", result, calls.Load())
	}
	logs, _ := os.ReadDir(filepath.Join(dir, "logs"))
	children, _ := os.ReadDir(filepath.Join(dir, "logs", logs[0].Name()))
	requestDirs := 0
	written := map[int64]bool{}
	for _, child := range children {
		if !child.IsDir() {
			continue
		}
		requestDirs++
		meta, _ := os.ReadFile(filepath.Join(dir, "logs", logs[0].Name(), child.Name(), "request.json"))
		if bytes.Contains(meta, []byte("private-token")) || bytes.Contains(meta, []byte("gemini-secret")) {
			t.Fatalf("request metadata leaked secret: %s", meta)
		}
		response := readMeta(filepath.Join(dir, "logs", logs[0].Name(), child.Name(), "response.json")).(map[string]any)
		if response["request_complete"] != true {
			t.Fatalf("response metadata = %#v", response)
		}
		written[int64(response["request_bytes"].(float64))] = true
	}
	if requestDirs != 2 {
		t.Fatalf("request log dirs = %d", requestDirs)
	}
	if !written[int64(len(`{"file":{"display_name":"notes"}}`))] || !written[4] {
		t.Fatalf("actual request byte counts = %#v", written)
	}
}

func TestGeminiFileUploadRejectsCrossOriginSessionURL(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Goog-Upload-URL", "https://attacker.invalid/upload")
	}))
	defer upstream.Close()
	a := &app{store: &store{dir: t.TempDir(), config: configuration{}}}
	_, err := a.executeNative(context.Background(), provider{ID: "g", Kind: "gemini", BaseURL: upstream.URL, ProxyURL: "-", APIKey: "secret"}, "files.upload", json.RawMessage(`{}`), resourceUploads{"file": {{Filename: "f", Reader: strings.NewReader("x")}}}, nil)
	if err == nil || !strings.Contains(err.Error(), "cross-origin") {
		t.Fatalf("error = %v", err)
	}
}

func TestGeminiUploadFailedStartLogsUnreadMetadata(t *testing.T) {
	dir := t.TempDir()
	a := &app{store: &store{dir: dir, config: configuration{}}}
	_, err := a.executeNative(context.Background(), provider{ID: "g", Kind: "gemini", BaseURL: "http://127.0.0.1:1", ProxyURL: "-", APIKey: "secret"}, "files.upload", json.RawMessage(`{"display_name":"notes"}`), resourceUploads{"file": {{Filename: "notes.txt", Reader: strings.NewReader("DATA")}}}, nil)
	if err == nil {
		t.Fatal("expected dial error")
	}
	logs, _ := os.ReadDir(filepath.Join(dir, "logs"))
	children, _ := os.ReadDir(filepath.Join(dir, "logs", logs[0].Name()))
	for _, child := range children {
		if child.IsDir() {
			meta := readMeta(filepath.Join(dir, "logs", logs[0].Name(), child.Name(), "response.json")).(map[string]any)
			if meta["request_bytes"] != float64(0) || meta["request_complete"] != false {
				t.Fatalf("response metadata = %#v", meta)
			}
		}
	}
}

func TestResolveNativeProfileUsesOneConfigurationSnapshot(t *testing.T) {
	cfg := configuration{Revision: 9, Providers: []provider{{ID: "p", Kind: "anthropic", APIKey: "snapshot-key"}}}
	p, err := resolveNativeProfileSnapshot(cfg, "anthropic", "p", "9")
	if err != nil {
		t.Fatal(err)
	}
	if p.APIKey != "snapshot-key" {
		t.Fatalf("provider = %#v", p)
	}
	if _, err = resolveNativeProfileSnapshot(cfg, "anthropic", "p", "8"); !errors.Is(err, errConflict) {
		t.Fatalf("stale revision error = %v", err)
	}
}

func TestNativeSSERequiresProtocolTerminalAndReturnsErrorEvents(t *testing.T) {
	tests := []struct {
		name, kind, operation, stream string
		wantErr                       bool
		wantErrorText                 string
	}{
		{"responses complete", "openai", "responses.create", "event: response.output_text.delta\ndata: {\"delta\":\"hi\"}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\"}\n\n", false, ""},
		{"responses nullable errors", "openai", "responses.create", "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\",\"status\":\"in_progress\",\"error\":null}}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"status\":\"completed\",\"error\":null}}\n\n", false, ""},
		{"responses truncated", "openai", "responses.create", "event: response.output_text.delta\ndata: {\"delta\":\"hi\"}\n\n", true, "ended before"},
		{"chat done", "xai", "chat.create", "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n", false, ""},
		{"chat finish reason", "openai", "chat.create", "data: {\"choices\":[{\"finish_reason\":\"stop\"}]}\n\n", false, ""},
		{"anthropic stop", "anthropic", "messages.create", "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", false, ""},
		{"gemini finish", "gemini", "content.generate", "data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n", false, ""},
		{"native error", "anthropic", "messages.create", "event: error\ndata: {\"type\":\"error\",\"error\":{\"message\":\"bad\"}}\n\n", true, "native SSE error: bad"},
		{"response failure event", "openai", "responses.create", "event: response.failed\ndata: {\"response\":{\"error\":{\"message\":\"bad\"}}}\n\n", true, "native SSE error: bad"},
		{"gemini error object", "gemini", "content.generate", "data: {\"error\":{\"message\":\"bad\"}}\n\n", true, "native SSE error: bad"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := collectNativeSSE(context.Background(), strings.NewReader(tc.stream), tc.kind, tc.operation)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v result=%#v", err, result)
			}
			if tc.wantErrorText != "" && !strings.Contains(err.Error(), tc.wantErrorText) {
				t.Fatalf("error = %v", err)
			}
			if partial, _ := result["partial"].(bool); partial != tc.wantErr {
				t.Fatalf("partial = %v", partial)
			}
		})
	}
}

func TestNativeSSEObserverReceivesEventsAndCanAbort(t *testing.T) {
	var observed []resourceEvent
	ctx := withNativeEventSink(context.Background(), func(event resourceEvent) error {
		observed = append(observed, event)
		return errors.New("browser disconnected")
	})
	result, err := collectNativeSSE(ctx, strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n"), "openai", "chat.create")
	if err == nil || !strings.Contains(err.Error(), "browser disconnected") {
		t.Fatalf("error = %v", err)
	}
	if len(observed) != 1 || len(result["events"].([]resourceEvent)) != 1 || result["partial"] != true {
		t.Fatalf("observed=%#v result=%#v", observed, result)
	}
}

func TestGeminiStreamingRoutesOutsideBody(t *testing.T) {
	value, err := nativePreview(provider{Kind: "gemini", BaseURL: "https://generativelanguage.googleapis.com"}, "content.generate", json.RawMessage(`{"model":"gemini-test","contents":[],"stream":true}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	preview := value.(nativeRequestPreview)
	if preview.URL != "https://generativelanguage.googleapis.com/v1beta/models/gemini-test:streamGenerateContent?alt=sse" {
		t.Fatalf("url = %s", preview.URL)
	}
	body := preview.Body.(map[string]any)
	if _, ok := body["stream"]; ok {
		t.Fatalf("body = %#v", body)
	}
}

func TestFailedDialLogsActualUnreadRequestBody(t *testing.T) {
	dir := t.TempDir()
	a := &app{store: &store{dir: dir, config: configuration{}}}
	_, err := a.executeNative(context.Background(), provider{ID: "p", Kind: "openai", BaseURL: "http://127.0.0.1:1/v1", ProxyURL: "-", APIKey: "key"}, "responses.create", json.RawMessage(`{"model":"m","input":"body never sent"}`), nil, nil)
	if err == nil {
		t.Fatal("expected dial error")
	}
	logs, _ := os.ReadDir(filepath.Join(dir, "logs"))
	children, _ := os.ReadDir(filepath.Join(dir, "logs", logs[0].Name()))
	for _, child := range children {
		if !child.IsDir() {
			continue
		}
		meta := readMeta(filepath.Join(dir, "logs", logs[0].Name(), child.Name(), "response.json")).(map[string]any)
		if meta["request_bytes"] != float64(0) || meta["request_complete"] != false {
			t.Fatalf("response metadata = %#v", meta)
		}
	}
}

func TestNativeErrorsRedactAPIKeyAndPreserveCause(t *testing.T) {
	const key = "private-native-key"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":"credential `+key+` rejected"}`)
	}))
	defer upstream.Close()
	a := &app{store: &store{dir: t.TempDir(), config: configuration{}}}
	_, err := a.executeNative(context.Background(), provider{ID: "p", Kind: "openai", BaseURL: upstream.URL, ProxyURL: "-", APIKey: key}, "models.list", json.RawMessage(`{}`), nil, nil)
	if err == nil || strings.Contains(err.Error(), key) || !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("error = %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = a.executeNative(canceled, provider{ID: "p", Kind: "openai", BaseURL: upstream.URL, ProxyURL: "-", APIKey: key}, "models.list", json.RawMessage(`{}`), nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation cause = %v", err)
	}
}

func TestGeminiUploadPreviewDescribesBothRequests(t *testing.T) {
	value, err := nativePreview(provider{Kind: "gemini", BaseURL: "https://generativelanguage.googleapis.com", APIKey: "secret"}, "files.upload", json.RawMessage(`{"display_name":"notes"}`), resourceUploads{"file": {{Filename: "notes.txt", ContentType: "text/plain", Reader: strings.NewReader("DATA")}}})
	if err != nil {
		t.Fatal(err)
	}
	preview := value.(nativeRequestPreview)
	if len(preview.Requests) != 2 {
		t.Fatalf("requests = %#v", preview.Requests)
	}
	if preview.Requests[0].URL != "https://generativelanguage.googleapis.com/upload/v1beta/files" || !strings.Contains(preview.Requests[1].URL, "X-Goog-Upload-URL") || preview.Requests[1].Method != "POST" {
		t.Fatalf("requests = %#v", preview.Requests)
	}
}
