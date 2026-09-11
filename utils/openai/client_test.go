package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientNativeJSONAndMultipart(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer k" {
			t.Error("missing auth")
		}
		switch r.URL.Path {
		case "/v1/test":
			io.WriteString(w, `{"ok":true,"future":42}`)
		case "/v1/upload":
			if err := r.ParseMultipartForm(1024); err != nil {
				t.Error(err)
			}
			if len(r.MultipartForm.Value["include[]"]) != 2 {
				t.Error("repeated form values lost")
			}
			f, _, err := r.FormFile("file")
			if err != nil {
				t.Error(err)
			} else {
				defer f.Close()
				b, _ := io.ReadAll(f)
				if string(b) != "abc" {
					t.Error("file content")
				}
			}
			io.WriteString(w, `{"id":"file_1"}`)
		case "/v1/speech":
			io.WriteString(w, "audio")
		}
	}))
	defer s.Close()
	c, err := New(Config{APIKey: "k", BaseURL: s.URL + "/v1", ProxyURL: "-"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseIdleConnections()
	var result map[string]any
	r, err := c.json(context.Background(), "POST", "/test", map[string]string{"model": "custom"}, &result)
	if err != nil || result["future"] != float64(42) || !json.Valid(r.Body) {
		t.Fatalf("%v %v", result, err)
	}
	_, err = c.multipartValues(context.Background(), "POST", "/upload", map[string][]string{"include[]": {"a", "b"}}, []Upload{{Field: "file", Filename: "test.txt", Reader: strings.NewReader("abc")}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var dst strings.Builder
	_, err = c.jsonDownload(context.Background(), "POST", "/speech", map[string]string{"input": "hello"}, &dst)
	if err != nil || dst.String() != "audio" {
		t.Fatal(err)
	}
}
func TestExtraConflictsAndPathID(t *testing.T) {
	type native struct {
		Model string `json:"model"`
		Store *bool  `json:"store,omitempty"`
	}
	b, err := marshalFields(native{Model: "m", Store: Ptr(false)}, map[string]any{"future": true})
	if err != nil || !strings.Contains(string(b), `"store":false`) {
		t.Fatalf("%s %v", b, err)
	}
	if _, err := marshalFields(native{Model: "m"}, map[string]any{"model": "other"}); err == nil {
		t.Fatal("silent field overwrite")
	}
	for _, id := range []string{"", "..", "a/b", "x?y"} {
		if _, err := pathID(id); err == nil {
			t.Fatalf("bad ID accepted %q", id)
		}
	}
	if _, err := New(Config{APIKey: "k", BaseURL: "https://example.test/?key=secret"}); err == nil {
		t.Fatal("base query allowed")
	}
}

type badUploadReader struct{}

func (badUploadReader) Read([]byte) (int, error) { return 0, errors.New("source read failed") }
func TestMultipartReadFailure(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.Copy(io.Discard, r.Body) }))
	defer s.Close()
	c, _ := New(Config{APIKey: "k", BaseURL: s.URL, ProxyURL: "-"})
	defer c.CloseIdleConnections()
	_, err := c.multipart(context.Background(), "POST", "/files", nil, []Upload{{Field: "file", Filename: "a", Reader: badUploadReader{}}}, nil)
	if err == nil {
		t.Fatal("source error lost")
	}
}

// Media URLs must reuse the configured proxy without forwarding API credentials.
func TestDownloadMediaOmitsAuthentication(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Host != "media.test" || r.URL.Path != "/image.png" {
			t.Errorf("target %s", r.URL)
		}
		if r.Header.Get("Authorization") != "" || r.Header.Get("X-Custom-Auth") != "" {
			t.Error("API headers leaked to media host")
		}
		io.WriteString(w, "PNG")
	}))
	defer proxy.Close()
	c, err := New(Config{APIKey: "key", ProxyURL: proxy.URL, Headers: http.Header{"X-Custom-Auth": {"private"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseIdleConnections()
	var dst strings.Builder
	_, err = c.DownloadMedia(context.Background(), "http://media.test/image.png?sig=private", &dst)
	if err != nil || dst.String() != "PNG" {
		t.Fatalf("%q %v", dst.String(), err)
	}
}
