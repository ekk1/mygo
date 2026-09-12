package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func curlPreviewForTest(t *testing.T, p provider, operation string, body json.RawMessage, uploads resourceUploads) map[string]any {
	t.Helper()
	value, err := nativePreview(p, operation, body, uploads)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err = json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func runCurlExport(t *testing.T, script string, env ...string) string {
	t.Helper()
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("installed curl unavailable")
	}
	if script == "" {
		t.Fatal("preview omitted curl export")
	}
	file := filepath.Join(t.TempDir(), "request.sh")
	if err := os.WriteFile(file, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", file)
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), append([]string{"API_KEY=test-runtime-key"}, env...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("export failed: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(cmd.Dir, "SHOULD_NOT_EXIST")); !os.IsNotExist(err) {
		t.Fatal("shell substitution executed")
	}
	return string(out)
}

func TestNativeCurlJSONExactAndShellSafe(t *testing.T) {
	var got []byte
	var auth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		auth = r.Header.Get("Authorization")
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()
	body, _ := json.Marshal(map[string]any{"model": "m", "input": strings.Repeat("long\n'$`$(touch SHOULD_NOT_EXIST)` ", 10000), "store": false, "temperature": 0})
	p := provider{Kind: "openai", BaseURL: upstream.URL, APIKey: "saved-secret", ProxyURL: "-"}
	preview := curlPreviewForTest(t, p, "responses.create", body, nil)
	script, _ := preview["curl"].(string)
	if strings.Contains(script, p.APIKey) {
		t.Fatal("saved key leaked")
	}
	runCurlExport(t, script)
	spec, err := buildNativeRequest(p, "responses.create", body, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, spec.jsonBody) {
		t.Fatalf("body changed: got %d want %d", len(got), len(spec.jsonBody))
	}
	if auth != "Bearer test-runtime-key" {
		t.Fatalf("auth %q", auth)
	}
}

func TestNativeCurlProviderHeadersAndGET(t *testing.T) {
	for _, kind := range []string{"openai", "compatible", "anthropic", "gemini", "xai"} {
		t.Run(kind, func(t *testing.T) {
			var got http.Header
			var method, query string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.Header.Clone()
				method = r.Method
				query = r.URL.Query().Get("after")
				w.Write([]byte(`{}`))
			}))
			defer upstream.Close()
			p := provider{Kind: kind, BaseURL: upstream.URL, APIKey: "saved-key", ProxyURL: "-"}
			preview := curlPreviewForTest(t, p, "models.list", json.RawMessage(`{"after":"$'\";@&`+"`"+`"}`), nil)
			script, _ := preview["curl"].(string)
			runCurlExport(t, script)
			if method != "GET" || query != "$'\";@&`" {
				t.Fatalf("request %s %q", method, query)
			}
			header := "Authorization"
			expected := "Bearer test-runtime-key"
			if kind == "anthropic" {
				header = "X-Api-Key"
				expected = "test-runtime-key"
				if got.Get("Anthropic-Version") != "2023-06-01" {
					t.Fatal("missing version")
				}
			}
			if kind == "gemini" {
				header = "X-Goog-Api-Key"
				expected = "test-runtime-key"
			}
			if got.Get(header) != expected {
				t.Fatalf("headers %v", got)
			}
		})
	}
}

