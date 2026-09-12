package main

import (
	"bytes"
	"embed"
	"fmt"
	"github.com/ekk1/mygo/utils/httpserver"
	"github.com/ekk1/mygo/utils/webui"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed assets/*
var workbenchAssets embed.FS

var pageNames = map[string]string{"/": "工作台", "/ai": "AI", "/settings": "设置", "/logs": "请求日志"}

func registerPages(s *httpserver.Server) error {
	assets, err := fs.Sub(workbenchAssets, "assets")
	if err != nil {
		return err
	}
	if err = s.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServerFS(assets))); err != nil {
		return err
	}
	for _, path := range []string{"/{$}", "/ai", "/ai/", "/settings", "/logs"} {
		if err = s.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) { renderWorkbenchPage(w, r, r.URL.Path, "工作台") }); err != nil {
			return err
		}
	}
	return nil
}
func renderWorkbenchPage(w http.ResponseWriter, r *http.Request, path, title string) {
	var output bytes.Buffer
	if err := webui.Render(&output, workbenchPage(path, title)); err != nil {
		http.Error(w, "页面暂时无法显示", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method != "HEAD" {
		if _, err := output.WriteTo(w); err != nil {
			fmt.Println(err)
		}
	}
}
func workbenchPage(path, title string) webui.Page {
	body := webui.El("div", webui.Attrs{"class": "app-shell", "data-page": strings.TrimPrefix(path, "/")},
		webui.El("aside", webui.Attrs{"class": "sidebar", "id": "sidebar"},
			webui.El("a", webui.Attrs{"href": "/", "class": "workbench-brand"}, webui.El("span", webui.Attrs{"class": "brand-mark"}, webui.Text("W")), webui.Text("个人工作台")),
			webui.El("nav", webui.Attrs{"id": "navigation", "aria-label": "主导航"}, webui.El("a", webui.Attrs{"href": "/ai", "class": "nav-link"}, webui.Text("AI"))),
			webui.El("div", webui.Attrs{"class": "sidebar-bottom"}, webui.El("a", webui.Attrs{"href": "/logs", "class": "nav-link"}, webui.Text("请求日志")), webui.El("a", webui.Attrs{"href": "/settings", "class": "nav-link"}, webui.Text("全局设置"))),
		),
		webui.El("main", webui.Attrs{"class": "app-main"},
			webui.El("header", webui.Attrs{"id": "workspace-header", "class": "workspace-header"}),
			webui.El("div", webui.Attrs{"id": "app", "class": "page-root"}, webui.El("p", nil, webui.Text("正在载入工作台…"))),
		),
	)
	return webui.Page{Title: title + " · 个人工作台", Theme: "sand", Styles: []string{"/assets/workbench.css"}, Scripts: []string{"/assets/workbench.js", "/assets/ai-pages.js", "/assets/resources.js"}, Body: body}
}
