package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/ekk1/mygo/utils/assetstore"
	"github.com/ekk1/mygo/utils/executil"
	"github.com/ekk1/mygo/utils/httpserver"
)

const mediaMaxBytes int64 = 8 << 30

type mediaInput struct {
	URL    string `json:"url"`
	Proxy  string `json:"proxy"`
	Format string `json:"format"`
}
type mediaFormat struct {
	ID         string   `json:"format_id"`
	Ext        string   `json:"ext"`
	VCodec     string   `json:"vcodec"`
	ACodec     string   `json:"acodec"`
	Width      *float64 `json:"width"`
	Height     *float64 `json:"height"`
	FPS        *float64 `json:"fps"`
	ABR        *float64 `json:"abr"`
	TBR        *float64 `json:"tbr"`
	Size       *float64 `json:"filesize"`
	ApproxSize *float64 `json:"filesize_approx"`
	Note       string   `json:"format_note"`
}
type mediaInfo struct {
	ID       string        `json:"id"`
	Title    string        `json:"title"`
	Duration *float64      `json:"duration"`
	Type     string        `json:"_type,omitempty"`
	Formats  []mediaFormat `json:"formats"`
}

func (a *app) registerMedia(s *httpserver.Server) error {
	for route, handler := range map[string]http.HandlerFunc{
		"POST /api/media/formats":             a.mediaFormatsAPI,
		"POST /api/media/download":            a.mediaDownloadAPI,
		"POST /api/assets/{id}/extract-audio": a.mediaExtractAPI,
		"GET /api/assets/{id}/position":       a.mediaPositionAPI,
		"POST /api/assets/{id}/position":      a.mediaPositionAPI,
		"GET /media/player/{id}":              a.mediaPlayer,
	} {
		if err := s.HandleFunc(route, handler); err != nil {
			return err
		}
	}
	return nil
}
func readMediaInput(w http.ResponseWriter, r *http.Request) (mediaInput, error) {
	var in mediaInput
	if err := decodeJSON(w, r, &in); err != nil {
		return in, err
	}
	in.URL, in.Proxy, in.Format = strings.TrimSpace(in.URL), strings.TrimSpace(in.Proxy), strings.TrimSpace(in.Format)
	u, err := url.Parse(in.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || strings.ContainsAny(in.URL, "\x00\r\n") {
		return in, fmt.Errorf("请填写 HTTP 或 HTTPS 媒体链接（不含登录凭据）")
	}
	if in.Proxy != "" && in.Proxy != "-" {
		p, err := url.Parse(in.Proxy)
		if err != nil || p.Hostname() == "" || !slices.Contains([]string{"http", "https", "socks5", "socks5h"}, p.Scheme) || strings.ContainsAny(in.Proxy, "\x00\r\n") {
			return in, fmt.Errorf("代理应为 http/https/socks5/socks5h 地址，或 - 表示直连")
		}
	}
	if len(in.Format) > 1024 || strings.ContainsAny(in.Format, "\x00\r\n,") {
		return in, fmt.Errorf("格式表达式无效；一次只下载一个结果，不支持逗号分隔的多格式")
	}
	if in.Format == "" {
		in.Format = "bv*+ba/b"
	}
	return in, nil
}

// Fixed Bash programs receive dynamic values only as quoted environment values.
// executil supplies process-group cancellation, including yt-dlp's ffmpeg child.
const mediaYTDLP = `umask 077
args=(--ignore-config --no-playlist --playlist-items 1 --no-progress --no-colors --no-cache-dir)
if [[ "$MEDIA_PROXY" == - ]]; then args+=(--proxy ""); elif [[ -n "$MEDIA_PROXY" ]]; then args+=(--proxy "$MEDIA_PROXY"); fi
`

func mediaEnv(in mediaInput, dir string) map[string]string {
	return map[string]string{"MEDIA_URL": in.URL, "MEDIA_PROXY": in.Proxy, "MEDIA_FORMAT": in.Format, "MEDIA_DIR": dir, "MEDIA_MANIFEST": filepath.Join(dir, "result.json"), "MEDIA_INFO": filepath.Join(dir, "info.json")}
}
func runMediaCommand(ctx context.Context, command string, env map[string]string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var tail string
	p, err := executil.StartEnv(command, env, func(chunk string) {
		tail += chunk
		if len(tail) > 32768 {
			tail = tail[len(tail)-32768:]
		}
	})
	if err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- p.Wait() }()
	select {
	case err = <-done:
	case <-ctx.Done():
		stopErr := p.Stop()
		err = errors.Join(ctx.Err(), stopErr, <-done)
	}
	if err != nil {
		// Command diagnostics can repeat URLs; do not persist proxy credentials.
		if proxy := env["MEDIA_PROXY"]; proxy != "" && proxy != "-" {
			tail = strings.ReplaceAll(tail, proxy, "[代理]")
			if u, e := url.Parse(proxy); e == nil && u.User != nil {
				tail = strings.ReplaceAll(tail, u.User.String(), "[凭据]")
				if password, ok := u.User.Password(); ok && password != "" {
					tail = strings.ReplaceAll(tail, password, "[凭据]")
				}
			}
		}
		return fmt.Errorf("命令执行失败（请确认运行环境中的 yt-dlp / ffmpeg 可用）: %w\n%s", err, strings.TrimSpace(tail))
	}
	return nil
}
func readMediaJSON(path string, value any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 16<<20+1))
	if err != nil {
		return err
	}
	if len(data) > 16<<20 {
		return fmt.Errorf("媒体信息超过 16 MiB")
	}
	return json.Unmarshal(data, value)
}
func (a *app) mediaFormatsAPI(w http.ResponseWriter, r *http.Request) {
	in, err := readMediaInput(w, r)
	if err != nil {
		apiError(w, 400, err)
		return
	}
	dir, err := os.MkdirTemp("", "workbench-media-*")
	if err != nil {
		apiError(w, 500, err)
		return
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	env := mediaEnv(in, dir)
	err = runMediaCommand(ctx, mediaYTDLP+`yt-dlp "${args[@]}" --dump-single-json --skip-download -- "$MEDIA_URL" > "$MEDIA_INFO"`, env)
	var info mediaInfo
	if err == nil {
		err = readMediaJSON(env["MEDIA_INFO"], &info)
	}
	if err == nil && (info.Type == "playlist" || info.Type == "multi_video") {
		err = fmt.Errorf("请提供单条媒体链接，不支持播放列表")
	}
	if err != nil {
		apiError(w, 400, err)
		return
	}
	if info.Formats == nil {
		info.Formats = []mediaFormat{}
	}
	writeJSON(w, 200, info)
}
func (a *app) mediaDownloadAPI(w http.ResponseWriter, r *http.Request) {
	in, err := readMediaInput(w, r)
	if err != nil {
		apiError(w, 400, err)
		return
	}
	task, ctx, err := a.reserveTask(provider{}, "media.download", "download", in.URL, "", false)
	if err != nil {
		apiError(w, errorStatus(err), err)
		return
	}
	go a.runMediaTask(task, ctx, nil, func(ctx context.Context, dir string) (assetstore.Asset, error) {
		env := mediaEnv(in, dir)
		command := mediaYTDLP + `yt-dlp "${args[@]}" --no-simulate --max-filesize 8G --format "$MEDIA_FORMAT" --output "$MEDIA_DIR/%(title).180B [%(id)s].%(ext)s" --print-to-file 'after_move:{"filepath":%(filepath)j,"vcodec":%(vcodec)j,"acodec":%(acodec)j}' "$MEDIA_MANIFEST" -- "$MEDIA_URL"`
		if err := runMediaCommand(ctx, command, env); err != nil {
			return assetstore.Asset{}, err
		}
		var output struct {
			Path   string `json:"filepath"`
			VCodec string `json:"vcodec"`
			ACodec string `json:"acodec"`
		}
		if err := readMediaJSON(env["MEDIA_MANIFEST"], &output); err != nil {
			return assetstore.Asset{}, fmt.Errorf("未取得单个完整下载文件: %w", err)
		}
		path, err := filepath.EvalSymlinks(output.Path)
		if err != nil {
			return assetstore.Asset{}, err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
			return assetstore.Asset{}, fmt.Errorf("下载结果不在任务目录内")
		}
		typ := mime.TypeByExtension(filepath.Ext(output.Path))
		if output.VCodec == "none" {
			switch strings.ToLower(filepath.Ext(output.Path)) {
			case ".mp4", ".m4a":
				typ = "audio/mp4"
			case ".webm":
				typ = "audio/webm"
			case ".ogg", ".opus":
				typ = "audio/ogg"
			}
		}
		return a.importMedia(ctx, path, filepath.Base(output.Path), typ, assetstore.Source{"kind": "yt-dlp", "url": in.URL, "format": in.Format, "task_id": task.snapshot(true).ID})
	})
	a.respondTask(w, r, task, nil)
}
func (a *app) importMedia(ctx context.Context, path, name, contentType string, source assetstore.Source) (assetstore.Asset, error) {
	if err := ctx.Err(); err != nil {
		return assetstore.Asset{}, err
	}
	f, err := os.Open(path)
	if err != nil {
		return assetstore.Asset{}, err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return assetstore.Asset{}, err
	}
	if !stat.Mode().IsRegular() || stat.Size() == 0 {
		return assetstore.Asset{}, fmt.Errorf("媒体输出不是非空普通文件")
	}
	return a.assets.Add(name, contentType, source, &mediaReader{ctx: ctx, r: f}, mediaMaxBytes)
}

type mediaReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *mediaReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

func (a *app) mediaExtractAPI(w http.ResponseWriter, r *http.Request) {
	var in struct{}
	if err := decodeJSON(w, r, &in); err != nil {
		apiError(w, 400, err)
		return
	}
	asset, file, err := a.assets.OpenContent(r.PathValue("id"))
	if err != nil {
		apiError(w, assetErrorStatus(err), err)
		return
	}
	if !strings.HasPrefix(asset.ContentType, "video/") && !strings.HasPrefix(asset.ContentType, "audio/") {
		file.Close()
		apiError(w, 400, fmt.Errorf("请选择音频或视频资产"))
		return
	}
	task, ctx, err := a.reserveTask(provider{}, "media.extract-audio", "extract-audio", asset.Name, "", false)
	if err != nil {
		file.Close()
		apiError(w, errorStatus(err), err)
		return
	}
	go a.runMediaTask(task, ctx, []string{asset.ID}, func(ctx context.Context, dir string) (assetstore.Asset, error) {
		inputPath := filepath.Join(dir, "input")
		input, err := os.OpenFile(inputPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return assetstore.Asset{}, err
		}
		_, err = io.Copy(input, &mediaReader{ctx: ctx, r: file})
		err = errors.Join(err, input.Close())
		if err != nil {
			return assetstore.Asset{}, err
		}
		output := filepath.Join(dir, "audio.mp3")
		err = runMediaCommand(ctx, `umask 077; ffmpeg -nostdin -hide_banner -loglevel error -y -protocol_whitelist file,pipe -i "$MEDIA_INPUT" -map 0:a:0 -vn -c:a libmp3lame -q:a 2 "$MEDIA_OUTPUT"`, map[string]string{"MEDIA_INPUT": inputPath, "MEDIA_OUTPUT": output})
		if err != nil {
			return assetstore.Asset{}, err
		}
		name := strings.TrimSuffix(asset.Name, filepath.Ext(asset.Name)) + ".mp3"
		return a.importMedia(ctx, output, name, "audio/mpeg", assetstore.Source{"kind": "ffmpeg", "asset_id": asset.ID, "task_id": task.snapshot(true).ID})
	}, file.Close)
	a.respondTask(w, r, task, nil)
}

func (a *app) runMediaTask(t *taskEntry, ctx context.Context, inputs []string, run func(context.Context, string) (assetstore.Asset, error), cleanup ...func() error) {
	defer a.active.Done()
	defer close(t.done)
	defer t.cancel(nil)
	for _, fn := range cleanup {
		defer fn()
	}
	t.mu.Lock()
	v := t.data
	v.Status, v.UpdatedAt, v.InputAssetIDs = "running", now(), inputs
	err := saveKV(filepath.Join(a.store.dir, "tasks", v.ID+".json"), v)
	if err == nil {
		t.data = v
	}
	t.mu.Unlock()
	var asset assetstore.Asset
	if err == nil && ctx.Err() == nil {
		var dir string
		dir, err = os.MkdirTemp("", "workbench-media-*")
		if err == nil {
			asset, err = run(ctx, dir)
			err = errors.Join(err, os.RemoveAll(dir))
		}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if ctx.Err() != nil && asset.ID != "" {
		err = errors.Join(err, a.assets.Delete(asset.ID))
		asset = assetstore.Asset{}
	}
	v = t.data
	v.Status, err = taskResultStatus(ctx, err)
	v.UpdatedAt = now()
	if asset.ID != "" {
		v.AssetIDs = []string{asset.ID}
		v.Result, _ = json.Marshal(map[string]any{"assets": []assetstore.Asset{asset}})
	}
	if err != nil {
		v.Error = err.Error()
	}
	if e := saveKV(filepath.Join(a.store.dir, "tasks", v.ID+".json"), v); e != nil {
		v.Status = "error"
		v.Error = errors.Join(err, fmt.Errorf("保存任务结果失败: %w", e)).Error()
	}
	t.data = v
}