func TestNativeCurlMultipartExactAndEscaped(t *testing.T) {
	var filename, contentType, purpose string
	var fileBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
			http.Error(w, "bad multipart", 400)
			return
		}
		defer r.MultipartForm.RemoveAll()
		purpose = r.FormValue("purpose")
		f, h, err := r.FormFile("file")
		if err != nil {
			t.Error(err)
			return
		}
		defer f.Close()
		filename = h.Filename
		contentType = h.Header.Get("Content-Type")
		fileBody, _ = io.ReadAll(f)
		if r.Header.Get("Anthropic-Beta") != "files-api-2025-04-14" {
			t.Error("missing beta header")
		}
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()
	name := "file\";headers=evil,quote'$.jsonl"
	local := filepath.Join(t.TempDir(), "local\"\\,;$(nope).jsonl")
	data := []byte("file content\x00\n")
	os.WriteFile(local, data, 0600)
	uploads := resourceUploads{"file": {{Filename: name, ContentType: "application/jsonl", Reader: bytes.NewReader(data)}}}
	raw, _ := json.Marshal(map[string]any{"purpose": "@/etc/passwd;type=text/plain\n'$(nope)"})
	preview := curlPreviewForTest(t, provider{Kind: "anthropic", BaseURL: upstream.URL, APIKey: "secret", ProxyURL: "-"}, "files.upload", raw, uploads)
	script, _ := preview["curl"].(string)
	runCurlExport(t, script, "FILE_1="+local)
	if filename != name || contentType != "application/jsonl" || !bytes.Equal(fileBody, data) || purpose != "@/etc/passwd;type=text/plain\n'$(nope)" {
		t.Fatalf("multipart changed: filename=%q type=%q purpose=%q data=%q", filename, contentType, purpose, fileBody)
	}
}

func TestNativeCurlLongMultipartFieldsAvoidArgumentLimit(t *testing.T) {
	long := strings.Repeat("prompt '\"$`\\\n;type=evil ", 10000)
	var got string
	reject := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
			http.Error(w, "bad form", 400)
			return
		}
		defer r.MultipartForm.RemoveAll()
		got = r.FormValue("prompt")
		if _, exists := r.MultipartForm.File["prompt"]; exists {
			t.Error("text field became a file upload")
		}
		if reject {
			http.Error(w, "rejected", 400)
			return
		}
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()
	local := filepath.Join(t.TempDir(), "image.png")
	os.WriteFile(local, []byte("png"), 0600)
	uploads := resourceUploads{"image": {{Filename: "image.png", ContentType: "image/png", Reader: strings.NewReader("png")}}}
	body, _ := json.Marshal(map[string]any{"model": "m", "prompt": long})
	preview := curlPreviewForTest(t, provider{Kind: "openai", BaseURL: upstream.URL, ProxyURL: "-"}, "images.edit", body, uploads)
	script, _ := preview["curl"].(string)
	temp := filepath.Join(t.TempDir(), "temporary '\"\\;directory")
	if err := os.Mkdir(temp, 0700); err != nil {
		t.Fatal(err)
	}
	runCurlExport(t, script, "FILE_1="+local, "TMPDIR="+temp)
	if got != long {
		t.Fatalf("long text changed: got %d want %d", len(got), len(long))
	}
	entries, err := os.ReadDir(temp)
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary text files remain: %v %v", entries, err)
	}
	reject = true
	runCurlExport(t, "if "+script+"then exit 1; else exit 0; fi\n", "FILE_1="+local, "TMPDIR="+temp)
	entries, err = os.ReadDir(temp)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed request left text files: %v %v", entries, err)
	}
}

func TestNativeCurlProxyAndUnsupportedMultipart(t *testing.T) {
	p := provider{Kind: "openai", BaseURL: "https://example.invalid", APIKey: "secret", ProxyURL: "http://user:password@localhost:8080"}
	preview := curlPreviewForTest(t, p, "models.list", nil, nil)
	script, _ := preview["curl"].(string)
	if !strings.Contains(script, "PROXY_URL") || strings.Contains(script, "password") {
		t.Fatalf("proxy export %s", script)
	}
	p.ProxyURL = ""
	preview = curlPreviewForTest(t, p, "models.list", nil, nil)
	script, _ = preview["curl"].(string)
	if strings.Contains(script, "--proxy") || strings.Contains(script, "--noproxy") {
		t.Fatal("environment proxy overridden")
	}
	uploads := resourceUploads{"file": {{Filename: "bad\nname", ContentType: "text/plain", Reader: strings.NewReader("x")}}}
	preview = curlPreviewForTest(t, p, "files.upload", json.RawMessage(`{"purpose":"batch"}`), uploads)
	if preview["curl_error"] == nil || preview["curl"] != "" {
		t.Fatalf("unsafe multipart not explained: %#v", preview)
	}
}

