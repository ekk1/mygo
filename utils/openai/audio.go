package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// SpeechRequest is the native JSON request for non-live text-to-speech.
// Voice may be a built-in voice string or a custom voice object.
type SpeechRequest struct {
	Model          string         `json:"model"`
	Input          string         `json:"input"`
	Voice          any            `json:"voice"`
	Instructions   string         `json:"instructions,omitempty"`
	ResponseFormat string         `json:"response_format,omitempty"`
	Speed          *float64       `json:"speed,omitempty"`
	StreamFormat   string         `json:"stream_format,omitempty"`
	Extra          map[string]any `json:"-"`
}

// SpeechUsage describes token usage reported by a speech.audio.done event.
type SpeechUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// SpeechStreamEvent represents speech.audio.delta and speech.audio.done events.
type SpeechStreamEvent struct {
	Type  string          `json:"type"`
	Audio string          `json:"audio,omitempty"`
	Usage SpeechUsage     `json:"usage,omitempty"`
	Raw   json.RawMessage `json:"-"`
}

// UnmarshalJSON decodes common fields and preserves the complete native event.
func (event *SpeechStreamEvent) UnmarshalJSON(data []byte) error {
	type alias SpeechStreamEvent
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*event = SpeechStreamEvent(decoded)
	event.Raw = append(event.Raw[:0], data...)
	return nil
}

// MarshalJSON adds forward-compatible Extra fields while rejecting collisions.
func (request SpeechRequest) MarshalJSON() ([]byte, error) {
	type alias SpeechRequest
	return marshalFields(alias(request), request.Extra)
}

// TranscriptionRequest describes a multipart non-live transcription request.
type TranscriptionRequest struct {
	Model                  string
	File                   Upload
	ChunkingStrategy       json.RawMessage
	Include                []string
	Keywords               []string
	KnownSpeakerNames      []string
	KnownSpeakerReferences []string
	Language               string
	Languages              []string
	Prompt                 string
	ResponseFormat         string
	Stream                 *bool
	Temperature            *float64
	TimestampGranularities []string
	Extra                  map[string]any
}

// TranslationRequest describes a multipart request that translates audio to English.
type TranslationRequest struct {
	Model          string
	File           Upload
	Prompt         string
	ResponseFormat string
	Temperature    *float64
	Extra          map[string]any
}

// TranscriptionLanguage is one language detected in the input audio.
type TranscriptionLanguage struct {
	Code string `json:"code"`
}

// TranscriptionLogprob contains token confidence data.
type TranscriptionLogprob struct {
	Token   string  `json:"token,omitempty"`
	Bytes   []int   `json:"bytes,omitempty"`
	Logprob float64 `json:"logprob,omitempty"`
}

// TranscriptionUsage preserves token-based or duration-based usage.
type TranscriptionUsage struct {
	Type              string `json:"type,omitempty"`
	InputTokens       int    `json:"input_tokens,omitempty"`
	OutputTokens      int    `json:"output_tokens,omitempty"`
	TotalTokens       int    `json:"total_tokens,omitempty"`
	InputTokenDetails struct {
		AudioTokens int `json:"audio_tokens,omitempty"`
		TextTokens  int `json:"text_tokens,omitempty"`
	} `json:"input_token_details,omitempty"`
	Seconds float64 `json:"seconds,omitempty"`
}

