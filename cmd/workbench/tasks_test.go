package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ekk1/mygo/utils/httpserver"
)

func backgroundApp(t *testing.T, upstream http.Handler) (*app, http.Handler, configuration) {
	t.Helper()
	remote := httptest.NewServer(upstream)
	t.Cleanup(remote.Close)
	a, server, err := newApp(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.stopAccepting(); a.active.Wait(); server.Close() })
	cfg := a.store.configSnapshot(false)
	cfg.Providers = []provider{{ID: "p", Name: "test", Kind: "openai", APIKey: "secret", BaseURL: remote.URL + "/v1", ProxyURL: "-"}}
	cfg, err = a.store.saveConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return a, server, cfg
}

func TestBackgroundGeminiHistoryUsesPromptTitle(t *testing.T) {
	for _, tc := range []struct {
		operation string
		params    map[string]any
		want      string
	}{
		{"content.generate", map[string]any{"model": "image", "contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "画一幅海边日落"}, map[string]any{"inlineData": map[string]any{"mimeType": "image/png", "data": "image bytes"}}}}}}, "画一幅海边日落"},
		{"videos.create", map[string]any{"model": "video", "instances": []any{map[string]any{"prompt": "海浪缓缓涌上沙滩"}}}, "海浪缓缓涌上沙滩"},
	} {
		t.Run(tc.operation, func(t *testing.T) {
			a, handler, cfg := backgroundApp(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				writeJSON(w, 200, map[string]string{"name": "operations/test"})
			}))
			cfg.Providers[0].Kind = "gemini"
			cfg.Providers[0].ID = "gemini"
			if _, err := a.store.saveConfig(cfg); err != nil {
				t.Fatal(err)
			}
			w := backgroundRequest(t, handler, "POST", "/api/native/gemini/"+tc.operation+"?background=1", map[string]any{"provider_id": "gemini", "params": tc.params})
			if w.Code != 202 {
				t.Fatalf("task: %d %s", w.Code, w.Body.String())
			}
			var response struct {
				Task generationTask `json:"task"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			final := awaitTask(t, handler, response.Task.ID, "complete")
			if final["title"] != tc.want {
				t.Fatalf("title=%q, want %q", final["title"], tc.want)
			}
		})
	}
}

func TestBackgroundShutdownWaitsForInterruptedTaskPersistence(t *testing.T) {
	started := make(chan struct{})
	a, handler, cfg := backgroundApp(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		close(started)
		<-r.Context().Done()
	}))
	server := handler.(*httpserver.Server)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, shutdown := context.WithCancel(context.Background())
	defer shutdown()
	done := make(chan error, 1)
	go func() { done <- serveWorkbench(ctx, a, server, listener) }()
	v, err := a.store.createSession("shutdown", "p", "responses.create")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{"provider_id": "p", "operation": "responses.create", "params": map[string]any{"model": "m", "input": "q"}, "revision": cfg.Revision, "text": "q", "background": true})
	req, _ := http.NewRequest("POST", "http://"+listener.Addr().String()+"/api/sessions/"+v.ID+"/native", bytes.NewReader(data))
	req.Header.Set("X-Workbench-Request", "1")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Task generationTask `json:"task"`
	}
	err = json.NewDecoder(res.Body).Decode(&response)
	res.Body.Close()
	if err != nil || res.StatusCode != 202 {
		t.Fatalf("ack = %+v, %v", response, err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream did not start")
	}
	shutdown()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown did not cancel and persist task")
	}
	var saved generationTask
	if err := loadKV(filepath.Join(a.store.dir, "tasks", response.Task.ID+".json"), &saved); err != nil || saved.Status != "interrupted" {
		t.Fatalf("shutdown task = %+v, %v", saved, err)
	}
	var savedSession session
	if err := loadKV(filepath.Join(a.store.dir, "sessions", v.ID+".json"), &savedSession); err != nil || savedSession.Messages[1].Status != "interrupted" {
		t.Fatalf("shutdown session = %+v, %v", savedSession, err)
	}
}

type blockedTaskWriter struct {
	*httptest.ResponseRecorder
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (w *blockedTaskWriter) Write(data []byte) (int, error) {
	w.once.Do(func() { close(w.entered); <-w.release })
	return w.ResponseRecorder.Write(data)
}

func TestBackgroundSlowStreamSubscriberDoesNotBlockWorker(t *testing.T) {
	a, handler, cfg := backgroundApp(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for i := 0; i < 200; i++ {
			io.WriteString(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"x\"}\n\n")
		}
		io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"role\":\"assistant\",\"content\":[{\"text\":\"finished despite slow subscriber\"}]}]}}\n\n")
	}))
	v, _ := a.store.createSession("slow reader", "p", "responses.create")
	data, _ := json.Marshal(map[string]any{"provider_id": "p", "operation": "responses.create", "params": map[string]any{"model": "m", "input": "q"}, "revision": cfg.Revision, "background": true, "stream": true})
	r := httptest.NewRequest("POST", "http://localhost/api/sessions/"+v.ID+"/native", bytes.NewReader(data))
	r.Header.Set("X-Workbench-Request", "1")
	w := &blockedTaskWriter{ResponseRecorder: httptest.NewRecorder(), entered: make(chan struct{}), release: make(chan struct{})}
	done := make(chan struct{})
	go func() { handler.ServeHTTP(w, r); close(done) }()
	defer func() { close(w.release); <-done }()
	select {
	case <-w.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not start")
	}
	current, _ := a.store.getSession(v.ID)
	taskID := current.Messages[1].TaskID
	awaitTask(t, handler, taskID, "complete")
	current, _ = a.store.getSession(v.ID)
	if current.Messages[1].Text != "finished despite slow subscriber" {
		t.Fatalf("saved output = %+v", current.Messages[1])
	}
}

func TestBackgroundSessionSaveFailureKeepsDurableResultAndReportsFailure(t *testing.T) {
	release := make(chan struct{})
	a, handler, cfg := backgroundApp(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"output_text":"upstream result retained"}`)
	}))
	v, _ := a.store.createSession("save failure", "p", "responses.create")
	w := backgroundRequest(t, handler, "POST", "/api/sessions/"+v.ID+"/native", map[string]any{"provider_id": "p", "operation": "responses.create", "params": map[string]any{"model": "m", "input": "q"}, "revision": cfg.Revision, "background": true})
	if w.Code != 202 {
		t.Fatalf("ack: %d %s", w.Code, w.Body.String())
	}
	var response struct {
		Task generationTask `json:"task"`
	}
	json.Unmarshal(w.Body.Bytes(), &response)
	if w := backgroundRequest(t, handler, "POST", "/api/sessions/"+v.ID+"/native", map[string]any{"provider_id": "p", "operation": "responses.create", "params": map[string]any{"model": "m", "input": "duplicate"}, "revision": cfg.Revision, "background": true}); w.Code != 409 {
		t.Fatalf("busy session accepted duplicate: %d %s", w.Code, w.Body.String())
	}
	path := filepath.Join(a.store.dir, "sessions", v.ID+".json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	close(release)
	final := awaitTask(t, handler, response.Task.ID, "error")
	if !strings.Contains(jsonValue(final["result"]), "upstream result retained") || final["error"] == "" {
		t.Fatalf("failed save lost recoverable result: %+v", final)
	}
	current, _ := a.store.getSession(v.ID)
	if current.Messages[1].Status != "pending" {
		t.Fatalf("failed save published session success: %+v", current.Messages[1])
	}
}

func TestBackgroundStreamDisconnectAndExplicitStop(t *testing.T) {
	for _, stop := range []bool{false, true} {
		t.Run(map[bool]string{false: "disconnect", true: "stop"}[stop], func(t *testing.T) {
			release := make(chan struct{})
			a, handler, cfg := backgroundApp(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"first\"}\n\n")
				w.(http.Flusher).Flush()
				select {
				case <-release:
					io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"role\":\"assistant\",\"content\":[{\"text\":\"first and final\"}]}]}}\n\n")
				case <-r.Context().Done():
				}
			}))
			local := httptest.NewServer(handler)
			defer local.Close()
			v, err := a.store.createSession("stream", "p", "responses.create")
			if err != nil {
				t.Fatal(err)
			}
			payload := map[string]any{"provider_id": "p", "operation": "responses.create", "params": map[string]any{"model": "m", "input": "q"}, "revision": cfg.Revision, "text": "q", "background": true, "stream": true}
			data, _ := json.Marshal(payload)
			req, _ := http.NewRequest("POST", local.URL+"/api/sessions/"+v.ID+"/native", bytes.NewReader(data))
			req.Header.Set("X-Workbench-Request", "1")
			response, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			decoder := json.NewDecoder(response.Body)
			var started struct {
				Type    string         `json:"type"`
				Task    generationTask `json:"task"`
				Session session        `json:"session"`
			}
			if err := decoder.Decode(&started); err != nil || started.Type != "started" || started.Task.ID == "" || started.Session.Messages[1].TaskID != started.Task.ID {
				t.Fatalf("start = %+v, %v", started, err)
			}
			var event map[string]any
			if err := decoder.Decode(&event); err != nil || event["type"] != "event" {
				t.Fatalf("event = %+v, %v", event, err)
			}
			response.Body.Close()
			wantStatus, wantText := "complete", "first and final"
			if stop {
				w := backgroundRequest(t, handler, "POST", "/api/tasks/"+started.Task.ID+"/cancel", nil)
				if w.Code != 200 {
					t.Fatalf("stop: %d %s", w.Code, w.Body.String())
				}
				wantStatus, wantText = "cancelled", "first"
			} else {
				pending, _ := a.store.getSession(v.ID)
				if pending.Messages[1].Status != "pending" {
					t.Fatalf("disconnect finalized task: %+v", pending.Messages[1])
				}
				close(release)
			}
			awaitTask(t, handler, started.Task.ID, wantStatus)
			got, _ := a.store.getSession(v.ID)
			if got.Messages[1].Status != wantStatus || got.Messages[1].Text != wantText {
				t.Fatalf("saved assistant = %+v", got.Messages[1])
			}
		})
	}
}

