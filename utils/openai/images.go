package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// ImageGenerateRequest is the native request body for image generation.
type ImageGenerateRequest struct {
	Model             string         `json:"model,omitempty"`
	Prompt            string         `json:"prompt"`
	Background        string         `json:"background,omitempty"`
	Moderation        string         `json:"moderation,omitempty"`
	N                 *int           `json:"n,omitempty"`
	OutputCompression *int           `json:"output_compression,omitempty"`
	OutputFormat      string         `json:"output_format,omitempty"`
	PartialImages     *int           `json:"partial_images,omitempty"`
	Quality           string         `json:"quality,omitempty"`
	ResponseFormat    string         `json:"response_format,omitempty"`
	Size              string         `json:"size,omitempty"`
	Stream            *bool          `json:"stream,omitempty"`
	Style             string         `json:"style,omitempty"`
	User              string         `json:"user,omitempty"`
	Extra             map[string]any `json:"-"`
}

// MarshalJSON adds forward-compatible Extra fields while rejecting collisions.
func (request ImageGenerateRequest) MarshalJSON() ([]byte, error) {
	type alias ImageGenerateRequest
	return marshalFields(alias(request), request.Extra)
}

// ImageReference identifies an image through the Files API or a URL/data URL.
type ImageReference struct {
	FileID   string `json:"file_id,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

// ImageEditRequest supports native JSON references or multipart file uploads.
// Set ImageFiles or MaskFile to select multipart encoding.
type ImageEditRequest struct {
	Model             string           `json:"model,omitempty"`
	Prompt            string           `json:"prompt"`
	Images            []ImageReference `json:"images,omitempty"`
	Mask              *ImageReference  `json:"mask,omitempty"`
	Background        string           `json:"background,omitempty"`
	InputFidelity     string           `json:"input_fidelity,omitempty"`
	Moderation        string           `json:"moderation,omitempty"`
	N                 *int             `json:"n,omitempty"`
	OutputCompression *int             `json:"output_compression,omitempty"`
	OutputFormat      string           `json:"output_format,omitempty"`
	PartialImages     *int             `json:"partial_images,omitempty"`
	Quality           string           `json:"quality,omitempty"`
	ResponseFormat    string           `json:"response_format,omitempty"`
	Size              string           `json:"size,omitempty"`
	Stream            *bool            `json:"stream,omitempty"`
	User              string           `json:"user,omitempty"`
	ImageFiles        []Upload         `json:"-"`
	MaskFile          *Upload          `json:"-"`
	Extra             map[string]any   `json:"-"`
}

// MarshalJSON adds forward-compatible Extra fields while rejecting collisions.
func (request ImageEditRequest) MarshalJSON() ([]byte, error) {
	type alias ImageEditRequest
	return marshalFields(alias(request), request.Extra)
}

// Image is one image returned by the Images API.
type Image struct {
	B64JSON       string `json:"b64_json,omitempty"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
	URL           string `json:"url,omitempty"`
}

// ImageTokenDetails splits image and text token usage.
type ImageTokenDetails struct {
	ImageTokens int `json:"image_tokens"`
	TextTokens  int `json:"text_tokens"`
}

// ImageUsage describes token usage for GPT Image models.
type ImageUsage struct {
	InputTokens         int               `json:"input_tokens"`
	InputTokensDetails  ImageTokenDetails `json:"input_tokens_details"`
	OutputTokens        int               `json:"output_tokens"`
	OutputTokensDetails ImageTokenDetails `json:"output_tokens_details"`
	TotalTokens         int               `json:"total_tokens"`
}

// ImageResponse is the decoded image generation or edit response.
type ImageResponse struct {
	Created      int64         `json:"created"`
	Background   string        `json:"background,omitempty"`
	Data         []Image       `json:"data,omitempty"`
	OutputFormat string        `json:"output_format,omitempty"`
	Quality      string        `json:"quality,omitempty"`
	Size         string        `json:"size,omitempty"`
	Usage        ImageUsage    `json:"usage,omitempty"`
	HTTP         *HTTPResponse `json:"-"`
}

// ImageStreamEvent represents partial and completed generation/edit events.
type ImageStreamEvent struct {
	Type              string          `json:"type"`
	B64JSON           string          `json:"b64_json,omitempty"`
	Background        string          `json:"background,omitempty"`
	CreatedAt         int64           `json:"created_at,omitempty"`
	OutputFormat      string          `json:"output_format,omitempty"`
	PartialImageIndex int             `json:"partial_image_index,omitempty"`
	Quality           string          `json:"quality,omitempty"`
	Size              string          `json:"size,omitempty"`
	Usage             ImageUsage      `json:"usage,omitempty"`
	Raw               json.RawMessage `json:"-"`
}

// UnmarshalJSON decodes common fields and preserves the complete native event.
func (event *ImageStreamEvent) UnmarshalJSON(data []byte) error {
	type alias ImageStreamEvent
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*event = ImageStreamEvent(decoded)
	event.Raw = append(event.Raw[:0], data...)
	return nil
}

// GenerateImage creates images with POST /images/generations.
func (c *Client) GenerateImage(ctx context.Context, request ImageGenerateRequest) (*ImageResponse, error) {
	result := new(ImageResponse)
	if request.Stream != nil && *request.Stream {
		return result, fmt.Errorf("openai: GenerateImage does not accept stream=true; use StreamImage")
	}
	response, err := c.json(ctx, http.MethodPost, "/images/generations", request, result)
	result.HTTP = response
	return result, err
}

