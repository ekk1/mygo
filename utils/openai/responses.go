package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

// StreamError reports a malformed or server-declared stream event while
// preserving the exact event supplied to the callback. Unwrap exposes the
// decoding or protocol error through errors.Is/errors.As.
type StreamError struct {
	Event Event
	Err   error
}

// Error implements error.
func (e *StreamError) Error() string {
	if e == nil {
		return "openai: stream error"
	}
	if e.Event.Type != "" {
		return fmt.Sprintf("openai: stream event %q: %v", e.Event.Type, e.Err)
	}
	return fmt.Sprintf("openai: stream event: %v", e.Err)
}

// Unwrap exposes the underlying stream decoding or protocol error.
func (e *StreamError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// ResponseRequest is the native body for POST /responses. Polymorphic input,
// text format, reasoning, tools, and tool choice are kept open to preserve the
// complete native protocol without a closed model or content enum.
type ResponseRequest struct {
	Background           *bool             `json:"background,omitempty"`
	ContextManagement    any               `json:"context_management,omitempty"`
	Conversation         any               `json:"conversation,omitempty"`
	Include              []string          `json:"include,omitempty"`
	Input                any               `json:"input,omitempty"`
	Instructions         any               `json:"instructions,omitempty"`
	MaxOutputTokens      *int              `json:"max_output_tokens,omitempty"`
	MaxToolCalls         *int              `json:"max_tool_calls,omitempty"`
	Metadata             map[string]string `json:"metadata,omitempty"`
	Model                string            `json:"model,omitempty"`
	Moderation           any               `json:"moderation,omitempty"`
	ParallelToolCalls    *bool             `json:"parallel_tool_calls,omitempty"`
	PreviousResponseID   string            `json:"previous_response_id,omitempty"`
	Prompt               any               `json:"prompt,omitempty"`
	PromptCacheKey       string            `json:"prompt_cache_key,omitempty"`
	PromptCacheOptions   any               `json:"prompt_cache_options,omitempty"`
	PromptCacheRetention string            `json:"prompt_cache_retention,omitempty"`
	Reasoning            any               `json:"reasoning,omitempty"`
	SafetyIdentifier     string            `json:"safety_identifier,omitempty"`
	ServiceTier          string            `json:"service_tier,omitempty"`
	Store                *bool             `json:"store,omitempty"`
	Stream               *bool             `json:"stream,omitempty"`
	StreamOptions        any               `json:"stream_options,omitempty"`
	Temperature          *float64          `json:"temperature,omitempty"`
	Text                 any               `json:"text,omitempty"`
	ToolChoice           any               `json:"tool_choice,omitempty"`
	Tools                []any             `json:"tools,omitempty"`
	TopLogprobs          *int              `json:"top_logprobs,omitempty"`
	TopP                 *float64          `json:"top_p,omitempty"`
	Truncation           string            `json:"truncation,omitempty"`
	User                 string            `json:"user,omitempty"`
	Extra                map[string]any    `json:"-"`
}

// MarshalJSON merges Extra into the native request and rejects collisions.
func (r ResponseRequest) MarshalJSON() ([]byte, error) {
	type requestAlias ResponseRequest
	return marshalFields(requestAlias(r), r.Extra)
}

// ResponseInputMessage is the common native message input shape. Content may
// be a string or []ResponseInputContent; callers can use raw native items for
// less common input variants.
type ResponseInputMessage struct {
	Type    string `json:"type,omitempty"`
	Role    string `json:"role"`
	Content any    `json:"content"`
	Status  string `json:"status,omitempty"`
}

// ResponseInputContent represents common text, image, and file input parts.
// Only fields applicable to Type need to be set.
type ResponseInputContent struct {
	Type                  string `json:"type"`
	Text                  string `json:"text,omitempty"`
	ImageURL              string `json:"image_url,omitempty"`
	Detail                string `json:"detail,omitempty"`
	FileID                string `json:"file_id,omitempty"`
	FileURL               string `json:"file_url,omitempty"`
	FileData              string `json:"file_data,omitempty"`
	Filename              string `json:"filename,omitempty"`
	PromptCacheBreakpoint any    `json:"prompt_cache_breakpoint,omitempty"`
}

// Response is a native Responses API resource. HTTP retains status, headers,
// and the original JSON so uncommon and newly introduced response fields remain available.
type Response struct {
	ID                 string               `json:"id"`
	Object             string               `json:"object"`
	CreatedAt          int64                `json:"created_at"`
	CompletedAt        *int64               `json:"completed_at,omitempty"`
	Status             string               `json:"status"`
	Error              json.RawMessage      `json:"error,omitempty"`
	IncompleteDetails  json.RawMessage      `json:"incomplete_details,omitempty"`
	Instructions       any                  `json:"instructions,omitempty"`
	MaxOutputTokens    *int                 `json:"max_output_tokens,omitempty"`
	MaxToolCalls       *int                 `json:"max_tool_calls,omitempty"`
	Metadata           map[string]string    `json:"metadata,omitempty"`
	Model              string               `json:"model,omitempty"`
	Output             []ResponseOutputItem `json:"output"`
	ParallelToolCalls  bool                 `json:"parallel_tool_calls,omitempty"`
	PreviousResponseID string               `json:"previous_response_id,omitempty"`
	Reasoning          json.RawMessage      `json:"reasoning,omitempty"`
	ServiceTier        string               `json:"service_tier,omitempty"`
	Text               json.RawMessage      `json:"text,omitempty"`
	ToolChoice         json.RawMessage      `json:"tool_choice,omitempty"`
	Tools              json.RawMessage      `json:"tools,omitempty"`
	Usage              Usage                `json:"usage,omitempty"`
	HTTP               *HTTPResponse        `json:"-"`
}

// ResponseOutputItem decodes fields common to messages and tool calls while
// retaining each item's complete JSON in Raw.
type ResponseOutputItem struct {
	ID               string            `json:"id,omitempty"`
	Type             string            `json:"type"`
	Status           string            `json:"status,omitempty"`
	Role             string            `json:"role,omitempty"`
	Content          []ResponseContent `json:"content,omitempty"`
	Name             string            `json:"name,omitempty"`
	CallID           string            `json:"call_id,omitempty"`
	Arguments        string            `json:"arguments,omitempty"`
	Output           any               `json:"output,omitempty"`
	ContainerID      string            `json:"container_id,omitempty"`
	EncryptedContent string            `json:"encrypted_content,omitempty"`
	Raw              json.RawMessage   `json:"-"`
}

// UnmarshalJSON decodes common fields and preserves the complete item JSON.
func (i *ResponseOutputItem) UnmarshalJSON(data []byte) error {
	type itemAlias ResponseOutputItem
	var decoded itemAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*i = ResponseOutputItem(decoded)
	i.Raw = append(i.Raw[:0], data...)
	return nil
}

// MarshalJSON writes the original native item when Raw is present. Clear Raw
// before marshaling to encode edits made through the common typed fields.
func (i ResponseOutputItem) MarshalJSON() ([]byte, error) {
	if len(i.Raw) != 0 {
		if !json.Valid(i.Raw) {
			return nil, fmt.Errorf("openai: invalid raw response output item")
		}
		return append([]byte(nil), i.Raw...), nil
	}
	type itemAlias ResponseOutputItem
	return json.Marshal(itemAlias(i))
}

// ResponseContent is a common text, refusal, image, or file content part.
// Raw contains the complete native content object.
type ResponseContent struct {
	Type        string          `json:"type"`
	Text        string          `json:"text,omitempty"`
	Refusal     string          `json:"refusal,omitempty"`
	Annotations json.RawMessage `json:"annotations,omitempty"`
	ImageURL    string          `json:"image_url,omitempty"`
	FileID      string          `json:"file_id,omitempty"`
	Filename    string          `json:"filename,omitempty"`
	Raw         json.RawMessage `json:"-"`
}

// UnmarshalJSON decodes common fields and preserves the complete content JSON.
func (c *ResponseContent) UnmarshalJSON(data []byte) error {
	type contentAlias ResponseContent
	var decoded contentAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*c = ResponseContent(decoded)
	c.Raw = append(c.Raw[:0], data...)
	return nil
}

// MarshalJSON writes the original native content when Raw is present. Clear
// Raw before marshaling to encode edits made through the common typed fields.
func (c ResponseContent) MarshalJSON() ([]byte, error) {
	if len(c.Raw) != 0 {
		if !json.Valid(c.Raw) {
			return nil, fmt.Errorf("openai: invalid raw response content")
		}
		return append([]byte(nil), c.Raw...), nil
	}
	type contentAlias ResponseContent
	return json.Marshal(contentAlias(c))
}

// CreateResponse creates a non-streaming model response.
func (c *Client) CreateResponse(ctx context.Context, request ResponseRequest) (*Response, error) {
	result := new(Response)
	if request.Stream != nil && *request.Stream {
		return result, fmt.Errorf("openai: CreateResponse does not accept stream=true; use StreamResponse")
	}
	httpResponse, err := c.json(ctx, http.MethodPost, "/responses", request, result)
	result.HTTP = httpResponse
	return result, err
}

// StreamResponse streams native response events. Completed, incomplete, and
// failed are valid protocol terminals; EOF before one returns io.ErrUnexpectedEOF.
func (c *Client) StreamResponse(ctx context.Context, request ResponseRequest, handle func(Event) error) (*HTTPResponse, error) {
	if handle == nil {
		return nil, fmt.Errorf("openai: nil response stream callback")
	}
	request.Stream = Ptr(true)
	terminal := false
	httpResponse, err := c.stream(ctx, "/responses", request, func(event Event) error {
		if err := handle(event); err != nil {
			return err
		}
		var envelope struct {
			Type string `json:"type"`
		}
		if unmarshalErr := json.Unmarshal(event.Data, &envelope); unmarshalErr != nil {
			return &StreamError{Event: event, Err: fmt.Errorf("decode response stream event: %w", unmarshalErr)}
		}
		eventType := envelope.Type
		if eventType == "" {
			eventType = event.Type
		}
		switch eventType {
		case "response.completed", "response.incomplete":
			terminal = true
		case "response.failed":
			terminal = true
			return &StreamError{Event: event, Err: errors.New("response failed event")}
		case "error":
			return &StreamError{Event: event, Err: errors.New("response stream error event")}
		}
		return nil
	})
	if err != nil {
		return httpResponse, err
	}
	if !terminal {
		return httpResponse, io.ErrUnexpectedEOF
	}
	return httpResponse, nil
}

// ResponseGetOptions controls optional fields and event offsets returned by GetResponse.
type ResponseGetOptions struct {
	Include            []string
	IncludeObfuscation *bool
	StartingAfter      *int64
}

// GetResponse retrieves a stored response by opaque response ID. Pass zero or
// one options value; omitting it emits no query parameters.
func (c *Client) GetResponse(ctx context.Context, responseID string, optional ...ResponseGetOptions) (*Response, error) {
	id, err := pathID(responseID)
	if err != nil {
		return nil, err
	}
	if len(optional) > 1 {
		return nil, fmt.Errorf("openai: GetResponse accepts at most one options value")
	}
	query := make(url.Values)
	if len(optional) == 1 {
		for _, include := range optional[0].Include {
			query.Add("include", include)
		}
		if optional[0].IncludeObfuscation != nil {
			query.Set("include_obfuscation", strconv.FormatBool(*optional[0].IncludeObfuscation))
		}
		if optional[0].StartingAfter != nil {
			query.Set("starting_after", strconv.FormatInt(*optional[0].StartingAfter, 10))
		}
	}
	path := "/responses/" + id
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	result := new(Response)
	httpResponse, err := c.json(ctx, http.MethodGet, path, nil, result)
	result.HTTP = httpResponse
	return result, err
}

// CancelResponse cancels an in-progress background response.
func (c *Client) CancelResponse(ctx context.Context, responseID string) (*Response, error) {
	id, err := pathID(responseID)
	if err != nil {
		return nil, err
	}
	result := new(Response)
	httpResponse, err := c.json(ctx, http.MethodPost, "/responses/"+id+"/cancel", nil, result)
	result.HTTP = httpResponse
	return result, err
}

// DeleteResponse deletes a stored response and returns its raw HTTP response.
func (c *Client) DeleteResponse(ctx context.Context, responseID string) (*HTTPResponse, error) {
	id, err := pathID(responseID)
	if err != nil {
		return nil, err
	}
	return c.json(ctx, http.MethodDelete, "/responses/"+id, nil, nil)
}

// ResponseInputItemsOptions controls pagination for ListResponseInputItems.
type ResponseInputItemsOptions struct {
	After   string
	Include []string
	Limit   int
	Order   string
}

// ResponseItemList is the native cursor page returned for response input items.
type ResponseItemList struct {
	Object  string               `json:"object"`
	Data    []ResponseOutputItem `json:"data"`
	FirstID string               `json:"first_id,omitempty"`
	LastID  string               `json:"last_id,omitempty"`
	HasMore bool                 `json:"has_more"`
	HTTP    *HTTPResponse        `json:"-"`
}

// ListResponseInputItems lists the input items of a stored response.
func (c *Client) ListResponseInputItems(ctx context.Context, responseID string, options ResponseInputItemsOptions) (*ResponseItemList, error) {
	id, err := pathID(responseID)
	if err != nil {
		return nil, err
	}
	query := make(url.Values)
	if options.After != "" {
		query.Set("after", options.After)
	}
	for _, include := range options.Include {
		query.Add("include", include)
	}
	if options.Limit != 0 {
		query.Set("limit", strconv.Itoa(options.Limit))
	}
	if options.Order != "" {
		query.Set("order", options.Order)
	}
	path := "/responses/" + id + "/input_items"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	result := new(ResponseItemList)
	httpResponse, err := c.json(ctx, http.MethodGet, path, nil, result)
	result.HTTP = httpResponse
	return result, err
}

// ResponseInputTokensRequest is the native body for POST /responses/input_tokens.
type ResponseInputTokensRequest struct {
	Conversation       any            `json:"conversation,omitempty"`
	Input              any            `json:"input,omitempty"`
	Instructions       any            `json:"instructions,omitempty"`
	Model              string         `json:"model,omitempty"`
	ParallelToolCalls  *bool          `json:"parallel_tool_calls,omitempty"`
	Personality        string         `json:"personality,omitempty"`
	PreviousResponseID string         `json:"previous_response_id,omitempty"`
	Prompt             any            `json:"prompt,omitempty"`
	Reasoning          any            `json:"reasoning,omitempty"`
	Text               any            `json:"text,omitempty"`
	ToolChoice         any            `json:"tool_choice,omitempty"`
	Tools              []any          `json:"tools,omitempty"`
	Truncation         string         `json:"truncation,omitempty"`
	Extra              map[string]any `json:"-"`
}

// MarshalJSON merges Extra into the token-counting request.
func (r ResponseInputTokensRequest) MarshalJSON() ([]byte, error) {
	type requestAlias ResponseInputTokensRequest
	return marshalFields(requestAlias(r), r.Extra)
}

// ResponseInputTokens is a server-computed input token count.
type ResponseInputTokens struct {
	Object      string        `json:"object"`
	InputTokens int           `json:"input_tokens"`
	HTTP        *HTTPResponse `json:"-"`
}

// CountResponseInputTokens counts tokens for a native Responses input without generating output.
func (c *Client) CountResponseInputTokens(ctx context.Context, request ResponseInputTokensRequest) (*ResponseInputTokens, error) {
	result := new(ResponseInputTokens)
	httpResponse, err := c.json(ctx, http.MethodPost, "/responses/input_tokens", request, result)
	result.HTTP = httpResponse
	return result, err
}

// ResponseCompactRequest is the native request body for POST /responses/compact.
type ResponseCompactRequest struct {
	Input                any            `json:"input,omitempty"`
	Instructions         any            `json:"instructions,omitempty"`
	Model                string         `json:"model,omitempty"`
	PreviousResponseID   string         `json:"previous_response_id,omitempty"`
	PromptCacheKey       string         `json:"prompt_cache_key,omitempty"`
	PromptCacheOptions   any            `json:"prompt_cache_options,omitempty"`
	PromptCacheRetention string         `json:"prompt_cache_retention,omitempty"`
	ServiceTier          string         `json:"service_tier,omitempty"`
	Extra                map[string]any `json:"-"`
}

// MarshalJSON merges Extra into the compaction request.
func (r ResponseCompactRequest) MarshalJSON() ([]byte, error) {
	type requestAlias ResponseCompactRequest
	return marshalFields(requestAlias(r), r.Extra)
}

// CompactedResponse is the native output of response compaction.
type CompactedResponse struct {
	ID        string               `json:"id"`
	Object    string               `json:"object"`
	CreatedAt int64                `json:"created_at"`
	Output    []ResponseOutputItem `json:"output"`
	Usage     Usage                `json:"usage"`
	HTTP      *HTTPResponse        `json:"-"`
}

// CompactResponse compacts a conversation into input items suitable for a later response.
func (c *Client) CompactResponse(ctx context.Context, request ResponseCompactRequest) (*CompactedResponse, error) {
	result := new(CompactedResponse)
	httpResponse, err := c.json(ctx, http.MethodPost, "/responses/compact", request, result)
	result.HTTP = httpResponse
	return result, err
}
