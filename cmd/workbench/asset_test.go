package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ekk1/mygo/utils/assetstore"
)

const assetTestPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jwioAAAAASUVORK5CYII="

func newAssetTestApp(t *testing.T) *app {
	t.Helper()
	s, err := assetstore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &app{assets: s}
}

func TestImportSavedSessionMediaIsExplicitAndIdempotent(t *testing.T) {
	a, server, err := newApp(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	v, err := a.store.createSession("old images", "deleted-profile", "responses.create")
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := a.store.entry(v.ID)
	v.Messages = []message{{ID: "old-image", Role: "assistant", Status: "complete", Output: json.RawMessage(`{"output":[{"type":"image_generation_call","result":"` + assetTestPNG + `"}]}`)}}
	v.HeadID = "old-image"
	entry.mu.Lock()
	err = a.store.persistSession(entry, v)
	entry.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if len(a.assets.List(false)) != 0 {
		t.Fatal("session loading must not import assets automatically")
	}
	for attempt := 0; attempt < 2; attempt++ {
		w := backgroundRequest(t, server, "POST", "/api/assets/import-sessions", nil)
		if w.Code != 200 {
			t.Fatalf("import: %d %s", w.Code, w.Body.String())
		}
		if len(a.assets.List(false)) != 1 {
			t.Fatal("repeated import duplicates media")
		}
		got, _ := a.store.getSession(v.ID)
		if len(got.Messages[0].AssetIDs) != 1 || string(got.Messages[0].Output) != string(v.Messages[0].Output) {
			t.Fatalf("import changed native context or lost reference: %+v", got.Messages[0])
		}
	}
}

func addTestImage(t *testing.T, a *app, favorite bool) assetstore.Asset {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(assetTestPNG)
	if err != nil {
		t.Fatal(err)
	}
	asset, err := a.assets.Add("image.png", "image/png", assetstore.Source{"kind": "upload"}, bytes.NewReader(b), 1000)
	if err != nil {
		t.Fatal(err)
	}
	asset, err = a.assets.Update(asset.ID, nil, &favorite)
	if err != nil {
		t.Fatal(err)
	}
	return asset
}

func TestAssetSelectionRequiresFavoriteAndPreservesNativeFields(t *testing.T) {
	a := newAssetTestApp(t)
	asset := addTestImage(t, a, false)
	params := json.RawMessage(`{"model":"m","input":"hello","temperature":0,"metadata":{"key":"value"}}`)
	_, _, cleanup, err := a.resolveAssets("openai", "responses.create", params, nil, map[string][]string{"attachment": {asset.ID}})
	cleanup()
	if err == nil {
		t.Fatal("unfavorited asset selected")
	}
	favorite := true
	if _, err = a.assets.Update(asset.ID, nil, &favorite); err != nil {
		t.Fatal(err)
	}
	resolved, _, cleanup, err := a.resolveAssets("openai", "responses.create", params, nil, map[string][]string{"attachment": {asset.ID}})
	defer cleanup()
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err = json.Unmarshal(resolved, &body); err != nil {
		t.Fatal(err)
	}
	if body["temperature"] != float64(0) || body["metadata"].(map[string]any)["key"] != "value" {
		t.Fatalf("native fields changed: %s", resolved)
	}
	input := body["input"].([]any)[0].(map[string]any)
	parts := input["content"].([]any)
	if len(parts) != 2 || parts[0].(map[string]any)["text"] != "hello" || parts[1].(map[string]any)["type"] != "input_image" || parts[1].(map[string]any)["image_url"] != "data:image/png;base64,"+assetTestPNG {
		t.Fatalf("responses image shape: %s", resolved)
	}
	if err = a.assets.Delete(asset.ID); err != nil {
		t.Fatal(err)
	}
	_, _, cleanup, err = a.resolveAssets("openai", "responses.create", params, nil, map[string][]string{"attachment": {asset.ID}})
	cleanup()
	if err == nil {
		t.Fatal("deleted selection accepted")
	}
}

func TestAssetProviderAttachmentShapes(t *testing.T) {
	a := newAssetTestApp(t)
	asset := addTestImage(t, a, true)
	for _, tc := range []struct{ vendor, operation, field, params, fragment string }{
		{"openai", "chat.create", "attachment", `{"messages":[{"role":"user","content":"hello"}]}`, `"image_url":{"url":"data:image/png;base64,`},
		{"compatible", "chat.create", "attachment", `{"messages":[{"role":"user","content":"hello"}]}`, `"type":"image_url"`},
		{"xai", "responses.create", "attachment", `{"input":"hello"}`, `"type":"input_image"`},
		{"anthropic", "messages.create", "attachment", `{"messages":[{"role":"user","content":"hello"}]}`, `"source":{"data":"`},
		{"gemini", "content.generate", "attachment", `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`, `"mimeType":"image/png"`},
		{"gemini", "images.generate", "image", `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`, `"inlineData":{"data":"`},
		{"xai", "images.edit", "image", `{"prompt":"edit"}`, `"url":"data:image/png;base64,`},
	} {
		t.Run(tc.vendor+"/"+tc.operation, func(t *testing.T) {
			body, uploads, cleanup, err := a.resolveAssets(tc.vendor, tc.operation, json.RawMessage(tc.params), nil, map[string][]string{tc.field: {asset.ID}})
			defer cleanup()
			if err != nil {
				t.Fatal(err)
			}
			if len(uploads) != 0 || !strings.Contains(string(body), tc.fragment) {
				t.Fatalf("wrong attachment shape: %s %+v", body, uploads)
			}
		})
	}
	for _, field := range []string{"image", "mask"} {
		_, uploads, cleanup, err := a.resolveAssets("openai", "images.edit", json.RawMessage(`{"prompt":"edit"}`), nil, map[string][]string{field: {asset.ID}})
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(uploads[field][0].Reader)
		cleanup()
		if err != nil || base64.StdEncoding.EncodeToString(b) != assetTestPNG {
			t.Fatalf("multipart bytes: %v", err)
		}
	}
}

func TestAssetContentUnsafeTypesDownloadOnly(t *testing.T) {
	a := newAssetTestApp(t)
	for _, tc := range []struct {
		name, typ, body string
		inline          bool
	}{
		{"unsafe.svg", "image/svg+xml", "<svg xmlns=\"http://www.w3.org/2000/svg\"><script/></svg>", false},
		{"unsafe.html", "image/png", "<!doctype html><script/>", false},
		{"image.png", "image/png", string(mustAssetPNG(t)), true},
	} {
		asset, err := a.assets.Add(tc.name, tc.typ, assetstore.Source{}, strings.NewReader(tc.body), 1000)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("GET", "/api/assets/"+asset.ID+"/content", nil)
		r.SetPathValue("id", asset.ID)
		w := httptest.NewRecorder()
		a.assetContentAPI(w, r)
		if w.Code != http.StatusOK || (strings.HasPrefix(w.Header().Get("Content-Disposition"), "inline") != tc.inline) {
			t.Fatalf("unsafe content response %s: %d %s", tc.name, w.Code, w.Header())
		}
		if !bytes.Equal(w.Body.Bytes(), []byte(tc.body)) {
			t.Fatal("download bytes changed")
		}
		if w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatal("missing nosniff")
		}
	}
}

