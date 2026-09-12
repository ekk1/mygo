package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/ekk1/mygo/utils/httpserver"
)

type nativeFilePreview struct {
	Field       string `json:"field"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type,omitempty"`
	Size        int64  `json:"size"`
}

type nativeRequestPreview struct {
	Method      string                 `json:"method"`
	URL         string                 `json:"url"`
	ContentType string                 `json:"content_type,omitempty"`
	Body        any                    `json:"body,omitempty"`
	Files       []nativeFilePreview    `json:"files"`
	Requests    []nativeRequestPreview `json:"requests,omitempty"`
	Curl        string                 `json:"curl"`
	CurlError   string                 `json:"curl_error,omitempty"`
}

type nativeRequestSpec struct {
	preview    nativeRequestPreview
	headers    http.Header
	jsonBody   []byte
	form       map[string][]string
	uploads    resourceUploads
	binary     bool
	filename   string
	geminiFile bool
}

func (a *app) registerNative(s *httpserver.Server) error {
	return s.HandleFunc("POST /api/native/{vendor}/{operation}", a.runNative)
}

func (a *app) runNative(w http.ResponseWriter, r *http.Request) {
	vendor := strings.ToLower(r.PathValue("vendor"))
	request, uploads, cleanup, err := parseResourceRequest(w, r)
	defer cleanup()
	if err != nil {
		apiError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(request.ProviderID) == "" {
		apiError(w, http.StatusBadRequest, fmt.Errorf("provider_id is required"))
		return
	}
	snapshot := a.store.configSnapshot(false)
	p, err := resolveNativeProfileSnapshot(snapshot, vendor, request.ProviderID, r.URL.Query().Get("revision"))
	if err != nil {
		apiError(w, errorStatus(err), err)
		return
	}
	kind := p.Kind
	if kind == "" {
		kind = "openai"
	}
	if kind != vendor {
		apiError(w, http.StatusBadRequest, fmt.Errorf("profile kind %q does not match vendor %q", kind, vendor))
		return
	}
	operation := r.PathValue("operation")
	originalUploads := uploads
	var assetCleanup func()
	request.Params, uploads, assetCleanup, err = a.resolveAssets(vendor, operation, request.Params, uploads, request.AssetIDs)
	defer assetCleanup()
	if err != nil {
		apiError(w, http.StatusBadRequest, err)
		return
	}
	if r.URL.Query().Get("preview") == "1" {
		result, previewErr := nativePreview(p, operation, request.Params, uploads)
		if previewErr != nil {
			apiError(w, http.StatusBadRequest, previewErr)
			return
		}
		writeJSON(w, http.StatusOK, result)
		return
	}
	if _, err := buildNativeRequest(p, operation, request.Params, uploads); err != nil {
		apiError(w, http.StatusBadRequest, err)
		return
	}
	inputAssetIDs, err := a.captureUploads(p, operation, "", "", originalUploads)
	if err != nil {
		apiError(w, http.StatusInternalServerError, err)
		return
	}
	inputAssetIDs = append(inputAssetIDs, flattenAssetIDs(request.AssetIDs)...)
	if r.URL.Query().Get("background") == "1" {
		copied, release, err := snapshotTaskUploads(uploads)
		if err != nil {
			apiError(w, http.StatusInternalServerError, err)
			return
		}
		var params map[string]any
		_ = json.Unmarshal(request.Params, &params)
		title := stringValue(params["prompt"])
		if title == "" {
			title = stringValue(params["input"])
		}
		if title == "" {
			title = stringValue(params["text"])
		}
		if title == "" && vendor == "gemini" {
			title = nativeText(map[string]any{"content": params["contents"]})
			if instances, ok := params["instances"].([]any); title == "" && ok && len(instances) > 0 {
				instance, _ := instances[0].(map[string]any)
				title = stringValue(instance["prompt"])
			}
		}
		feature := r.URL.Query().Get("feature")
		if feature == "" {
			feature = operation
		}
		task, ctx, err := a.reserveTask(p, operation, feature, title, "", false)
		if err != nil {
			release()
			apiError(w, http.StatusInternalServerError, err)
			return
		}
		if request.SaveResponse == nil {
			request.SaveResponse = &snapshot.SaveResponses
		}
		go a.runGeneration(task, ctx, nativeGeneration{provider: p, operation: operation, params: request.Params, uploads: copied, saveResponse: request.SaveResponse, cleanup: release, inputAssetIDs: inputAssetIDs})
		a.respondTask(w, r, task, nil)
		return
	}
	result, err := a.executeNative(r.Context(), p, operation, request.Params, uploads, request.SaveResponse)
	if err != nil {
		if download, ok := result.(resourceDownload); ok {
			_ = os.Remove(download.Path)
		}
		apiError(w, http.StatusBadGateway, err)
		return
	}
	if download, ok := result.(resourceDownload); ok {
		defer os.Remove(download.Path)
		if _, err := a.captureAssets(r.Context(), p, operation, "", "", download); err != nil {
			apiError(w, http.StatusInternalServerError, err)
			return
		}
		f, openErr := os.Open(download.Path)
		if openErr != nil {
			apiError(w, http.StatusInternalServerError, openErr)
			return
		}
		defer f.Close()
		info, statErr := f.Stat()
		if statErr != nil {
			apiError(w, http.StatusInternalServerError, statErr)
			return
		}
		contentType := download.ContentType
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": download.Name}))
		http.ServeContent(w, r, download.Name, info.ModTime(), f)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func resolveNativeProfileSnapshot(cfg configuration, vendor, id, rawRevision string) (provider, error) {
	if rawRevision != "" {
		revision, err := strconv.ParseInt(rawRevision, 10, 64)
		if err != nil {
			return provider{}, fmt.Errorf("revision must be an integer")
		}
		if revision != cfg.Revision {
			return provider{}, errConflict
		}
	}
	for _, p := range cfg.Providers {
		if p.ID == id {
			kind := p.Kind
			if kind == "" {
				kind = "openai"
			}
			if kind != vendor {
				return provider{}, fmt.Errorf("profile kind %q does not match vendor %q", kind, vendor)
			}
			return p, nil
		}
	}
	return provider{}, errNotFound
}

func nativePreview(p provider, operation string, params json.RawMessage, uploads resourceUploads) (any, error) {
	spec, err := buildNativeRequest(p, operation, params, uploads)
	if err != nil {
		return nil, err
	}
	spec.preview.Curl, err = nativeCurl(p, spec)
	if err != nil {
		spec.preview.CurlError = err.Error()
	}
	return spec.preview, nil
}

func (a *app) executeNative(ctx context.Context, p provider, operation string, params json.RawMessage, uploads resourceUploads, saveResponse *bool) (any, error) {
	spec, err := buildNativeRequest(p, operation, params, uploads)
	if err != nil {
		return nil, err
	}
	result, err := a.sendNative(ctx, p, operation, spec, saveResponse)
	return result, redactNativeError(err, p.APIKey)
}

type nativeRedactedError struct {
	err error
	key string
}

func (e *nativeRedactedError) Error() string {
	return strings.ReplaceAll(e.err.Error(), e.key, "[REDACTED]")
}

func (e *nativeRedactedError) Unwrap() error { return e.err }

func redactNativeError(err error, key string) error {
	if err == nil || key == "" || !strings.Contains(err.Error(), key) {
		return err
	}
	return &nativeRedactedError{err: err, key: key}
}

func nativeParams(raw json.RawMessage) (map[string]json.RawMessage, error) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, fmt.Errorf("invalid operation params: expected a JSON object")
	}
	return fields, nil
}

func takeString(fields map[string]json.RawMessage, name string, required bool) (string, error) {
	raw, ok := fields[name]
	if !ok {
		if required {
			return "", fmt.Errorf("%s is required", name)
		}
		return "", nil
	}
	delete(fields, name)
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s must be a non-empty string", name)
	}
	return value, nil
}

