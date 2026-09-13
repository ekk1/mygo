package main

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ekk1/mygo/utils/assetstore"
	"github.com/ekk1/mygo/utils/httpserver"
)

func (a *app) registerAssets(s *httpserver.Server) error {
	for pattern, handler := range map[string]http.HandlerFunc{
		"GET /api/assets":                  a.assetsAPI,
		"POST /api/assets":                 a.assetsAPI,
		"POST /api/assets/import-sessions": a.importSessionAssets,
		"GET /api/assets/{id}":             a.assetAPI,
		"PATCH /api/assets/{id}":           a.assetAPI,
		"DELETE /api/assets/{id}":          a.assetAPI,
		"GET /api/assets/{id}/content":     a.assetContentAPI,
	} {
		if err := s.HandleFunc(pattern, handler); err != nil {
			return err
		}
	}
	return nil
}

// Import is explicit and local: it never fetches old remote URLs or dispatches
// a generation. A message's saved IDs make repeated imports idempotent.
func (a *app) importSessionAssets(w http.ResponseWriter, r *http.Request) {
	imported := []assetstore.Asset{}
	for _, summary := range a.store.listSessions() {
		entry, err := a.store.entry(summary.ID)
		if err != nil {
			continue
		}
		entry.mu.Lock()
		if entry.busy || entry.deleted {
			entry.mu.Unlock()
			continue
		}
		v := clone(entry.data)
		p := provider{ID: v.ProfileID}
		var added []string
		for i := range v.Messages {
			m := &v.Messages[i]
			if len(m.AssetIDs) != 0 || len(m.Output) == 0 {
				continue
			}
			var ids []string
			ids, err = a.captureAssetResult(r.Context(), p, v.Operation, m.TaskID, v.ID, m.Output, false)
			added = append(added, ids...)
			if err != nil {
				break
			}
			m.AssetIDs = ids
		}
		if err == nil && len(added) > 0 {
			err = a.store.persistSession(entry, v)
		}
		entry.mu.Unlock()
		if err != nil {
			for _, id := range added {
				err = errors.Join(err, a.assets.Delete(id))
			}
			apiError(w, 500, fmt.Errorf("导入未完成，之前成功导入的会话已保留，请刷新资产列表: %w", err))
			return
		}
		for _, id := range added {
			if item, getErr := a.assets.Get(id); getErr == nil {
				imported = append(imported, item)
			}
		}
	}
	writeJSON(w, 200, imported)
}

func assetErrorStatus(err error) int {
	if errors.Is(err, assetstore.ErrNotFound) {
		return http.StatusNotFound
	}
	if errors.Is(err, assetstore.ErrTooLarge) {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusBadRequest
}

func (a *app) assetsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, a.assets.List(r.URL.Query().Get("favorite") == "1"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 300<<20)
	multipart, err := r.MultipartReader()
	if err != nil {
		apiError(w, 400, fmt.Errorf("multipart file upload is required"))
		return
	}
	items := []assetstore.Asset{}
	// Roll back the request's assets if a later part fails.
	committed := false
	defer func() {
		if !committed {
			for _, item := range items {
				_ = a.assets.Delete(item.ID)
			}
		}
	}()
	for {
		part, nextErr := multipart.NextPart()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			apiError(w, 400, nextErr)
			return
		}
		if part.FileName() == "" {
			part.Close()
			continue
		}
		if len(items) >= 100 {
			part.Close()
			apiError(w, 400, fmt.Errorf("at most 100 files may be uploaded together"))
			return
		}
		item, addErr := a.assets.Add(part.FileName(), part.Header.Get("Content-Type"), assetstore.Source{"kind": "upload"}, part, 0)
		part.Close()
		if addErr != nil {
			apiError(w, assetErrorStatus(addErr), addErr)
			return
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		apiError(w, 400, fmt.Errorf("at least one file is required"))
		return
	}
	committed = true
	writeJSON(w, http.StatusCreated, items)
}

func (a *app) assetAPI(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if r.Method == http.MethodDelete {
		a.mediaMu.Lock()
		defer a.mediaMu.Unlock()
		if err := a.assets.Delete(id); err != nil {
			apiError(w, assetErrorStatus(err), err)
			return
		}
		if err := os.Remove(filepath.Join(a.store.dir, "positions", id+".json")); err != nil && !errors.Is(err, os.ErrNotExist) {
			apiError(w, 500, fmt.Errorf("资产已删除，但播放位置清理失败: %w", err))
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
		return
	}
	if r.Method == http.MethodPatch {
		var in struct {
			Name     *string `json:"name"`
			Favorite *bool   `json:"favorite"`
		}
		if err := decodeJSON(w, r, &in); err != nil {
			apiError(w, 400, err)
			return
		}
		item, err := a.assets.Update(id, in.Name, in.Favorite)
		if err != nil {
			apiError(w, assetErrorStatus(err), err)
			return
		}
		writeJSON(w, 200, item)
		return
	}
	item, err := a.assets.Get(id)
	if err != nil {
		apiError(w, assetErrorStatus(err), err)
		return
	}
	writeJSON(w, 200, item)
}

func safeAssetInline(contentType string) bool {
	typ, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	switch strings.ToLower(typ) {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/avif", "image/bmp", "image/x-icon", "audio/mpeg", "audio/mp3", "audio/mp4", "audio/aac", "audio/ogg", "audio/wav", "audio/wave", "audio/x-wav", "audio/webm", "audio/flac", "audio/x-flac", "video/mp4", "video/webm", "video/ogg", "video/quicktime":
		return true
	}
	return false
}

func (a *app) assetContentAPI(w http.ResponseWriter, r *http.Request) {
	item, file, err := a.assets.OpenContent(r.PathValue("id"))
	if err != nil {
		apiError(w, assetErrorStatus(err), err)
		return
	}
	defer file.Close()
	disposition := "attachment"
	if r.URL.Query().Get("download") != "1" && safeAssetInline(item.ContentType) {
		disposition = "inline"
	}
	w.Header().Set("Content-Type", item.ContentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": item.Name}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	created, _ := time.Parse(time.RFC3339Nano, item.CreatedAt)
	http.ServeContent(w, r, item.Name, created, file)
}
