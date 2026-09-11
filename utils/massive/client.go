// Package massive 获取 Massive（原 Polygon）的 REST 股票日线和 1 分钟线。
package massive

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ekk1/mygo/utils/httpclient"
)

// Config 配置 API key、代理和单次请求超时。
type Config struct {
	// APIKey 为 Massive API key，必填。
	APIKey string
	// ProxyURL 为空使用环境代理，"-" 直连，也可指定 HTTP 代理 URL。
	ProxyURL string
	// Timeout 为每页请求超时，默认 30 秒，负数无效。
	Timeout time.Duration
}

// Client 持有独立连接池，必须通过 New 创建，可并发调用。
type Client struct {
	http    *httpclient.Client
	baseURL string
}

// Bar 是一个聚合时间窗口内的价格与交易量。
type Bar struct {
	// Timestamp 为窗口起点的 Unix 毫秒时间戳。
	Timestamp int64 `json:"t"`
	// Open、High、Low、Close、Volume 为该窗口的 OHLCV；成交量可为小数。
	Open   float64 `json:"o"`
	High   float64 `json:"h"`
	Low    float64 `json:"l"`
	Close  float64 `json:"c"`
	Volume float64 `json:"v"`
	// VWAP 为成交量加权均价，服务端未提供时为 0。
	VWAP float64 `json:"vw"`
	// Transactions 为成交笔数，服务端未提供时为 0。
	Transactions int64 `json:"n"`
	// OTC 表示场外交易标的，服务端未提供时为 false。
	OTC bool `json:"otc"`
}

// New 验证配置并创建客户端；API key 为空或 HTTP 配置无效时返回 error。
func New(cfg Config) (*Client, error) {
	key := strings.TrimSpace(cfg.APIKey)
	if key == "" || strings.ContainsAny(key, "\r\n") {
		return nil, fmt.Errorf("massive: invalid API key")
	}
	h, err := httpclient.New(httpclient.Config{ProxyURL: cfg.ProxyURL, Timeout: cfg.Timeout, Headers: http.Header{"Authorization": {"Bearer " + key}}})
	if err != nil {
		return nil, fmt.Errorf("massive: %w", err)
	}
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{http: h, baseURL: "https://api.massive.com"}, nil
}

// CloseIdleConnections 释放空闲连接，不中断正在进行的请求。
func (c *Client) CloseIdleConnections() { c.http.CloseIdleConnections() }

// Daily 获取日线；日期为 YYYY-MM-DD，adjusted 指定是否进行拆股调整（不含分红）。
// 自动读取全部分页并按时间升序返回；无数据返回空切片，失败不返回部分数据。
func (c *Client) Daily(ctx context.Context, ticker, start, end string, adjusted bool) ([]Bar, error) {
	return c.bars(ctx, ticker, start, end, "day", adjusted)
}

// Minute 获取 1 分钟线，包含服务端提供的盘前、盘中与盘后数据；其他行为同 Daily。
func (c *Client) Minute(ctx context.Context, ticker, start, end string, adjusted bool) ([]Bar, error) {
	return c.bars(ctx, ticker, start, end, "minute", adjusted)
}

func (c *Client) bars(ctx context.Context, ticker, start, end, span string, adjusted bool) ([]Bar, error) {
	if err := validateRange(ticker, start, end); err != nil {
		return nil, err
	}
	q := url.Values{"adjusted": {strconv.FormatBool(adjusted)}, "sort": {"asc"}, "limit": {"50000"}}
	current, _ := url.Parse(c.baseURL + "/v2/aggs/ticker/" + url.PathEscape(ticker) + "/range/1/" + span + "/" + start + "/" + end + "?" + q.Encode())
	origin := *current
	seen := make(map[string]bool)
	bars := make([]Bar, 0)
	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("massive: %w", err)
		}
		endpoint := current.String()
		if seen[endpoint] {
			return nil, fmt.Errorf("massive: pagination loop")
		}
		seen[endpoint] = true
		var page struct {
			Status  string `json:"status"`
			Results []Bar  `json:"results"`
			NextURL string `json:"next_url"`
		}
		_, err := c.http.JSON(ctx, http.MethodGet, endpoint, nil, &page)
		if err != nil {
			return nil, fmt.Errorf("massive: %s bars: %w", span, err)
		}
		if page.Status != "OK" && page.Status != "DELAYED" {
			return nil, fmt.Errorf("massive: unexpected API status %q", page.Status)
		}
		bars = append(bars, page.Results...)
		if page.NextURL == "" {
			break
		}
		next, err := url.Parse(page.NextURL)
		if err != nil {
			return nil, fmt.Errorf("massive: invalid pagination URL")
		}
		next = current.ResolveReference(next)
		if next.Scheme != origin.Scheme || next.Host != origin.Host || next.User != nil || next.Fragment != "" {
			return nil, fmt.Errorf("massive: pagination URL must stay on the API origin")
		}
		current = next
	}
	sort.SliceStable(bars, func(i, j int) bool { return bars[i].Timestamp < bars[j].Timestamp })
	return bars, nil
}

func validateRange(ticker, start, end string) error {
	if strings.TrimSpace(ticker) == "" || strings.TrimSpace(ticker) != ticker || strings.ContainsAny(ticker, "/\\?#\r\n") || ticker == "." || ticker == ".." {
		return fmt.Errorf("massive: invalid ticker")
	}
	for _, date := range []string{start, end} {
		if _, err := time.Parse(time.DateOnly, date); err != nil {
			return fmt.Errorf("massive: date must be YYYY-MM-DD: %w", err)
		}
	}
	if start > end {
		return fmt.Errorf("massive: start must not be after end")
	}
	return nil
}
