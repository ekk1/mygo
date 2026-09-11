// Package tiingo 获取 Tiingo EOD 原始与调整后的股票日线。
package tiingo

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/ekk1/mygo/utils/httpclient"
)

// Config 配置 API key、代理和单次请求超时。
type Config struct {
	// APIKey 为 Tiingo API token，必填。
	APIKey string
	// ProxyURL 为空使用环境代理，"-" 直连，也可指定 HTTP 代理 URL。
	ProxyURL string
	// Timeout 默认 30 秒，负数无效。
	Timeout time.Duration
}

// Client 持有独立连接池，必须通过 New 创建，可并发调用。
type Client struct {
	http    *httpclient.Client
	baseURL string
}

// Bar 保留 EOD 原始数据、分红和拆股调整后的数据。
type Bar struct {
	// Date 是数据所属交易日期（API 返回的 UTC 日期）。
	Date time.Time `json:"date"`
	// Open、High、Low、Close、Volume 是原始 OHLCV。
	Open   float64 `json:"open"`
	High   float64 `json:"high"`
	Low    float64 `json:"low"`
	Close  float64 `json:"close"`
	Volume int64   `json:"volume"`
	// AdjOpen、AdjHigh、AdjLow、AdjClose、AdjVolume 是调整后的 OHLCV。
	AdjOpen   float64 `json:"adjOpen"`
	AdjHigh   float64 `json:"adjHigh"`
	AdjLow    float64 `json:"adjLow"`
	AdjClose  float64 `json:"adjClose"`
	AdjVolume int64   `json:"adjVolume"`
	// DivCash 为当日（除息日）分红。
	DivCash float64 `json:"divCash"`
	// SplitFactor 为拆股调整因子，未拆股通常为 1。
	SplitFactor float64 `json:"splitFactor"`
}

// New 验证配置并创建客户端；API key 为空或 HTTP 配置无效时返回 error。
func New(cfg Config) (*Client, error) {
	key := strings.TrimSpace(cfg.APIKey)
	if key == "" || strings.ContainsAny(key, "\r\n") {
		return nil, fmt.Errorf("tiingo: invalid API key")
	}
	h, err := httpclient.New(httpclient.Config{ProxyURL: cfg.ProxyURL, Timeout: cfg.Timeout, Headers: http.Header{"Authorization": {"Token " + key}}})
	if err != nil {
		return nil, fmt.Errorf("tiingo: %w", err)
	}
	// 由调用方处理重定向响应，避免在跳转中继续携带凭据。
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{http: h, baseURL: "https://api.tiingo.com"}, nil
}

// CloseIdleConnections 释放空闲连接，不中断正在进行的请求。
func (c *Client) CloseIdleConnections() { c.http.CloseIdleConnections() }

// EOD 返回指定日期范围内的日线，按日期升序排列，包含原始与调整价格。
// start、end 必须为 YYYY-MM-DD，允许同一天；无数据返回空切片，失败返回 nil 和 error。
func (c *Client) EOD(ctx context.Context, ticker, start, end string) ([]Bar, error) {
	if err := validateRange(ticker, start, end); err != nil {
		return nil, err
	}
	q := url.Values{"startDate": {start}, "endDate": {end}, "resampleFreq": {"daily"}, "format": {"json"}}
	endpoint := c.baseURL + "/tiingo/daily/" + url.PathEscape(ticker) + "/prices?" + q.Encode()
	var bars []Bar
	_, err := c.http.JSON(ctx, http.MethodGet, endpoint, nil, &bars)
	if err != nil {
		return nil, fmt.Errorf("tiingo: EOD: %w", err)
	}
	if bars == nil {
		return nil, fmt.Errorf("tiingo: expected a JSON price array")
	}
	sort.SliceStable(bars, func(i, j int) bool { return bars[i].Date.Before(bars[j].Date) })
	return bars, nil
}

func validateRange(ticker, start, end string) error {
	if strings.TrimSpace(ticker) == "" || strings.TrimSpace(ticker) != ticker || strings.ContainsAny(ticker, "/\\?#\r\n") || ticker == "." || ticker == ".." {
		return fmt.Errorf("tiingo: invalid ticker")
	}
	for _, date := range []string{start, end} {
		if _, err := time.Parse(time.DateOnly, date); err != nil {
			return fmt.Errorf("tiingo: date must be YYYY-MM-DD: %w", err)
		}
	}
	if start > end {
		return fmt.Errorf("tiingo: start must not be after end")
	}
	return nil
}
