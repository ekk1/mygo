# httpclient

方便配置代理、CA 和客户端证书，一次调用完成 JSON/Form 请求及 JSON 响应解码。

## 接口

```go
type Config struct {
    ProxyURL         string
    CAFile           string
    CertFile         string
    KeyFile          string
    Timeout          time.Duration
    Headers          http.Header
    MaxResponseBytes int64
}
func New(cfg Config) (*Client, error)

type Client struct { *http.Client /* 私有配置 */ }
func (c *Client) JSON(ctx context.Context, method, rawURL string, input, output any) (*Response, error)
func (c *Client) Form(ctx context.Context, method, rawURL string, values url.Values, output any) (*Response, error)

func JSON(ctx context.Context, method, rawURL string, input, output any) (*Response, error)
func Form(ctx context.Context, method, rawURL string, values url.Values, output any) (*Response, error)

type Response struct {
    StatusCode int
    Header     http.Header
    Body       []byte
}
type StatusError struct { Response *Response }
func (e *StatusError) Error() string
```

- `New` 创建独立连接池，配置错误返回 `error`。必须通过 `New` 创建 `Client`；无需配置时直接用包级函数，共享默认连接池。
- `ProxyURL`：空值使用 `HTTP_PROXY`、`HTTPS_PROXY`、`NO_PROXY` 等标准环境代理规则；`"-"` 直连；显式 URL 支持 `http`、`https`、`socks5`、`socks5h`，可带代理用户名和密码。
- `CAFile`：PEM 文件，添加到系统信任池；没有可用系统信任池时使用该文件。`CertFile`、`KeyFile` 用于 mTLS，必须同时指定。始终校验证书与主机名，最低 TLS 1.2。
- `Timeout`：请求总超时，包含读取响应，默认 30 秒；`MaxResponseBytes`：解压后的响应体上限，默认 16 MiB。两者零值采用默认值，负数无效。
- `Headers`：JSON/Form 的默认请求头，创建时复制。`Content-Type` 由函数按编码覆盖，未指定 `Accept` 时使用 `application/json`。
- `JSON` 自动编码 `input`，`nil` 表示没有请求体；`Form` 使用 `application/x-www-form-urlencoded`，`url.Values` 支持重复字段，非 multipart 上传。
- `output` 为非 nil 时应传入目标指针；响应非空时按 JSON 解码，空响应跳过。传 nil 可只获取原始响应。
- 函数自动读取并关闭响应体；`Response` 中保留状态码、响应头和原始字节。无需再关闭，独立响应可自行持有。
- 非 2xx 返回 `Response` 和 `*StatusError`，不解码到 `output`。响应体读取失败、超限或 JSON 解码失败时返回 `Response` 和错误，读取失败/超限时 `Body` 可能不完整。超限优先于状态错误。`StatusError.Error()` 只包含 HTTP 状态，不包含响应正文。
- 嵌入的标准 `http.Client` 提供 `Do`、`Get`、`CloseIdleConnections` 等接口。标准请求仍使用配置的代理、证书和超时，但不应用辅助函数的默认请求头、响应限制或自动关闭行为；标准 `Do` 的响应体由调用方关闭。重定向遵循标准库默认规则，不自动重试请求。

## 使用示例

无需配置，发送 JSON 并解码：

```go
var result struct { ID int `json:"id"` }
res, err := httpclient.JSON(ctx, http.MethodPost,
    "https://example.com/api/items", map[string]string{"name": "demo"}, &result)
if err != nil {
    return err
}
fmt.Println(res.StatusCode, result.ID)
```

使用自定义代理和证书：

```go
c, err := httpclient.New(httpclient.Config{
    ProxyURL: "http://127.0.0.1:8080", // 无代理用 "-"
    CAFile:   "ca.pem",              // 不需要自定义 CA 时省略
    CertFile: "client.pem",          // 不需要 mTLS 时同时省略这两项
    KeyFile:  "client-key.pem",
    Headers:  http.Header{"Authorization": {"Bearer token"}},
})
if err != nil {
    return err
}
defer c.CloseIdleConnections()

var result map[string]any
res, err := c.Form(ctx, http.MethodPost, "https://example.com/login",
    url.Values{"username": {"demo"}, "password": {"secret"}}, &result)
if err != nil {
    var statusErr *httpclient.StatusError
    if errors.As(err, &statusErr) {
        fmt.Println(statusErr.Response.StatusCode)
    }
    return err
}
fmt.Println(res.StatusCode, result)
```

以上为函数内片段，`ctx` 为调用方的 `context.Context`；包路径为 `github.com/ekk1/mygo/utils/httpclient`。

## 并发与生命周期

客户端及包级函数可并发请求，默认请求头在创建时复制，内部不再修改。共享客户端有独立连接池，应复用；不再需要时调用 `CloseIdleConnections()` 释放空闲连接。不要在请求期间修改嵌入的 `http.Client`、Transport 或 TLS 配置。同一个 `output` 不可由多个请求同时写入。
