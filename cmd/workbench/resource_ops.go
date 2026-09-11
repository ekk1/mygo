package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/ekk1/mygo/utils/openai"
)

type resourceOperation struct {
	ID           string         `json:"id"`
	Group        string         `json:"group"`
	Label        string         `json:"label"`
	Body         map[string]any `json:"body"`
	Fields       []string       `json:"fields,omitempty"`
	UploadFields []string       `json:"upload_fields,omitempty"`
	Binary       bool           `json:"binary,omitempty"`
}

func op(id, group, label string, body map[string]any) resourceOperation {
	return resourceOperation{ID: id, Group: group, Label: label, Body: body}
}

var operationCatalog = []resourceOperation{
	{ID: "files.upload", Group: "files", Label: "Upload file", Body: map[string]any{"purpose": "assistants"}, UploadFields: []string{"file"}},
	op("files.list", "files", "List files", map[string]any{"limit": 100}),
	{ID: "files.get", Group: "files", Label: "Get file", Body: map[string]any{"file_id": ""}, Fields: []string{"file_id"}},
	{ID: "files.delete", Group: "files", Label: "Delete file", Body: map[string]any{"file_id": ""}, Fields: []string{"file_id"}},
	{ID: "files.download", Group: "files", Label: "Download file", Body: map[string]any{"file_id": "", "filename": "download.bin"}, Fields: []string{"file_id", "filename"}, Binary: true},
	op("containers.create", "containers", "Create container", map[string]any{"name": "workbench"}),
	op("containers.list", "containers", "List containers", map[string]any{"limit": 100}),
	{ID: "containers.get", Group: "containers", Label: "Get container", Body: map[string]any{"container_id": ""}, Fields: []string{"container_id"}},
	{ID: "containers.delete", Group: "containers", Label: "Delete container", Body: map[string]any{"container_id": ""}, Fields: []string{"container_id"}},
	{ID: "containers.files.add", Group: "containers", Label: "Add existing file", Body: map[string]any{"container_id": "", "file_id": ""}, Fields: []string{"container_id", "file_id"}},
	{ID: "containers.files.upload", Group: "containers", Label: "Upload container file", Body: map[string]any{"container_id": ""}, Fields: []string{"container_id"}, UploadFields: []string{"file"}},
	{ID: "containers.files.list", Group: "containers", Label: "List container files", Body: map[string]any{"container_id": "", "limit": 100}, Fields: []string{"container_id"}},
	{ID: "containers.files.get", Group: "containers", Label: "Get container file", Body: map[string]any{"container_id": "", "file_id": ""}, Fields: []string{"container_id", "file_id"}},
	{ID: "containers.files.delete", Group: "containers", Label: "Delete container file", Body: map[string]any{"container_id": "", "file_id": ""}, Fields: []string{"container_id", "file_id"}},
	{ID: "containers.files.download", Group: "containers", Label: "Download container file", Body: map[string]any{"container_id": "", "file_id": "", "filename": "download.bin"}, Fields: []string{"container_id", "file_id", "filename"}, Binary: true},
	op("batches.create", "batches", "Create batch", map[string]any{"input_file_id": "", "endpoint": "/v1/responses", "completion_window": "24h"}),
	op("batches.list", "batches", "List batches", map[string]any{"limit": 100}),
	{ID: "batches.get", Group: "batches", Label: "Get batch", Body: map[string]any{"batch_id": ""}, Fields: []string{"batch_id"}},
	{ID: "batches.cancel", Group: "batches", Label: "Cancel batch", Body: map[string]any{"batch_id": ""}, Fields: []string{"batch_id"}},
	op("responses.create", "responses", "Create response", map[string]any{"model": "", "input": ""}),
	op("responses.stream", "responses", "Stream response", map[string]any{"model": "", "input": ""}),
	{ID: "responses.get", Group: "responses", Label: "Get response", Body: map[string]any{"response_id": ""}, Fields: []string{"response_id"}},
	{ID: "responses.cancel", Group: "responses", Label: "Cancel response", Body: map[string]any{"response_id": ""}, Fields: []string{"response_id"}},
	{ID: "responses.delete", Group: "responses", Label: "Delete response", Body: map[string]any{"response_id": ""}, Fields: []string{"response_id"}},
	{ID: "responses.input_items.list", Group: "responses", Label: "List response input items", Body: map[string]any{"response_id": "", "limit": 100}, Fields: []string{"response_id"}},
	op("responses.input_tokens.count", "responses", "Count response input tokens", map[string]any{"model": "", "input": ""}),
	op("responses.compact", "responses", "Compact response input", map[string]any{"model": "", "input": []any{}}),
	op("chat.create", "chat", "Create chat completion", map[string]any{"model": "", "messages": []any{}}),
	op("chat.stream", "chat", "Stream chat completion", map[string]any{"model": "", "messages": []any{}}),
	op("images.generate", "images", "Generate image", map[string]any{"model": "gpt-image-1", "prompt": ""}),
	op("images.stream", "images", "Stream image generation", map[string]any{"model": "gpt-image-1", "prompt": ""}),
	{ID: "images.edit", Group: "images", Label: "Edit image", Body: map[string]any{"model": "gpt-image-1", "prompt": ""}, UploadFields: []string{"image", "mask"}},
	{ID: "images.edit_stream", Group: "images", Label: "Stream image edit", Body: map[string]any{"model": "gpt-image-1", "prompt": ""}, UploadFields: []string{"image", "mask"}},
	{ID: "audio.speech", Group: "audio", Label: "Create speech", Body: map[string]any{"model": "gpt-4o-mini-tts", "input": "", "voice": "alloy", "response_format": "mp3", "filename": "speech.mp3"}, Fields: []string{"filename"}, Binary: true},
	op("audio.speech_stream", "audio", "Stream speech events", map[string]any{"model": "gpt-4o-mini-tts", "input": "", "voice": "alloy"}),
	{ID: "audio.transcribe", Group: "audio", Label: "Transcribe audio", Body: map[string]any{"model": "gpt-4o-transcribe"}, UploadFields: []string{"file"}},
	{ID: "audio.transcribe_stream", Group: "audio", Label: "Stream transcription", Body: map[string]any{"model": "gpt-4o-transcribe"}, UploadFields: []string{"file"}},
	{ID: "audio.translate", Group: "audio", Label: "Translate audio", Body: map[string]any{"model": "whisper-1"}, UploadFields: []string{"file"}},
}