func TestBackgroundMultipartUploadSurvivesHandlerCleanupAndIsSaved(t *testing.T) {
	want := append([]byte("RIFF"), bytes.Repeat([]byte{0, 255, 17, 0}, 2300000)...)
	received := make(chan []byte, 1)
	release := make(chan struct{})
	a, handler, _ := backgroundApp(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
			return
		}
		defer r.MultipartForm.RemoveAll()
		f, _, err := r.FormFile("file")
		if err != nil {
			t.Error(err)
			return
		}
		defer f.Close()
		data, _ := io.ReadAll(f)
		received <- data
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"text":"transcribed"}`)
	}))
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	writer.WriteField("provider_id", "p")
	writer.WriteField("params", `{"model":"whisper-1"}`)
	file, err := writer.CreateFormFile("file", "voice.wav")
	if err != nil {
		t.Fatal(err)
	}
	file.Write(want)
	writer.Close()
	r := httptest.NewRequest("POST", "http://localhost/api/native/openai/audio.transcribe?background=1&feature=transcribe", &body)
	r.Header.Set("X-Workbench-Request", "1")
	r.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 202 {
		t.Fatalf("multipart acknowledgement: %d %s", w.Code, w.Body.String())
	}
	// Force the same cleanup net/http performs when its request finishes.
	r.MultipartForm.RemoveAll()
	var response struct {
		Task generationTask `json:"task"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-received:
		if !bytes.Equal(got, want) {
			t.Fatalf("upload bytes changed: got %d want %d", len(got), len(want))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("task could not read upload after handler cleanup")
	}
	close(release)
	awaitTask(t, handler, response.Task.ID, "complete")
	if len(a.assets.List(false)) == 0 {
		t.Fatal("input upload was not saved to asset library")
	}
}