func mustAssetPNG(t *testing.T) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(assetTestPNG)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCaptureGeneratedInlineAndBinaryKeepsNativeResult(t *testing.T) {
	a := newAssetTestApp(t)
	p := provider{ID: "p1", Kind: "openai"}
	for _, result := range []any{
		map[string]any{"data": []any{map[string]any{"b64_json": assetTestPNG}}},
		map[string]any{"output": []any{map[string]any{"type": "image_generation_call", "result": assetTestPNG}}},
		map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"inlineData": map[string]any{"mimeType": "image/png", "data": assetTestPNG}, "thoughtSignature": "keep"}}}}}},
		map[string]any{"choices": []any{map[string]any{"message": map[string]any{"audio": map[string]any{"data": assetTestPNG, "id": "audio-id"}}}}},
	} {
		before, _ := json.Marshal(result)
		ids, err := a.captureAssets(context.Background(), p, "images.generate", "task1", "session1", result)
		if err != nil || len(ids) != 1 {
			t.Fatalf("capture %s: %v %v", before, ids, err)
		}
		after, _ := json.Marshal(result)
		if !bytes.Equal(before, after) {
			t.Fatal("native result changed")
		}
		asset, err := a.assets.Get(ids[0])
		if err != nil || asset.Favorite || asset.Source["task_id"] != "task1" || asset.Source["session_id"] != "session1" {
			t.Fatalf("asset origin: %+v %v", asset, err)
		}
	}
	f, err := os.CreateTemp(t.TempDir(), "download-")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write(mustAssetPNG(t)); err != nil {
		t.Fatal(err)
	}
	f.Close()
	ids, err := a.captureAssets(context.Background(), p, "audio.speech", "", "", resourceDownload{Path: f.Name(), Name: "audio.bin", ContentType: "audio/mpeg"})
	if err != nil || len(ids) != 1 {
		t.Fatalf("binary capture: %v %v", ids, err)
	}
	if _, err = os.Stat(f.Name()); err != nil {
		t.Fatal("capture deleted caller-owned download")
	}
}

func TestCaptureMediaURLWithoutAuthAndIgnoreArbitraryURLs(t *testing.T) {
	a := newAssetTestApp(t)
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "" || r.Header.Get("X-API-Key") != "" || r.Header.Get("X-Goog-API-Key") != "" {
			t.Error("profile credentials leaked to media URL")
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(mustAssetPNG(t))
	}))
	defer upstream.Close()
	p := provider{ID: "p", Kind: "openai", BaseURL: upstream.URL, ProxyURL: "-", APIKey: "secret"}
	ids, err := a.captureAssets(context.Background(), p, "images.generate", "t", "", map[string]any{"data": []any{map[string]any{"url": upstream.URL + "/generated.png"}}})
	if err != nil || len(ids) != 1 || requests.Load() != 1 {
		t.Fatalf("generated download: %v %v requests=%d", ids, err, requests.Load())
	}
	_, err = a.captureAssets(context.Background(), p, "responses.create", "t", "", map[string]any{"url": upstream.URL + "/arbitrary", "output": []any{map[string]any{"type": "function_call", "arguments": map[string]any{"url": upstream.URL + "/tool"}}}})
	if err != nil || requests.Load() != 1 {
		t.Fatalf("arbitrary URL fetched: %v requests=%d", err, requests.Load())
	}
	ids, err = a.captureAssets(context.Background(), p, "images.generate", "t", "", map[string]any{"data": []any{map[string]any{"url": "https://127.0.0.1/private"}}})
	if err == nil || len(ids) != 0 {
		t.Fatal("cross-origin private media host accepted")
	}
}

