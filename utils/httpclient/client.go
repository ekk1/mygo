// Package httpclient 提供带代理、证书配置及 JSON/Form 编解码的 HTTP 客户端。
package httpclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Config 配置独立客户端；零值使用环境代理、系统 CA、30 秒超时和 16 MiB 响应限制。
type Config struct {
	// ProxyURL 为空时使用环境代理，"-" 表示直连；支持 http、https、socks5、socks5h URL。
	ProxyURL string
	// CAFile 为附加到系统信任池的 PEM 证书文件。
	CAFile string
	// CertFile 和 KeyFile 为可选的 PEM 客户端证书和私钥，必须同时指定。
	CertFile, KeyFile string
	// Timeout 为包含响应读取的请求总超时；零值为 30 秒，负数无效。
	Timeout time.Duration
	// Headers 为 JSON/Form 的默认请求头，创建时复制。
	Headers http.Header
	// MaxResponseBytes 限制 JSON/Form 响应体；零值为 16 MiB，负数无效。
	MaxResponseBytes int64
}

// Client 持有独立连接池。配置完成后可并发请求，勿并发修改嵌入的 http.Client。
// 标准 Do/Get 等方法保持 net/http 行为，不应用 JSON/Form 的编解码、请求头或响应限制。
type Client struct {
	*http.Client
	headers          http.Header
	maxResponseBytes int64
}

// Response 保存已读取并关闭的响应，Body 可重复访问。
type Response struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

// StatusError 表示非 2xx 响应，Response 保留服务端响应供调用方检查。
type StatusError struct{ Response *Response }

// Error 返回 HTTP 状态，不包含可能敏感的响应内容。
func (e *StatusError) Error() string {
	return fmt.Sprintf("httpclient: HTTP %d %s", e.Response.StatusCode, http.StatusText(e.Response.StatusCode))
}

// New 验证配置并创建独立连接池；无效代理、证书或限制返回 error。
func New(cfg Config) (*Client, error) {
	if cfg.Timeout < 0 || cfg.MaxResponseBytes < 0 {
		return nil, fmt.Errorf("httpclient: timeout and response limit must not be negative")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.MaxResponseBytes == 0 {
		cfg.MaxResponseBytes = 16 << 20
	}
	transport := transportTemplate.Clone()
	switch cfg.ProxyURL {
	case "":
	case "-":
		transport.Proxy = nil
	default:
		proxy, err := url.Parse(cfg.ProxyURL)
		if err != nil {
			return nil, fmt.Errorf("httpclient: proxy URL: %w", err)
		}
		if proxy.Hostname() == "" || (proxy.Scheme != "http" && proxy.Scheme != "https" && proxy.Scheme != "socks5" && proxy.Scheme != "socks5h") {
			return nil, fmt.Errorf("httpclient: invalid proxy URL")
		}
		transport.Proxy = http.ProxyURL(proxy)
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if cfg.CAFile != "" {
		data, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("httpclient: read CA: %w", err)
		}
		roots, err := x509.SystemCertPool()
		if err != nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(data) {
			return nil, fmt.Errorf("httpclient: CA file contains no certificates")
		}
		tlsConfig.RootCAs = roots
	}
	if (cfg.CertFile == "") != (cfg.KeyFile == "") {
		return nil, fmt.Errorf("httpclient: certificate and key must be supplied together")
	}
	if cfg.CertFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("httpclient: client certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}
	transport.TLSClientConfig = tlsConfig
	return &Client{Client: &http.Client{Transport: transport, Timeout: cfg.Timeout}, headers: cfg.Headers.Clone(), maxResponseBytes: cfg.MaxResponseBytes}, nil
}

// 在初始化时保存标准连接池模板，后续创建不依赖应用对全局 Transport 的替换。
var transportTemplate = http.DefaultTransport.(*http.Transport).Clone()
var defaultClient = func() *Client { c, _ := New(Config{}); return c }()

// JSON 使用共享默认客户端发送 JSON；input 为 nil 时无请求体，output 非 nil 时解码响应。
func JSON(ctx context.Context, method, rawURL string, input, output any) (*Response, error) {
	return defaultClient.JSON(ctx, method, rawURL, input, output)
}

// Form 使用共享默认客户端发送 application/x-www-form-urlencoded 表单。
func Form(ctx context.Context, method, rawURL string, values url.Values, output any) (*Response, error) {
	return defaultClient.Form(ctx, method, rawURL, values, output)
}

// JSON 编码 input、发送请求并可选解码 output。非 2xx 返回 Response 和 *StatusError。
func (c *Client) JSON(ctx context.Context, method, rawURL string, input, output any) (*Response, error) {
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return nil, fmt.Errorf("httpclient: encode json: %w", err)
		}
		body = bytes.NewReader(data)
	}
	return c.send(ctx, method, rawURL, "application/json", body, output)
}

// Form 发送 URL 编码的表单并可选解码 JSON 响应，支持重复字段值。
func (c *Client) Form(ctx context.Context, method, rawURL string, values url.Values, output any) (*Response, error) {
	return c.send(ctx, method, rawURL, "application/x-www-form-urlencoded", strings.NewReader(values.Encode()), output)
}

func (c *Client) send(ctx context.Context, method, rawURL, contentType string, body io.Reader, output any) (*Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return nil, err
	}
	req.Header = c.headers.Clone()
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	req.Header.Set("Content-Type", contentType)
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}
	res, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	result := &Response{StatusCode: res.StatusCode, Header: res.Header.Clone()}
	result.Body, err = io.ReadAll(io.LimitReader(res.Body, c.maxResponseBytes))
	if err != nil {
		return result, fmt.Errorf("httpclient: read response: %w", err)
	}
	var extra [1]byte
	n, readErr := io.ReadFull(res.Body, extra[:])
	if n > 0 {
		return result, fmt.Errorf("httpclient: response exceeds %d bytes", c.maxResponseBytes)
	}
	if readErr != nil && readErr != io.EOF {
		return result, fmt.Errorf("httpclient: read response: %w", readErr)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return result, &StatusError{Response: result}
	}
	if output != nil && len(result.Body) > 0 {
		if err := json.Unmarshal(result.Body, output); err != nil {
			return result, fmt.Errorf("httpclient: decode json: %w", err)
		}
	}
	return result, nil
}