type resourceUpload struct {
	Filename    string
	ContentType string
	Reader      io.Reader
}

type resourceUploads map[string][]resourceUpload

type resourceEvent struct {
	Type string `json:"type"`
	ID   string `json:"id,omitempty"`
	Data any    `json:"data"`
}

type resourceStreamResult struct {
	Events []resourceEvent      `json:"events"`
	HTTP   *openai.HTTPResponse `json:"http,omitempty"`
}

const maxResourceStreamBytes int64 = 256 << 20

type resourceEventCollector struct {
	events []resourceEvent
	bytes  int64
	limit  int64
}

func newResourceEventCollector() resourceEventCollector {
	return resourceEventCollector{limit: maxResourceStreamBytes}
}

func (c *resourceEventCollector) append(event resourceEvent, rawBytes int) error {
	added := int64(rawBytes + len(event.Type) + len(event.ID) + 32)
	if c.limit <= 0 {
		c.limit = maxResourceStreamBytes
	}
	if added > c.limit-c.bytes {
		return fmt.Errorf("resource stream exceeds %d byte collection limit", c.limit)
	}
	c.events = append(c.events, event)
	c.bytes += added
	return nil
}

type resourceDownload struct {
	Path        string
	Name        string
	ContentType string
}

func resourceEventData(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	if json.Valid(raw) {
		return json.RawMessage(append([]byte(nil), raw...))
	}
	return string(raw)
}

func decodeResourceParams(raw json.RawMessage, dst any) error {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return fmt.Errorf("invalid operation params: expected a JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("invalid operation params: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("invalid operation params: multiple JSON values")
	}
	return nil
}

