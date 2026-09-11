package main

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"strings"

	"github.com/ekk1/mygo/utils/httpserver"
	"github.com/ekk1/mygo/utils/webui"
)

//go:embed assets/*
var workbenchAssets embed.FS

var pageNames = map[string]string{
	"/":           "对话",
	"/settings":   "设置",
	"/files":      "文件",
	"/containers": "容器",
	"/batches":    "批处理",
	"/native":     "原生操作",
	"/logs":       "请求日志",
}

func registerPages(s *httpserver.Server) error {
	assets, err := fs.Sub(workbenchAssets, "assets")
	if err != nil {
		return err
	}
	if err := s.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServerFS(assets))); err != nil {
		return err
	}
	for path, title := range pageNames {
		path, title := path, title
		pattern := "GET " + path
		if path == "/" {
			pattern = "GET /{$}"
		}
		if err := s.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			renderWorkbenchPage(w, r, path, title)
		}); err != nil {
			return err
		}
	}
	return nil
}

func renderWorkbenchPage(w http.ResponseWriter, r *http.Request, path, title string) {
	var output bytes.Buffer
	if err := webui.Render(&output, workbenchPage(path, title)); err != nil {
		http.Error(w, "页面暂时无法显示。", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method != http.MethodHead {
		if _, err := output.WriteTo(w); err != nil {
			fmt.Println(err)
		}
	}
}

func workbenchPage(path, title string) webui.Page {
	pageKey := strings.TrimPrefix(path, "/")
	if pageKey == "" {
		pageKey = "chat"
	}
	nav := make([]webui.Node, 0, len(pageNames))
	for _, item := range []struct{ path, label, icon string }{
		{"/", "对话", "✦"}, {"/files", "文件", "□"}, {"/containers", "容器", "◇"},
		{"/batches", "批处理", "≋"}, {"/native", "原生操作", "⌘"}, {"/logs", "请求日志", "◷"}, {"/settings", "设置", "⚙"},
	} {
		attrs := webui.Attrs{"href": item.path, "class": "nav-link", "data-route": strings.TrimPrefix(item.path, "/")}
		if item.path == path {
			attrs["aria-current"] = "page"
		}
		nav = append(nav, webui.El("a", attrs,
			webui.El("span", webui.Attrs{"class": "nav-icon", "aria-hidden": "true"}, webui.Text(item.icon)),
			webui.El("span", nil, webui.Text(item.label)),
		))
	}
	body := webui.El("div", webui.Attrs{"class": "app-shell", "data-page": pageKey},
		webui.El("aside", webui.Attrs{"class": "sidebar"},
			webui.El("a", webui.Attrs{"href": "/", "class": "workbench-brand"},
				webui.El("span", webui.Attrs{"class": "brand-mark", "aria-hidden": "true"}, webui.Text("W")),
				webui.El("span", nil, webui.Text("个人工作台")),
			),
			webui.El("nav", webui.Attrs{"aria-label": "主导航"}, webui.Group(nav...)),
			webui.El("p", webui.Attrs{"class": "sidebar-note"}, webui.Text("数据仅保存在本机")),
		),
		webui.El("main", webui.Attrs{"class": "app-main"},
			webui.El("header", webui.Attrs{"class": "mobile-header"},
				webui.El("span", webui.Attrs{"class": "brand-mark", "aria-hidden": "true"}, webui.Text("W")),
				webui.El("strong", nil, webui.Text(title)),
			),
			webui.El("div", webui.Attrs{"id": "app", "class": "page-root", "aria-live": "polite"},
				webui.El("div", webui.Attrs{"class": "loading-card"},
					webui.El("span", webui.Attrs{"class": "spinner", "aria-hidden": "true"}),
					webui.El("p", nil, webui.Text("正在载入工作台…")),
				),
			),
		),
	)
	return webui.Page{Title: title + " · 个人工作台", Theme: "sand", Scripts: []string{"/assets/workbench.js"}, Body: body}
}
