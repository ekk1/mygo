package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// ChatCompletionRequest is the native request body for POST /chat/completions.
// Content, tools, tool choice, response format, reasoning, and future polymorphic
// fields intentionally remain open so callers can use the server's native JSON.
type ChatCompletionRequest struct {
	Model                string            `json:"model"`
	Messages             []ChatMessage     `json:"messages"`
	Audio                any               `json:"audio,omitempty"`
	FrequencyPenalty     *float64          `json:"frequency_penalty,omitempty"`
	FunctionCall         any               `json:"function_call,omitempty"`
	Functions            []any             `json:"functions,omitempty"`
	LogitBias            map[string]int    `json:"logit_bias,omitempty"`
	Logprobs             *bool             `json:"logprobs,omitempty"`
	MaxCompletionTokens  *int              `json:"max_completion_tokens,omitempty"`
	MaxTokens            *int              `json:"max_tokens,omitempty"`
	Metadata             map[string]string `json:"metadata,omitempty"`
	Modalities           []string          `json:"modalities,omitempty"`
	Moderation           any               `json:"moderation,omitempty"`
	N                    *int              `json:"n,omitempty"`
	ParallelToolCalls    *bool             `json:"parallel_tool_calls,omitempty"`
	Prediction           any               `json:"prediction,omitempty"`
	PresencePenalty      *float64          `json:"presence_penalty,omitempty"`
	PromptCacheKey       string            `json:"prompt_cache_key,omitempty"`
	PromptCacheOptions   any               `json:"prompt_cache_options,omitempty"`
	PromptCacheRetention string            `json:"prompt_cache_retention,omitempty"`
	ReasoningEffort      string            `json:"reasoning_effort,omitempty"`
	ResponseFormat       any               `json:"response_format,omitempty"`
	SafetyIdentifier     string            `json:"safety_identifier,omitempty"`
	Seed                 *int64            `json:"seed,omitempty"`
	ServiceTier          string            `json:"service_tier,omitempty"`
	Stop                 any               `json:"stop,omitempty"`
	Store                *bool             `json:"store,omitempty"`
	Stream               *bool             `json:"stream,omitempty"`
	StreamOptions        any               `json:"stream_options,omitempty"`
	Temperature          *float64          `json:"temperature,omitempty"`
	ToolChoice           any               `json:"tool_choice,omitempty"`
	Tools                []any             `json:"tools,omitempty"`
	TopLogprobs          *int              `json:"top_logprobs,omitempty"`
	TopP                 *float64          `json:"top_p,omitempty"`
	User                 string            `json:"user,omitempty"`
	Verbosity            string            `json:"verbosity,omitempty"`
	WebSearchOptions     any               `json:"web_search_options,omitempty"`
	Extra                map[string]any    `json:"-"`
}

// MarshalJSON merges forward-compatible native fields from Extra while
// rejecting collisions with typed fields already present in the request.
func (r ChatCompletionRequest) MarshalJSON() ([]byte, error) {
	type requestAlias ChatCompletionRequest
	return marshalFields(requestAlias(r), r.Extra)
}

// ChatMessage represents a native Chat Completions message. Content may be a
// string, null, or an array of native multimodal content parts.
type ChatMessage struct {
	Role         string          `json:"role"`
	Content      any             `json:"content"`
	Name         string          `json:"name,omitempty"`
	Audio        any             `json:"audio,omitempty"`
	Annotations  json.RawMessage `json:"annotations,omitempty"`
	FunctionCall any             `json:"function_call,omitempty"`
	Refusal      string          `json:"refusal,omitempty"`
	ToolCallID   string          `json:"tool_call_id,omitempty"`
	ToolCalls    []ChatToolCall  `json:"tool_calls,omitempty"`
}

// ChatToolCall is a function or custom tool call emitted in a chat message.
type ChatToolCall struct {
	ID       string            `json:"id,omitempty"`
	Type     string            `json:"type,omitempty"`
	Function *ChatFunctionCall `json:"function,omitempty"`
	Custom   json.RawMessage   `json:"custom,omitempty"`
}