func decodeNativeResourceParams(raw json.RawMessage, dst any) error {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("invalid operation params: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return fmt.Errorf("invalid operation params: expected a JSON object")
	}
	value := reflect.ValueOf(dst)
	if value.Kind() != reflect.Pointer || value.IsNil() || value.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("invalid native operation destination")
	}
	known := make(map[string]bool)
	collectJSONFields(value.Elem().Type(), known)
	extra := findExtraField(value.Elem())
	for name, encoded := range fields {
		if known[name] {
			continue
		}
		if !extra.IsValid() {
			return fmt.Errorf("invalid operation params: unsupported field %q", name)
		}
		if extra.IsNil() {
			extra.Set(reflect.MakeMap(extra.Type()))
		}
		extra.SetMapIndex(reflect.ValueOf(name), reflect.ValueOf(any(json.RawMessage(append([]byte(nil), encoded...)))))
	}
	return nil
}

func collectJSONFields(t reflect.Type, fields map[string]bool) {
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.PkgPath != "" {
			continue
		}
		tag := field.Tag.Get("json")
		name := strings.Split(tag, ",")[0]
		if name == "-" {
			continue
		}
		if field.Anonymous && name == "" && field.Type.Kind() == reflect.Struct {
			collectJSONFields(field.Type, fields)
			continue
		}
		if name == "" {
			name = field.Name
		}
		fields[name] = true
	}
}

func findExtraField(value reflect.Value) reflect.Value {
	t := value.Type()
	for i := 0; i < value.NumField(); i++ {
		fieldType := t.Field(i)
		fieldValue := value.Field(i)
		if fieldType.Name == "Extra" && fieldValue.CanSet() && fieldValue.Type() == reflect.TypeFor[map[string]any]() {
			return fieldValue
		}
		if fieldType.Anonymous && fieldValue.Kind() == reflect.Struct {
			if found := findExtraField(fieldValue); found.IsValid() {
				return found
			}
		}
	}
	return reflect.Value{}
}

func oneUpload(files resourceUploads, field string) (openai.Upload, error) {
	values := files[field]
	if len(values) != 1 || values[0].Reader == nil {
		return openai.Upload{}, fmt.Errorf("operation requires exactly one %q upload", field)
	}
	u := values[0]
	return openai.Upload{Filename: u.Filename, ContentType: u.ContentType, Reader: u.Reader}, nil
}

func newDownload(name string, write func(io.Writer) (*openai.HTTPResponse, error)) (resourceDownload, error) {
	f, err := os.CreateTemp("", "mygo-workbench-download-*")
	if err != nil {
		return resourceDownload{}, err
	}
	path := f.Name()
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	response, err := write(f)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return resourceDownload{}, err
	}
	contentType := "application/octet-stream"
	if response != nil && response.Header.Get("Content-Type") != "" {
		contentType = response.Header.Get("Content-Type")
	}
	if filepath.Base(name) != name || name == "." || name == "" {
		name = "download.bin"
	}
	ok = true
	return resourceDownload{Path: path, Name: name, ContentType: contentType}, nil
}