// TranscriptionWord is a word with its start and end time in seconds.
type TranscriptionWord struct {
	Word  string  `json:"word"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// TranscriptionSegment preserves both verbose and diarized segment fields.
// ID is raw because verbose responses use a number while diarized responses use a string.
type TranscriptionSegment struct {
	ID               json.RawMessage `json:"id,omitempty"`
	Type             string          `json:"type,omitempty"`
	Seek             int             `json:"seek,omitempty"`
	Start            float64         `json:"start"`
	End              float64         `json:"end"`
	Text             string          `json:"text"`
	Speaker          string          `json:"speaker,omitempty"`
	Tokens           []int           `json:"tokens,omitempty"`
	Temperature      float64         `json:"temperature,omitempty"`
	AvgLogprob       float64         `json:"avg_logprob,omitempty"`
	CompressionRatio float64         `json:"compression_ratio,omitempty"`
	NoSpeechProb     float64         `json:"no_speech_prob,omitempty"`
}

// Transcription contains JSON, verbose JSON, diarized JSON, or plain-text output.
type Transcription struct {
	Task      string                  `json:"task,omitempty"`
	Language  string                  `json:"language,omitempty"`
	Languages []TranscriptionLanguage `json:"languages,omitempty"`
	Duration  float64                 `json:"duration,omitempty"`
	Text      string                  `json:"text"`
	Words     []TranscriptionWord     `json:"words,omitempty"`
	Segments  []TranscriptionSegment  `json:"segments,omitempty"`
	Logprobs  []TranscriptionLogprob  `json:"logprobs,omitempty"`
	Usage     TranscriptionUsage      `json:"usage,omitempty"`
	HTTP      *HTTPResponse           `json:"-"`
}

// TranscriptionStreamEvent represents delta, diarized segment, and done events.
type TranscriptionStreamEvent struct {
	Type      string                  `json:"type"`
	Delta     string                  `json:"delta,omitempty"`
	Text      string                  `json:"text,omitempty"`
	SegmentID string                  `json:"segment_id,omitempty"`
	ID        string                  `json:"id,omitempty"`
	Start     float64                 `json:"start,omitempty"`
	End       float64                 `json:"end,omitempty"`
	Speaker   string                  `json:"speaker,omitempty"`
	Languages []TranscriptionLanguage `json:"languages,omitempty"`
	Logprobs  []TranscriptionLogprob  `json:"logprobs,omitempty"`
	Usage     TranscriptionUsage      `json:"usage,omitempty"`
	Raw       json.RawMessage         `json:"-"`
}

// UnmarshalJSON decodes common fields and preserves the complete native event.
func (event *TranscriptionStreamEvent) UnmarshalJSON(data []byte) error {
	type alias TranscriptionStreamEvent
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*event = TranscriptionStreamEvent(decoded)
	event.Raw = append(event.Raw[:0], data...)
	return nil
}

// CreateSpeech writes the binary response from POST /audio/speech to dst.
func (c *Client) CreateSpeech(ctx context.Context, request SpeechRequest, dst io.Writer) (*HTTPResponse, error) {
	if request.StreamFormat == "sse" {
		return nil, fmt.Errorf("openai: CreateSpeech does not accept stream_format=sse; use StreamSpeech")
	}
	return c.jsonDownload(ctx, http.MethodPost, "/audio/speech", request, dst)
}

// StreamSpeech streams base64 audio chunks and requires speech.audio.done.
func (c *Client) StreamSpeech(ctx context.Context, request SpeechRequest, handle func(SpeechStreamEvent) error) (*HTTPResponse, error) {
	if handle == nil {
		return nil, fmt.Errorf("openai: nil speech stream callback")
	}
	request.StreamFormat = "sse"
	done := false
	response, err := c.stream(ctx, "/audio/speech", request, func(raw Event) error {
		if err := mediaEventFailure(raw); err != nil {
			return err
		}
		var event SpeechStreamEvent
		if err := json.Unmarshal(raw.Data, &event); err != nil {
			return &StreamError{Event: raw, Err: fmt.Errorf("decode speech stream event: %w", err)}
		}
		if event.Type == "" {
			event.Type = raw.Type
		}
		if event.Type == "speech.audio.done" {
			done = true
		}
		if err := handle(event); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return response, err
	}
	if !done {
		return response, io.ErrUnexpectedEOF
	}
	return response, nil
}

// CreateTranscription transcribes an uploaded audio file.
func (c *Client) CreateTranscription(ctx context.Context, request TranscriptionRequest) (*Transcription, error) {
	if request.Stream != nil && *request.Stream {
		return &Transcription{}, fmt.Errorf("openai: CreateTranscription does not accept stream=true; use StreamTranscription")
	}
	fields, files, err := transcriptionMultipart(request)
	if err != nil {
		return &Transcription{}, err
	}
	return c.createAudioText(ctx, "/audio/transcriptions", request.ResponseFormat, fields, files)
}

// CreateTranslation translates an uploaded audio file into English.
func (c *Client) CreateTranslation(ctx context.Context, request TranslationRequest) (*Transcription, error) {
	fields := make(map[string][]string)
	addMediaString(fields, "model", request.Model)
	addMediaString(fields, "prompt", request.Prompt)
	addMediaString(fields, "response_format", request.ResponseFormat)
	addMediaFloat(fields, "temperature", request.Temperature)
	if err := addMediaExtra(fields, request.Extra); err != nil {
		return &Transcription{}, err
	}
	file := request.File
	file.Field = "file"
	return c.createAudioText(ctx, "/audio/translations", request.ResponseFormat, fields, []Upload{file})
}

// StreamTranscription streams a completed recording and requires transcript.text.done.
func (c *Client) StreamTranscription(ctx context.Context, request TranscriptionRequest, handle func(TranscriptionStreamEvent) error) (*HTTPResponse, error) {
	if handle == nil {
		return nil, fmt.Errorf("openai: nil transcription stream callback")
	}
	request.Stream = Ptr(true)
	fields, files, err := transcriptionMultipart(request)
	if err != nil {
		return nil, err
	}
	done := false
	response, err := c.streamMultipartValues(ctx, "/audio/transcriptions", fields, files, func(raw Event) error {
		if err := mediaEventFailure(raw); err != nil {
			return err
		}
		var event TranscriptionStreamEvent
		if err := json.Unmarshal(raw.Data, &event); err != nil {
			return &StreamError{Event: raw, Err: fmt.Errorf("decode transcription stream event: %w", err)}
		}
		if event.Type == "" {
			event.Type = raw.Type
		}
		if event.Type == "transcript.text.done" {
			done = true
		}
		if handle != nil {
			return handle(event)
		}
		return nil
	})
	if err != nil {
		return response, err
	}
	if !done {
		return response, io.ErrUnexpectedEOF
	}
	return response, nil
}

func (c *Client) createAudioText(ctx context.Context, path, format string, fields map[string][]string, files []Upload) (*Transcription, error) {
	result := new(Transcription)
	if isPlainAudioFormat(format) {
		response, err := c.multipartValues(ctx, http.MethodPost, path, fields, files, nil)
		result.HTTP = response
		if response != nil {
			result.Text = string(response.Body)
		}
		return result, err
	}
	response, err := c.multipartValues(ctx, http.MethodPost, path, fields, files, result)
	result.HTTP = response
	return result, err
}

func isPlainAudioFormat(format string) bool {
	switch format {
	case "text", "srt", "vtt":
		return true
	default:
		return false
	}
}

func transcriptionMultipart(request TranscriptionRequest) (map[string][]string, []Upload, error) {
	fields := make(map[string][]string)
	addMediaString(fields, "model", request.Model)
	if len(request.ChunkingStrategy) != 0 {
		if err := addChunkingStrategy(fields, request.ChunkingStrategy); err != nil {
			return nil, nil, err
		}
	}
	addMediaList(fields, "include[]", request.Include)
	addMediaList(fields, "keywords[]", request.Keywords)
	addMediaList(fields, "known_speaker_names[]", request.KnownSpeakerNames)
	addMediaList(fields, "known_speaker_references[]", request.KnownSpeakerReferences)
	addMediaString(fields, "language", request.Language)
	addMediaList(fields, "languages[]", request.Languages)
	addMediaString(fields, "prompt", request.Prompt)
	addMediaString(fields, "response_format", request.ResponseFormat)
	addMediaBool(fields, "stream", request.Stream)
	addMediaFloat(fields, "temperature", request.Temperature)
	addMediaList(fields, "timestamp_granularities[]", request.TimestampGranularities)
	if err := addMediaExtra(fields, request.Extra); err != nil {
		return nil, nil, err
	}
	file := request.File
	file.Field = "file"
	return fields, []Upload{file}, nil
}

func addChunkingStrategy(fields map[string][]string, raw json.RawMessage) error {
	var automatic string
	if err := json.Unmarshal(raw, &automatic); err == nil {
		fields["chunking_strategy"] = []string{automatic}
		return nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return fmt.Errorf("openai: chunking_strategy: %w", err)
	}
	for name, value := range object {
		encoded, err := mediaRawValue(value)
		if err != nil {
			return fmt.Errorf("openai: chunking_strategy.%s: %w", name, err)
		}
		fields["chunking_strategy["+name+"]"] = []string{encoded}
	}
	return nil
}

func mediaRawValue(raw json.RawMessage) (string, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}
	if !json.Valid(raw) {
		return "", fmt.Errorf("invalid JSON")
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return "", err
	}
	return compact.String(), nil
}

func addMediaString(fields map[string][]string, name, value string) {
	if value != "" {
		fields[name] = []string{value}
	}
}

func addMediaList(fields map[string][]string, name string, values []string) {
	if len(values) != 0 {
		fields[name] = append([]string(nil), values...)
	}
}

func addMediaInt(fields map[string][]string, name string, value *int) {
	if value != nil {
		fields[name] = []string{strconv.Itoa(*value)}
	}
}

func addMediaFloat(fields map[string][]string, name string, value *float64) {
	if value != nil {
		fields[name] = []string{strconv.FormatFloat(*value, 'g', -1, 64)}
	}
}

func addMediaBool(fields map[string][]string, name string, value *bool) {
	if value != nil {
		fields[name] = []string{strconv.FormatBool(*value)}
	}
}

func addMediaExtra(fields map[string][]string, extra map[string]any) error {
	for name, value := range extra {
		if _, exists := fields[name]; exists {
			return fmt.Errorf("openai: extra field %q conflicts with request field", name)
		}
		if strings.HasSuffix(name, "[]") {
			if values, ok := value.([]string); ok {
				fields[name] = append([]string(nil), values...)
				continue
			}
		}
		switch value := value.(type) {
		case string:
			fields[name] = []string{value}
		case json.RawMessage:
			encoded, err := mediaRawValue(value)
			if err != nil {
				return fmt.Errorf("openai: extra field %q: %w", name, err)
			}
			fields[name] = []string{encoded}
		default:
			encoded, err := json.Marshal(value)
			if err != nil {
				return fmt.Errorf("openai: extra field %q: %w", name, err)
			}
			fields[name] = []string{string(encoded)}
		}
	}
	return nil
}
