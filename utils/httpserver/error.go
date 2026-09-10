package httpserver

import (
	"html/template"
	"net/http"
)

var errorPage = template.Must(template.New("error").Parse(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>{{.Status}} · {{.Title}}</title>
<style>:root{color-scheme:light dark;font-family:system-ui,-apple-system,sans-serif}*{box-sizing:border-box}body{margin:0;min-height:100vh;display:grid;place-items:center;padding:24px;background:#f3f5f9;color:#182230}main{width:100%;max-width:480px;padding:40px;background:#fff;border:1px solid #e3e8ef;border-radius:20px;box-shadow:0 12px 40px #18223008}.code{color:#62748e;font-size:14px;font-weight:600;letter-spacing:.1em}h1{font-size:28px;line-height:1.2;margin:16px 0}p{color:#526175;line-height:1.7;overflow-wrap:anywhere}a{display:inline-block;margin-top:16px;padding:11px 18px;background:#2857d9;border-radius:10px;color:#fff;text-decoration:none;font-weight:600}a:hover{background:#2149b7}a:focus-visible{outline:3px solid #8aafff;outline-offset:4px}@media(prefers-color-scheme:dark){body{background:#111827;color:#eef2f7}main{background:#1b2535;border-color:#344055}p,.code{color:#b0bfd3}}</style></head>
<body><main><div class="code">HTTP {{.Status}}</div><h1>{{.Title}}</h1><p>{{.Message}}</p><a href="/">返回首页</a></main></body></html>`))

// ServerError 写入简洁 HTML 错误页，提供返回 / 的按钮；调用后处理函数应立即 return。
// status 必须为 400–599，否则使用 500；message 为空时显示通用说明，非空内容自动转义。
// 应在写入响应前调用，不应把包含内部细节的 error.Error() 直接作为公开消息。
func ServerError(w http.ResponseWriter, r *http.Request, status int, message string) {
	if status < 400 || status > 599 {
		status = http.StatusInternalServerError
	}
	title := http.StatusText(status)
	if title == "" {
		title = "Request failed"
	}
	if message == "" {
		message = "请求暂时无法完成，你可以返回首页后重试。"
	}
	w.Header().Del("Content-Length")
	w.Header().Del("Content-Encoding")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'")
	w.WriteHeader(status)
	if r.Method == http.MethodHead {
		return
	}
	_ = errorPage.Execute(w, struct {
		Status         int
		Title, Message string
	}{status, title, message})
}