func TestBackgroundBinaryResultPersistsAndDeleteOnlyRemovesTerminalTask(t *testing.T) {
	release := make(chan struct{})
	want := []byte{'I', 'D', '3', 0, 255, 0, 1, 2, 3}
	a, handler, _ := backgroundApp(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Write(want)
	}))
	w := backgroundRequest(t, handler, "POST", "/api/native/openai/audio.speech?background=1&feature=speech", map[string]any{"provider_id": "p", "params": map[string]any{"model": "tts", "input": "hello", "voice": "alloy"}})
	if w.Code != 202 {
		t.Fatalf("binary task: %d %s", w.Code, w.Body.String())
	}
	var response struct {
		Task generationTask `json:"task"`
	}
	json.Unmarshal(w.Body.Bytes(), &response)
	w = backgroundRequest(t, handler, "DELETE", "/api/tasks/"+response.Task.ID, nil)
	if w.Code != 409 {
		t.Fatalf("deleted running task: %d %s", w.Code, w.Body.String())
	}
	close(release)
	final := awaitTask(t, handler, response.Task.ID, "complete")
	ids, _ := final["asset_ids"].([]any)
	if len(ids) != 1 {
		t.Fatalf("binary asset missing: %+v", final)
	}
	_, file, err := a.assets.OpenContent(ids[0].(string))
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(file)
	file.Close()
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("saved binary = %v, %v", got, err)
	}
	if strings.Contains(jsonValue(final), "workbench-download-") || strings.Contains(jsonValue(final), `"Path"`) {
		t.Fatalf("temporary path leaked into durable result: %+v", final)
	}
	w = backgroundRequest(t, handler, "DELETE", "/api/tasks/"+response.Task.ID, nil)
	if w.Code != 200 {
		t.Fatalf("delete final task: %d %s", w.Code, w.Body.String())
	}
	if w = backgroundRequest(t, handler, "GET", "/api/tasks/"+response.Task.ID, nil); w.Code != 404 {
		t.Fatalf("deleted task remains: %d", w.Code)
	}
	if _, err := a.assets.Get(ids[0].(string)); err != nil {
		t.Fatalf("task deletion removed independent asset: %v", err)
	}
	if _, err := os.Stat(filepath.Join(a.store.dir, "tasks", response.Task.ID+".json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted task remains on disk: %v", err)
	}
}

