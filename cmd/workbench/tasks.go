package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var errTaskCancelled = errors.New("已停止生成")
var errTaskInterrupted = errors.New("生成因服务退出而中断；未自动重试")

type generationTask struct {
	ID            string          `json:"id"`
	ProviderID    string          `json:"provider_id"`
	Vendor        string          `json:"vendor"`
	Operation     string          `json:"operation"`
	Feature       string          `json:"feature"`
	Title         string          `json:"title"`
	Status        string          `json:"status"`
	CreatedAt     string          `json:"created_at"`
	UpdatedAt     string          `json:"updated_at"`
	SessionID     string          `json:"session_id,omitempty"`
	AssetIDs      []string        `json:"asset_ids,omitempty"`
	InputAssetIDs []string        `json:"input_asset_ids,omitempty"`
	Result        json.RawMessage `json:"result,omitempty"`
	Error         string          `json:"error,omitempty"`
}

type taskEntry struct {
	mu     sync.Mutex
	data   generationTask
	cancel context.CancelCauseFunc
	done   chan struct{}
	stream *taskStream
}

// Only the original response subscribes. A slow consumer loses its live view,
// while the worker continues collecting the full native result independently.
type taskStream struct {
	events   chan resourceEvent
	detached chan struct{}
	once     sync.Once
}

func (s *taskStream) publish(event resourceEvent) error {
	select {
	case <-s.detached:
		return nil
	default:
	}
	select {
	case s.events <- event:
	default:
		s.once.Do(func() { close(s.detached) })
	}
	return nil
}

func taskActive(status string) bool { return status == "queued" || status == "running" }

func (a *app) loadTasks() error {
	dir := filepath.Join(a.store.dir, "tasks")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	a.tasks = make(map[string]*taskEntry)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		var v generationTask
		if err := loadKV(filepath.Join(dir, entry.Name()), &v); err != nil {
			return fmt.Errorf("load task %s: %w", entry.Name(), err)
		}
		if !validID.MatchString(v.ID) || entry.Name() != v.ID+".json" {
			return fmt.Errorf("invalid task file %s", entry.Name())
		}
		if taskActive(v.Status) {
			v.Status, v.Error, v.UpdatedAt = "interrupted", errTaskInterrupted.Error(), now()
			if err := saveKV(filepath.Join(dir, entry.Name()), v); err != nil {
				return err
			}
		}
		done := make(chan struct{})
		close(done)
		a.tasks[v.ID] = &taskEntry{data: v, done: done}
	}
	return nil
}

// Task registration and WaitGroup.Add share the shutdown gate with requests.
// A persisted reservation always gets a worker, including when shutdown races.
func (a *app) reserveTask(p provider, operation, feature, title, sessionID string, stream bool) (*taskEntry, context.Context, error) {
	a.activeMu.Lock()
	defer a.activeMu.Unlock()
	if a.closing {
		return nil, nil, errTaskInterrupted
	}
	if title = strings.TrimSpace(title); title == "" {
		title = operation
	}
	if text := []rune(title); len(text) > 120 {
		title = string(text[:120])
	}
	v := generationTask{ID: newID(), ProviderID: p.ID, Vendor: p.Kind, Operation: operation, Feature: feature, Title: title, Status: "queued", CreatedAt: now(), UpdatedAt: now(), SessionID: sessionID}
	if err := saveKV(filepath.Join(a.store.dir, "tasks", v.ID+".json"), v); err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	task := &taskEntry{data: v, cancel: cancel, done: make(chan struct{})}
	if stream {
		task.stream = &taskStream{events: make(chan resourceEvent, 64), detached: make(chan struct{})}
	}
	a.tasksMu.Lock()
	a.tasks[v.ID] = task
	a.tasksMu.Unlock()
	a.active.Add(1)
	return task, ctx, nil
}

func (t *taskEntry) snapshot(summary bool) generationTask {
	t.mu.Lock()
	defer t.mu.Unlock()
	v := t.data
	if summary {
		v.Result = nil
	}
	return clone(v)
}

