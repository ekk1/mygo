package openai

import (
	"context"
	"encoding/json"
	"net/http"
)

// Model is native model metadata. Raw preserves fields not explicitly decoded.
type Model struct {
	ID      string          `json:"id"`
	Object  string          `json:"object"`
	Created int64           `json:"created"`
	OwnedBy string          `json:"owned_by"`
	Raw     json.RawMessage `json:"-"`
}

// UnmarshalJSON decodes common model fields and preserves the native object.
func (m *Model) UnmarshalJSON(data []byte) error {
	type alias Model
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*m = Model(decoded)
	m.Raw = append(m.Raw[:0], data...)
	return nil
}

// MarshalJSON writes the preserved native object when available.
func (m Model) MarshalJSON() ([]byte, error) {
	if len(m.Raw) != 0 {
		return append([]byte(nil), m.Raw...), nil
	}
	type alias Model
	return json.Marshal(alias(m))
}

// ModelList is the complete native page returned by ListModels.
type ModelList struct {
	Object string        `json:"object"`
	Data   []Model       `json:"data"`
	HTTP   *HTTPResponse `json:"-"`
}

// ListModels returns the model catalog exposed by the configured provider.
func (c *Client) ListModels(ctx context.Context) (*ModelList, error) {
	result := new(ModelList)
	response, err := c.json(ctx, http.MethodGet, "/models", nil, result)
	result.HTTP = response
	return result, err
}