// ChatFunctionCall contains a function name and its JSON-encoded arguments.
type ChatFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ChatCompletion is a native chat completion with selected common fields
// decoded and the complete HTTP response retained in HTTP.
type ChatCompletion struct {
	ID                string                 `json:"id"`
	Object            string                 `json:"object"`
	Created           int64                  `json:"created"`
	Model             string                 `json:"model"`
	Choices           []ChatCompletionChoice `json:"choices"`
	Usage             Usage                  `json:"usage"`
	ServiceTier       string                 `json:"service_tier,omitempty"`
	SystemFingerprint string                 `json:"system_fingerprint,omitempty"`
	Metadata          map[string]string      `json:"metadata,omitempty"`
	Moderation        json.RawMessage        `json:"moderation,omitempty"`
	HTTP              *HTTPResponse          `json:"-"`
}

// ChatCompletionChoice is one generated choice.
type ChatCompletionChoice struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
	Logprobs     any         `json:"logprobs,omitempty"`
}

// Usage contains token accounting shared by Chat Completions, Responses, and
// response compaction. Endpoint-specific detail objects are preserved raw.
type Usage struct {
	PromptTokens            int             `json:"prompt_tokens,omitempty"`
	CompletionTokens        int             `json:"completion_tokens,omitempty"`
	InputTokens             int             `json:"input_tokens,omitempty"`
	OutputTokens            int             `json:"output_tokens,omitempty"`
	TotalTokens             int             `json:"total_tokens,omitempty"`
	PromptTokensDetails     json.RawMessage `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails json.RawMessage `json:"completion_tokens_details,omitempty"`
	InputTokensDetails      json.RawMessage `json:"input_tokens_details,omitempty"`
	OutputTokensDetails     json.RawMessage `json:"output_tokens_details,omitempty"`
}

// CreateChatCompletion creates a non-streaming native chat completion.
func (c *Client) CreateChatCompletion(ctx context.Context, request ChatCompletionRequest) (*ChatCompletion, error) {
	result := new(ChatCompletion)
	if request.Stream != nil && *request.Stream {
		return result, fmt.Errorf("openai: CreateChatCompletion does not accept stream=true; use StreamChatCompletion")
	}
	httpResponse, err := c.json(ctx, "POST", "/chat/completions", request, result)
	result.HTTP = httpResponse
	return result, err
}

// StreamChatCompletion streams raw SSE events. A successful stream must end
// with the Chat Completions [DONE] marker; an early EOF returns io.ErrUnexpectedEOF.
func (c *Client) StreamChatCompletion(ctx context.Context, request ChatCompletionRequest, handle func(Event) error) (*HTTPResponse, error) {
	if handle == nil {
		return nil, fmt.Errorf("openai: nil chat stream callback")
	}
	request.Stream = Ptr(true)
	done := false
	httpResponse, err := c.stream(ctx, "/chat/completions", request, func(event Event) error {
		if err := handle(event); err != nil {
			return err
		}
		if string(event.Data) == "[DONE]" {
			done = true
			return nil
		}
		var envelope struct {
			Type  string          `json:"type"`
			Error json.RawMessage `json:"error"`
		}
		if unmarshalErr := json.Unmarshal(event.Data, &envelope); unmarshalErr != nil {
			return &StreamError{Event: event, Err: fmt.Errorf("decode chat stream event: %w", unmarshalErr)}
		}
		if event.Type == "error" || envelope.Type == "error" || (len(envelope.Error) != 0 && !bytes.Equal(bytes.TrimSpace(envelope.Error), []byte("null"))) {
			return &StreamError{Event: event, Err: errors.New("chat stream error event")}
		}
		return nil
	})
	if err != nil {
		return httpResponse, err
	}
	if !done {
		return httpResponse, io.ErrUnexpectedEOF
	}
	return httpResponse, nil
}
