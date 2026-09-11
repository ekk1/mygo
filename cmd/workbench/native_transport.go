package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ekk1/mygo/utils/httpclient"
)

const maxNativeResponseBytes int64 = 256 << 20

type nativeHTTPError struct {
	Status int
	Body   []byte
}

type nativeCountingReader struct {
	reader io.Reader
	bytes  int64
}

type nativeEventSinkContextKey struct{}

func withNativeEventSink(ctx context.Context, sink func(resourceEvent) error) context.Context {
	return context.WithValue(ctx, nativeEventSinkContextKey{}, sink)
}

type nativeTrackedBody struct {
	io.ReadCloser
	mu        sync.Mutex
	expected  int64
	bytes     int64
	complete  bool
	closed    bool
	closeOnce sync.Once
	closeErr  error
}

func (b *nativeTrackedBody) Read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return 0, io.ErrClosedPipe
	}
	n, err := b.ReadCloser.Read(p)
	b.bytes += int64(n)
	if err == io.EOF {
		b.complete = true
	}
	return n, err
}
func (b *nativeTrackedBody) Close() error {
	b.closeOnce.Do(func() { b.closeErr = b.ReadCloser.Close(); b.mu.Lock(); b.closed = true; b.mu.Unlock() })
	return b.closeErr
}
func (b *nativeTrackedBody) finish() (int64, bool, error) {
	closeErr := b.Close()
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.expected >= 0 && b.bytes == b.expected && closeErr == nil {
		b.complete = true
	}
	return b.bytes, b.complete, closeErr
}

func trackNativeBody(req *http.Request) *nativeTrackedBody {
	body := req.Body
	if body == nil {
		body = http.NoBody
	}
	expected := req.ContentLength
	if body != http.NoBody && expected == 0 {
		expected = -1
	}
	tracked := &nativeTrackedBody{ReadCloser: body, expected: expected}
	if body == http.NoBody {
		tracked.complete = true
	}
	req.Body = tracked
	return tracked
}

func (r *nativeCountingReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.bytes += int64(n)
	return n, err
}

func (e *nativeHTTPError) Error() string {
	text := strings.TrimSpace(string(e.Body))
	if len(text) > 4096 {
		text = text[:4096] + "…"
	}
	if text == "" {
		return fmt.Sprintf("upstream HTTP %d %s", e.Status, http.StatusText(e.Status))
	}
	return fmt.Sprintf("upstream HTTP %d: %s", e.Status, text)
}

type nativeLog struct {
	operation       operationLog
	dir             string
	request         string
	saveBody        bool
	apiKey          string
	requestBytes    int64
	requestComplete bool
}