func (a *app) task(id string) (*taskEntry, error) {
	a.tasksMu.RLock()
	defer a.tasksMu.RUnlock()
	t, ok := a.tasks[id]
	if !ok {
		return nil, errNotFound
	}
	return t, nil
}

func (a *app) tasksAPI(w http.ResponseWriter, r *http.Request) {
	a.tasksMu.RLock()
	entries := make([]*taskEntry, 0, len(a.tasks))
	for _, t := range a.tasks {
		entries = append(entries, t)
	}
	a.tasksMu.RUnlock()
	items := make([]generationTask, 0, len(entries))
	for _, entry := range entries {
		v := entry.snapshot(true)
		if id := r.URL.Query().Get("provider_id"); id != "" && v.ProviderID != id {
			continue
		}
		if feature := r.URL.Query().Get("feature"); feature != "" && v.Feature != feature {
			continue
		}
		items = append(items, v)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt > items[j].CreatedAt })
	writeJSON(w, http.StatusOK, items)
}

func (a *app) taskAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method == "DELETE" {
		if err := a.deleteTask(r.PathValue("id")); err != nil {
			apiError(w, errorStatus(err), err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	t, err := a.task(r.PathValue("id"))
	if err != nil {
		apiError(w, errorStatus(err), err)
		return
	}
	if r.Method == "POST" {
		t.mu.Lock()
		if taskActive(t.data.Status) && t.cancel != nil {
			t.cancel(errTaskCancelled)
		}
		t.mu.Unlock()
	}
	writeJSON(w, http.StatusOK, t.snapshot(false))
}

func (a *app) deleteTask(id string) error {
	a.tasksMu.Lock()
	defer a.tasksMu.Unlock()
	t, ok := a.tasks[id]
	if !ok {
		return errNotFound
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if taskActive(t.data.Status) {
		return errBusy
	}
	if err := os.Remove(filepath.Join(a.store.dir, "tasks", id+".json")); err != nil {
		return err
	}
	delete(a.tasks, id)
	return nil
}

type nativeGeneration struct {
	startupErr    error
	provider      provider
	operation     string
	params        json.RawMessage
	uploads       resourceUploads
	saveResponse  *bool
	stream        bool
	session       *sessionEntry
	assistantID   string
	cleanup       func()
	inputAssetIDs []string
}

func taskResultStatus(ctx context.Context, err error) (string, error) {
	if cause := context.Cause(ctx); cause != nil {
		status := "cancelled"
		if errors.Is(cause, errTaskInterrupted) {
			status = "interrupted"
		}
		return status, errors.Join(cause, err)
	}
	if err != nil {
		return "error", err
	}
	return "complete", nil
}

func (a *app) runGeneration(t *taskEntry, ctx context.Context, job nativeGeneration) {
	defer a.active.Done()
	defer close(t.done)
	defer t.cancel(nil)
	if job.cleanup != nil {
		defer job.cleanup()
	}
	if t.stream != nil {
		ctx = withNativeEventSink(ctx, t.stream.publish)
	}
	t.mu.Lock()
	v := t.data
	v.Status, v.UpdatedAt, v.InputAssetIDs = "running", now(), job.inputAssetIDs
	callErr := errors.Join(job.startupErr, saveKV(filepath.Join(a.store.dir, "tasks", v.ID+".json"), v))
	if callErr == nil {
		t.data = v
	}
	t.mu.Unlock()
	var result any
	if callErr == nil && ctx.Err() == nil {
		ctx = context.WithValue(ctx, usageContextKey{}, usageOrigin{SessionID: v.SessionID, MessageID: job.assistantID, TaskID: v.ID})
		result, callErr = a.executeNative(ctx, job.provider, job.operation, job.params, job.uploads, job.saveResponse)
	}
	if job.stream {
		result = reduceConversationStream(job.provider.Kind, job.operation, result)
	}
	if download, ok := result.(resourceDownload); ok {
		defer os.Remove(download.Path)
	}
	assetIDs, assetErr := a.captureAssets(ctx, job.provider, job.operation, v.ID, v.SessionID, result)
	if callErr == nil && assetErr != nil {
		assetErr = fmt.Errorf("上游请求已完成，但本地资产保存失败（请勿直接重新生成）: %w", assetErr)
	}
	callErr = errors.Join(callErr, assetErr)
	if download, ok := result.(resourceDownload); ok {
		// The durable asset owns the bytes; temporary transport paths are private.
		result = map[string]any{"name": download.Name, "content_type": download.ContentType, "asset_ids": assetIDs}
	}
	// Serializing cancellation with final persistence makes Stop unambiguous:
	// either it cancels a running task or observes an already completed result.
	t.mu.Lock()
	defer t.mu.Unlock()
	status, callErr := taskResultStatus(ctx, callErr)
	if job.session != nil {
		_, persistErr := a.finishConversation(job.session, job.assistantID, result, assetIDs, status, callErr)
		if persistErr != nil {
			callErr = errors.Join(callErr, persistErr)
			status = "error"
		}
	}
	v = t.data
	v.Status, v.UpdatedAt, v.AssetIDs = status, now(), assetIDs
	encoded, encodeErr := json.Marshal(result)
	callErr = errors.Join(callErr, encodeErr)
	if encodeErr != nil {
		v.Status = "error"
	} else {
		v.Result = encoded
	}
	if callErr != nil {
		v.Error = callErr.Error()
	}
	if err := saveKV(filepath.Join(a.store.dir, "tasks", v.ID+".json"), v); err != nil {
		// Never publish success when its durable record could not be saved.
		v.Status, v.Error = "error", errors.Join(callErr, fmt.Errorf("保存任务结果失败: %w", err)).Error()
		fmt.Fprintln(os.Stderr, v.Error)
	}
	t.data = v
}

func (a *app) respondTask(w http.ResponseWriter, r *http.Request, t *taskEntry, initial *session) {
	started := map[string]any{"task": t.snapshot(true)}
	if initial != nil {
		started["session"] = initial
	}
	if t.stream == nil {
		writeJSON(w, http.StatusAccepted, started)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	write := func(value any) error {
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(5 * time.Second))
		return writeConversationEvent(w, value)
	}
	defer t.stream.once.Do(func() { close(t.stream.detached) })
	started["type"] = "started"
	if write(started) != nil {
		return
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case <-t.stream.detached:
			_ = write(map[string]any{"type": "error", "error": "实时显示已停止，任务仍在后台运行，请刷新状态", "task": t.snapshot(true)})
			return
		case event := <-t.stream.events:
			if write(map[string]any{"type": "event", "event": event}) != nil {
				return
			}
		case <-t.done:
			for len(t.stream.events) > 0 {
				if write(map[string]any{"type": "event", "event": <-t.stream.events}) != nil {
					return
				}
			}
			final := t.snapshot(true)
			frame := map[string]any{"type": "done", "task": final}
			if final.SessionID != "" {
				if v, err := a.store.getSession(final.SessionID); err == nil {
					frame["session"] = v
				}
			}
			_ = write(frame)
			return
		}
	}
}

// HTTP multipart cleanup also runs after the handler returns. Independent
// private copies keep task uploads readable throughout their actual lifetime.
func snapshotTaskUploads(uploads resourceUploads) (resourceUploads, func(), error) {
	copy := make(resourceUploads, len(uploads))
	var files []*os.File
	cleanup := func() {
		for _, file := range files {
			_ = file.Close()
			_ = os.Remove(file.Name())
		}
	}
	for field, items := range uploads {
		for _, item := range items {
			file, err := os.CreateTemp("", "workbench-task-upload-*")
			if err != nil {
				cleanup()
				return nil, func() {}, err
			}
			files = append(files, file)
			if _, err = io.Copy(file, item.Reader); err == nil {
				_, err = file.Seek(0, io.SeekStart)
			}
			if err != nil {
				cleanup()
				return nil, func() {}, err
			}
			item.Reader = file
			copy[field] = append(copy[field], item)
		}
	}
	return copy, cleanup, nil
}

func flattenAssetIDs(fields map[string][]string) []string {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var ids []string
	for _, key := range keys {
		ids = append(ids, fields[key]...)
	}
	return ids
}