func TestTaskRestartMarksInterruptedAndDoesNotDispatch(t *testing.T) {
	dir := t.TempDir()
	a, server, err := newApp(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	server.Close()
	v, _ := a.store.createSession("interrupted", "p", "responses.create")
	e, _ := a.store.entry(v.ID)
	v.Messages = []message{{ID: "assistant", Role: "assistant", Status: "pending", TaskID: "unfinished"}}
	v.HeadID = "assistant"
	e.mu.Lock()
	err = a.store.persistSession(e, v)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"queued", "running", "complete"} {
		task := generationTask{ID: status, Status: status, SessionID: v.ID, Result: json.RawMessage(`{"retained":true}`)}
		if err := saveKV(filepath.Join(dir, "tasks", status+".json"), task); err != nil {
			t.Fatal(err)
		}
	}
	restarted, server, err := newApp(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	for _, id := range []string{"queued", "running", "complete"} {
		var persisted generationTask
		if err := loadKV(filepath.Join(dir, "tasks", id+".json"), &persisted); err != nil {
			t.Fatal(err)
		}
		want := "interrupted"
		if id == "complete" {
			want = "complete"
		}
		if persisted.Status != want || string(persisted.Result) != `{"retained":true}` {
			t.Fatalf("restored task: %+v", persisted)
		}
	}
	var saved session
	if err := loadKV(filepath.Join(dir, "sessions", v.ID+".json"), &saved); err != nil || saved.Messages[0].Status != "interrupted" {
		t.Fatalf("interrupted session: %+v, %v", saved, err)
	}
	restarted.active.Wait()
	logs, err := os.ReadDir(filepath.Join(dir, "logs"))
	if err == nil && len(logs) > 0 {
		t.Fatal("restart dispatched upstream work")
	}
}

func backgroundRequest(t *testing.T, handler http.Handler, method, path string, value any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(method, "http://localhost"+path, bytes.NewReader(b))
	r.Header.Set("X-Workbench-Request", "1")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func awaitTask(t *testing.T, handler http.Handler, id, status string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		w := backgroundRequest(t, handler, "GET", "/api/tasks/"+id, nil)
		var value map[string]any
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &value) != nil {
			t.Fatalf("task lookup: %d %s", w.Code, w.Body.String())
		}
		if value["status"] == status {
			return value
		}
		if value["status"] != "running" && value["status"] != "queued" {
			t.Fatalf("task status = %v, want %s: %s", value["status"], status, w.Body.String())
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("task %s never reached %s", id, status)
	return nil
}

func TestBackgroundConversationAcknowledgesBeforeResultAndSurvivesDisconnect(t *testing.T) {
	release := make(chan struct{})
	a, handler, cfg := backgroundApp(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		select {
		case <-release:
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"id":"r1","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"saved after disconnect"}]}]}`)
		case <-r.Context().Done():
		}
	}))
	v, err := a.store.createSession("background", "p", "responses.create")
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"provider_id": "p", "operation": "responses.create", "params": map[string]any{"model": "m", "input": "question"}, "revision": cfg.Revision, "text": "question", "background": true}
	data, _ := json.Marshal(payload)
	ctx, disconnect := context.WithCancel(context.Background())
	defer disconnect()
	r := httptest.NewRequest("POST", "http://localhost/api/sessions/"+v.ID+"/native", bytes.NewReader(data)).WithContext(ctx)
	r.Header.Set("X-Workbench-Request", "1")
	w := httptest.NewRecorder()
	ack := make(chan struct{})
	go func() { handler.ServeHTTP(w, r); close(ack) }()
	select {
	case <-ack:
	case <-time.After(time.Second):
		t.Fatal("background acknowledgement waited for upstream")
	}
	if w.Code != 202 {
		t.Fatalf("background acknowledgement: %d %s", w.Code, w.Body.String())
	}
	var response struct {
		Task struct {
			ID string `json:"id"`
		} `json:"task"`
		Session session `json:"session"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.Task.ID == "" || len(response.Session.Messages) != 2 {
		t.Fatalf("invalid acknowledgement: %s (%v)", w.Body.String(), err)
	}
	if err := a.store.deleteSession(v.ID); !errors.Is(err, errBusy) {
		t.Fatalf("pending task allowed deleting its session: %v", err)
	}
	disconnect()
	close(release)
	awaitTask(t, handler, response.Task.ID, "complete")
	got, err := a.store.getSession(v.ID)
	if err != nil || got.Messages[1].Status != "complete" || got.Messages[1].Text != "saved after disconnect" {
		t.Fatalf("final session = %+v, %v", got, err)
	}
	var persisted map[string]any
	if err := loadKV(filepath.Join(a.store.dir, "tasks", response.Task.ID+".json"), &persisted); err != nil || persisted["status"] != "complete" {
		t.Fatalf("durable task = %+v, %v", persisted, err)
	}
	listed := backgroundRequest(t, handler, "GET", "/api/tasks?provider_id=p&feature=chat", nil)
	if listed.Code != 200 || strings.Contains(listed.Body.String(), "saved after disconnect") {
		t.Fatalf("list includes large result: %d %s", listed.Code, listed.Body.String())
	}
}
