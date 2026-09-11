package openai

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

// FileExpiration is an OpenAI file expiration policy. Files use the
// created_at anchor and a lifetime in seconds.
type FileExpiration struct {
	Anchor  string `json:"anchor"`
	Seconds int64  `json:"seconds"`
}

// UploadFileParams contains the multipart fields accepted by POST /files.
type UploadFileParams struct {
	File         Upload
	Purpose      string
	ExpiresAfter *FileExpiration
}

// ListFilesParams controls filtering, ordering, and cursor pagination for files.
type ListFilesParams struct {
	After   *string
	Limit   *int
	Order   *string
	Purpose *string
}

// File is an uploaded OpenAI file. Deprecated status fields remain available
// because the API can still return them.
type File struct {
	ID            string        `json:"id"`
	Object        string        `json:"object"`
	Bytes         int64         `json:"bytes"`
	CreatedAt     int64         `json:"created_at"`
	ExpiresAt     *int64        `json:"expires_at,omitempty"`
	Filename      string        `json:"filename"`
	Purpose       string        `json:"purpose"`
	Status        string        `json:"status,omitempty"`
	StatusDetails string        `json:"status_details,omitempty"`
	HTTP          *HTTPResponse `json:"-"`
}

// FileList is one page returned by ListFiles.
type FileList struct {
	Object  string        `json:"object"`
	Data    []File        `json:"data"`
	FirstID string        `json:"first_id"`
	LastID  string        `json:"last_id"`
	HasMore bool          `json:"has_more"`
	HTTP    *HTTPResponse `json:"-"`
}

// FileDeletion is the deletion status returned by DeleteFile.
type FileDeletion struct {
	ID      string        `json:"id"`
	Object  string        `json:"object"`
	Deleted bool          `json:"deleted"`
	HTTP    *HTTPResponse `json:"-"`
}

// UploadFile uploads a file without buffering its full content in this package.
func (c *Client) UploadFile(ctx context.Context, params UploadFileParams) (*File, error) {
	fields := map[string]string{"purpose": params.Purpose}
	if params.ExpiresAfter != nil {
		fields["expires_after[anchor]"] = params.ExpiresAfter.Anchor
		fields["expires_after[seconds]"] = strconv.FormatInt(params.ExpiresAfter.Seconds, 10)
	}
	upload := params.File
	upload.Field = "file"
	result := new(File)
	httpResponse, err := c.multipart(ctx, http.MethodPost, "/files", fields, []Upload{upload}, result)
	result.HTTP = httpResponse
	return result, err
}

// ListFiles returns one native page of files. It does not follow cursors.
func (c *Client) ListFiles(ctx context.Context, params ListFilesParams) (*FileList, error) {
	query := url.Values{}
	addStringQuery(query, "after", params.After)
	addIntQuery(query, "limit", params.Limit)
	addStringQuery(query, "order", params.Order)
	addStringQuery(query, "purpose", params.Purpose)
	result := new(FileList)
	httpResponse, err := c.json(ctx, http.MethodGet, withQuery("/files", query), nil, result)
	result.HTTP = httpResponse
	return result, err
}

// GetFile retrieves metadata for one file.
func (c *Client) GetFile(ctx context.Context, fileID string) (*File, error) {
	id, err := pathID(fileID)
	result := new(File)
	if err != nil {
		return result, fmt.Errorf("openai: file ID: %w", err)
	}
	httpResponse, err := c.json(ctx, http.MethodGet, "/files/"+id, nil, result)
	result.HTTP = httpResponse
	return result, err
}

// DeleteFile deletes one file.
func (c *Client) DeleteFile(ctx context.Context, fileID string) (*FileDeletion, error) {
	id, err := pathID(fileID)
	result := new(FileDeletion)
	if err != nil {
		return result, fmt.Errorf("openai: file ID: %w", err)
	}
	httpResponse, err := c.json(ctx, http.MethodDelete, "/files/"+id, nil, result)
	result.HTTP = httpResponse
	return result, err
}

// DownloadFile streams a file's raw content to dst. Batch output and error
// JSONL files are downloaded with this same method.
func (c *Client) DownloadFile(ctx context.Context, fileID string, dst io.Writer) (*HTTPResponse, error) {
	id, err := pathID(fileID)
	if err != nil {
		return nil, fmt.Errorf("openai: file ID: %w", err)
	}
	return c.download(ctx, "/files/"+id+"/content", dst)
}

func addStringQuery(query url.Values, key string, value *string) {
	if value != nil {
		query.Set(key, *value)
	}
}

func addIntQuery(query url.Values, key string, value *int) {
	if value != nil {
		query.Set(key, strconv.Itoa(*value))
	}
}

func withQuery(path string, query url.Values) string {
	if len(query) == 0 {
		return path
	}
	return path + "?" + query.Encode()
}
