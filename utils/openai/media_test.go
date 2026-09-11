package openai

import (
	"bytes"
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

func newMediaTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := New(Config{APIKey: "test-key", BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.CloseIdleConnections)
	return client
}

func decodeMediaJSON(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	defer r.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}

func readMediaMultipart(t *testing.T, r *http.Request) (map[string][]string, map[string][]byte) {
	t.Helper()
	reader, err := r.MultipartReader()
	if err != nil {
		t.Fatal(err)
	}
	fields := make(map[string][]string)
	files := make(map[string][]byte)
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(part)
		if err != nil {
			t.Fatal(err)
		}
		if part.FileName() != "" {
			files[part.FormName()+":"+part.FileName()] = data
		} else {
			fields[part.FormName()] = append(fields[part.FormName()], string(data))
		}
	}
	return fields, files
}

func TestGenerateImageUsesNativeJSONAndPreservesHTTP(t *testing.T) {
	client := newMediaTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/images/generations" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		body := decodeMediaJSON(t, r)
		want := map[string]any{
			"model": "gpt-image-custom", "prompt": "a paper fox", "background": "transparent",
			"n": float64(0), "output_compression": float64(0), "output_format": "webp",
			"partial_images": float64(2), "quality": "high", "response_format": "b64_json",
			"size": "1536x864", "stream": false, "user": "u", "future": "kept",
		}
		if !reflect.DeepEqual(body, want) {
			t.Errorf("body = %#v, want %#v", body, want)
		}
		w.Header().Set("X-Request-ID", "req-image")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"created":7,"background":"transparent","data":[{"b64_json":"abc","revised_prompt":"better"}],"output_format":"webp","quality":"high","size":"1536x864","usage":{"total_tokens":9,"output_tokens_details":{"image_tokens":4}}}`)
	})

	got, err := client.GenerateImage(context.Background(), ImageGenerateRequest{
		Model: "gpt-image-custom", Prompt: "a paper fox", Background: "transparent", N: Ptr(0),
		OutputCompression: Ptr(0), OutputFormat: "webp", PartialImages: Ptr(2), Quality: "high",
		ResponseFormat: "b64_json", Size: "1536x864", Stream: Ptr(false), User: "u",
		Extra: map[string]any{"future": "kept"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.HTTP == nil || got.HTTP.StatusCode != http.StatusCreated || got.HTTP.Header.Get("X-Request-ID") != "req-image" {
		t.Fatalf("HTTP = %#v", got.HTTP)
	}
	if got.Data[0].B64JSON != "abc" || got.Usage.TotalTokens != 9 || got.Usage.OutputTokensDetails.ImageTokens != 4 {
		t.Fatalf("response = %#v", got)
	}
}

func TestEditImageSupportsNativeJSONReferences(t *testing.T) {
	client := newMediaTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
			t.Errorf("Content-Type = %q", got)
		}
		body := decodeMediaJSON(t, r)
		if body["model"] != "gpt-image-edit-custom" || body["prompt"] != "add a hat" || body["input_fidelity"] != "high" {
			t.Errorf("body = %#v", body)
		}
		images := body["images"].([]any)
		if images[0].(map[string]any)["file_id"] != "file_1" || images[1].(map[string]any)["image_url"] != "data:image/png;base64,AA" {
			t.Errorf("images = %#v", images)
		}
		if body["mask"].(map[string]any)["file_id"] != "file_mask" {
			t.Errorf("mask = %#v", body["mask"])
		}
		_, _ = io.WriteString(w, `{"data":[{"b64_json":"edited"}]}`)
	})

	got, err := client.EditImage(context.Background(), ImageEditRequest{
		Model: "gpt-image-edit-custom", Prompt: "add a hat", InputFidelity: "high",
		Images: []ImageReference{{FileID: "file_1"}, {ImageURL: "data:image/png;base64,AA"}},
		Mask:   &ImageReference{FileID: "file_mask"},
	})
	if err != nil || got.Data[0].B64JSON != "edited" {
		t.Fatalf("got = %#v, err = %v", got, err)
	}
}

func TestEditImageMultipartIncludesMultipleImagesMaskAndFields(t *testing.T) {
	client := newMediaTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		fields, files := readMediaMultipart(t, r)
		if !reflect.DeepEqual(fields["model"], []string{"gpt-image-1.5"}) || !reflect.DeepEqual(fields["stream"], []string{"false"}) || !reflect.DeepEqual(fields["partial_images"], []string{"0"}) {
			t.Errorf("fields = %#v", fields)
		}
		if string(files["image[]:one.png"]) != "one" || string(files["image[]:two.png"]) != "two" || string(files["mask:mask.png"]) != "mask" {
			t.Errorf("files = %#v", files)
		}
		_, _ = io.WriteString(w, `{"data":[{"b64_json":"edited"}]}`)
	})

	got, err := client.EditImage(context.Background(), ImageEditRequest{
		Model: "gpt-image-1.5", Prompt: "combine", Stream: Ptr(false), PartialImages: Ptr(0),
		ImageFiles: []Upload{
			{Filename: "one.png", ContentType: "image/png", Reader: strings.NewReader("one")},
			{Filename: "two.png", ContentType: "image/png", Reader: strings.NewReader("two")},
		},
		MaskFile: &Upload{Filename: "mask.png", ContentType: "image/png", Reader: strings.NewReader("mask")},
	})
	if err != nil || got.Data[0].B64JSON != "edited" {
		t.Fatalf("got = %#v, err = %v", got, err)
	}
}

func TestStreamImageRequiresCompletedAndReportsEventErrors(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{"complete", "event: image_generation.partial_image\ndata: {\"type\":\"image_generation.partial_image\",\"b64_json\":\"part\",\"partial_image_index\":0}\n\nevent: image_generation.completed\ndata: {\"type\":\"image_generation.completed\",\"b64_json\":\"final\",\"future\":1}\n\n", ""},
		{"truncated", "event: image_generation.partial_image\ndata: {\"type\":\"image_generation.partial_image\",\"b64_json\":\"part\"}\n\n", "unexpected EOF"},
		{"error", "event: error\ndata: {\"type\":\"error\",\"error\":{\"message\":\"unsafe prompt\",\"code\":\"bad_prompt\"}}\n\n", "unsafe prompt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newMediaTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, tt.body)
			})
			var events []ImageStreamEvent
			resp, err := client.StreamImage(context.Background(), ImageGenerateRequest{Model: "gpt-image-1.5", Prompt: "x", PartialImages: Ptr(1)}, func(event ImageStreamEvent) error {
				events = append(events, event)
				return nil
			})
			if resp == nil {
				t.Fatal("nil HTTP response")
			}
			if tt.wantErr == "" {
				if err != nil || len(events) != 2 || events[1].B64JSON != "final" || !bytes.Contains(events[1].Raw, []byte(`"future":1`)) {
					t.Fatalf("events = %#v, err = %v", events, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			} else if tt.name == "error" {
				var streamErr *StreamError
				if !errors.As(err, &streamErr) || streamErr.Event.Type != "error" {
					t.Fatalf("stream error = %#v", err)
				}
			}
		})
	}
}

func TestStreamImageEditUsesMultipartAndEditTerminal(t *testing.T) {
	client := newMediaTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		fields, files := readMediaMultipart(t, r)
		if !reflect.DeepEqual(fields["stream"], []string{"true"}) || string(files["image[]:source.png"]) != "source" {
			t.Errorf("fields = %#v, files = %#v", fields, files)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: image_edit.completed\ndata: {\"type\":\"image_edit.completed\",\"b64_json\":\"edited\"}\n\n")
	})
	var final ImageStreamEvent
	resp, err := client.StreamImageEdit(context.Background(), ImageEditRequest{
		Model: "edit-custom", Prompt: "edit",
		ImageFiles: []Upload{{Filename: "source.png", Reader: strings.NewReader("source")}},
	}, func(event ImageStreamEvent) error { final = event; return nil })
	if err != nil || resp == nil || final.Type != "image_edit.completed" || final.B64JSON != "edited" {
		t.Fatalf("event = %#v, resp = %#v, err = %v", final, resp, err)
	}
}

func TestCreateSpeechWritesBinaryResponseAndNativeVoice(t *testing.T) {
	client := newMediaTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/audio/speech" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		body := decodeMediaJSON(t, r)
		voice := body["voice"].(map[string]any)
		if body["model"] != "speech-custom" || body["input"] != "hello" || voice["id"] != "voice_1" || body["speed"] != float64(0) || body["stream_format"] != "audio" {
			t.Errorf("body = %#v", body)
		}
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write([]byte{1, 2, 3})
	})
	var dst bytes.Buffer
	resp, err := client.CreateSpeech(context.Background(), SpeechRequest{
		Model: "speech-custom", Input: "hello", Voice: json.RawMessage(`{"id":"voice_1"}`),
		Instructions: "calm", ResponseFormat: "wav", Speed: Ptr(0.0), StreamFormat: "audio",
	}, &dst)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || !bytes.Equal(dst.Bytes(), []byte{1, 2, 3}) {
		t.Fatalf("response = %#v, bytes = %v", resp, dst.Bytes())
	}
}

type rejectingMediaWriter struct{}

func (rejectingMediaWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestCreateSpeechReturnsWriterError(t *testing.T) {
	client := newMediaTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = io.WriteString(w, "audio")
	})
	resp, err := client.CreateSpeech(context.Background(), SpeechRequest{Model: "tts-1", Input: "x", Voice: "alloy"}, rejectingMediaWriter{})
	if resp == nil || err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("resp = %#v, err = %v", resp, err)
	}
}

func TestStreamSpeechRequiresDoneAndPreservesRawEvents(t *testing.T) {
	client := newMediaTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body := decodeMediaJSON(t, r)
		if body["stream_format"] != "sse" {
			t.Errorf("body = %#v", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: speech.audio.delta\ndata: {\"type\":\"speech.audio.delta\",\"audio\":\"AQI=\",\"future\":1}\n\nevent: speech.audio.done\ndata: {\"type\":\"speech.audio.done\",\"usage\":{\"input_tokens\":2,\"output_tokens\":3,\"total_tokens\":5}}\n\n")
	})
	var events []SpeechStreamEvent
	resp, err := client.StreamSpeech(context.Background(), SpeechRequest{Model: "gpt-4o-mini-tts", Input: "hi", Voice: "alloy"}, func(event SpeechStreamEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil || resp == nil || len(events) != 2 || events[0].Audio != "AQI=" || events[1].Usage.TotalTokens != 5 || !bytes.Contains(events[0].Raw, []byte(`"future":1`)) {
		t.Fatalf("events = %#v, resp = %#v, err = %v", events, resp, err)
	}

	truncated := newMediaTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: speech.audio.delta\ndata: {\"type\":\"speech.audio.delta\",\"audio\":\"AQI=\"}\n\n")
	})
	_, err = truncated.StreamSpeech(context.Background(), SpeechRequest{Model: "custom", Input: "x", Voice: "alloy"}, func(SpeechStreamEvent) error { return nil })
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated err = %v", err)
	}
}

func TestCreateSpeechRejectsSSEAndStreamSpeechRejectsNilCallback(t *testing.T) {
	called := false
	client := newMediaTestClient(t, func(w http.ResponseWriter, r *http.Request) { called = true })
	if _, err := client.CreateSpeech(context.Background(), SpeechRequest{StreamFormat: "sse"}, io.Discard); err == nil {
		t.Error("CreateSpeech accepted stream_format=sse")
	}
	if _, err := client.StreamSpeech(context.Background(), SpeechRequest{}, nil); err == nil {
		t.Error("StreamSpeech accepted nil callback")
	}
	if called {
		t.Fatal("invalid speech request reached HTTP server")
	}
}

func TestCreateTranscriptionEncodesAllNativeMultipartFields(t *testing.T) {
	client := newMediaTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		fields, files := readMediaMultipart(t, r)
		checks := map[string][]string{
			"model": {"transcribe-custom"}, "language": {"en"}, "prompt": {"names"},
			"response_format": {"verbose_json"}, "stream": {"false"}, "temperature": {"0"},
			"chunking_strategy[type]":                {"server_vad"},
			"chunking_strategy[prefix_padding_ms]":   {"0"},
			"chunking_strategy[silence_duration_ms]": {"500"},
			"chunking_strategy[threshold]":           {"0"},
			"include[]":                              {"logprobs"}, "keywords[]": {"Codex", "Go"}, "languages[]": {"en", "fr"},
			"known_speaker_names[]":      {"agent", "caller"},
			"known_speaker_references[]": {"data:audio/wav;base64,AA", "data:audio/wav;base64,BB"},
			"timestamp_granularities[]":  {"word", "segment"}, "future": {"value"},
		}
		for key, want := range checks {
			if !reflect.DeepEqual(fields[key], want) {
				t.Errorf("field %s = %#v, want %#v", key, fields[key], want)
			}
		}
		if string(files["file:sample.wav"]) != "audio" {
			t.Errorf("files = %#v", files)
		}
		_, _ = io.WriteString(w, `{"text":"hello","language":"en","duration":1.25,"words":[{"word":"hello","start":0,"end":1}],"segments":[{"id":1,"start":0,"end":1,"text":"hello"}],"usage":{"type":"duration","seconds":1.25}}`)
	})

	got, err := client.CreateTranscription(context.Background(), TranscriptionRequest{
		Model: "transcribe-custom", File: Upload{Filename: "sample.wav", ContentType: "audio/wav", Reader: strings.NewReader("audio")},
		Language: "en", Languages: []string{"en", "fr"}, Prompt: "names", ResponseFormat: "verbose_json",
		Stream: Ptr(false), Temperature: Ptr(0.0), TimestampGranularities: []string{"word", "segment"}, Include: []string{"logprobs"},
		Keywords: []string{"Codex", "Go"}, KnownSpeakerNames: []string{"agent", "caller"},
		KnownSpeakerReferences: []string{"data:audio/wav;base64,AA", "data:audio/wav;base64,BB"},
		ChunkingStrategy:       json.RawMessage(`{"type":"server_vad","prefix_padding_ms":0,"silence_duration_ms":500,"threshold":0}`),
		Extra:                  map[string]any{"future": "value"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "hello" || got.Words[0].Start != 0 || got.Segments[0].Text != "hello" || got.Usage.Seconds != 1.25 || got.HTTP == nil {
		t.Fatalf("transcription = %#v", got)
	}
}

func TestTranscriptionAutoChunkingUsesScalarField(t *testing.T) {
	fields, _, err := transcriptionMultipart(TranscriptionRequest{ChunkingStrategy: json.RawMessage(`"auto"`)})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fields["chunking_strategy"], []string{"auto"}) {
		t.Fatalf("fields = %#v", fields)
	}
}

func TestCreateTranscriptionAndTranslationSupportTextResponses(t *testing.T) {
	for _, tc := range []struct {
		name, path, format string
		call               func(*Client) (*Transcription, error)
	}{
		{"transcription", "/audio/transcriptions", "text", func(c *Client) (*Transcription, error) {
			return c.CreateTranscription(context.Background(), TranscriptionRequest{Model: "whisper-1", File: Upload{Filename: "a.mp3", Reader: strings.NewReader("a")}, ResponseFormat: "text"})
		}},
		{"translation", "/audio/translations", "srt", func(c *Client) (*Transcription, error) {
			return c.CreateTranslation(context.Background(), TranslationRequest{Model: "whisper-custom", File: Upload{Filename: "a.mp3", Reader: strings.NewReader("a")}, ResponseFormat: "srt", Prompt: "English", Temperature: Ptr(0.0)})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := newMediaTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tc.path {
					t.Errorf("path = %q", r.URL.Path)
				}
				fields, _ := readMediaMultipart(t, r)
				if !reflect.DeepEqual(fields["response_format"], []string{tc.format}) {
					t.Errorf("fields = %#v", fields)
				}
				if tc.name == "translation" && (!reflect.DeepEqual(fields["model"], []string{"whisper-custom"}) || !reflect.DeepEqual(fields["prompt"], []string{"English"}) || !reflect.DeepEqual(fields["temperature"], []string{"0"})) {
					t.Errorf("translation fields = %#v", fields)
				}
				_, _ = io.WriteString(w, "plain output")
			})
			got, err := tc.call(client)
			if err != nil || got.Text != "plain output" || string(got.HTTP.Body) != "plain output" {
				t.Fatalf("got = %#v, err = %v", got, err)
			}
		})
	}
}

func TestStreamTranscriptionRequiresDoneAndKeepsSpeakerEvents(t *testing.T) {
	client := newMediaTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		fields, _ := readMediaMultipart(t, r)
		if !reflect.DeepEqual(fields["stream"], []string{"true"}) {
			t.Errorf("fields = %#v", fields)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: transcript.text.delta\ndata: {\"type\":\"transcript.text.delta\",\"delta\":\"Hi\",\"segment_id\":\"seg_1\"}\n\nevent: transcript.text.segment\ndata: {\"type\":\"transcript.text.segment\",\"id\":\"seg_1\",\"start\":0,\"end\":1,\"text\":\"Hi\",\"speaker\":\"agent\"}\n\nevent: transcript.text.done\ndata: {\"type\":\"transcript.text.done\",\"text\":\"Hi\",\"languages\":[{\"code\":\"en\"}]}\n\n")
	})
	var events []TranscriptionStreamEvent
	resp, err := client.StreamTranscription(context.Background(), TranscriptionRequest{
		Model: "gpt-4o-transcribe-diarize", File: Upload{Filename: "a.wav", Reader: strings.NewReader("a")}, ResponseFormat: "diarized_json",
	}, func(event TranscriptionStreamEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil || resp == nil || len(events) != 3 || events[1].Speaker != "agent" || events[2].Languages[0].Code != "en" || !bytes.Contains(events[1].Raw, []byte(`"speaker":"agent"`)) {
		t.Fatalf("events = %#v, resp = %#v, err = %v", events, resp, err)
	}

	truncated := newMediaTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: transcript.text.delta\ndata: {\"type\":\"transcript.text.delta\",\"delta\":\"Hi\"}\n\n")
	})
	_, err = truncated.StreamTranscription(context.Background(), TranscriptionRequest{Model: "custom", File: Upload{Filename: "a.wav", Reader: strings.NewReader("a")}}, func(TranscriptionStreamEvent) error { return nil })
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated err = %v", err)
	}
}

func TestMediaRequestExtraCollisionFailsBeforeHTTP(t *testing.T) {
	called := false
	client := newMediaTestClient(t, func(w http.ResponseWriter, r *http.Request) { called = true })
	_, err := client.GenerateImage(context.Background(), ImageGenerateRequest{Prompt: "x", Extra: map[string]any{"prompt": "override"}})
	if err == nil || !strings.Contains(err.Error(), "prompt") || called {
		t.Fatalf("err = %v, called = %v", err, called)
	}
}

func TestMediaMethodsRejectInvalidModeBeforeHTTP(t *testing.T) {
	called := false
	client := newMediaTestClient(t, func(w http.ResponseWriter, r *http.Request) { called = true })
	if _, err := client.GenerateImage(context.Background(), ImageGenerateRequest{Prompt: "x", Stream: Ptr(true)}); err == nil {
		t.Error("GenerateImage accepted stream=true")
	}
	if _, err := client.CreateTranscription(context.Background(), TranscriptionRequest{Stream: Ptr(true)}); err == nil {
		t.Error("CreateTranscription accepted stream=true")
	}
	if _, err := client.EditImage(context.Background(), ImageEditRequest{
		Prompt: "x", Images: []ImageReference{{FileID: "file_1"}},
		ImageFiles: []Upload{{Filename: "x.png", Reader: strings.NewReader("x")}},
	}); err == nil {
		t.Error("EditImage accepted mixed JSON and multipart inputs")
	}
	if _, err := client.StreamImage(context.Background(), ImageGenerateRequest{}, nil); err == nil {
		t.Error("StreamImage accepted nil callback")
	}
	if _, err := client.StreamImageEdit(context.Background(), ImageEditRequest{}, nil); err == nil {
		t.Error("StreamImageEdit accepted nil callback")
	}
	if _, err := client.StreamTranscription(context.Background(), TranscriptionRequest{}, nil); err == nil {
		t.Error("StreamTranscription accepted nil callback")
	}
	if called {
		t.Fatal("invalid media method reached HTTP server")
	}
}
