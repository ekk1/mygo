package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func newResourcesClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := New(Config{APIKey: "test-key", BaseURL: server.URL + "/v1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(client.CloseIdleConnections)
	return client
}

func writeResourceJSON(t *testing.T, w http.ResponseWriter, value string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Request-ID", "req_resources")
	_, _ = io.WriteString(w, value)
}

func TestFilesLifecycleUsesNativeRoutesAndParameters(t *testing.T) {
	t.Helper()
	var calls int
	client := newResourcesClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q", got)
		}
		switch calls {
		case 1:
			if r.Method != http.MethodPost || r.URL.Path != "/v1/files" {
				t.Fatalf("upload request = %s %s", r.Method, r.URL.Path)
			}
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatalf("ParseMultipartForm: %v", err)
			}
			for key, want := range map[string]string{
				"purpose": "batch", "expires_after[anchor]": "created_at", "expires_after[seconds]": "3600",
			} {
				if got := r.FormValue(key); got != want {
					t.Errorf("form %s = %q, want %q", key, got, want)
				}
			}
			file, header, err := r.FormFile("file")
			if err != nil {
				t.Fatalf("FormFile: %v", err)
			}
			defer file.Close()
			data, _ := io.ReadAll(file)
			if header.Filename != "requests.jsonl" || string(data) != "{\"custom_id\":\"one\"}\n" {
				t.Errorf("uploaded file = (%q, %q)", header.Filename, data)
			}
			writeResourceJSON(t, w, `{"id":"file one","object":"file","bytes":20,"created_at":10,"expires_at":3610,"filename":"requests.jsonl","purpose":"batch"}`)
		case 2:
			if r.Method != http.MethodGet || r.URL.Path != "/v1/files" {
				t.Fatalf("list request = %s %s", r.Method, r.URL.Path)
			}
			want := "after=cursor+%26+next&limit=2&order=asc&purpose=batch"
			if got := r.URL.RawQuery; got != want {
				t.Errorf("query = %q, want %q", got, want)
			}
			writeResourceJSON(t, w, `{"object":"list","data":[{"id":"file one","object":"file","bytes":20,"created_at":10,"filename":"requests.jsonl","purpose":"batch"}],"first_id":"file one","last_id":"file one","has_more":false}`)
		case 3:
			if r.Method != http.MethodGet || r.URL.EscapedPath() != "/v1/files/file%20one" {
				t.Fatalf("get request = %s %s", r.Method, r.URL.EscapedPath())
			}
			writeResourceJSON(t, w, `{"id":"file one","object":"file","bytes":20,"created_at":10,"filename":"requests.jsonl","purpose":"batch"}`)
		case 4:
			if r.Method != http.MethodGet || r.URL.EscapedPath() != "/v1/files/file%20one/content" {
				t.Fatalf("download request = %s %s", r.Method, r.URL.EscapedPath())
			}
			w.Header().Set("X-Request-ID", "req_download")
			_, _ = io.WriteString(w, "{\"custom_id\":\"one\",\"response\":{}}\n")
		case 5:
			if r.Method != http.MethodDelete || r.URL.EscapedPath() != "/v1/files/file%20one" {
				t.Fatalf("delete request = %s %s", r.Method, r.URL.EscapedPath())
			}
			writeResourceJSON(t, w, `{"id":"file one","object":"file","deleted":true}`)
		default:
			t.Fatalf("unexpected request %d", calls)
		}
	})

	file, err := client.UploadFile(context.Background(), UploadFileParams{
		File:         Upload{Filename: "requests.jsonl", ContentType: "application/jsonl", Reader: strings.NewReader("{\"custom_id\":\"one\"}\n")},
		Purpose:      "batch",
		ExpiresAfter: &FileExpiration{Anchor: "created_at", Seconds: 3600},
	})
	if err != nil || file.ID != "file one" || file.ExpiresAt == nil || *file.ExpiresAt != 3610 || file.HTTP == nil {
		t.Fatalf("UploadFile = %#v, %v", file, err)
	}
	list, err := client.ListFiles(context.Background(), ListFilesParams{
		After: Ptr("cursor & next"), Limit: Ptr(2), Order: Ptr("asc"), Purpose: Ptr("batch"),
	})
	if err != nil || len(list.Data) != 1 || list.HTTP == nil {
		t.Fatalf("ListFiles = %#v, %v", list, err)
	}
	got, err := client.GetFile(context.Background(), "file one")
	if err != nil || got.ID != "file one" || got.HTTP.Header.Get("X-Request-ID") != "req_resources" {
		t.Fatalf("GetFile = %#v, %v", got, err)
	}
	var output bytes.Buffer
	httpResponse, err := client.DownloadFile(context.Background(), "file one", &output)
	if err != nil || output.String() != "{\"custom_id\":\"one\",\"response\":{}}\n" || httpResponse.Header.Get("X-Request-ID") != "req_download" {
		t.Fatalf("DownloadFile = %q, %#v, %v", output.String(), httpResponse, err)
	}
	deleted, err := client.DeleteFile(context.Background(), "file one")
	if err != nil || !deleted.Deleted || deleted.HTTP == nil {
		t.Fatalf("DeleteFile = %#v, %v", deleted, err)
	}
}