func (a *app) startNativeLog(p provider, operation string, saveResponse *bool) (*nativeLog, error) {
	save := a.store.configSnapshot(false).SaveResponses
	if saveResponse != nil {
		save = *saveResponse
	}
	entry := operationLog{ID: newID(), ProviderID: p.ID, Operation: operation, StartedAt: now(), Status: "running", SaveResponse: save}
	dir := filepath.Join(a.store.dir, "logs", entry.ID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err := writeFileJSON(filepath.Join(dir, "context.json"), entry); err != nil {
		return nil, err
	}
	requestDir, err := os.MkdirTemp(dir, time.Now().UTC().Format("20060102T150405.000000000")+"-")
	if err != nil {
		return nil, err
	}
	return &nativeLog{operation: entry, dir: dir, request: requestDir, saveBody: save, apiKey: p.APIKey}, nil
}

func nativeRedactedHeaders(headers http.Header) http.Header {
	out := headers.Clone()
	for key := range out {
		low := strings.ToLower(key)
		if strings.Contains(low, "auth") || strings.Contains(low, "api-key") || strings.Contains(low, "apikey") || strings.Contains(low, "token") || strings.Contains(low, "secret") || strings.Contains(low, "cookie") || strings.Contains(low, "upload-url") || low == "location" {
			out[key] = []string{"[REDACTED]"}
		}
	}
	return out
}

func nativeRedactedURL(u *url.URL) string {
	v := *u
	q := v.Query()
	for key := range q {
		low := strings.ToLower(key)
		if strings.Contains(low, "key") || strings.Contains(low, "token") || strings.Contains(low, "secret") || strings.Contains(low, "credential") || strings.Contains(low, "signature") || strings.Contains(low, "upload_id") {
			q.Set(key, "[REDACTED]")
		}
	}
	v.RawQuery = q.Encode()
	return v.String()
}

func (l *nativeLog) begin(req *http.Request, bodyPath string) error {
	meta := map[string]any{"method": req.Method, "url": nativeRedactedURL(req.URL), "headers": nativeRedactedHeaders(req.Header), "started_at": time.Now().UTC()}
	if err := writeFileJSON(filepath.Join(l.request, "request.json"), meta); err != nil {
		return err
	}
	if bodyPath == "" {
		return os.WriteFile(filepath.Join(l.request, "request.body"), nil, 0600)
	}
	source, err := os.Open(bodyPath)
	if err != nil {
		return err
	}
	defer source.Close()
	destination, err := os.OpenFile(filepath.Join(l.request, "request.body"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(destination, source)
	if info, statErr := source.Stat(); statErr == nil {
		l.requestBytes = info.Size()
	}
	return errors.Join(copyErr, destination.Close())
}

func (l *nativeLog) finish(res *http.Response, started time.Time, responseBytes int64, complete bool, callErr error) error {
	requestErr := l.finishRequest(res, started, responseBytes, complete, callErr)
	return errors.Join(requestErr, l.finishContext(callErr))
}

func (l *nativeLog) finishRequest(res *http.Response, started time.Time, responseBytes int64, complete bool, callErr error) error {
	meta := map[string]any{"finished_at": time.Now().UTC(), "elapsed_ms": time.Since(started).Milliseconds(), "request_bytes": l.requestBytes, "request_complete": l.requestComplete, "response_bytes": responseBytes, "response_complete": complete, "received_response": res != nil}
	if res != nil {
		meta["status_code"] = res.StatusCode
		meta["headers"] = nativeRedactedHeaders(res.Header)
	}
	if callErr != nil {
		meta["error"] = "request or response processing failed"
	}
	return writeFileJSON(filepath.Join(l.request, "response.json"), meta)
}

func (l *nativeLog) finishContext(callErr error) error {
	l.operation.FinishedAt = now()
	l.operation.Status = "complete"
	if callErr != nil {
		l.operation.Status = "error"
		l.operation.Error = callErr.Error()
		if l.apiKey != "" {
			l.operation.Error = strings.ReplaceAll(l.operation.Error, l.apiKey, "[REDACTED]")
		}
	}
	return writeFileJSON(filepath.Join(l.dir, "context.json"), l.operation)
}

func rewindUploads(uploads resourceUploads) error {
	for _, values := range uploads {
		for _, upload := range values {
			if seeker, ok := upload.Reader.(io.Seeker); ok {
				if _, err := seeker.Seek(0, io.SeekStart); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func encodeNativeBody(spec nativeRequestSpec) (pathName, contentType string, err error) {
	f, err := os.CreateTemp("", "mygo-workbench-native-request-*")
	if err != nil {
		return "", "", err
	}
	pathName = f.Name()
	ok := false
	defer func() {
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		if !ok {
			_ = os.Remove(pathName)
		}
	}()
	if err = rewindUploads(spec.uploads); err != nil {
		return "", "", err
	}
	if spec.preview.ContentType == "multipart/form-data" {
		writer := multipart.NewWriter(f)
		for key, values := range spec.form {
			for _, value := range values {
				if err = writer.WriteField(key, value); err != nil {
					return "", "", err
				}
			}
		}
		for field, uploads := range spec.uploads {
			for _, upload := range uploads {
				header := make(textproto.MIMEHeader)
				header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": field, "filename": upload.Filename}))
				if upload.ContentType != "" {
					header.Set("Content-Type", upload.ContentType)
				}
				part, e := writer.CreatePart(header)
				if e != nil {
					return "", "", e
				}
				if _, e = io.Copy(part, upload.Reader); e != nil {
					return "", "", e
				}
			}
		}
		if err = writer.Close(); err != nil {
			return "", "", err
		}
		contentType = writer.FormDataContentType()
	} else if spec.geminiFile {
		writer := multipart.NewWriter(f)
		metadata := map[string]any{"file": map[string]any{}}
		fileMeta := metadata["file"].(map[string]any)
		for key, values := range spec.form {
			if len(values) == 1 {
				fileMeta[key] = values[0]
			} else {
				fileMeta[key] = values
			}
		}
		h := make(textproto.MIMEHeader)
		h.Set("Content-Type", "application/json; charset=UTF-8")
		part, e := writer.CreatePart(h)
		if e != nil {
			return "", "", e
		}
		if e = json.NewEncoder(part).Encode(metadata); e != nil {
			return "", "", e
		}
		var upload *resourceUpload
		for _, values := range spec.uploads {
			if len(values) > 0 {
				u := values[0]
				upload = &u
				break
			}
		}
		if upload == nil {
			return "", "", fmt.Errorf("operation requires one file upload")
		}
		h = make(textproto.MIMEHeader)
		if upload.ContentType != "" {
			h.Set("Content-Type", upload.ContentType)
		}
		part, e = writer.CreatePart(h)
		if e != nil {
			return "", "", e
		}
		if _, e = io.Copy(part, upload.Reader); e != nil {
			return "", "", e
		}
		if err = writer.Close(); err != nil {
			return "", "", err
		}
		contentType = "multipart/related; boundary=" + writer.Boundary()
	} else if len(spec.jsonBody) > 0 {
		if _, err = f.Write(spec.jsonBody); err != nil {
			return "", "", err
		}
		contentType = "application/json"
	}
	if err = f.Chmod(0600); err != nil {
		return "", "", err
	}
	ok = true
	return pathName, contentType, nil
}

func (a *app) sendNative(ctx context.Context, p provider, operation string, spec nativeRequestSpec, saveResponse *bool) (result any, err error) {
	if spec.geminiFile {
		return a.sendGeminiFile(ctx, p, operation, spec, saveResponse)
	}
	logEntry, err := a.startNativeLog(p, operation, saveResponse)
	if err != nil {
		return nil, err
	}
	started := time.Now()
	var response *http.Response
	var responseBytes int64
	complete := false
	defer func() {
		finishErr := logEntry.finish(response, started, responseBytes, complete, err)
		err = errors.Join(err, finishErr)
	}()
	bodyPath, contentType, err := encodeNativeBody(spec)
	if err != nil {
		return nil, err
	}
	defer os.Remove(bodyPath)
	if contentType == "" {
		contentType = spec.preview.ContentType
	}
	body, err := os.Open(bodyPath)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	req, err := http.NewRequestWithContext(ctx, spec.preview.Method, spec.preview.URL, body)
	if err != nil {
		return nil, err
	}
	req.Header = spec.headers.Clone()
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	info, err := body.Stat()
	if err != nil {
		return nil, err
	}
	req.ContentLength = info.Size()
	if err = logEntry.begin(req, bodyPath); err != nil {
		return nil, err
	}
	trackedBody := trackNativeBody(req)
	defer func() {
		sent, requestComplete, trackErr := trackedBody.finish()
		logEntry.requestBytes = sent
		logEntry.requestComplete = requestComplete
		err = errors.Join(err, trackErr)
	}()
	client, err := httpclient.New(httpclient.Config{ProxyURL: p.ProxyURL, Timeout: 10 * time.Minute, MaxResponseBytes: maxNativeResponseBytes})
	if err != nil {
		return nil, err
	}
	defer client.CloseIdleConnections()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err = client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, response.Body.Close()) }()
	var reader io.Reader = response.Body
	var responseLog *os.File
	if logEntry.saveBody {
		responseLog, err = os.OpenFile(filepath.Join(logEntry.request, "response.body"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return nil, err
		}
		defer func() { err = errors.Join(err, responseLog.Close()) }()
		reader = io.TeeReader(reader, responseLog)
	}
	counted := &nativeCountingReader{reader: reader}
	reader = counted
	defer func() { responseBytes = counted.bytes }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, e := readNativeLimit(reader)
		complete = e == nil
		if e != nil {
			return nil, e
		}
		return nil, &nativeHTTPError{Status: response.StatusCode, Body: body}
	}
	if spec.binary {
		download, e := writeNativeDownload(reader, spec.filename, response.Header.Get("Content-Type"))
		if e != nil {
			return nil, e
		}
		complete = true
		return download, nil
	}
	if strings.HasPrefix(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream") {
		events, e := collectNativeSSE(ctx, reader, p.Kind, operation)
		if e == nil {
			complete = true
		}
		return events, e
	}
	mediaType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if (operation == "audio.transcribe" || operation == "audio.translate") && strings.HasPrefix(strings.ToLower(mediaType), "text/") {
		bodyBytes, e := readNativeLimit(reader)
		if e != nil {
			return nil, e
		}
		complete = true
		return string(bodyBytes), nil
	}
	bodyBytes, e := readNativeLimit(reader)
	if e != nil {
		return nil, e
	}
	complete = true
	if len(bytes.TrimSpace(bodyBytes)) == 0 {
		return map[string]any{}, nil
	}
	var decoded any
	if e = json.Unmarshal(bodyBytes, &decoded); e != nil {
		return nil, fmt.Errorf("decode upstream JSON: %w", e)
	}
	return decoded, nil
}

func (a *app) sendGeminiFile(ctx context.Context, p provider, operation string, spec nativeRequestSpec, saveResponse *bool) (result any, err error) {
	logEntry, err := a.startNativeLog(p, operation, saveResponse)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, logEntry.finishContext(err)) }()
	client, err := httpclient.New(httpclient.Config{ProxyURL: p.ProxyURL, Timeout: 10 * time.Minute, MaxResponseBytes: maxNativeResponseBytes})
	if err != nil {
		return nil, err
	}
	defer client.CloseIdleConnections()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	upload := spec.uploads["file"][0]
	size := int64(-1)
	if len(spec.preview.Files) == 1 {
		size = spec.preview.Files[0].Size
	}
	if size < 0 {
		return nil, fmt.Errorf("Gemini resumable upload requires a seekable file")
	}
	metadataPath, err := writeNativeTemp(spec.jsonBody)
	if err != nil {
		return nil, err
	}
	defer os.Remove(metadataPath)
	metadata, err := os.Open(metadataPath)
	if err != nil {
		return nil, err
	}
	defer metadata.Close()
	startReq, err := http.NewRequestWithContext(ctx, http.MethodPost, spec.preview.URL, metadata)
	if err != nil {
		return nil, err
	}
	startReq.Header = spec.headers.Clone()
	startReq.Header.Set("Content-Type", "application/json")
	startReq.Header.Set("X-Goog-Upload-Protocol", "resumable")
	startReq.Header.Set("X-Goog-Upload-Command", "start")
	startReq.Header.Set("X-Goog-Upload-Header-Content-Length", strconv.FormatInt(size, 10))
	startReq.ContentLength = int64(len(spec.jsonBody))
	contentType := upload.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	startReq.Header.Set("X-Goog-Upload-Header-Content-Type", contentType)
	startResponse, startBody, stageErr := runNativeStage(client, logEntry, startReq, metadataPath)
	if stageErr != nil {
		return nil, stageErr
	}
	if startResponse.StatusCode < 200 || startResponse.StatusCode >= 300 {
		return nil, &nativeHTTPError{Status: startResponse.StatusCode, Body: startBody}
	}
	uploadURL := startResponse.Header.Get("X-Goog-Upload-URL")
	if uploadURL == "" {
		return nil, fmt.Errorf("Gemini upload start response omitted X-Goog-Upload-URL")
	}
	parsedUpload, parseErr := url.Parse(uploadURL)
	base, _ := url.Parse(p.BaseURL)
	if parseErr != nil || parsedUpload.Scheme != base.Scheme || parsedUpload.Host != base.Host || parsedUpload.User != nil || parsedUpload.Fragment != "" {
		return nil, fmt.Errorf("Gemini returned an invalid cross-origin upload URL")
	}
	requestDir, mkdirErr := os.MkdirTemp(logEntry.dir, time.Now().UTC().Format("20060102T150405.000000000")+"-")
	if mkdirErr != nil {
		return nil, mkdirErr
	}
	logEntry.request = requestDir
	logEntry.requestBytes = 0
	logEntry.requestComplete = false
	if err = rewindUploads(spec.uploads); err != nil {
		return nil, err
	}
	uploadPath, err := copyNativeTemp(upload.Reader)
	if err != nil {
		return nil, err
	}
	defer os.Remove(uploadPath)
	uploadBody, err := os.Open(uploadPath)
	if err != nil {
		return nil, err
	}
	defer uploadBody.Close()
	uploadReq, err := http.NewRequestWithContext(ctx, http.MethodPost, parsedUpload.String(), uploadBody)
	if err != nil {
		return nil, err
	}
	uploadReq.Header = spec.headers.Clone()
	uploadReq.Header.Set("Content-Type", contentType)
	uploadReq.Header.Set("X-Goog-Upload-Offset", "0")
	uploadReq.Header.Set("X-Goog-Upload-Command", "upload, finalize")
	uploadReq.ContentLength = size
	finalResponse, finalBody, stageErr := runNativeStage(client, logEntry, uploadReq, uploadPath)
	if stageErr != nil {
		return nil, stageErr
	}
	if finalResponse.StatusCode < 200 || finalResponse.StatusCode >= 300 {
		return nil, &nativeHTTPError{Status: finalResponse.StatusCode, Body: finalBody}
	}
	if len(bytes.TrimSpace(finalBody)) == 0 {
		return map[string]any{}, nil
	}
	if err = json.Unmarshal(finalBody, &result); err != nil {
		return nil, fmt.Errorf("decode upstream JSON: %w", err)
	}
	return result, nil
}

func writeNativeTemp(data []byte) (string, error) {
	f, err := os.CreateTemp("", "mygo-workbench-native-stage-*")
	if err != nil {
		return "", err
	}
	name := f.Name()
	if _, err = f.Write(data); err == nil {
		err = f.Chmod(0600)
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		_ = os.Remove(name)
		return "", err
	}
	return name, nil
}
func copyNativeTemp(reader io.Reader) (string, error) {
	f, err := os.CreateTemp("", "mygo-workbench-native-upload-*")
	if err != nil {
		return "", err
	}
	name := f.Name()
	_, copyErr := io.Copy(f, reader)
	err = errors.Join(copyErr, f.Chmod(0600), f.Close())
	if err != nil {
		_ = os.Remove(name)
		return "", err
	}
	return name, nil
}

func runNativeStage(client *httpclient.Client, logEntry *nativeLog, req *http.Request, bodyPath string) (res *http.Response, data []byte, err error) {
	started := time.Now()
	if err = logEntry.begin(req, bodyPath); err != nil {
		return nil, nil, err
	}
	trackedBody := trackNativeBody(req)
	res, err = client.Do(req)
	sent, requestComplete, trackErr := trackedBody.finish()
	logEntry.requestBytes = sent
	logEntry.requestComplete = requestComplete
	err = errors.Join(err, trackErr)
	if err != nil {
		err = errors.Join(err, logEntry.finishRequest(nil, started, 0, false, err))
		return nil, nil, err
	}
	defer func() { err = errors.Join(err, res.Body.Close()) }()
	var reader io.Reader = res.Body
	var responseLog *os.File
	if logEntry.saveBody {
		responseLog, err = os.OpenFile(filepath.Join(logEntry.request, "response.body"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return res, nil, err
		}
		defer func() { err = errors.Join(err, responseLog.Close()) }()
		reader = io.TeeReader(reader, responseLog)
	}
	data, readErr := readNativeLimit(reader)
	finishErr := logEntry.finishRequest(res, started, int64(len(data)), readErr == nil, readErr)
	return res, data, errors.Join(readErr, finishErr)
}

func readNativeLimit(reader io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxNativeResponseBytes+1))
	if int64(len(data)) > maxNativeResponseBytes {
		return data[:maxNativeResponseBytes], fmt.Errorf("native response exceeds %d bytes", maxNativeResponseBytes)
	}
	return data, err
}

func writeNativeDownload(reader io.Reader, name, contentType string) (resourceDownload, error) {
	f, err := os.CreateTemp("", "mygo-workbench-native-download-*")
	if err != nil {
		return resourceDownload{}, err
	}
	pathName := f.Name()
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = os.Remove(pathName)
		}
	}()
	written, err := io.Copy(f, io.LimitReader(reader, maxNativeResponseBytes+1))
	if err == nil && written > maxNativeResponseBytes {
		err = fmt.Errorf("native download exceeds %d bytes", maxNativeResponseBytes)
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return resourceDownload{}, err
	}
	if filepath.Base(name) != name || name == "" || name == "." {
		name = "download.bin"
	}
	ok = true
	return resourceDownload{Path: pathName, Name: name, ContentType: contentType}, nil
}

