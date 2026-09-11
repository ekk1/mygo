package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ekk1/mygo/utils/openai"
)

func TestOperationCatalogCoversNativeClient(t *testing.T) {
	want := []string{
		"files.upload", "files.list", "files.get", "files.delete", "files.download",
		"containers.create", "containers.list", "containers.get", "containers.delete",
		"containers.files.add", "containers.files.upload", "containers.files.list", "containers.files.get", "containers.files.delete", "containers.files.download",
		"batches.create", "batches.list", "batches.get", "batches.cancel",
		"responses.create", "responses.stream", "responses.get", "responses.cancel", "responses.delete", "responses.input_items.list", "responses.input_tokens.count", "responses.compact",
		"chat.create", "chat.stream",
		"images.generate", "images.stream", "images.edit", "images.edit_stream",
		"audio.speech", "audio.speech_stream", "audio.transcribe", "audio.transcribe_stream", "audio.translate",
	}
	got := make([]string, len(operationCatalog))
	for i, operation := range operationCatalog {
		got[i] = operation.ID
		if operation.Group == "" || operation.Label == "" || operation.Body == nil {
			t.Errorf("incomplete catalog entry %#v", operation)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("operation IDs = %#v, want %#v", got, want)
	}
	c, err := openai.New(openai.Config{APIKey: "test", BaseURL: "http://provider.test/v1", ProxyURL: "-"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseIdleConnections()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, operation := range operationCatalog {
		_, err := dispatchResourceOperation(ctx, c, operation.ID, json.RawMessage(`{}`), nil)
		if err != nil && strings.Contains(err.Error(), "unknown operation") {
			t.Errorf("catalog operation has no dispatcher: %s", operation.ID)
		}
	}
}

func TestDispatchResourceOperationsUseNativeMethods(t *testing.T) {
	var calls int
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch calls {
		case 1:
			if r.Method != http.MethodGet || r.URL.Path != "/v1/files" || r.URL.RawQuery != "limit=2" {
				t.Fatalf("list request = %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
			}
			io.WriteString(w, `{"object":"list","data":[],"has_more":false}`)
		case 2:
			if r.Method != http.MethodPost || r.URL.Path != "/v1/files" {
				t.Fatalf("upload request = %s %s", r.Method, r.URL.Path)
			}
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			file, header, err := r.FormFile("file")
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			body, _ := io.ReadAll(file)
			if r.FormValue("purpose") != "batch" || header.Filename != "jobs.jsonl" || string(body) != "job\n" {
				t.Fatalf("upload = %q %q %q", r.FormValue("purpose"), header.Filename, body)
			}
			io.WriteString(w, `{"id":"file_1","object":"file","filename":"jobs.jsonl","purpose":"batch"}`)
		case 3:
			if r.Method != http.MethodPost || r.URL.Path != "/v1/responses" {
				t.Fatalf("stream request = %s %s", r.Method, r.URL.Path)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n")
			io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\"}\n\n")
		case 4:
			if r.Method != http.MethodPost || r.URL.Path != "/v1/audio/speech" {
				t.Fatalf("speech request = %s %s", r.Method, r.URL.Path)
			}
			var speech map[string]any
			if err := json.NewDecoder(r.Body).Decode(&speech); err != nil {
				t.Fatal(err)
			}
			if speech["model"] != "tts-test" || speech["input"] != "hello" || speech["voice"] != "alloy" {
				t.Fatalf("speech body = %#v", speech)
			}
			w.Header().Set("Content-Type", "audio/mpeg")
			io.WriteString(w, "MP3")
		default:
			t.Fatalf("unexpected call %d", calls)
		}
	}))
	defer s.Close()
	c, err := openai.New(openai.Config{APIKey: "test", BaseURL: s.URL + "/v1", ProxyURL: "-"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseIdleConnections()

	result, err := dispatchResourceOperation(context.Background(), c, "files.list", json.RawMessage(`{"limit":2}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if list, ok := result.(*openai.FileList); !ok || list.HTTP == nil {
		t.Fatalf("files.list result = %#v", result)
	}
	result, err = dispatchResourceOperation(context.Background(), c, "files.upload", json.RawMessage(`{"purpose":"batch"}`), resourceUploads{
		"file": {{Filename: "jobs.jsonl", ContentType: "application/jsonl", Reader: strings.NewReader("job\n")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if file, ok := result.(*openai.File); !ok || file.ID != "file_1" {
		t.Fatalf("files.upload result = %#v", result)
	}
	result, err = dispatchResourceOperation(context.Background(), c, "responses.stream", json.RawMessage(`{"model":"gpt-test","input":"hello"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	stream, ok := result.(resourceStreamResult)
	if !ok || len(stream.Events) != 2 || stream.Events[0].Type != "response.output_text.delta" {
		t.Fatalf("responses.stream result = %#v", result)
	}
	result, err = dispatchResourceOperation(context.Background(), c, "audio.speech", json.RawMessage(`{"model":"tts-test","input":"hello","voice":"alloy","filename":"voice.mp3"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	download, ok := result.(resourceDownload)
	if !ok || download.Name != "voice.mp3" || download.ContentType != "audio/mpeg" {
		t.Fatalf("audio.speech result = %#v", result)
	}
	defer os.Remove(download.Path)
	content, err := os.ReadFile(download.Path)
	if err != nil || string(content) != "MP3" {
		t.Fatalf("speech download = %q, %v", content, err)
	}
}

func TestDispatchRejectsUnknownOperationAndMissingUpload(t *testing.T) {
	c, err := openai.New(openai.Config{APIKey: "test", BaseURL: "http://example.test", ProxyURL: "-"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseIdleConnections()
	if _, err := dispatchResourceOperation(context.Background(), c, "proxy.any_url", json.RawMessage(`{}`), nil); err == nil || !strings.Contains(err.Error(), "unknown operation") {
		t.Fatalf("unknown operation error = %v", err)
	}
	if _, err := dispatchResourceOperation(context.Background(), c, "files.upload", json.RawMessage(`{"purpose":"batch"}`), nil); err == nil || !strings.Contains(err.Error(), "upload") {
		t.Fatalf("missing upload error = %v", err)
	}
	if _, err := dispatchResourceOperation(context.Background(), c, "files.list", json.RawMessage(`{"unsupported":true}`), nil); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unsupported list field error = %v", err)
	}
}

func TestDispatchPreservesFutureNativeFieldsAndLocalOnlySpeechFilename(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch calls {
		case 1:
			if r.URL.Path != "/v1/responses" {
				t.Fatalf("response path = %s", r.URL.Path)
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["custom_native"] != true {
				t.Fatalf("future response field dropped: %#v", body)
			}
			io.WriteString(w, `{"id":"resp_1","object":"response","status":"completed","output":[]}`)
		case 2:
			if r.URL.Path != "/v1/audio/speech" {
				t.Fatalf("speech path = %s", r.URL.Path)
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["custom_native"] != "kept" || body["filename"] != nil {
				t.Fatalf("speech native/local fields = %#v", body)
			}
			io.WriteString(w, "audio")
		case 3:
			if r.URL.Path != "/v1/audio/transcriptions" {
				t.Fatalf("transcription path = %s", r.URL.Path)
			}
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			if r.FormValue("language") != "zh" || r.FormValue("known_speaker_names[]") != "Alice" || r.FormValue("timestamp_granularities[]") != "word" || r.FormValue("custom_native") != "kept" {
				t.Fatalf("transcription form = %#v", r.MultipartForm.Value)
			}
			io.WriteString(w, `{"text":"ok"}`)
		default:
			t.Fatalf("unexpected request %d", calls)
		}
	}))
	defer server.Close()
	c, err := openai.New(openai.Config{APIKey: "test", BaseURL: server.URL + "/v1", ProxyURL: "-"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseIdleConnections()

	if _, err := dispatchResourceOperation(context.Background(), c, "responses.create", json.RawMessage(`{"model":"gpt-test","input":"hello","custom_native":true}`), nil); err != nil {
		t.Fatal(err)
	}
	result, err := dispatchResourceOperation(context.Background(), c, "audio.speech", json.RawMessage(`{"model":"tts-test","input":"hello","voice":"alloy","filename":"voice.mp3","custom_native":"kept"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	download := result.(resourceDownload)
	defer os.Remove(download.Path)
	transcriptionParams := json.RawMessage(`{"model":"transcribe-test","language":"zh","known_speaker_names":["Alice"],"timestamp_granularities":["word"],"custom_native":"kept"}`)
	if _, err := dispatchResourceOperation(context.Background(), c, "audio.transcribe", transcriptionParams, resourceUploads{"file": {{Filename: "audio.wav", Reader: strings.NewReader("wave")}}}); err != nil {
		t.Fatal(err)
	}
}

func TestResourceEventCollectorCapsAggregateSizeAndRetainsPartialEvents(t *testing.T) {
	collector := resourceEventCollector{limit: 40}
	if err := collector.append(resourceEvent{Type: "delta", Data: "one"}, 3); err != nil {
		t.Fatal(err)
	}
	err := collector.append(resourceEvent{Type: "delta", Data: strings.Repeat("x", 40)}, 40)
	if err == nil || !strings.Contains(err.Error(), "stream") || len(collector.events) != 1 || collector.events[0].Data != "one" {
		t.Fatalf("collector = %#v, %v", collector, err)
	}
}

func TestResourceAPIMultipartUsesProviderProxyAndOmitsResponseBodyLog(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Host != "provider.test" || r.URL.Path != "/v1/files" {
			if r.Method == http.MethodGet && r.URL.Path == "/v1/files/file_proxy/content" {
				w.Header().Set("Content-Type", "text/plain")
				io.WriteString(w, "downloaded")
				return
			}
			t.Fatalf("proxy target = %s %s", r.Method, r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		body, _ := io.ReadAll(file)
		if header.Filename != "jobs.jsonl" || string(body) != "payload\n" || r.FormValue("purpose") != "batch" {
			t.Fatalf("provider upload = %q %q %q", header.Filename, body, r.FormValue("purpose"))
		}
		io.WriteString(w, `{"id":"file_proxy","object":"file","filename":"jobs.jsonl","purpose":"batch"}`)
	}))
	defer proxy.Close()

	dataDir := t.TempDir()
	a, server, err := newApp(dataDir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	cfg := a.store.configSnapshot(false)
	cfg.Providers = []provider{{ID: "proxy", Name: "Proxy", APIKey: "secret", BaseURL: "http://provider.test/v1", ProxyURL: proxy.URL}}
	if _, err := a.store.saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("provider_id", "proxy")
	_ = writer.WriteField("params", `{"purpose":"batch"}`)
	part, err := writer.CreateFormFile("file", "jobs.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(part, "payload\n")
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, httpServer.URL+"/api/operations/files.upload", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-Workbench-Request", "1")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	responseBody, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK || !bytes.Contains(responseBody, []byte(`"id":"file_proxy"`)) {
		t.Fatalf("resource response = %d %s", response.StatusCode, responseBody)
	}

	logDirs, err := os.ReadDir(filepath.Join(dataDir, "logs"))
	if err != nil || len(logDirs) != 1 {
		t.Fatalf("operation logs = %d, %v", len(logDirs), err)
	}
	operationDir := filepath.Join(dataDir, "logs", logDirs[0].Name())
	requestDirs, err := os.ReadDir(operationDir)
	if err != nil {
		t.Fatal(err)
	}
	var nativeDir string
	for _, entry := range requestDirs {
		if entry.IsDir() {
			nativeDir = filepath.Join(operationDir, entry.Name())
		}
	}
	if nativeDir == "" {
		t.Fatal("native request log missing")
	}
	requestBody, err := os.ReadFile(filepath.Join(nativeDir, "request.body"))
	if err != nil || !bytes.Contains(requestBody, []byte("payload\n")) {
		t.Fatalf("logged request body missing payload: %v", err)
	}
	if _, err := os.Stat(filepath.Join(nativeDir, "response.body")); !os.IsNotExist(err) {
		t.Fatalf("response body log exists: %v", err)
	}
	metadata, err := os.ReadFile(filepath.Join(operationDir, "context.json"))
	if err != nil || !bytes.Contains(metadata, []byte(`"status": "complete"`)) || !bytes.Contains(metadata, []byte(`"save_response": false`)) {
		t.Fatalf("operation metadata = %s, %v", metadata, err)
	}

	downloadRequest, _ := http.NewRequest(http.MethodPost, httpServer.URL+"/api/operations/files.download", strings.NewReader(`{"provider_id":"proxy","params":{"file_id":"file_proxy","filename":"jobs.jsonl"}}`))
	downloadRequest.Header.Set("Content-Type", "application/json")
	downloadRequest.Header.Set("X-Workbench-Request", "1")
	downloadResponse, err := http.DefaultClient.Do(downloadRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer downloadResponse.Body.Close()
	downloadBody, _ := io.ReadAll(downloadResponse.Body)
	if downloadResponse.StatusCode != http.StatusOK || downloadResponse.Header.Get("Content-Type") != "application/octet-stream" || !strings.Contains(downloadResponse.Header.Get("Content-Disposition"), "jobs.jsonl") || string(downloadBody) != "downloaded" {
		t.Fatalf("download response = %d %q %q %q", downloadResponse.StatusCode, downloadResponse.Header.Get("Content-Type"), downloadResponse.Header.Get("Content-Disposition"), downloadBody)
	}
}

func TestResourceAPIChatStreamSerializesDoneSentinel(t *testing.T) {
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("provider request = %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"id\":\"chunk_1\",\"choices\":[]}\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer providerServer.Close()

	dataDir := t.TempDir()
	a, server, err := newApp(dataDir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	cfg := a.store.configSnapshot(false)
	cfg.Providers = []provider{{ID: "local", Name: "Local", APIKey: "secret", BaseURL: providerServer.URL + "/v1", ProxyURL: "-"}}
	if _, err := a.store.saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	body := strings.NewReader(`{"provider_id":"local","params":{"model":"gpt-test","messages":[{"role":"user","content":"hello"}]}}`)
	req, _ := http.NewRequest(http.MethodPost, httpServer.URL+"/api/operations/chat.stream", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Workbench-Request", "1")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	responseBody, _ := io.ReadAll(response.Body)
	var decoded struct {
		Events []struct {
			Data any `json:"data"`
		} `json:"events"`
	}
	if response.StatusCode != http.StatusOK || json.Unmarshal(responseBody, &decoded) != nil || len(decoded.Events) != 2 || decoded.Events[1].Data != "[DONE]" {
		t.Fatalf("chat stream response = %d %s (%#v)", response.StatusCode, responseBody, decoded)
	}
}
