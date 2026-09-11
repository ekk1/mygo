# tiingo

通过 Tiingo REST API 获取股票 EOD 日线，一次返回原始价格与包含分红、拆股调整的价格。

## 接口

```go
type Config struct {
    APIKey   string
    ProxyURL string
    Timeout  time.Duration
}
func New(cfg Config) (*Client, error)
type Client struct { /* 私有连接池及配置 */ }
func (c *Client) EOD(ctx context.Context, ticker, start, end string) ([]Bar, error)
func (c *Client) CloseIdleConnections()

type Bar struct {
    Date        time.Time
    Open        float64
    High        float64
    Low         float64
    Close       float64
    Volume      int64
    AdjOpen     float64
    AdjHigh     float64
    AdjLow      float64
    AdjClose    float64
    AdjVolume   int64
    DivCash     float64
    SplitFactor float64
}
```

- `New`：APIKey 为必填 Tiingo token，使用 `Authorization: Token ...` 鉴权；空 key、无效代理或负超时返回 error。必须通过 `New` 创建客户端。
- `ProxyURL`：空值使用标准环境代理；`"-"` 直连；支持 http、https、socks5、socks5h URL（可带代理认证），行为沿用 [httpclient](../httpclient/README.md)。
- `Timeout`：单次请求含响应读取的超时，零值为 30 秒。可用 context 提前取消。响应上限为 16 MiB。
- `EOD`：ticker 原样传入，start/end 必填，格式 `YYYY-MM-DD`，起止日期包含在范围内，允许查询同一天；固定 daily，不重采样。结果按日期升序排列，交易日缺口不补数据。
- `Date` 为 API 返回的 UTC 交易日期；`Open/High/Low/Close/Volume` 是原始 OHLCV，`Adj*` 是调整后的 OHLCV。`DivCash` 为除息日分红，`SplitFactor` 为拆股因子（无拆股通常为 1）。字段保留原 API JSON 名称，可直接 JSON 序列化。
- 无数据返回空切片；无效参数、HTTP 非 2xx、无效 JSON 或非数组响应返回 nil 和 error。HTTP 状态可通过 `errors.As(err, &statusErr)` 提取 `*httpclient.StatusError`；网络与 context 错误保留错误链。
- 不自动重试或跟随重定向，不做本地缓存。数据源可能修正历史行情；缓存与刷新策略由调用方负责。

## 最小示例

以下是函数内片段，`ctx` 为调用方 context，`apiKey` 为 Tiingo token：

```go
c, err := tiingo.New(tiingo.Config{
    APIKey: apiKey,
    ProxyURL: "http://127.0.0.1:7890",
})
if err != nil {
    return err
}
defer c.CloseIdleConnections()

bars, err := c.EOD(ctx, "AAPL", "2024-01-01", "2024-01-31")
if err != nil {
    return err
}
for _, b := range bars {
    fmt.Println(b.Date.Format("2006-01-02"), b.Close, b.AdjClose)
}
```

包路径：`github.com/ekk1/mygo/utils/tiingo`。

## 并发与生命周期

客户端创建后配置不变，可复用并并发请求；每次调用返回独立切片。调用方若共享并修改返回值，需要自行同步。`CloseIdleConnections` 释放空闲连接，不中断正在执行的请求。

官方文档：[EOD 字段与请求](https://www.tiingo.com/documentation/end-of-day)、[鉴权](https://www.tiingo.com/documentation/general/connecting)。
