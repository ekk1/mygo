package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// CreateBatchParams is the native JSON body for POST /batches.
type CreateBatchParams struct {
	InputFileID        string            `json:"input_file_id"`
	Endpoint           string            `json:"endpoint"`
	CompletionWindow   string            `json:"completion_window"`
	Metadata           map[string]string `json:"metadata,omitempty"`
	OutputExpiresAfter *FileExpiration   `json:"output_expires_after,omitempty"`
	Extra              map[string]any    `json:"-"`
}

// MarshalJSON adds forward-compatible native fields and rejects collisions
// with typed fields that are present.
func (p CreateBatchParams) MarshalJSON() ([]byte, error) {
	type alias CreateBatchParams
	return marshalFields(alias(p), p.Extra)
}

// ListBatchesParams controls cursor pagination for batches.
type ListBatchesParams struct {
	After *string
	Limit *int
}

// BatchErrorList contains validation errors associated with batch input lines.
type BatchErrorList struct {
	Object string       `json:"object,omitempty"`
	Data   []BatchError `json:"data,omitempty"`
}

// BatchError describes one batch validation error.
type BatchError struct {
	Code    string  `json:"code,omitempty"`
	Line    *int    `json:"line,omitempty"`
	Message string  `json:"message,omitempty"`
	Param   *string `json:"param,omitempty"`
}

// BatchRequestCounts reports input request outcomes.
type BatchRequestCounts struct {
	Total     int `json:"total"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
}

// BatchUsage is aggregate token usage returned for recent batches. Detail
// objects remain raw to preserve endpoint-specific and future fields.
type BatchUsage struct {
	InputTokens         int             `json:"input_tokens"`
	InputTokensDetails  json.RawMessage `json:"input_tokens_details,omitempty"`
	OutputTokens        int             `json:"output_tokens"`
	OutputTokensDetails json.RawMessage `json:"output_tokens_details,omitempty"`
	TotalTokens         int             `json:"total_tokens"`
}

// Batch is an asynchronous OpenAI batch job.
type Batch struct {
	ID               string              `json:"id"`
	Object           string              `json:"object"`
	InputFileID      string              `json:"input_file_id"`
	Endpoint         string              `json:"endpoint"`
	CompletionWindow string              `json:"completion_window"`
	Status           string              `json:"status"`
	CreatedAt        int64               `json:"created_at"`
	Errors           *BatchErrorList     `json:"errors,omitempty"`
	OutputFileID     *string             `json:"output_file_id,omitempty"`
	ErrorFileID      *string             `json:"error_file_id,omitempty"`
	InProgressAt     *int64              `json:"in_progress_at,omitempty"`
	ExpiresAt        *int64              `json:"expires_at,omitempty"`
	FinalizingAt     *int64              `json:"finalizing_at,omitempty"`
	CompletedAt      *int64              `json:"completed_at,omitempty"`
	FailedAt         *int64              `json:"failed_at,omitempty"`
	ExpiredAt        *int64              `json:"expired_at,omitempty"`
	CancellingAt     *int64              `json:"cancelling_at,omitempty"`
	CancelledAt      *int64              `json:"cancelled_at,omitempty"`
	RequestCounts    *BatchRequestCounts `json:"request_counts,omitempty"`
	Metadata         map[string]string   `json:"metadata,omitempty"`
	Model            string              `json:"model,omitempty"`
	Usage            *BatchUsage         `json:"usage,omitempty"`
	HTTP             *HTTPResponse       `json:"-"`
}

// BatchList is one page returned by ListBatches.
type BatchList struct {
	Object  string        `json:"object"`
	Data    []Batch       `json:"data"`
	FirstID string        `json:"first_id"`
	LastID  string        `json:"last_id"`
	HasMore bool          `json:"has_more"`
	HTTP    *HTTPResponse `json:"-"`
}

// CreateBatch submits a batch job and returns immediately with its job state.
func (c *Client) CreateBatch(ctx context.Context, params CreateBatchParams) (*Batch, error) {
	result := new(Batch)
	httpResponse, err := c.json(ctx, http.MethodPost, "/batches", params, result)
	result.HTTP = httpResponse
	return result, err
}

// ListBatches returns one native page and does not follow cursors.
func (c *Client) ListBatches(ctx context.Context, params ListBatchesParams) (*BatchList, error) {
	query := url.Values{}
	addStringQuery(query, "after", params.After)
	addIntQuery(query, "limit", params.Limit)
	result := new(BatchList)
	httpResponse, err := c.json(ctx, http.MethodGet, withQuery("/batches", query), nil, result)
	result.HTTP = httpResponse
	return result, err
}

// GetBatch retrieves one batch job.
func (c *Client) GetBatch(ctx context.Context, batchID string) (*Batch, error) {
	id, err := pathID(batchID)
	result := new(Batch)
	if err != nil {
		return result, fmt.Errorf("openai: batch ID: %w", err)
	}
	httpResponse, err := c.json(ctx, http.MethodGet, "/batches/"+id, nil, result)
	result.HTTP = httpResponse
	return result, err
}

// CancelBatch requests cancellation of an in-progress batch.
func (c *Client) CancelBatch(ctx context.Context, batchID string) (*Batch, error) {
	id, err := pathID(batchID)
	result := new(Batch)
	if err != nil {
		return result, fmt.Errorf("openai: batch ID: %w", err)
	}
	httpResponse, err := c.json(ctx, http.MethodPost, "/batches/"+id+"/cancel", nil, result)
	result.HTTP = httpResponse
	return result, err
}