func TestContainersAndContainerFilesUseNativeRoutes(t *testing.T) {
	var calls int
	client := newResourcesClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch calls {
		case 1:
			if r.Method != http.MethodPost || r.URL.Path != "/v1/containers" {
				t.Fatalf("create container = %s %s", r.Method, r.URL.Path)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			want := map[string]any{"name": "work", "file_ids": []any{"file-global"}, "memory_limit": "4g", "expires_after": map[string]any{"anchor": "last_active_at", "minutes": float64(30)}, "network_policy": map[string]any{"type": "disabled"}}
			if !reflect.DeepEqual(body, want) {
				t.Errorf("create body = %#v, want %#v", body, want)
			}
			writeResourceJSON(t, w, `{"id":"cntr one","object":"container","created_at":1,"status":"running","name":"work","memory_limit":"4g"}`)
		case 2:
			if r.Method != http.MethodGet || r.URL.Path != "/v1/containers" || r.URL.RawQuery != "after=next+%26+one&limit=3&name=work&order=desc" {
				t.Fatalf("list containers = %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
			}
			writeResourceJSON(t, w, `{"object":"list","data":[],"first_id":"","last_id":"","has_more":false}`)
		case 3:
			if r.Method != http.MethodGet || r.URL.EscapedPath() != "/v1/containers/cntr%20one" {
				t.Fatalf("get container = %s %s", r.Method, r.URL.EscapedPath())
			}
			writeResourceJSON(t, w, `{"id":"cntr one","object":"container","created_at":1,"status":"running","name":"work"}`)
		case 4:
			if r.Method != http.MethodPost || r.URL.EscapedPath() != "/v1/containers/cntr%20one/files" {
				t.Fatalf("add file = %s %s", r.Method, r.URL.EscapedPath())
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if !reflect.DeepEqual(body, map[string]any{"file_id": "file-global"}) {
				t.Errorf("add file body = %#v", body)
			}
			writeResourceJSON(t, w, `{"id":"cfile one","object":"container.file","bytes":4,"container_id":"cntr one","created_at":2,"path":"/mnt/data/a.txt","source":"user"}`)
		case 5:
			if r.Method != http.MethodPost || r.URL.EscapedPath() != "/v1/containers/cntr%20one/files" {
				t.Fatalf("upload container file = %s %s", r.Method, r.URL.EscapedPath())
			}
			reader, err := r.MultipartReader()
			if err != nil {
				t.Fatal(err)
			}
			part, err := reader.NextPart()
			if err != nil {
				t.Fatal(err)
			}
			data, _ := io.ReadAll(part)
			if part.FormName() != "file" || part.FileName() != "b.txt" || string(data) != "data" {
				t.Errorf("multipart part = %q %q %q", part.FormName(), part.FileName(), data)
			}
			if _, err := reader.NextPart(); err != io.EOF {
				t.Errorf("second multipart part error = %v", err)
			}
			writeResourceJSON(t, w, `{"id":"cfile two","object":"container.file","bytes":4,"container_id":"cntr one","created_at":3,"path":"/mnt/data/b.txt","source":"user"}`)
		case 6:
			if r.Method != http.MethodGet || r.URL.EscapedPath() != "/v1/containers/cntr%20one/files" || r.URL.RawQuery != "after=cfile+one&limit=10&order=asc" {
				t.Fatalf("list container files = %s %s?%s", r.Method, r.URL.EscapedPath(), r.URL.RawQuery)
			}
			writeResourceJSON(t, w, `{"object":"list","data":[{"id":"cfile one","object":"container.file","bytes":4,"container_id":"cntr one","created_at":2,"path":"/mnt/data/a.txt","source":"user"}],"first_id":"cfile one","last_id":"cfile one","has_more":false}`)
		case 7:
			if r.Method != http.MethodGet || r.URL.EscapedPath() != "/v1/containers/cntr%20one/files/cfile%20one" {
				t.Fatalf("get container file = %s %s", r.Method, r.URL.EscapedPath())
			}
			writeResourceJSON(t, w, `{"id":"cfile one","object":"container.file","bytes":4,"container_id":"cntr one","created_at":2,"path":"/mnt/data/a.txt","source":"user"}`)
		case 8:
			if r.Method != http.MethodGet || r.URL.EscapedPath() != "/v1/containers/cntr%20one/files/cfile%20one/content" {
				t.Fatalf("download container file = %s %s", r.Method, r.URL.EscapedPath())
			}
			_, _ = io.WriteString(w, "data")
		case 9:
			if r.Method != http.MethodDelete || r.URL.EscapedPath() != "/v1/containers/cntr%20one/files/cfile%20one" {
				t.Fatalf("delete container file = %s %s", r.Method, r.URL.EscapedPath())
			}
			writeResourceJSON(t, w, `{"id":"cfile one","object":"container.file.deleted","deleted":true}`)
		case 10:
			if r.Method != http.MethodDelete || r.URL.EscapedPath() != "/v1/containers/cntr%20one" {
				t.Fatalf("delete container = %s %s", r.Method, r.URL.EscapedPath())
			}
			writeResourceJSON(t, w, `{"id":"cntr one","object":"container.deleted","deleted":true}`)
		default:
			t.Fatalf("unexpected request %d", calls)
		}
	})

	container, err := client.CreateContainer(context.Background(), CreateContainerParams{
		Name: "work", FileIDs: []string{"file-global"}, MemoryLimit: Ptr("4g"),
		ExpiresAfter: &ContainerExpiration{Anchor: "last_active_at", Minutes: 30},
		Extra:        map[string]any{"network_policy": map[string]any{"type": "disabled"}},
	})
	if err != nil || container.ID != "cntr one" || container.HTTP == nil {
		t.Fatalf("CreateContainer = %#v, %v", container, err)
	}
	containers, err := client.ListContainers(context.Background(), ListContainersParams{After: Ptr("next & one"), Limit: Ptr(3), Name: Ptr("work"), Order: Ptr("desc")})
	if err != nil || containers.HTTP == nil {
		t.Fatalf("ListContainers = %#v, %v", containers, err)
	}
	if _, err := client.GetContainer(context.Background(), "cntr one"); err != nil {
		t.Fatalf("GetContainer: %v", err)
	}
	added, err := client.AddContainerFile(context.Background(), "cntr one", AddContainerFileParams{FileID: "file-global"})
	if err != nil || added.ID != "cfile one" {
		t.Fatalf("AddContainerFile = %#v, %v", added, err)
	}
	uploaded, err := client.UploadContainerFile(context.Background(), "cntr one", Upload{Filename: "b.txt", ContentType: "text/plain", Reader: strings.NewReader("data")})
	if err != nil || uploaded.ID != "cfile two" {
		t.Fatalf("UploadContainerFile = %#v, %v", uploaded, err)
	}
	files, err := client.ListContainerFiles(context.Background(), "cntr one", ListContainerFilesParams{After: Ptr("cfile one"), Limit: Ptr(10), Order: Ptr("asc")})
	if err != nil || len(files.Data) != 1 || files.HTTP == nil {
		t.Fatalf("ListContainerFiles = %#v, %v", files, err)
	}
	if _, err := client.GetContainerFile(context.Background(), "cntr one", "cfile one"); err != nil {
		t.Fatalf("GetContainerFile: %v", err)
	}
	var content bytes.Buffer
	if _, err := client.DownloadContainerFile(context.Background(), "cntr one", "cfile one", &content); err != nil || content.String() != "data" {
		t.Fatalf("DownloadContainerFile = %q, %v", content.String(), err)
	}
	deletedFile, err := client.DeleteContainerFile(context.Background(), "cntr one", "cfile one")
	if err != nil || !deletedFile.Deleted || deletedFile.HTTP == nil {
		t.Fatalf("DeleteContainerFile = %#v, %v", deletedFile, err)
	}
	deletedContainer, err := client.DeleteContainer(context.Background(), "cntr one")
	if err != nil || !deletedContainer.Deleted || deletedContainer.HTTP == nil {
		t.Fatalf("DeleteContainer = %#v, %v", deletedContainer, err)
	}
}

func TestBatchesLifecycleAndRawJSONLDownload(t *testing.T) {
	var calls int
	client := newResourcesClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch calls {
		case 1:
			if r.Method != http.MethodPost || r.URL.Path != "/v1/batches" {
				t.Fatalf("create batch = %s %s", r.Method, r.URL.Path)
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			want := map[string]any{"input_file_id": "file-in", "endpoint": "/v1/responses", "completion_window": "24h", "metadata": map[string]any{"job": "nightly"}, "output_expires_after": map[string]any{"anchor": "created_at", "seconds": float64(7200)}, "custom": "kept"}
			if !reflect.DeepEqual(body, want) {
				t.Errorf("create batch body = %#v, want %#v", body, want)
			}
			writeResourceJSON(t, w, `{"id":"batch one","object":"batch","input_file_id":"file-in","endpoint":"/v1/responses","completion_window":"24h","status":"validating","created_at":10,"request_counts":{"total":0,"completed":0,"failed":0}}`)
		case 2:
			if r.Method != http.MethodGet || r.URL.Path != "/v1/batches" || r.URL.RawQuery != "after=batch+%26+zero&limit=4" {
				t.Fatalf("list batches = %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
			}
			writeResourceJSON(t, w, `{"object":"list","data":[],"first_id":"","last_id":"","has_more":false}`)
		case 3:
			if r.Method != http.MethodGet || r.URL.EscapedPath() != "/v1/batches/batch%20one" {
				t.Fatalf("get batch = %s %s", r.Method, r.URL.EscapedPath())
			}
			writeResourceJSON(t, w, `{"id":"batch one","object":"batch","input_file_id":"file-in","endpoint":"/v1/responses","completion_window":"24h","status":"completed","created_at":10,"output_file_id":"file-out","error_file_id":"file-errors","errors":{"object":"list","data":[{"code":"bad","line":2,"message":"invalid","param":"body"}]},"request_counts":{"total":2,"completed":1,"failed":1},"usage":{"input_tokens":4,"output_tokens":2,"total_tokens":6}}`)
		case 4:
			if r.Method != http.MethodPost || r.URL.EscapedPath() != "/v1/batches/batch%20one/cancel" {
				t.Fatalf("cancel batch = %s %s", r.Method, r.URL.EscapedPath())
			}
			body, _ := io.ReadAll(r.Body)
			if len(body) != 0 {
				t.Errorf("cancel batch body = %q, want empty", body)
			}
			writeResourceJSON(t, w, `{"id":"batch one","object":"batch","input_file_id":"file-in","endpoint":"/v1/responses","completion_window":"24h","status":"cancelling","created_at":10}`)
		case 5:
			if r.Method != http.MethodGet || r.URL.EscapedPath() != "/v1/files/file-out/content" {
				t.Fatalf("batch output download = %s %s", r.Method, r.URL.EscapedPath())
			}
			_, _ = io.WriteString(w, "{\"custom_id\":\"one\",\"response\":{\"status_code\":200}}\n")
		default:
			t.Fatalf("unexpected request %d", calls)
		}
	})

	created, err := client.CreateBatch(context.Background(), CreateBatchParams{
		InputFileID: "file-in", Endpoint: "/v1/responses", CompletionWindow: "24h",
		Metadata: map[string]string{"job": "nightly"}, OutputExpiresAfter: &FileExpiration{Anchor: "created_at", Seconds: 7200},
		Extra: map[string]any{"custom": "kept"},
	})
	if err != nil || created.ID != "batch one" || created.HTTP == nil {
		t.Fatalf("CreateBatch = %#v, %v", created, err)
	}
	listed, err := client.ListBatches(context.Background(), ListBatchesParams{After: Ptr("batch & zero"), Limit: Ptr(4)})
	if err != nil || listed.HTTP == nil {
		t.Fatalf("ListBatches = %#v, %v", listed, err)
	}
	batch, err := client.GetBatch(context.Background(), "batch one")
	if err != nil || batch.OutputFileID == nil || *batch.OutputFileID != "file-out" || batch.Errors == nil || len(batch.Errors.Data) != 1 || batch.Usage == nil || batch.Usage.TotalTokens != 6 {
		t.Fatalf("GetBatch = %#v, %v", batch, err)
	}
	cancelled, err := client.CancelBatch(context.Background(), "batch one")
	if err != nil || cancelled.Status != "cancelling" || cancelled.HTTP == nil {
		t.Fatalf("CancelBatch = %#v, %v", cancelled, err)
	}
	var results bytes.Buffer
	if _, err := client.DownloadFile(context.Background(), *batch.OutputFileID, &results); err != nil || results.String() != "{\"custom_id\":\"one\",\"response\":{\"status_code\":200}}\n" {
		t.Fatalf("batch DownloadFile = %q, %v", results.String(), err)
	}
}

func TestResourceMethodsPreserveHTTPErrorResponses(t *testing.T) {
	client := newResourcesClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-ID", "req_failed")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"bad resource"}}`)
	})

	file, err := client.GetFile(context.Background(), "file-bad")
	if err == nil || file == nil || file.HTTP == nil || file.HTTP.StatusCode != http.StatusBadRequest || string(file.HTTP.Body) != `{"error":{"message":"bad resource"}}` {
		t.Fatalf("GetFile error result = %#v, %v", file, err)
	}
	container, err := client.GetContainer(context.Background(), "cntr-bad")
	if err == nil || container == nil || container.HTTP == nil || container.HTTP.Header.Get("X-Request-ID") != "req_failed" {
		t.Fatalf("GetContainer error result = %#v, %v", container, err)
	}
	batch, err := client.GetBatch(context.Background(), "batch-bad")
	if err == nil || batch == nil || batch.HTTP == nil || batch.HTTP.StatusCode != http.StatusBadRequest {
		t.Fatalf("GetBatch error result = %#v, %v", batch, err)
	}
}

