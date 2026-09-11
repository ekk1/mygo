package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListModelsPreservesNativeMetadata(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("X-Request-ID", "req_models")
		io.WriteString(w, `{"object":"list","data":[{"id":"gpt-test","object":"model","created":12,"owned_by":"vendor","capabilities":{"vision":true}}]}`)
	}))
	defer s.Close()
	c, err := New(Config{APIKey: "test", BaseURL: s.URL + "/v1", ProxyURL: "-"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseIdleConnections()
	models, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if models.Object != "list" || len(models.Data) != 1 || models.Data[0].ID != "gpt-test" || models.HTTP == nil || models.HTTP.Header.Get("X-Request-ID") != "req_models" {
		t.Fatalf("ListModels = %#v", models)
	}
	var raw map[string]any
	if err := json.Unmarshal(models.Data[0].Raw, &raw); err != nil || raw["owned_by"] != "vendor" || raw["capabilities"] == nil {
		t.Fatalf("raw metadata = %#v, %v", raw, err)
	}
}
