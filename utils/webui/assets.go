package webui

import (
	"bytes"
	"embed"
	"net/http"
	"strings"
	"time"
)

//go:embed assets/webui.css assets/webui.js assets/theme.js assets/themes/*.css
var assets embed.FS

// Assets 返回 CSS/JS 的 HTTP Handler，仅支持 GET/HEAD，不提供目录列表。
// 使用 http.StripPrefix("/webui/", Assets()) 挂载到 /webui/。
func Assets() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/")
		switch name {
		case "webui.css", "themes/rose.css", "themes/sand.css", "themes/sage.css", "themes/dusk.css":
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
		case "webui.js", "theme.js":
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		default:
			http.NotFound(w, r)
			return
		}
		if name[0] == '/' {
			name = name[1:]
		}
		data, err := assets.ReadFile("assets/" + name)
		if err != nil {
			http.Error(w, "Asset unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
	})
}
