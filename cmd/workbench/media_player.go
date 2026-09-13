package main

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/ekk1/mygo/utils/webui"
)

type mediaPosition struct {
	Seconds *float64 `json:"seconds"`
}

func (a *app) mediaPositionAPI(w http.ResponseWriter, r *http.Request) {
	a.mediaMu.Lock()
	defer a.mediaMu.Unlock()
	id := r.PathValue("id")
	asset, err := a.assets.Get(id)
	if err != nil {
		apiError(w, assetErrorStatus(err), err)
		return
	}
	if !strings.HasPrefix(asset.ContentType, "video/") && !strings.HasPrefix(asset.ContentType, "audio/") {
		apiError(w, 400, fmt.Errorf("该资产不支持播放位置"))
		return
	}
	// Serialize writes and reads of each durable position file; no shared cache.
	path := filepath.Join(a.store.dir, "positions", id+".json")
	var position mediaPosition
	if r.Method == http.MethodPost {
		if err = decodeJSON(w, r, &position); err != nil {
			apiError(w, 400, err)
			return
		}
		if position.Seconds == nil || math.IsNaN(*position.Seconds) || math.IsInf(*position.Seconds, 0) || *position.Seconds < 0 {
			apiError(w, 400, fmt.Errorf("seconds 必须是有限的非负数字"))
			return
		}
		if err = os.MkdirAll(filepath.Dir(path), 0700); err == nil {
			err = saveKV(path, position)
		}
		if err != nil {
			apiError(w, 500, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err = loadKV(path, &position); errors.Is(err, os.ErrNotExist) {
		writeJSON(w, 200, nil)
		return
	}
	if err != nil {
		apiError(w, 500, err)
		return
	}
	writeJSON(w, 200, position)
}
func (a *app) mediaPlayer(w http.ResponseWriter, r *http.Request) {
	asset, err := a.assets.Get(r.PathValue("id"))
	if err != nil {
		apiError(w, assetErrorStatus(err), err)
		return
	}
	if !strings.HasPrefix(asset.ContentType, "video/") && !strings.HasPrefix(asset.ContentType, "audio/") {
		apiError(w, 400, fmt.Errorf("请选择音频或视频资产"))
		return
	}
	endpoint := "/api/assets/" + asset.ID + "/position"
	controls, err := webui.VideoPositionControls("asset-player", endpoint, endpoint)
	if err != nil {
		apiError(w, 500, err)
		return
	}
	page := webui.Page{Title: asset.Name + " · 播放器", Theme: "sand", Styles: []string{"/assets/media.css"}, Body: webui.El("main", webui.Attrs{"class": "media-player stack"},
		webui.El("div", webui.Attrs{"class": "actions"}, webui.El("a", webui.Attrs{"href": "/library"}, webui.Text("← 资产库")), webui.El("a", webui.Attrs{"href": "/media"}, webui.Text("下载器"))),
		webui.El("h1", nil, webui.Text(asset.Name)),
		webui.El("video", webui.Attrs{"id": "asset-player", "src": "/api/assets/" + asset.ID + "/content", "controls": "", "preload": "metadata", "aria-label": asset.Name}), controls,
		webui.El("p", webui.Attrs{"class": "muted"}, webui.Text("手动保存和恢复播放位置；恢复后不自动播放。浏览器无法播放的编码可下载后查看，或在资产库抽取音频。")),
	)}
	var b bytes.Buffer
	if err = webui.Render(&b, page); err != nil {
		apiError(w, 500, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method != "HEAD" {
		_, _ = w.Write(b.Bytes())
	}
}
