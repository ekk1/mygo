// webui-demo 展示由 Go 渲染、无需 JavaScript 的表单预览流程。
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/ekk1/mygo/utils/httpserver"
	"github.com/ekk1/mygo/utils/webui"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "HTTP listen address")
	flag.Parse()
	if err := run(*addr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(addr string) error {
	s, err := newServer()
	if err != nil {
		return err
	}
	defer s.Close()
	fmt.Printf("webui demo: http://%s\n", addr)
	err = s.ListenHTTP(addr)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func newServer() (*httpserver.Server, error) {
	s := httpserver.New()
	if err := s.Handle("/webui/", http.StripPrefix("/webui/", webui.Assets())); err != nil {
		s.Close()
		return nil, err
	}
	if err := s.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		render(w, r, http.StatusOK, "", "", "")
	}); err != nil {
		s.Close()
		return nil, err
	}
	if err := s.HandleFunc("GET /preview", func(w http.ResponseWriter, r *http.Request) {
		title, note := r.URL.Query().Get("title"), r.URL.Query().Get("note")
		render(w, r, http.StatusOK, title, note, "")
	}); err != nil {
		s.Close()
		return nil, err
	}
	if err := s.HandleFunc("POST /preview", submit); err != nil {
		s.Close()
		return nil, err
	}
	if err := registerComponents(s); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func submit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		var tooLarge *http.MaxBytesError
		status := http.StatusBadRequest
		if errors.As(err, &tooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		httpserver.ServerError(w, r, status, "表单内容无法读取。")
		return
	}
	title, note := strings.TrimSpace(r.PostForm.Get("title")), r.PostForm.Get("note")
	message := ""
	switch {
	case title == "":
		message = "请输入标题。"
	case utf8.RuneCountInString(title) > 80:
		message = "标题请控制在 80 个字符以内。"
	case utf8.RuneCountInString(note) > 500:
		message = "备注请控制在 500 个字符以内。"
	}
	if message != "" {
		render(w, r, http.StatusUnprocessableEntity, title, note, message)
		return
	}
	// This demo previews public input only; it does not persist data or use a session.
	query := url.Values{"title": {title}, "note": {note}}
	http.Redirect(w, r, "/preview?"+query.Encode(), http.StatusSeeOther)
}

func render(w http.ResponseWriter, r *http.Request, status int, title, note, message string) {
	var output bytes.Buffer
	if err := webui.Render(&output, demoPage(title, note, message)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		httpserver.ServerError(w, r, 500, "页面暂时无法显示。")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		if _, err := w.Write(output.Bytes()); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
	}
}

func demoPage(title, note, message string) webui.Page {
	var notice webui.Node
	if message != "" {
		notice = webui.El("div", webui.Attrs{"class": "notice", "role": "alert", "id": "form-error"}, webui.Text(message))
	}
	titleAttrs := webui.Attrs{"id": "title", "name": "title", "required": "", "maxlength": "80", "placeholder": "输入标题", "value": title}
	noteAttrs := webui.Attrs{"id": "note", "name": "note", "maxlength": "500", "placeholder": "输入备注，可换行"}
	if message != "" {
		titleAttrs["aria-describedby"] = "form-error"
		noteAttrs["aria-describedby"] = "form-error"
	}
	preview := webui.Group(
		webui.El("h2", nil, webui.Text(title)),
		webui.El("p", webui.Attrs{"class": "prose"}, webui.Text(note)),
	)
	if title == "" {
		preview = webui.El("div", webui.Attrs{"class": "empty"},
			webui.El("p", nil, webui.Text("还没有内容")),
			webui.El("p", webui.Attrs{"class": "muted"}, webui.Text("填写标题和备注后，点击“更新预览”。")),
		)
	}
	var themes []webui.Node
	for _, t := range []struct{ id, name string }{{"rose", "玫瑰灰"}, {"sand", "燕麦"}, {"sage", "鼠尾草"}, {"dusk", "暮色"}} {
		pressed := "false"
		if t.id == "rose" {
			pressed = "true"
		}
		themes = append(themes, webui.El("button", webui.Attrs{"type": "button", "data-webui-theme": t.id, "aria-pressed": pressed}, webui.Text(t.name)))
	}
	return webui.Page{Title: "预览编辑器 · webui", Body: webui.Group(
		webui.El("header", nil,
			webui.El("a", webui.Attrs{"href": "/", "class": "brand"}, webui.Text("webui")),
			webui.El("a", webui.Attrs{"href": "/components"}, webui.Text("视频与 K 线")),
			webui.El("div", webui.Attrs{"class": "theme-switcher", "data-webui-theme-controls": "", "role": "group", "aria-label": "配色", "hidden": ""}, webui.Group(themes...)),
			webui.El("span", webui.Attrs{"class": "theme-status", "data-webui-theme-status": "", "role": "status"}, webui.Text("")),
		),
		webui.El("main", nil,
			webui.El("h1", nil, webui.Text("预览编辑器")),
			webui.El("div", webui.Attrs{"class": "workspace"},
				webui.El("section", webui.Attrs{"class": "editor", "aria-label": "编辑内容"},
					webui.El("h2", webui.Attrs{"class": "section-title"}, webui.Text("编辑")),
					notice,
					webui.El("form", webui.Attrs{"method": "post", "action": "/preview"},
						webui.El("label", webui.Attrs{"for": "title"}, webui.Text("标题")),
						webui.El("input", titleAttrs),
						webui.El("label", webui.Attrs{"for": "note"}, webui.Text("备注")),
						webui.El("textarea", noteAttrs, webui.Text(note)),
						webui.El("div", webui.Attrs{"class": "actions"},
							webui.El("button", webui.Attrs{"type": "submit"}, webui.Text("更新预览")),
							webui.El("a", webui.Attrs{"href": "/", "class": "quiet-link"}, webui.Text("清空")),
						),
						webui.El("p", webui.Attrs{"class": "field-help"}, webui.Text("标题最多 80 字，备注最多 500 字。")),
					),
				),
				webui.El("section", webui.Attrs{"class": "preview", "aria-label": "想法预览"},
					webui.El("h2", webui.Attrs{"class": "section-title"}, webui.Text("预览")),
					webui.El("article", webui.Attrs{"class": "document"}, preview),
				),
			),
		),
		webui.El("footer", nil, webui.Text("仅供预览，不保存内容。提交后内容会出现在网址中，请勿填写私密信息。")),
	)}
}