func dispatchResourceOperation(ctx context.Context, c *openai.Client, operation string, raw json.RawMessage, files resourceUploads) (any, error) {
	switch operation {
	case "files.upload":
		var p struct {
			Purpose      string                 `json:"purpose"`
			ExpiresAfter *openai.FileExpiration `json:"expires_after"`
		}
		if err := decodeResourceParams(raw, &p); err != nil {
			return nil, err
		}
		u, err := oneUpload(files, "file")
		if err != nil {
			return nil, err
		}
		return c.UploadFile(ctx, openai.UploadFileParams{File: u, Purpose: p.Purpose, ExpiresAfter: p.ExpiresAfter})
	case "files.list":
		var p struct {
			After   *string `json:"after"`
			Limit   *int    `json:"limit"`
			Order   *string `json:"order"`
			Purpose *string `json:"purpose"`
		}
		if err := decodeResourceParams(raw, &p); err != nil {
			return nil, err
		}
		return c.ListFiles(ctx, openai.ListFilesParams{After: p.After, Limit: p.Limit, Order: p.Order, Purpose: p.Purpose})
	case "files.get", "files.delete", "files.download":
		var p struct {
			FileID   string `json:"file_id"`
			Filename string `json:"filename"`
		}
		if err := decodeResourceParams(raw, &p); err != nil {
			return nil, err
		}
		switch operation {
		case "files.get":
			return c.GetFile(ctx, p.FileID)
		case "files.delete":
			return c.DeleteFile(ctx, p.FileID)
		default:
			return newDownload(p.Filename, func(w io.Writer) (*openai.HTTPResponse, error) { return c.DownloadFile(ctx, p.FileID, w) })
		}
	case "containers.create":
		var p openai.CreateContainerParams
		if err := decodeNativeResourceParams(raw, &p); err != nil {
			return nil, err
		}
		return c.CreateContainer(ctx, p)
	case "containers.list":
		var p struct {
			After *string `json:"after"`
			Limit *int    `json:"limit"`
			Name  *string `json:"name"`
			Order *string `json:"order"`
		}
		if err := decodeResourceParams(raw, &p); err != nil {
			return nil, err
		}
		return c.ListContainers(ctx, openai.ListContainersParams{After: p.After, Limit: p.Limit, Name: p.Name, Order: p.Order})
	case "containers.get", "containers.delete":
		var p struct {
			ContainerID string `json:"container_id"`
		}
		if err := decodeResourceParams(raw, &p); err != nil {
			return nil, err
		}
		if operation == "containers.get" {
			return c.GetContainer(ctx, p.ContainerID)
		}
		return c.DeleteContainer(ctx, p.ContainerID)
	case "containers.files.add":
		var p struct {
			ContainerID string         `json:"container_id"`
			FileID      string         `json:"file_id"`
			Extra       map[string]any `json:"-"`
		}
		if err := decodeNativeResourceParams(raw, &p); err != nil {
			return nil, err
		}
		return c.AddContainerFile(ctx, p.ContainerID, openai.AddContainerFileParams{FileID: p.FileID, Extra: p.Extra})
	case "containers.files.upload":
		var p struct {
			ContainerID string `json:"container_id"`
		}
		if err := decodeResourceParams(raw, &p); err != nil {
			return nil, err
		}
		u, err := oneUpload(files, "file")
		if err != nil {
			return nil, err
		}
		return c.UploadContainerFile(ctx, p.ContainerID, u)
	case "containers.files.list":
		var p struct {
			ContainerID string  `json:"container_id"`
			After       *string `json:"after"`
			Limit       *int    `json:"limit"`
			Order       *string `json:"order"`
		}
		if err := decodeResourceParams(raw, &p); err != nil {
			return nil, err
		}
		return c.ListContainerFiles(ctx, p.ContainerID, openai.ListContainerFilesParams{After: p.After, Limit: p.Limit, Order: p.Order})
	case "containers.files.get", "containers.files.delete", "containers.files.download":
		var p struct {
			ContainerID string `json:"container_id"`
			FileID      string `json:"file_id"`
			Filename    string `json:"filename"`
		}
		if err := decodeResourceParams(raw, &p); err != nil {
			return nil, err
		}
		switch operation {
		case "containers.files.get":
			return c.GetContainerFile(ctx, p.ContainerID, p.FileID)
		case "containers.files.delete":
			return c.DeleteContainerFile(ctx, p.ContainerID, p.FileID)
		default:
			return newDownload(p.Filename, func(w io.Writer) (*openai.HTTPResponse, error) {
				return c.DownloadContainerFile(ctx, p.ContainerID, p.FileID, w)
			})
		}
	case "batches.create":
		var p openai.CreateBatchParams
		if err := decodeNativeResourceParams(raw, &p); err != nil {
			return nil, err
		}
		return c.CreateBatch(ctx, p)
	case "batches.list":
		var p struct {
			After *string `json:"after"`
			Limit *int    `json:"limit"`
		}
		if err := decodeResourceParams(raw, &p); err != nil {
			return nil, err
		}
		return c.ListBatches(ctx, openai.ListBatchesParams{After: p.After, Limit: p.Limit})
	case "batches.get", "batches.cancel":
		var p struct {
			BatchID string `json:"batch_id"`
		}
		if err := decodeResourceParams(raw, &p); err != nil {
			return nil, err
		}
		if operation == "batches.get" {
			return c.GetBatch(ctx, p.BatchID)
		}
		return c.CancelBatch(ctx, p.BatchID)
	case "responses.create":
		var p openai.ResponseRequest
		if err := decodeNativeResourceParams(raw, &p); err != nil {
			return nil, err
		}
		return c.CreateResponse(ctx, p)
	case "responses.stream":
		var p openai.ResponseRequest
		if err := decodeNativeResourceParams(raw, &p); err != nil {
			return nil, err
		}
		events := newResourceEventCollector()
		response, err := c.StreamResponse(ctx, p, func(e openai.Event) error {
			return events.append(resourceEvent{Type: e.Type, ID: e.ID, Data: resourceEventData(e.Data)}, len(e.Data))
		})
		return resourceStreamResult{Events: events.events, HTTP: response}, err
	case "responses.get":
		var p struct {
			ResponseID         string   `json:"response_id"`
			Include            []string `json:"include"`
			IncludeObfuscation *bool    `json:"include_obfuscation"`
			StartingAfter      *int64   `json:"starting_after"`
		}
		if err := decodeResourceParams(raw, &p); err != nil {
			return nil, err
		}
		return c.GetResponse(ctx, p.ResponseID, openai.ResponseGetOptions{Include: p.Include, IncludeObfuscation: p.IncludeObfuscation, StartingAfter: p.StartingAfter})
	case "responses.cancel", "responses.delete":
		var p struct {
			ResponseID string `json:"response_id"`
		}
		if err := decodeResourceParams(raw, &p); err != nil {
			return nil, err
		}
		if operation == "responses.cancel" {
			return c.CancelResponse(ctx, p.ResponseID)
		}
		return c.DeleteResponse(ctx, p.ResponseID)
	case "responses.input_items.list":
		var p struct {
			ResponseID string   `json:"response_id"`
			After      string   `json:"after"`
			Include    []string `json:"include"`
			Limit      int      `json:"limit"`
			Order      string   `json:"order"`
		}
		if err := decodeResourceParams(raw, &p); err != nil {
			return nil, err
		}
		return c.ListResponseInputItems(ctx, p.ResponseID, openai.ResponseInputItemsOptions{After: p.After, Include: p.Include, Limit: p.Limit, Order: p.Order})
	case "responses.input_tokens.count":
		var p openai.ResponseInputTokensRequest
		if err := decodeNativeResourceParams(raw, &p); err != nil {
			return nil, err
		}
		return c.CountResponseInputTokens(ctx, p)
	case "responses.compact":
		var p openai.ResponseCompactRequest
		if err := decodeNativeResourceParams(raw, &p); err != nil {
			return nil, err
		}
		return c.CompactResponse(ctx, p)
	case "chat.create":
		var p openai.ChatCompletionRequest
		if err := decodeNativeResourceParams(raw, &p); err != nil {
			return nil, err
		}
		return c.CreateChatCompletion(ctx, p)
	case "chat.stream":
		var p openai.ChatCompletionRequest
		if err := decodeNativeResourceParams(raw, &p); err != nil {
			return nil, err
		}
		events := newResourceEventCollector()
		response, err := c.StreamChatCompletion(ctx, p, func(e openai.Event) error {
			return events.append(resourceEvent{Type: e.Type, ID: e.ID, Data: resourceEventData(e.Data)}, len(e.Data))
		})
		return resourceStreamResult{Events: events.events, HTTP: response}, err
	case "images.generate", "images.stream":
		var p openai.ImageGenerateRequest
		if err := decodeNativeResourceParams(raw, &p); err != nil {
			return nil, err
		}
		if operation == "images.generate" {
			return c.GenerateImage(ctx, p)
		}
		events := newResourceEventCollector()
		response, err := c.StreamImage(ctx, p, func(e openai.ImageStreamEvent) error {
			return events.append(resourceEvent{Type: e.Type, Data: resourceEventData(e.Raw)}, len(e.Raw))
		})
		return resourceStreamResult{Events: events.events, HTTP: response}, err
	case "images.edit", "images.edit_stream":
		var p openai.ImageEditRequest
		if err := decodeNativeResourceParams(raw, &p); err != nil {
			return nil, err
		}
		for _, u := range files["image"] {
			p.ImageFiles = append(p.ImageFiles, openai.Upload{Filename: u.Filename, ContentType: u.ContentType, Reader: u.Reader})
		}
		for _, u := range files["images"] {
			p.ImageFiles = append(p.ImageFiles, openai.Upload{Filename: u.Filename, ContentType: u.ContentType, Reader: u.Reader})
		}
		if masks := files["mask"]; len(masks) > 0 {
			if len(masks) != 1 {
				return nil, fmt.Errorf("operation accepts at most one mask upload")
			}
			p.MaskFile = &openai.Upload{Filename: masks[0].Filename, ContentType: masks[0].ContentType, Reader: masks[0].Reader}
		}
		if operation == "images.edit" {
			return c.EditImage(ctx, p)
		}
		events := newResourceEventCollector()
		response, err := c.StreamImageEdit(ctx, p, func(e openai.ImageStreamEvent) error {
			return events.append(resourceEvent{Type: e.Type, Data: resourceEventData(e.Raw)}, len(e.Raw))
		})
		return resourceStreamResult{Events: events.events, HTTP: response}, err
	case "audio.speech", "audio.speech_stream":
		var p struct {
			openai.SpeechRequest
			Filename string `json:"filename"`
		}
		if err := decodeNativeResourceParams(raw, &p); err != nil {
			return nil, err
		}
		if operation == "audio.speech" {
			return newDownload(p.Filename, func(w io.Writer) (*openai.HTTPResponse, error) { return c.CreateSpeech(ctx, p.SpeechRequest, w) })
		}
		events := newResourceEventCollector()
		response, err := c.StreamSpeech(ctx, p.SpeechRequest, func(e openai.SpeechStreamEvent) error {
			return events.append(resourceEvent{Type: e.Type, Data: resourceEventData(e.Raw)}, len(e.Raw))
		})
		return resourceStreamResult{Events: events.events, HTTP: response}, err
	case "audio.transcribe", "audio.transcribe_stream":
		var p struct {
			Model                  string          `json:"model"`
			ChunkingStrategy       json.RawMessage `json:"chunking_strategy"`
			Include                []string        `json:"include"`
			Keywords               []string        `json:"keywords"`
			KnownSpeakerNames      []string        `json:"known_speaker_names"`
			KnownSpeakerReferences []string        `json:"known_speaker_references"`
			Language               string          `json:"language"`
			Languages              []string        `json:"languages"`
			Prompt                 string          `json:"prompt"`
			ResponseFormat         string          `json:"response_format"`
			Temperature            *float64        `json:"temperature"`
			TimestampGranularities []string        `json:"timestamp_granularities"`
			Extra                  map[string]any  `json:"-"`
		}
		if err := decodeNativeResourceParams(raw, &p); err != nil {
			return nil, err
		}
		u, err := oneUpload(files, "file")
		if err != nil {
			return nil, err
		}
		request := openai.TranscriptionRequest{Model: p.Model, File: u, ChunkingStrategy: p.ChunkingStrategy, Include: p.Include, Keywords: p.Keywords, KnownSpeakerNames: p.KnownSpeakerNames, KnownSpeakerReferences: p.KnownSpeakerReferences, Language: p.Language, Languages: p.Languages, Prompt: p.Prompt, ResponseFormat: p.ResponseFormat, Temperature: p.Temperature, TimestampGranularities: p.TimestampGranularities, Extra: p.Extra}
		if operation == "audio.transcribe" {
			return c.CreateTranscription(ctx, request)
		}
		events := newResourceEventCollector()
		response, err := c.StreamTranscription(ctx, request, func(e openai.TranscriptionStreamEvent) error {
			return events.append(resourceEvent{Type: e.Type, ID: e.ID, Data: resourceEventData(e.Raw)}, len(e.Raw))
		})
		return resourceStreamResult{Events: events.events, HTTP: response}, err
	case "audio.translate":
		var p struct {
			Model          string         `json:"model"`
			Prompt         string         `json:"prompt"`
			ResponseFormat string         `json:"response_format"`
			Temperature    *float64       `json:"temperature"`
			Extra          map[string]any `json:"-"`
		}
		if err := decodeNativeResourceParams(raw, &p); err != nil {
			return nil, err
		}
		u, err := oneUpload(files, "file")
		if err != nil {
			return nil, err
		}
		return c.CreateTranslation(ctx, openai.TranslationRequest{Model: p.Model, File: u, Prompt: p.Prompt, ResponseFormat: p.ResponseFormat, Temperature: p.Temperature, Extra: p.Extra})
	default:
		return nil, fmt.Errorf("unknown operation %q", operation)
	}
}
