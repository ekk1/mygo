package main

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/ekk1/mygo/utils/httpserver"
)

func (a *app) registerResources(s *httpserver.Server) error {
	if err := s.HandleFunc("GET /api/operations", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, operationCatalog)
	}); err != nil {
		return err
	}
	return s.HandleFunc("POST /api/operations/{operation}", a.runResourceOperation)
}

type resourceRequest struct {
	ProviderID   string          `json:"provider_id"`
	Params       json.RawMessage `json:"params"`
	SaveResponse *bool           `json:"save_response,omitempty"`
}

func knownOperation(id string) bool {
	for _, operation := range operationCatalog {
		if operation.ID == id {
			return true
		}
	}
	return false
}

func parseResourceRequest(w http.ResponseWriter, r *http.Request) (request resourceRequest, uploads resourceUploads, cleanup func(), err error) {
	cleanup = func() {}
	mediaType, _, parseErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if parseErr != nil {
		return request, nil, cleanup, fmt.Errorf("invalid Content-Type: %w", parseErr)
	}
	if mediaType == "application/json" {
		err = decodeJSON(w, r, &request)
		return request, nil, cleanup, err
	}
	if mediaType != "multipart/form-data" {
		return request, nil, cleanup, fmt.Errorf("Content-Type must be application/json or multipart/form-data")
	}
	if err = r.ParseMultipartForm(8 << 20); err != nil {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
		return request, nil, cleanup, fmt.Errorf("invalid multipart request: %w", err)
	}
	cleanup = func() { _ = r.MultipartForm.RemoveAll() }
	request.ProviderID = r.FormValue("provider_id")
	if raw := r.FormValue("params"); raw != "" {
		request.Params = json.RawMessage(raw)
		if !json.Valid(request.Params) {
			return request, nil, cleanup, fmt.Errorf("params must be valid JSON")
		}
	}
	if raw := r.FormValue("save_response"); raw != "" {
		value, parseErr := strconv.ParseBool(raw)
		if parseErr != nil {
			return request, nil, cleanup, fmt.Errorf("save_response must be true or false")
		}
		request.SaveResponse = &value
	}
	uploads = make(resourceUploads)
	var opened []io.Closer
	oldCleanup := cleanup
	cleanup = func() {
		for _, file := range opened {
			_ = file.Close()
		}
		oldCleanup()
	}
	for field, headers := range r.MultipartForm.File {
		for _, header := range headers {
			file, openErr := header.Open()
			if openErr != nil {
				return request, nil, cleanup, fmt.Errorf("open upload %q: %w", field, openErr)
			}
			opened = append(opened, file)
			uploads[field] = append(uploads[field], resourceUpload{Filename: header.Filename, ContentType: header.Header.Get("Content-Type"), Reader: file})
		}
	}
	return request, uploads, cleanup, nil
}

func (a *app) runResourceOperation(w http.ResponseWriter, r *http.Request) {
	operation := r.PathValue("operation")
	if !knownOperation(operation) {
		apiError(w, http.StatusNotFound, fmt.Errorf("unknown operation %q", operation))
		return
	}
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
	client, finish, err := a.providerClient(request.ProviderID, operation, request.SaveResponse)
	if err != nil {
		apiError(w, errorStatus(err), err)
		return
	}
	defer client.CloseIdleConnections()
	result, callErr := dispatchResourceOperation(r.Context(), client, operation, request.Params, uploads)
	err = finish(callErr)
	if err != nil {
		if download, ok := result.(resourceDownload); ok {
			_ = os.Remove(download.Path)
		}
		apiError(w, http.StatusBadGateway, err)
		return
	}
	if download, ok := result.(resourceDownload); ok {
		defer os.Remove(download.Path)
		file, openErr := os.Open(download.Path)
		if openErr != nil {
			apiError(w, http.StatusInternalServerError, openErr)
			return
		}
		defer file.Close()
		info, statErr := file.Stat()
		if statErr != nil {
			apiError(w, http.StatusInternalServerError, statErr)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": download.Name}))
		http.ServeContent(w, r, download.Name, info.ModTime(), file)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