func collectNativeSSE(ctx context.Context, reader io.Reader, kind, operation string) (map[string]any, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64<<10), int(maxNativeResponseBytes))
	events := []resourceEvent{}
	var eventType, eventID string
	var data []string
	var total int64
	terminal := false
	flush := func() error {
		if len(data) == 0 && eventType == "" && eventID == "" {
			return nil
		}
		raw := []byte(strings.Join(data, "\n"))
		total += int64(len(raw) + len(eventType) + len(eventID))
		if total > maxNativeResponseBytes {
			return fmt.Errorf("native stream exceeds %d bytes", maxNativeResponseBytes)
		}
		event := resourceEvent{Type: eventType, ID: eventID, Data: resourceEventData(raw)}
		events = append(events, event)
		if sink, ok := ctx.Value(nativeEventSinkContextKey{}).(func(resourceEvent) error); ok && sink != nil {
			if err := sink(event); err != nil {
				return fmt.Errorf("native event sink: %w", err)
			}
		}
		if nativeSSETerminal(kind, operation, eventType, raw) {
			terminal = true
		}
		if message := nativeSSEError(eventType, raw); message != "" {
			return errors.New(message)
		}
		eventType, eventID, data = "", "", nil
		return nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				return map[string]any{"events": events, "partial": true}, err
			}
			continue
		}
		if strings.HasPrefix(line, "event:") {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		}
		if strings.HasPrefix(line, "id:") {
			eventID = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
		}
		if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := scanner.Err(); err != nil {
		return map[string]any{"events": events, "partial": true}, err
	}
	if err := flush(); err != nil {
		return map[string]any{"events": events, "partial": true}, err
	}
	if !terminal {
		return map[string]any{"events": events, "partial": true}, fmt.Errorf("native SSE ended before %s", nativeSSETerminalName(kind, operation))
	}
	return map[string]any{"events": events, "partial": false}, nil
}