func TestCaptureGeminiPCMAndSkipDuplicateNativeEvents(t *testing.T) {
	a := newAssetTestApp(t)
	part := map[string]any{"inlineData": map[string]any{"mimeType": "audio/L16;rate=24000", "data": "AQACAAMA"}}
	result := map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{part}}}}, "native_events": []any{part}}
	ids, err := a.captureAssets(context.Background(), provider{Kind: "gemini"}, "content.generate", "", "", result)
	if err != nil || len(ids) != 1 {
		t.Fatalf("PCM import %v %v", ids, err)
	}
	item, file, err := a.assets.OpenContent(ids[0])
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	b, err := io.ReadAll(file)
	if err != nil || item.ContentType != "audio/wave" || len(b) != 50 || string(b[:4]) != "RIFF" || !bytes.Equal(b[44:], []byte{1, 0, 2, 0, 3, 0}) {
		t.Fatalf("PCM WAV %+v %v %v", item, b, err)
	}
}

func TestCaptureChatOggRemainsPlayableAudio(t *testing.T) {
	a := newAssetTestApp(t)
	encoded := base64.StdEncoding.EncodeToString([]byte("OggS\x00audio-container"))
	result := map[string]any{"choices": []any{map[string]any{"message": map[string]any{"audio": map[string]any{"data": encoded}}}}}
	ids, err := a.captureAssets(context.Background(), provider{}, "chat.create", "", "", result)
	if err != nil || len(ids) != 1 {
		t.Fatalf("capture: %v %v", ids, err)
	}
	item, _ := a.assets.Get(ids[0])
	if item.ContentType != "audio/ogg" || item.Name != "speech.ogg" {
		t.Fatalf("unplayable Ogg asset: %+v", item)
	}
}

func TestCaptureUploadsRewindsAndCloudSelectionStreams(t *testing.T) {
	a := newAssetTestApp(t)
	reader := strings.NewReader("{\"request\":1}\n")
	ids, err := a.captureUploads(provider{ID: "p"}, "files.upload", "t", "", resourceUploads{"file": {{Filename: "batch.jsonl", ContentType: "application/jsonl", Reader: reader}}})
	if err != nil || len(ids) != 1 {
		t.Fatalf("capture upload %v %v", ids, err)
	}
	if pos, _ := reader.Seek(0, io.SeekCurrent); pos != 0 {
		t.Fatal("upload reader was not rewound")
	}
	favorite := true
	if _, err = a.assets.Update(ids[0], nil, &favorite); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"files.upload", "containers.files.upload"} {
		_, uploads, cleanup, err := a.resolveAssets("openai", operation, json.RawMessage(`{"purpose":"batch"}`), nil, map[string][]string{"file": ids})
		if err != nil {
			t.Fatal(err)
		}
		b, readErr := io.ReadAll(uploads["file"][0].Reader)
		cleanup()
		if readErr != nil || string(b) != "{\"request\":1}\n" || uploads["file"][0].Filename != "batch.jsonl" {
			t.Fatalf("cloud file bytes %q %v", b, readErr)
		}
	}
}

func TestXAIEditMultipleImagesAndRejectNonPNGMask(t *testing.T) {
	a := newAssetTestApp(t)
	first, second := addTestImage(t, a, true), addTestImage(t, a, true)
	raw, _, cleanup, err := a.resolveAssets("xai", "images.edit", json.RawMessage(`{"prompt":"combine"}`), nil, map[string][]string{"image": {first.ID, second.ID}})
	defer cleanup()
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Images []struct {
			URL  string `json:"url"`
			Type string `json:"type"`
		} `json:"images"`
	}
	if err = json.Unmarshal(raw, &body); err != nil || len(body.Images) != 2 || body.Images[0].Type != "image_url" || body.Images[1].URL != "data:image/png;base64,"+assetTestPNG {
		t.Fatalf("xAI multiple images: %s %v", raw, err)
	}
	gif, err := a.assets.Add("mask.gif", "image/gif", nil, strings.NewReader("GIF89a123456789"), 100)
	if err != nil {
		t.Fatal(err)
	}
	favorite := true
	if _, err = a.assets.Update(gif.ID, nil, &favorite); err != nil {
		t.Fatal(err)
	}
	_, _, cleanup, err = a.resolveAssets("openai", "images.edit", json.RawMessage(`{}`), nil, map[string][]string{"mask": {gif.ID}})
	cleanup()
	if err == nil {
		t.Fatal("non-PNG mask accepted")
	}
}