func cleanResourceID(value, prefix string) (string, error) {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, prefix+"/")
	if value == "" {
		return "", fmt.Errorf("resource ID is required")
	}
	return url.PathEscape(value), nil
}

func appendEndpoint(base, endpoint string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("invalid provider base URL")
	}
	target := strings.TrimRight(u.String(), "/") + endpoint
	if _, err = url.ParseRequestURI(target); err != nil {
		return "", fmt.Errorf("invalid native endpoint: %w", err)
	}
	return target, nil
}

func queryValue(raw json.RawMessage) ([]string, error) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	switch v := value.(type) {
	case string:
		return []string{v}, nil
	case bool:
		return []string{strconv.FormatBool(v)}, nil
	case float64:
		return []string{strconv.FormatFloat(v, 'f', -1, 64)}, nil
	case nil:
		return nil, nil
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			b, _ := json.Marshal(item)
			if s, ok := item.(string); ok {
				out = append(out, s)
			} else {
				out = append(out, string(b))
			}
		}
		return out, nil
	default:
		b, _ := json.Marshal(v)
		return []string{string(b)}, nil
	}
}

func addQuery(rawURL string, fields map[string]json.RawMessage, aliases map[string]string) (string, error) {
	u, _ := url.Parse(rawURL)
	q := u.Query()
	for key, raw := range fields {
		if alias := aliases[key]; alias != "" {
			key = alias
		}
		values, err := queryValue(raw)
		if err != nil {
			return "", fmt.Errorf("invalid query field %q: %w", key, err)
		}
		for _, value := range values {
			q.Add(key, value)
		}
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func formValues(fields map[string]json.RawMessage) (map[string][]string, map[string]any, error) {
	form := map[string][]string{}
	view := map[string]any{}
	for key, raw := range fields {
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, nil, err
		}
		view[key] = value
		switch v := value.(type) {
		case string:
			form[key] = []string{v}
		case []any:
			for _, item := range v {
				if s, ok := item.(string); ok {
					form[key] = append(form[key], s)
				} else {
					b, _ := json.Marshal(item)
					form[key] = append(form[key], string(b))
				}
			}
		default:
			form[key] = []string{string(raw)}
		}
	}
	return form, view, nil
}

func uploadPreviews(uploads resourceUploads) ([]nativeFilePreview, error) {
	result := []nativeFilePreview{}
	for field, values := range uploads {
		for _, upload := range values {
			size := int64(-1)
			if seeker, ok := upload.Reader.(io.Seeker); ok {
				position, err := seeker.Seek(0, io.SeekCurrent)
				if err != nil {
					return nil, err
				}
				size, err = seeker.Seek(0, io.SeekEnd)
				if err != nil {
					return nil, err
				}
				if _, err = seeker.Seek(position, io.SeekStart); err != nil {
					return nil, err
				}
			}
			result = append(result, nativeFilePreview{Field: field, Filename: upload.Filename, ContentType: upload.ContentType, Size: size})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Field == result[j].Field {
			return result[i].Filename < result[j].Filename
		}
		return result[i].Field < result[j].Field
	})
	return result, nil
}

func requireUploads(uploads resourceUploads, field string, exact int) error {
	if len(uploads[field]) != exact {
		return fmt.Errorf("operation requires exactly %d %q upload(s)", exact, field)
	}
	for _, upload := range uploads[field] {
		if upload.Reader == nil {
			return fmt.Errorf("upload %q has no content", field)
		}
	}
	return nil
}

func buildNativeRequest(p provider, operation string, raw json.RawMessage, uploads resourceUploads) (nativeRequestSpec, error) {
	kind := p.Kind
	if kind == "" {
		kind = "openai"
	}
	if kind == "compatible" {
		group := strings.Split(operation, ".")[0]
		if group == "files" || group == "containers" || group == "batches" || group == "images" || group == "audio" {
			enabled := false
			for _, capability := range p.Resources {
				if capability == group {
					enabled = true
					break
				}
			}
			if !enabled {
				return nativeRequestSpec{}, fmt.Errorf("compatible capability %q is not enabled for this profile", group)
			}
		}
	}
	fields, err := nativeParams(raw)
	if err != nil {
		return nativeRequestSpec{}, err
	}
	method, endpoint := "", ""
	binary, multipartBody, geminiFile := false, false, false
	aliases := map[string]string{}

	idPath := func(field, prefix string) (string, error) {
		value, e := takeString(fields, field, true)
		if e != nil {
			return "", e
		}
		return cleanResourceID(value, prefix)
	}
	switch kind {
	case "openai", "compatible":
		switch operation {
		case "models.list":
			method, endpoint = "GET", "/models"
		case "responses.create":
			method, endpoint = "POST", "/responses"
		case "chat.create":
			method, endpoint = "POST", "/chat/completions"
		case "images.generate":
			method, endpoint = "POST", "/images/generations"
		case "images.edit":
			method, endpoint, multipartBody = "POST", "/images/edits", len(uploads) > 0
		case "audio.speech":
			method, endpoint, binary = "POST", "/audio/speech", true
		case "audio.transcribe":
			if err = requireUploads(uploads, "file", 1); err != nil {
				return nativeRequestSpec{}, err
			}
			method, endpoint, multipartBody = "POST", "/audio/transcriptions", true
		case "audio.translate":
			if err = requireUploads(uploads, "file", 1); err != nil {
				return nativeRequestSpec{}, err
			}
			method, endpoint, multipartBody = "POST", "/audio/translations", true
		case "videos.create":
			method, endpoint = "POST", "/videos"
		case "videos.get":
			id, e := idPath("video_id", "videos")
			if e != nil {
				return nativeRequestSpec{}, e
			}
			method, endpoint = "GET", "/videos/"+id
		case "files.upload":
			if err = requireUploads(uploads, "file", 1); err != nil {
				return nativeRequestSpec{}, err
			}
			method, endpoint, multipartBody = "POST", "/files", true
		case "files.list":
			method, endpoint = "GET", "/files"
		case "files.get", "files.delete", "files.download":
			id, e := idPath("file_id", "files")
			if e != nil {
				return nativeRequestSpec{}, e
			}
			method, endpoint = "GET", "/files/"+id
			if operation == "files.delete" {
				method = "DELETE"
			}
			if operation == "files.download" {
				endpoint += "/content"
				binary = true
			}
		case "batches.create":
			method, endpoint = "POST", "/batches"
		case "batches.list":
			method, endpoint = "GET", "/batches"
		case "batches.get", "batches.cancel":
			id, e := idPath("batch_id", "batches")
			if e != nil {
				return nativeRequestSpec{}, e
			}
			method, endpoint = "GET", "/batches/"+id
			if operation == "batches.cancel" {
				method = "POST"
				endpoint += "/cancel"
			}
		default:
			if (kind == "openai" || kind == "compatible") && strings.HasPrefix(operation, "containers.") {
				return buildOpenAIContainer(p, operation, fields, uploads)
			}
			return nativeRequestSpec{}, fmt.Errorf("operation %q is not supported for %s", operation, kind)
		}
	case "anthropic":
		switch operation {
		case "models.list":
			method, endpoint = "GET", "/models"
		case "messages.create":
			method, endpoint = "POST", "/messages"
		case "files.upload":
			if err = requireUploads(uploads, "file", 1); err != nil {
				return nativeRequestSpec{}, err
			}
			method, endpoint, multipartBody = "POST", "/files", true
		case "files.list":
			method, endpoint = "GET", "/files"
		case "files.get", "files.delete", "files.download":
			id, e := idPath("file_id", "files")
			if e != nil {
				return nativeRequestSpec{}, e
			}
			method, endpoint = "GET", "/files/"+id
			if operation == "files.delete" {
				method = "DELETE"
			}
			if operation == "files.download" {
				endpoint += "/content"
				binary = true
			}
		case "batches.create":
			method, endpoint = "POST", "/messages/batches"
		case "batches.list":
			method, endpoint = "GET", "/messages/batches"
		case "batches.get", "batches.cancel", "batches.delete", "batches.results":
			id, e := idPath("batch_id", "batches")
			if e != nil {
				return nativeRequestSpec{}, e
			}
			method, endpoint = "GET", "/messages/batches/"+id
			switch operation {
			case "batches.cancel":
				method = "POST"
				endpoint += "/cancel"
			case "batches.delete":
				method = "DELETE"
			case "batches.results":
				endpoint += "/results"
				binary = true
			}
		default:
			return nativeRequestSpec{}, fmt.Errorf("operation %q is not supported for anthropic", operation)
		}
	case "gemini":
		switch operation {
		case "models.list":
			method, endpoint = "GET", "/v1beta/models"
		case "content.generate", "images.generate":
			stream := false
			if operation == "content.generate" {
				if raw, ok := fields["stream"]; ok {
					if e := json.Unmarshal(raw, &stream); e != nil {
						return nativeRequestSpec{}, fmt.Errorf("stream must be a boolean")
					}
					delete(fields, "stream")
				}
			}
			model, e := takeString(fields, "model", true)
			if e != nil {
				return nativeRequestSpec{}, e
			}
			id, e := cleanResourceID(model, "models")
			if e != nil {
				return nativeRequestSpec{}, e
			}
			method, endpoint = "POST", "/v1beta/models/"+id+":generateContent"
			if stream {
				endpoint = "/v1beta/models/" + id + ":streamGenerateContent?alt=sse"
			}
		case "files.upload":
			if err = requireUploads(uploads, "file", 1); err != nil {
				return nativeRequestSpec{}, err
			}
			method, endpoint, geminiFile = "POST", "/upload/v1beta/files", true
		case "files.list":
			method, endpoint = "GET", "/v1beta/files"
		case "files.get", "files.delete", "files.download":
			nameValue := ""
			if _, ok := fields["name"]; ok {
				nameValue, err = takeString(fields, "name", true)
			} else {
				nameValue, err = takeString(fields, "file_id", true)
			}
			if err != nil {
				return nativeRequestSpec{}, err
			}
			resource, e := geminiNamePath(nameValue, "files")
			if e != nil {
				return nativeRequestSpec{}, e
			}
			method, endpoint = "GET", "/v1beta/"+resource
			if operation == "files.delete" {
				method = "DELETE"
			}
			if operation == "files.download" {
				endpoint += ":download?alt=media"
				binary = true
			}
		case "videos.create":
			model, e := takeString(fields, "model", true)
			if e != nil {
				return nativeRequestSpec{}, e
			}
			id, e := cleanResourceID(model, "models")
			if e != nil {
				return nativeRequestSpec{}, e
			}
			method, endpoint = "POST", "/v1beta/models/"+id+":predictLongRunning"
		case "videos.get":
			name := ""
			if _, ok := fields["name"]; ok {
				name, err = takeString(fields, "name", true)
			} else {
				name, err = takeString(fields, "video_id", true)
			}
			if err != nil {
				return nativeRequestSpec{}, err
			}
			endpointName, e := geminiNamePath(name, "operations")
			if e != nil {
				return nativeRequestSpec{}, e
			}
			method, endpoint = "GET", "/v1beta/"+endpointName
		case "batches.create":
			model, e := takeString(fields, "model", true)
			if e != nil {
				return nativeRequestSpec{}, e
			}
			id, e := cleanResourceID(model, "models")
			if e != nil {
				return nativeRequestSpec{}, e
			}
			method, endpoint = "POST", "/v1beta/models/"+id+":batchGenerateContent"
		case "batches.list":
			method, endpoint = "GET", "/v1beta/batches"
		case "batches.get", "batches.cancel", "batches.delete":
			name := ""
			if _, ok := fields["name"]; ok {
				name, err = takeString(fields, "name", true)
			} else {
				name, err = takeString(fields, "batch_id", true)
			}
			if err != nil {
				return nativeRequestSpec{}, err
			}
			resource, e := geminiNamePath(name, "batches")
			if e != nil {
				return nativeRequestSpec{}, e
			}
			method, endpoint = "GET", "/v1beta/"+resource
			if operation == "batches.cancel" {
				method = "POST"
				endpoint += ":cancel"
			}
			if operation == "batches.delete" {
				method = "DELETE"
			}
		default:
			return nativeRequestSpec{}, fmt.Errorf("operation %q is not supported for gemini", operation)
		}
	case "xai":
		switch operation {
		case "models.list":
			method, endpoint = "GET", "/models"
		case "responses.create":
			method, endpoint = "POST", "/responses"
		case "chat.create":
			method, endpoint = "POST", "/chat/completions"
		case "images.generate":
			method, endpoint = "POST", "/images/generations"
		case "images.edit":
			method, endpoint, multipartBody = "POST", "/images/edits", len(uploads) > 0
		case "audio.speech":
			method, endpoint, binary = "POST", "/tts", true
			if raw, ok := fields["with_timestamps"]; ok {
				var withTimestamps bool
				if json.Unmarshal(raw, &withTimestamps) == nil && withTimestamps {
					binary = false
				}
			}
		case "audio.transcribe":
			if err = requireUploads(uploads, "file", 1); err != nil {
				return nativeRequestSpec{}, err
			}
			method, endpoint, multipartBody = "POST", "/stt", true
		case "videos.create":
			method, endpoint = "POST", "/videos/generations"
		case "videos.get":
			id, e := idPath("video_id", "videos")
			if e != nil {
				return nativeRequestSpec{}, e
			}
			method, endpoint = "GET", "/videos/"+id
		case "files.upload":
			if err = requireUploads(uploads, "file", 1); err != nil {
				return nativeRequestSpec{}, err
			}
			method, endpoint, multipartBody = "POST", "/files", true
		case "files.list":
			method, endpoint = "GET", "/files"
		case "files.get", "files.delete", "files.download":
			id, e := idPath("file_id", "files")
			if e != nil {
				return nativeRequestSpec{}, e
			}
			method, endpoint = "GET", "/files/"+id
			if operation == "files.delete" {
				method = "DELETE"
			}
			if operation == "files.download" {
				endpoint += "/content"
				binary = true
			}
		case "batches.create":
			method, endpoint = "POST", "/batches"
		case "batches.list":
			method, endpoint = "GET", "/batches"
		case "batches.get", "batches.cancel", "batches.requests", "batches.results":
			id, e := idPath("batch_id", "batches")
			if e != nil {
				return nativeRequestSpec{}, e
			}
			method, endpoint = "GET", "/batches/"+id
			switch operation {
			case "batches.cancel":
				method = "POST"
				endpoint += ":cancel"
			case "batches.requests":
				method = "POST"
				endpoint += "/requests"
			case "batches.results":
				endpoint += "/results"
			}
		default:
			return nativeRequestSpec{}, fmt.Errorf("operation %q is not supported for xai", operation)
		}
	default:
		return nativeRequestSpec{}, fmt.Errorf("unknown provider kind %q", kind)
	}

	baseURL, err := appendEndpoint(p.BaseURL, endpoint)
	if err != nil {
		return nativeRequestSpec{}, err
	}
	files, err := uploadPreviews(uploads)
	if err != nil {
		return nativeRequestSpec{}, err
	}
	headers := nativeHeaders(kind, p.APIKey, operation)
	if kind == "anthropic" && operation == "messages.create" && anthropicMessagesUseFile(fields["messages"]) {
		headers.Set("anthropic-beta", "files-api-2025-04-14")
	}
	spec := nativeRequestSpec{preview: nativeRequestPreview{Method: method, URL: baseURL, Files: files}, headers: headers, uploads: uploads, binary: binary, geminiFile: geminiFile}
	if filenameRaw, ok := fields["filename"]; ok && binary {
		var filename string
		if json.Unmarshal(filenameRaw, &filename) == nil {
			spec.filename = filename
		}
		delete(fields, "filename")
	}
	if spec.filename == "" {
		spec.filename = defaultNativeFilename(operation)
	}
	if method == "GET" || method == "DELETE" {
		spec.preview.URL, err = addQuery(spec.preview.URL, fields, aliases)
		if err != nil {
			return nativeRequestSpec{}, err
		}
		return spec, nil
	}
	if geminiFile {
		metadata := map[string]json.RawMessage{"file": marshalNativeFields(fields)}
		spec.jsonBody, err = json.Marshal(metadata)
		if err != nil {
			return nativeRequestSpec{}, err
		}
		if err = json.Unmarshal(spec.jsonBody, &spec.preview.Body); err != nil {
			return nativeRequestSpec{}, err
		}
		spec.preview.ContentType = "application/json"
		start := spec.preview
		uploadType := "application/octet-stream"
		if len(files) == 1 && files[0].ContentType != "" {
			uploadType = files[0].ContentType
		}
		finalize := nativeRequestPreview{Method: http.MethodPost, URL: "${X-Goog-Upload-URL (same origin as provider)}", ContentType: uploadType, Body: "<binary file content>", Files: files}
		spec.preview.Requests = []nativeRequestPreview{start, finalize}
		return spec, nil
	}
	if multipartBody {
		form, view, e := formValues(fields)
		if e != nil {
			return nativeRequestSpec{}, e
		}
		spec.form = form
		spec.preview.Body = view
		spec.preview.ContentType = "multipart/form-data"
		if geminiFile {
			spec.preview.ContentType = "multipart/related"
		}
		return spec, nil
	}
	if len(fields) > 0 {
		spec.jsonBody, err = json.Marshal(fields)
		if err != nil {
			return nativeRequestSpec{}, err
		}
		var view any
		if err = json.Unmarshal(spec.jsonBody, &view); err != nil {
			return nativeRequestSpec{}, err
		}
		spec.preview.Body = view
		spec.preview.ContentType = "application/json"
	}
	return spec, nil
}

func marshalNativeFields(fields map[string]json.RawMessage) json.RawMessage {
	b, _ := json.Marshal(fields)
	return b
}

func anthropicMessagesUseFile(raw json.RawMessage) bool {
	var messages []any
	if json.Unmarshal(raw, &messages) != nil {
		return false
	}
	for _, messageValue := range messages {
		message, ok := messageValue.(map[string]any)
		if !ok {
			continue
		}
		content, ok := message["content"].([]any)
		if !ok {
			continue
		}
		for _, blockValue := range content {
			block, ok := blockValue.(map[string]any)
			if !ok || block["type"] != "document" {
				continue
			}
			source, ok := block["source"].(map[string]any)
			if !ok || source["type"] != "file" {
				continue
			}
			fileID, _ := source["file_id"].(string)
			if strings.TrimSpace(fileID) != "" {
				return true
			}
		}
	}
	return false
}

func geminiNamePath(name, prefix string) (string, error) {
	name = strings.TrimPrefix(strings.TrimSpace(name), prefix+"/")
	if name == "" || strings.Contains(name, "/") {
		return "", fmt.Errorf("invalid %s name", prefix)
	}
	return prefix + "/" + url.PathEscape(name), nil
}

func nativeHeaders(kind, key, operation string) http.Header {
	h := make(http.Header)
	h.Set("Accept", "application/json")
	switch kind {
	case "anthropic":
		h.Set("x-api-key", key)
		h.Set("anthropic-version", "2023-06-01")
		if strings.HasPrefix(operation, "files.") {
			h.Set("anthropic-beta", "files-api-2025-04-14")
		}
	case "gemini":
		h.Set("x-goog-api-key", key)
	default:
		h.Set("Authorization", "Bearer "+key)
	}
	return h
}

// nativeGeminiStageHeaders is shared by sending and curl export after the
// request builder has validated the single Gemini file upload.
func nativeGeminiStageHeaders(spec nativeRequestSpec, finalize bool) http.Header {
	headers := spec.headers.Clone()
	file := spec.preview.Files[0]
	contentType := file.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if finalize {
		headers.Set("Content-Type", contentType)
		headers.Set("X-Goog-Upload-Offset", "0")
		headers.Set("X-Goog-Upload-Command", "upload, finalize")
	} else {
		headers.Set("Content-Type", "application/json")
		headers.Set("X-Goog-Upload-Protocol", "resumable")
		headers.Set("X-Goog-Upload-Command", "start")
		headers.Set("X-Goog-Upload-Header-Content-Length", strconv.FormatInt(file.Size, 10))
		headers.Set("X-Goog-Upload-Header-Content-Type", contentType)
	}
	return headers
}

func defaultNativeFilename(operation string) string {
	if operation == "audio.speech" {
		return "speech.bin"
	}
	if strings.HasSuffix(operation, "results") {
		return "results.jsonl"
	}
	return path.Base(operation) + ".bin"
}

func buildOpenAIContainer(p provider, operation string, fields map[string]json.RawMessage, uploads resourceUploads) (nativeRequestSpec, error) {
	method, endpoint, multipartBody, binary := "", "", false, false
	takeID := func(field, prefix string) (string, error) {
		v, e := takeString(fields, field, true)
		if e != nil {
			return "", e
		}
		return cleanResourceID(v, prefix)
	}
	switch operation {
	case "containers.create":
		method, endpoint = "POST", "/containers"
	case "containers.list":
		method, endpoint = "GET", "/containers"
	case "containers.get", "containers.delete":
		id, e := takeID("container_id", "containers")
		if e != nil {
			return nativeRequestSpec{}, e
		}
		method, endpoint = "GET", "/containers/"+id
		if operation == "containers.delete" {
			method = "DELETE"
		}
	case "containers.files.add":
		cid, e := takeID("container_id", "containers")
		if e != nil {
			return nativeRequestSpec{}, e
		}
		method, endpoint = "POST", "/containers/"+cid+"/files"
	case "containers.files.upload":
		if err := requireUploads(uploads, "file", 1); err != nil {
			return nativeRequestSpec{}, err
		}
		cid, e := takeID("container_id", "containers")
		if e != nil {
			return nativeRequestSpec{}, e
		}
		method, endpoint, multipartBody = "POST", "/containers/"+cid+"/files", true
	case "containers.files.list":
		cid, e := takeID("container_id", "containers")
		if e != nil {
			return nativeRequestSpec{}, e
		}
		method, endpoint = "GET", "/containers/"+cid+"/files"
	case "containers.files.get", "containers.files.delete", "containers.files.download":
		cid, e := takeID("container_id", "containers")
		if e != nil {
			return nativeRequestSpec{}, e
		}
		fid, e := takeID("file_id", "files")
		if e != nil {
			return nativeRequestSpec{}, e
		}
		method, endpoint = "GET", "/containers/"+cid+"/files/"+fid
		if operation == "containers.files.delete" {
			method = "DELETE"
		}
		if operation == "containers.files.download" {
			endpoint += "/content"
			binary = true
		}
	default:
		return nativeRequestSpec{}, fmt.Errorf("operation %q is not supported for openai", operation)
	}
	base, err := appendEndpoint(p.BaseURL, endpoint)
	if err != nil {
		return nativeRequestSpec{}, err
	}
	files, err := uploadPreviews(uploads)
	if err != nil {
		return nativeRequestSpec{}, err
	}
	spec := nativeRequestSpec{preview: nativeRequestPreview{Method: method, URL: base, Files: files}, headers: nativeHeaders("openai", p.APIKey, operation), uploads: uploads, binary: binary, filename: defaultNativeFilename(operation)}
	if filenameRaw, ok := fields["filename"]; ok {
		_ = json.Unmarshal(filenameRaw, &spec.filename)
		delete(fields, "filename")
	}
	if method == "GET" || method == "DELETE" {
		spec.preview.URL, err = addQuery(base, fields, nil)
		return spec, err
	}
	if multipartBody {
		spec.form, spec.preview.Body, err = formValues(fields)
		spec.preview.ContentType = "multipart/form-data"
		return spec, err
	}
	spec.jsonBody, err = json.Marshal(fields)
	if err != nil {
		return spec, err
	}
	_ = json.Unmarshal(spec.jsonBody, &spec.preview.Body)
	spec.preview.ContentType = "application/json"
	return spec, nil
}
