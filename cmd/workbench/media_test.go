package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mediaTestApp(t *testing.T) (*app, http.Handler) {
	t.Helper()
	a, s, err := newApp(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.stopAccepting(); a.active.Wait(); s.Close() })
	return a, s
}

// The external process is replaced; task persistence and asset IO remain real.
func mediaExecutable(t *testing.T, name, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/bash\nset -eu\n"+script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
}
func mediaSubmit(t *testing.T, h http.Handler, path string, body any) string {
	t.Helper()
	w := backgroundRequest(t, h, "POST", path, body)
	if w.Code != 202 {
		t.Fatalf("submit: %d %s", w.Code, w.Body.String())
	}
	var v struct {
		Task generationTask `json:"task"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v.Task.ID
}
func mediaWait(t *testing.T, a *app, id, status string) generationTask {
	t.Helper()
	task, err := a.task(id)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-task.done:
	case <-time.After(5 * time.Second):
		t.Fatal("task did not stop")
	}
	v := task.snapshot(false)
	if v.Status != status {
		t.Fatalf("task status: %+v", v)
	}
	return v
}
func TestMediaDiscoveryAndDownload(t *testing.T) {
	mediaExecutable(t, "yt-dlp", `
args=("$@")
[[ " ${args[*]} " == *" --ignore-config "* ]]
[[ " ${args[*]} " == *" --no-playlist "* ]]
[[ "${args[-1]}" == 'https://example.com/watch?v=1&x=$(nope)' ]]
for ((i=0;i<$#;i++)); do
 if [[ "${args[i]}" == --proxy ]]; then [[ "${args[i+1]}" == 'socks5://localhost:1080' ]]; fi
done
if [[ " ${args[*]} " == *" --dump-single-json "* ]]; then
 echo 'a warning on stderr' >&2
 echo '{"id":"1","title":"Podcast","duration":120,"formats":[{"format_id":"a","ext":"m4a","vcodec":"none","acodec":"aac","abr":128},{"format_id":"v","ext":"mp4","vcodec":"avc1","acodec":"none","height":1080,"filesize_approx":1000000}],"url":"https://secret.example/token"}'
else
 [[ " ${args[*]} " == *" --format a "* ]]
 printf '\000\000\000\030ftypmp42\000\000\000\000mp42isom' > "$MEDIA_DIR/Podcast.m4a"
 printf '{"filepath":"%s/Podcast.m4a","vcodec":"none","acodec":"aac"}\n' "$MEDIA_DIR" > "$MEDIA_MANIFEST"
fi
`)
	a, h := mediaTestApp(t)
	in := map[string]string{"url": "https://example.com/watch?v=1&x=$(nope)", "proxy": "socks5://localhost:1080", "format": "a"}
	w := backgroundRequest(t, h, "POST", "/api/media/formats", in)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"format_id":"a"`) || strings.Contains(w.Body.String(), "secret.example") {
		t.Fatalf("formats: %d %s", w.Code, w.Body.String())
	}
	id := mediaSubmit(t, h, "/api/media/download", in)
	v := mediaWait(t, a, id, "complete")
	if len(v.AssetIDs) != 1 {
		t.Fatalf("missing asset: %+v", v)
	}
	asset, err := a.assets.Get(v.AssetIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if asset.Name != "Podcast.m4a" || asset.ContentType != "audio/mp4" || asset.Source["kind"] != "yt-dlp" || asset.Source["url"] != in["url"] || asset.Favorite {
		t.Fatalf("asset: %+v", asset)
	}
	if strings.Contains(string(v.Result), "socks5") {
		t.Fatal("proxy persisted in task")
	}
}
func TestMediaValidationAndPositions(t *testing.T) {
	a, h := mediaTestApp(t)
	for _, url := range []string{"", "--exec=oops", "file:///etc/passwd"} {
		w := backgroundRequest(t, h, "POST", "/api/media/formats", map[string]string{"url": url})
		if w.Code != 400 {
			t.Fatalf("invalid url %q: %d", url, w.Code)
		}
	}
	asset, err := a.assets.Add("clip.mp4", "video/mp4", nil, strings.NewReader("\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00mp42isom"), 0)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := "/api/assets/" + asset.ID + "/position"
	w := backgroundRequest(t, h, "GET", endpoint, nil)
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != "null" {
		t.Fatalf("unsaved: %d %s", w.Code, w.Body.String())
	}
	for _, body := range []any{map[string]any{}, map[string]any{"seconds": nil}, map[string]any{"seconds": -1}, map[string]any{"seconds": "3"}} {
		if w := backgroundRequest(t, h, "POST", endpoint, body); w.Code != 400 {
			t.Fatalf("invalid position accepted: %s", w.Body.String())
		}
	}
	if w := backgroundRequest(t, h, "POST", endpoint, map[string]any{"seconds": 12.5}); w.Code != 204 {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	// A second application reads the durable position, not a browser-local value.
	b, s, err := newApp(a.store.dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_ = b
	w = backgroundRequest(t, s, "GET", endpoint, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "12.5") {
		t.Fatalf("restored: %d %s", w.Code, w.Body.String())
	}
	w = backgroundRequest(t, h, "GET", "/media/player/"+asset.ID, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "data-webui-video") {
		t.Fatalf("player: %d %s", w.Code, w.Body.String())
	}
}
func TestMediaExtractionAndCancellation(t *testing.T) {
	mediaExecutable(t, "ffmpeg", `
[[ " $* " == *" -map 0:a:0 "* ]]
[[ " $* " == *" -vn "* ]]
printf 'ID3audio' > "$MEDIA_OUTPUT"
`)
	a, h := mediaTestApp(t)
	asset, err := a.assets.Add("clip.mp4", "video/mp4", nil, strings.NewReader("\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00mp42isom"), 0)
	if err != nil {
		t.Fatal(err)
	}
	id := mediaSubmit(t, h, "/api/assets/"+asset.ID+"/extract-audio", map[string]any{})
	v := mediaWait(t, a, id, "complete")
	if len(v.AssetIDs) != 1 {
		t.Fatalf("missing extracted asset: %+v", v)
	}
	audio, err := a.assets.Get(v.AssetIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if audio.Name != "clip.mp3" || audio.Source["asset_id"] != asset.ID || audio.ContentType != "audio/mpeg" {
		t.Fatalf("audio: %+v", audio)
	}
	mediaExecutable(t, "yt-dlp", `sleep 60`)
	id = mediaSubmit(t, h, "/api/media/download", map[string]string{"url": "https://example.com/video"})
	w := backgroundRequest(t, h, "POST", "/api/tasks/"+id+"/cancel", map[string]any{})
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	v = mediaWait(t, a, id, "cancelled")
	if len(v.AssetIDs) != 0 {
		t.Fatal("cancelled task imported asset")
	}
}
func TestMediaFailureDoesNotImportPartialFiles(t *testing.T) {
	mediaExecutable(t, "yt-dlp", `printf partial > "$MEDIA_DIR/partial.mp4"; echo 'download failed' >&2; exit 7`)
	a, h := mediaTestApp(t)
	id := mediaSubmit(t, h, "/api/media/download", map[string]string{"url": "https://example.com/video"})
	v := mediaWait(t, a, id, "error")
	if len(a.assets.List(false)) != 0 || !strings.Contains(v.Error, "download failed") {
		t.Fatalf("failure: %+v", v)
	}
}

func TestMediaDeletingAssetCleansPosition(t *testing.T) {
	a, h := mediaTestApp(t)
	asset, err := a.assets.Add("clip.mp4", "video/mp4", nil, strings.NewReader("\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00mp42isom"), 0)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := "/api/assets/" + asset.ID
	if w := backgroundRequest(t, h, "POST", endpoint+"/position", map[string]any{"seconds": 1}); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if w := backgroundRequest(t, h, "DELETE", endpoint, nil); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if _, err := os.Stat(filepath.Join(a.store.dir, "positions", asset.ID+".json")); !os.IsNotExist(err) {
		t.Fatalf("position remains: %v", err)
	}
	if w := backgroundRequest(t, h, "POST", endpoint+"/position", map[string]any{"seconds": 2}); w.Code != 404 {
		t.Fatal(w.Code)
	}
}

func TestMediaRejectsUnsafeAndIncompleteOutput(t *testing.T) {
	for _, mode := range []string{"outside", "multiple", "empty"} {
		t.Run(mode, func(t *testing.T) {
			outside := filepath.Join(t.TempDir(), "outside.mp4")
			if err := os.WriteFile(outside, []byte("external asset"), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("MEDIA_TEST_OUTSIDE", outside)
			t.Setenv("MEDIA_TEST_MODE", mode)
			mediaExecutable(t, "yt-dlp", `
case "$MEDIA_TEST_MODE" in
 outside) printf '{"filepath":"%s","vcodec":"h264"}' "$MEDIA_TEST_OUTSIDE" > "$MEDIA_MANIFEST" ;;
 multiple) printf '{}\n{}\n' > "$MEDIA_MANIFEST" ;;
 empty) : > "$MEDIA_DIR/empty.mp4"; printf '{"filepath":"%s/empty.mp4"}' "$MEDIA_DIR" > "$MEDIA_MANIFEST" ;;
esac
`)
			a, h := mediaTestApp(t)
			id := mediaSubmit(t, h, "/api/media/download", map[string]string{"url": "https://example.com/video"})
			mediaWait(t, a, id, "error")
			if len(a.assets.List(false)) != 0 {
				t.Fatal("invalid output imported")
			}
		})
	}
}
func TestMediaRunningProcessCancellationAndCredentialRedaction(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "started")
	t.Setenv("MEDIA_TEST_STARTED", marker)
	mediaExecutable(t, "yt-dlp", `touch "$MEDIA_TEST_STARTED"; sleep 60`)
	a, h := mediaTestApp(t)
	id := mediaSubmit(t, h, "/api/media/download", map[string]string{"url": "https://example.com/video"})
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("command never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	backgroundRequest(t, h, "POST", "/api/tasks/"+id+"/cancel", map[string]any{})
	mediaWait(t, a, id, "cancelled")
	mediaExecutable(t, "yt-dlp", `echo "$MEDIA_PROXY" >&2; echo 'pw-secret' >&2; exit 1`)
	id = mediaSubmit(t, h, "/api/media/download", map[string]string{"url": "https://example.com/video", "proxy": "http://name:pw-secret@localhost:3128"})
	v := mediaWait(t, a, id, "error")
	if strings.Contains(v.Error, "pw-secret") {
		t.Fatal("proxy credential leaked")
	}
}