func nativeSSETerminal(kind, operation, eventType string, raw []byte) bool {
	if operation == "responses.create" {
		return eventType == "response.completed" || nativeJSONType(raw) == "response.completed"
	}
	if operation == "chat.create" {
		if strings.TrimSpace(string(raw)) == "[DONE]" {
			return true
		}
		var value struct {
			Choices []struct {
				FinishReason any `json:"finish_reason"`
			} `json:"choices"`
		}
		if json.Unmarshal(raw, &value) == nil {
			for _, choice := range value.Choices {
				if choice.FinishReason != nil && choice.FinishReason != "" {
					return true
				}
			}
		}
		return false
	}
	if kind == "anthropic" && operation == "messages.create" {
		return eventType == "message_stop" || nativeJSONType(raw) == "message_stop"
	}
	if kind == "gemini" && (operation == "content.generate" || operation == "images.generate") {
		var value struct {
			Candidates []struct {
				FinishReason string `json:"finishReason"`
			} `json:"candidates"`
		}
		if json.Unmarshal(raw, &value) == nil {
			for _, candidate := range value.Candidates {
				if candidate.FinishReason != "" {
					return true
				}
			}
		}
	}
	return false
}

func nativeSSEError(eventType string, raw []byte) string {
	typ := nativeJSONType(raw)
	var value map[string]any
	_ = json.Unmarshal(raw, &value)
	errorValue, hasError := value["error"]
	hasError = hasError && errorValue != nil
	if response, ok := value["response"].(map[string]any); ok {
		responseError, responseHasError := response["error"]
		responseHasError = responseHasError && responseError != nil
		hasError = hasError || responseHasError
	}
	if eventType != "error" && eventType != "response.failed" && eventType != "response.incomplete" && typ != "error" && typ != "response.failed" && typ != "response.incomplete" && !hasError {
		return ""
	}
	if nested, ok := value["error"].(map[string]any); ok {
		if message, ok := nested["message"].(string); ok && message != "" {
			return "native SSE error: " + message
		}
	}
	if response, ok := value["response"].(map[string]any); ok {
		if nested, ok := response["error"].(map[string]any); ok {
			if message, ok := nested["message"].(string); ok && message != "" {
				return "native SSE error: " + message
			}
		}
	}
	if message, ok := value["message"].(string); ok && message != "" {
		return "native SSE error: " + message
	}
	if typ != "" {
		return "native SSE error: " + typ
	}
	return "native SSE error event"
}

func nativeJSONType(raw []byte) string {
	var value struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(raw, &value)
	return value.Type
}

func nativeSSETerminalName(kind, operation string) string {
	switch {
	case operation == "responses.create":
		return "response.completed"
	case operation == "chat.create":
		return "[DONE] or a finish_reason"
	case kind == "anthropic" && operation == "messages.create":
		return "message_stop"
	case kind == "gemini" && (operation == "content.generate" || operation == "images.generate"):
		return "a candidate finishReason"
	default:
		return "a terminal event"
	}
}
