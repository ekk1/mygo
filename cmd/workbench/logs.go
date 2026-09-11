package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type operationLog struct {
	ID           string `json:"id"`
	ProviderID   string `json:"provider_id"`
	Operation    string `json:"operation"`
	StartedAt    string `json:"started_at"`
	FinishedAt   string `json:"finished_at,omitempty"`
	Status       string `json:"status"`
	Error        string `json:"error,omitempty"`
	SaveResponse bool   `json:"save_response"`
}

func writeFileJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".meta-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, e := f.Write(b)
	err = errors.Join(e, f.Close())
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func (a *app) listLogs(w http.ResponseWriter, r *http.Request) {
	dirs, err := os.ReadDir(filepath.Join(a.store.dir, "logs"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		apiError(w, 500, err)
		return
	}
	logs := []operationLog{}
	for _, e := range dirs {
		if !e.IsDir() || !validID.MatchString(e.Name()) {
			continue
		}
		var v operationLog
		b, err := os.ReadFile(filepath.Join(a.store.dir, "logs", e.Name(), "context.json"))
		if err == nil && json.Unmarshal(b, &v) == nil {
			logs = append(logs, v)
		}
	}
	sort.Slice(logs, func(i, j int) bool { return logs[i].StartedAt > logs[j].StartedAt })
	writeJSON(w, 200, logs)
}
func preview(path string) (string, bool, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, (256<<10)+1))
	if err != nil {
		return "", false, err
	}
	truncated := len(b) > 256<<10
	if truncated {
		b = b[:256<<10]
	}
	return string(b), truncated, nil
}
func readMeta(path string) any {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var v any
	if json.Unmarshal(b, &v) != nil {
		return nil
	}
	return v
}
func (a *app) getLog(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validID.MatchString(id) {
		apiError(w, 404, errNotFound)
		return
	}
	dir := filepath.Join(a.store.dir, "logs", id)
	meta := readMeta(filepath.Join(dir, "context.json"))
	if meta == nil {
		apiError(w, 404, errNotFound)
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		apiError(w, 500, err)
		return
	}
	requests := []any{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		child := filepath.Join(dir, e.Name())
		requestBody, rt, err := preview(filepath.Join(child, "request.body"))
		if err != nil {
			apiError(w, 500, err)
			return
		}
		responseBody, st, err := preview(filepath.Join(child, "response.body"))
		if err != nil {
			apiError(w, 500, err)
			return
		}
		requests = append(requests, map[string]any{"id": e.Name(), "request": readMeta(filepath.Join(child, "request.json")), "response": readMeta(filepath.Join(child, "response.json")), "request_body": requestBody, "response_body": responseBody, "request_truncated": rt, "response_truncated": st})
	}
	writeJSON(w, 200, map[string]any{"metadata": meta, "requests": requests})
}
func (a *app) downloadLog(w http.ResponseWriter, r *http.Request) {
	id, req, file := r.PathValue("id"), r.PathValue("request"), r.PathValue("file")
	if !validID.MatchString(id) || req == "" || strings.ContainsAny(req, "/\\") || strings.Contains(req, "..") || (file != "request.body" && file != "response.body") {
		apiError(w, 404, errNotFound)
		return
	}
	path := filepath.Join(a.store.dir, "logs", id, req, file)
	f, err := os.Open(path)
	if err != nil {
		apiError(w, 404, errNotFound)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		apiError(w, 500, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, file))
	http.ServeContent(w, r, file, st.ModTime(), f)
}
