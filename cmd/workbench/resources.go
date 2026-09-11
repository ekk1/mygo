package main

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
)

type resourceRequest struct {
	ProviderID   string          `json:"provider_id"`
	Params       json.RawMessage `json:"params"`
	SaveResponse *bool           `json:"save_response,omitempty"`
}

func parseResourceRequest(w http.ResponseWriter, r *http.Request) (request resourceRequest, uploads resourceUploads, cleanup func(), err error) {
	cleanup = func() {}
	mediaType, _, parseErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if parseErr != nil {
		return request, nil, cleanup, fmt.Errorf("invalid Content-Type: %w", parseErr)
	}
	if mediaType == "application/json" {
		err = decodeJSONLimit(w, r, &request, 64<<20)
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

type resourceUpload struct {
	Filename, ContentType string
	Reader                io.Reader
}
type resourceUploads map[string][]resourceUpload
type resourceDownload struct{ Path, Name, ContentType string }
type resourceEvent struct {
	Type string `json:"type"`
	ID   string `json:"id,omitempty"`
	Data any    `json:"data"`
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