func TestResourceMethodsRejectUnsafeOpaqueIDsBeforeRequest(t *testing.T) {
	var called bool
	client := newResourcesClient(t, func(w http.ResponseWriter, r *http.Request) { called = true })
	for name, invoke := range map[string]func() error{
		"file slash":           func() error { _, err := client.GetFile(context.Background(), "a/b"); return err },
		"container dot":        func() error { _, err := client.GetContainer(context.Background(), ".."); return err },
		"container file slash": func() error { _, err := client.GetContainerFile(context.Background(), "cntr", "a/b"); return err },
		"batch empty":          func() error { _, err := client.GetBatch(context.Background(), ""); return err },
	} {
		t.Run(name, func(t *testing.T) {
			if err := invoke(); err == nil {
				t.Fatal("expected invalid ID error")
			}
		})
	}
	if called {
		t.Fatal("invalid ID caused an HTTP request")
	}
}

func TestResourceRequestExtraCannotOverrideTypedField(t *testing.T) {
	client := newResourcesClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("collision caused an HTTP request")
	})
	_, err := client.CreateBatch(context.Background(), CreateBatchParams{
		InputFileID: "file-in", Endpoint: "/v1/responses", CompletionWindow: "24h",
		Extra: map[string]any{"endpoint": "/v1/chat/completions"},
	})
	if err == nil || !strings.Contains(err.Error(), "endpoint") {
		t.Fatalf("CreateBatch collision error = %v", err)
	}
}