func TestNativeCurlGeminiTwoStages(t *testing.T) {
	var requests int
	var base string
	var uploaded []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("X-Goog-Api-Key") != "test-runtime-key" {
			t.Error("missing auth")
		}
		if requests == 1 {
			if r.Header.Get("X-Goog-Upload-Command") != "start" || r.Header.Get("X-Goog-Upload-Header-Content-Length") != "5" {
				t.Error("bad start headers")
			}
			w.Header().Set("X-Goog-Upload-URL", base+"/upload/session?token=generated")
			w.Write([]byte(`{}`))
			return
		}
		if r.Header.Get("X-Goog-Upload-Command") != "upload, finalize" || r.Header.Get("X-Goog-Upload-Offset") != "0" {
			t.Error("bad final headers")
		}
		uploaded, _ = io.ReadAll(r.Body)
		w.Write([]byte(`{"file":{}}`))
	}))
	defer upstream.Close()
	base = upstream.URL
	local := filepath.Join(t.TempDir(), "upload.bin")
	os.WriteFile(local, []byte("hello"), 0600)
	uploads := resourceUploads{"file": {{Filename: "upload.bin", ContentType: "text/plain", Reader: strings.NewReader("hello")}}}
	preview := curlPreviewForTest(t, provider{Kind: "gemini", BaseURL: base, APIKey: "saved-secret", ProxyURL: "-"}, "files.upload", json.RawMessage(`{"display_name":"example"}`), uploads)
	script, _ := preview["curl"].(string)
	runCurlExport(t, script, "FILE_1="+local)
	if requests != 2 || string(uploaded) != "hello" {
		t.Fatalf("requests=%d upload=%q", requests, uploaded)
	}
}

func TestNativeCurlRepeatedFilesPreserveTransportOrder(t *testing.T) {
	var names []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reader, err := r.MultipartReader()
		if err != nil {
			t.Error(err)
			return
		}
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Error(err)
				return
			}
			if part.FormName() == "image" {
				names = append(names, part.FileName())
			}
			io.Copy(io.Discard, part)
			part.Close()
		}
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()
	local := filepath.Join(t.TempDir(), "image.png")
	os.WriteFile(local, []byte("png"), 0600)
	uploads := resourceUploads{"image": {{Filename: "z-first.png", ContentType: "image/png", Reader: strings.NewReader("png")}, {Filename: "a-second.png", ContentType: "image/png", Reader: strings.NewReader("png")}}}
	preview := curlPreviewForTest(t, provider{Kind: "openai", BaseURL: upstream.URL, ProxyURL: "-"}, "images.edit", json.RawMessage(`{"model":"m","prompt":"edit"}`), uploads)
	script, _ := preview["curl"].(string)
	runCurlExport(t, script, "FILE_1="+local, "FILE_2="+local)
	if strings.Join(names, ",") != "z-first.png,a-second.png" {
		t.Fatalf("file order changed: %v", names)
	}
}

func TestNativeCurlGeminiRejectsCrossOriginUpload(t *testing.T) {
	var leaked bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true }))
	defer other.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Goog-Upload-URL", other.URL+"/steal")
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()
	local := filepath.Join(t.TempDir(), "file")
	os.WriteFile(local, []byte("x"), 0600)
	preview := curlPreviewForTest(t, provider{Kind: "gemini", BaseURL: upstream.URL, ProxyURL: "-"}, "files.upload", nil, resourceUploads{"file": {{Filename: "file", ContentType: "text/plain", Reader: strings.NewReader("x")}}})
	script, _ := preview["curl"].(string)
	// A failed upload URL check must stop before the second curl.
	runCurlExport(t, "if "+script+"then exit 1; else exit 0; fi\n", "FILE_1="+local)
	if leaked {
		t.Fatal("credentials sent to another origin")
	}
}