// EditImage edits referenced or uploaded images with POST /images/edits.
func (c *Client) EditImage(ctx context.Context, request ImageEditRequest) (*ImageResponse, error) {
	result := new(ImageResponse)
	if request.Stream != nil && *request.Stream {
		return result, fmt.Errorf("openai: EditImage does not accept stream=true; use StreamImageEdit")
	}
	if (len(request.Images) != 0 || request.Mask != nil) && (len(request.ImageFiles) != 0 || request.MaskFile != nil) {
		return result, fmt.Errorf("openai: image edit cannot mix JSON references and multipart files")
	}
	if len(request.ImageFiles) == 0 && request.MaskFile == nil {
		response, err := c.json(ctx, http.MethodPost, "/images/edits", request, result)
		result.HTTP = response
		return result, err
	}
	fields, files, err := imageEditMultipart(request)
	if err != nil {
		return result, err
	}
	response, err := c.multipartValues(ctx, http.MethodPost, "/images/edits", fields, files, result)
	result.HTTP = response
	return result, err
}

// StreamImage streams partial images and requires an image_generation.completed terminal event.
func (c *Client) StreamImage(ctx context.Context, request ImageGenerateRequest, handle func(ImageStreamEvent) error) (*HTTPResponse, error) {
	if handle == nil {
		return nil, fmt.Errorf("openai: nil image stream callback")
	}
	request.Stream = Ptr(true)
	return c.streamImageEvents(ctx, "/images/generations", request, nil, nil, "image_generation.completed", handle)
}

// StreamImageEdit streams JSON-reference or multipart image edits and requires image_edit.completed.
func (c *Client) StreamImageEdit(ctx context.Context, request ImageEditRequest, handle func(ImageStreamEvent) error) (*HTTPResponse, error) {
	if handle == nil {
		return nil, fmt.Errorf("openai: nil image edit stream callback")
	}
	if (len(request.Images) != 0 || request.Mask != nil) && (len(request.ImageFiles) != 0 || request.MaskFile != nil) {
		return nil, fmt.Errorf("openai: image edit cannot mix JSON references and multipart files")
	}
	request.Stream = Ptr(true)
	if len(request.ImageFiles) == 0 && request.MaskFile == nil {
		return c.streamImageEvents(ctx, "/images/edits", request, nil, nil, "image_edit.completed", handle)
	}
	fields, files, err := imageEditMultipart(request)
	if err != nil {
		return nil, err
	}
	return c.streamImageEvents(ctx, "/images/edits", nil, fields, files, "image_edit.completed", handle)
}

func (c *Client) streamImageEvents(ctx context.Context, path string, input any, fields map[string][]string, files []Upload, terminal string, handle func(ImageStreamEvent) error) (*HTTPResponse, error) {
	completed := false
	consume := func(raw Event) error {
		if err := mediaEventFailure(raw); err != nil {
			return err
		}
		var event ImageStreamEvent
		if err := json.Unmarshal(raw.Data, &event); err != nil {
			return &StreamError{Event: raw, Err: fmt.Errorf("decode image stream event: %w", err)}
		}
		if event.Type == "" {
			event.Type = raw.Type
		}
		if event.Type == terminal {
			completed = true
		}
		if handle != nil {
			return handle(event)
		}
		return nil
	}
	var response *HTTPResponse
	var err error
	if fields != nil {
		response, err = c.streamMultipartValues(ctx, path, fields, files, consume)
	} else {
		response, err = c.stream(ctx, path, input, consume)
	}
	if err != nil {
		return response, err
	}
	if !completed {
		return response, io.ErrUnexpectedEOF
	}
	return response, nil
}

func imageEditMultipart(request ImageEditRequest) (map[string][]string, []Upload, error) {
	fields := make(map[string][]string)
	addMediaString(fields, "model", request.Model)
	addMediaString(fields, "prompt", request.Prompt)
	addMediaString(fields, "background", request.Background)
	addMediaString(fields, "input_fidelity", request.InputFidelity)
	addMediaString(fields, "moderation", request.Moderation)
	addMediaInt(fields, "n", request.N)
	addMediaInt(fields, "output_compression", request.OutputCompression)
	addMediaString(fields, "output_format", request.OutputFormat)
	addMediaInt(fields, "partial_images", request.PartialImages)
	addMediaString(fields, "quality", request.Quality)
	addMediaString(fields, "response_format", request.ResponseFormat)
	addMediaString(fields, "size", request.Size)
	addMediaBool(fields, "stream", request.Stream)
	addMediaString(fields, "user", request.User)
	if err := addMediaExtra(fields, request.Extra); err != nil {
		return nil, nil, err
	}
	files := make([]Upload, 0, len(request.ImageFiles)+1)
	for _, file := range request.ImageFiles {
		file.Field = "image[]"
		files = append(files, file)
	}
	if request.MaskFile != nil {
		mask := *request.MaskFile
		mask.Field = "mask"
		files = append(files, mask)
	}
	return fields, files, nil
}

func mediaEventFailure(event Event) error {
	if event.Type != "error" {
		var kind struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(event.Data, &kind) != nil || kind.Type != "error" {
			return nil
		}
	}
	var payload struct {
		Message string `json:"message"`
		Code    string `json:"code"`
		Error   struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(event.Data, &payload); err != nil {
		return &StreamError{Event: event, Err: fmt.Errorf("decode stream error event: %w", err)}
	}
	if payload.Message == "" {
		payload.Message, payload.Code = payload.Error.Message, payload.Error.Code
	}
	if payload.Message == "" {
		payload.Message = "stream error"
	}
	if payload.Code != "" {
		return &StreamError{Event: event, Err: fmt.Errorf("%s: %s", payload.Code, payload.Message)}
	}
	return &StreamError{Event: event, Err: errors.New(payload.Message)}
}
