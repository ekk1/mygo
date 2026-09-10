# httpserver

用标准 Handler 快速注册路由、组合认证中间件、发布本地目录，并监听 HTTP、TLS 或 Unix socket。

## 接口

```go
type Middleware func(http.Handler) http.Handler
type Server struct { /* 私有字段 */ }
func New(middleware ...Middleware) *Server
func (s *Server) Handle(pattern string, handler http.Handler, middleware ...Middleware) error
func (s *Server) HandleFunc(pattern string, handler http.HandlerFunc, middleware ...Middleware) error
func (s *Server) Static(prefix, dir string, middleware ...Middleware) error
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request)
func (s *Server) Serve(listener net.Listener) error
func (s *Server) ListenHTTP(addr string) error
func (s *Server) ListenTLS(addr, certFile, keyFile string) error
func (s *Server) ListenUnix(socketPath string) error
func (s *Server) Shutdown(ctx context.Context) error
func (s *Server) Close() error

func Methods(methods ...string) Middleware
func BasicAuth(username, password string) Middleware
func CookieAuth(name string, check func(string) bool, redirectURL string) Middleware
func ServerError(w http.ResponseWriter, r *http.Request, status int, message string)
```

- `New` 创建独立服务，必须使用构造函数，不能复制 `Server`。默认执行请求日志、panic 恢复、路径校验，然后是全局中间件和路由中间件。每组中间件从左到右进入，nil 中间件忽略。
- `Handle` / `HandleFunc` 使用 Go 1.24 `http.ServeMux` 的模式，如 `/health`、`GET /items/{id}`，用 `r.PathValue("id")` 取参数。`/` 是兜底路由；仅匹配首页使用 `/{$}`。无效、冲突模式和 nil handler 返回错误；未匹配路由保留标准 404/405 行为。
- `Methods` 将方法转为大写，GET 自动包含 HEAD；不匹配时返回 405 及排序后的 `Allow`，不自动处理 OPTIONS。空列表拒绝全部请求。
- `BasicAuth` 校验固定用户名和密码，摘要恒定时间比较；失败返回 401 和 Basic challenge。通过网络传输凭据时搭配 TLS。
- `CookieAuth` 将 cookie 值交给 `check`，缺失、nil 检查函数或返回 false 时 303 跳转到 `redirectURL`；空地址使用 `/`。地址由应用提供，不要直接使用用户输入。登录路由应放在该中间件之外，避免循环跳转；不负责签发或设置 Cookie。
- `ServerError` 在写响应前调用，然后立即 `return`；400–599 以外的状态统一改为 500。空消息显示通用说明，非空消息自动 HTML 转义。页面适配浅色/深色、带“返回首页”按钮跳到 `/`，保留错误状态，不自动定时跳转。HEAD 不写正文。

## 最小示例

```go
package main

import (
    "errors"
    "fmt"
    "net/http"

    "github.com/ekk1/mygo/utils/httpserver"
)

func main() {
    if err := run(); err != nil {
        fmt.Println(err)
    }
}

func run() error {
    s := httpserver.New()
    defer s.Close()

    if err := s.HandleFunc("/hello/{name}", func(w http.ResponseWriter, r *http.Request) {
        fmt.Fprintf(w, "Hello, %s", r.PathValue("name"))
    }, httpserver.Methods("GET")); err != nil {
        return err
    }
    if err := s.HandleFunc("/admin", func(w http.ResponseWriter, r *http.Request) {
        fmt.Fprintln(w, "admin")
    }, httpserver.BasicAuth("admin", "change-me")); err != nil {
        return err
    }
    err := s.ListenHTTP(":8080")
    if errors.Is(err, http.ErrServerClosed) {
        return nil
    }
    return err
}
```

发布静态目录，可在上面的 `ListenHTTP` 前添加：

```go
// 使用标准库 os 准备目录，文件写入仍由应用负责。
if err := os.MkdirAll("./public", 0755); err != nil {
    return err
}
if err := s.Static("/assets", "./public"); err != nil {
    return err
}

// ./private 必须已存在；检查函数需支持并发调用。
if err := s.Static("/private", "./private",
    httpserver.CookieAuth("session", func(value string) bool {
        return value == "example-token" // 最小示例，可换成应用的会话校验
    }, "/login")); err != nil {
    return err
}
```

应用错误页：

```go
if err != nil {
    logutil.Error(err) // 内部详情只记日志
    httpserver.ServerError(w, r, http.StatusInternalServerError, "服务暂时不可用，请稍后重试。")
    return
}
```

## 静态目录与默认行为

- 目录必须存在，`Static` 打开并持有目录，注册失败会释放资源。前缀必须为绝对 URL 路径，可带末尾 `/`，不能含通配符、查询、片段或百分号；`/` 可发布到根路径。
- `/assets` 使用标准 ServeMux 301 跳到 `/assets/`，此补斜线跳转发生在路由中间件之前，后续文件请求才执行认证。仅允许 GET/HEAD；支持 `index.html`、目录列表、Range 和条件请求，未找到文件为 404，不可访问为 403。
- 发布目录里的文件（包括隐藏文件）可被访问；`os.Root` 阻止 `..` 和符号链接逃逸，但不隔离目录内的挂载点或特殊设备。适合发布专门准备的普通文件目录。Go 的 `os.Root` 在 js 平台不具备同等符号链接竞态保护。
- 默认路径校验检查解码后的 URL 路径，拒绝 `.`/`..` 段、重复斜线、反斜线和控制字符，返回 400；不检查查询参数，不代替业务参数校验。
- 请求日志复用 `logutil`，包含方法、路径、状态、耗时和对端地址，转义控制字符，不记录查询、Authorization、Cookie 或请求体。可用 `logutil.SetLevel` 调整整个进程的日志级别。
- panic 在尚未写响应时记录日志并返回通用 500 页面，不向客户端泄露内部信息；写入后 panic 会中止连接，避免拼接错误正文。`http.ErrAbortHandler` 保留标准行为。日志包装支持 Flush、Hijack、Push 和 `http.ResponseController` 穿透。

## 监听、关闭与并发

`ListenHTTP(":8080")`、`ListenTLS(":8443", "server.pem", "server-key.pem")`、`ListenUnix("/tmp/app.sock")` 都阻塞运行并返回错误；正常关闭返回 `http.ErrServerClosed`。TLS 最低 1.2，支持标准库协商 HTTP/2；Unix socket 权限 0600，不覆盖已有路径，关闭后自动清理，父目录必须存在，平台需支持 Unix socket。

默认请求头读取超时 10 秒、空闲超时 60 秒，不限制整个请求体读取或响应时长。`Serve(listener)` 接管已有监听器；`Server` 也实现 `http.Handler`，需要自定义超时或 TLS 策略时可交给标准 `http.Server`。

在另一个 goroutine 中调用 `s.Shutdown(ctx)` 可优雅停止：拒绝新路由注册、关闭监听器、等待活动请求结束，成功后释放静态目录。超时会返回错误，目录仍保留，可再次 `Shutdown` 或用 `Close` 强制关闭。`Close()` 立即关闭连接及目录，可重复调用；关闭后的服务不可重新启动。

路由注册、请求和关闭可并发调用；应用的处理函数、认证回调及中间件也必须支持并发。Server 不等待被 Hijack 的连接；若把 Server 当作 Handler 交给外部服务，需先关闭外部服务并等待请求结束，再调用这里的 `Close` 释放目录。
