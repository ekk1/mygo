# massive

通过 Massive（原 Polygon）的 REST 股票聚合接口获取日线和 1 分钟线，自动读取全部分页。

## 接口

```go
type Config struct {
    APIKey   string
    ProxyURL string
    Timeout  time.Duration
}
func New(cfg Config) (*Client, error)
type Client struct { /* 私有连接池及配置 */ }
func (c *Client) Daily(ctx context.Context, ticker, start, end string, adjusted bool) ([]Bar, error)
func (c *Client) Minute(ctx context.Context, ticker, start, end string, adjusted bool) ([]Bar, error)
func (c *Client) CloseIdleConnections()

type Bar struct {
    Timestamp    int64
    Open         float64
    High         float64
    Low          float64
    Close        float64
    Volume       float64
    VWAP         float64
    Transactions int64
    OTC          bool
}
```

- `New`：APIKey 必填，使用 `Authorization: Bearer ...` 鉴权，默认访问 `https://api.massive.com`；空 key、无效代理或负超时返回 error。必须通过 `New` 创建客户端。
- `ProxyURL`：空值使用标准环境代理；`"-"` 直连；支持 http、https、socks5、socks5h URL（可带代理认证），行为沿用 [httpclient](../httpclient/README.md)。
- `Timeout`：每页请求含响应读取的超时，零值为 30 秒；整个多页查询的期限由 context 控制。每页响应上限为 16 MiB。
- `Daily`/`Minute`：ticker 区分大小写，start/end 必填 `YYYY-MM-DD`，允许同一天，按服务端美国东部时间解释，包含起止日期。分别固定 `1/day` 与 `1/minute`，`sort=asc`、`limit=50000`（底层聚合查询上限，不是整个结果数上限）。
- `adjusted=true` 使用拆股调整，false 获取原始价格；**不包含分红调整**。如需两套数据，分别调用。分钟线保留服务端提供的盘前、盘中与盘后数据，不主动过滤交易时段。
- 自动跟随同源 `next_url`，最后按时间升序返回；检测重复分页 URL。无符合条件的交易则没有对应 bar，不补空窗口；无数据返回空切片。
- `Timestamp` 是聚合窗口起点的 Unix 毫秒时间戳，可用 `time.UnixMilli(b.Timestamp)` 转换；OHLC 为窗口价格，Volume 为成交量（允许小数），VWAP 为成交量加权均价，Transactions 为成交笔数，OTC 标识场外标的。后三项缺失时为 Go 零值。字段保留原 API JSON 标签 `t/o/h/l/c/v/vw/n/otc`。
- 无效参数、HTTP 非 2xx、JSON 解码失败、API 非 OK/DELAYED 状态或分页异常返回 nil 和 error，**分页中途失败也不返回部分结果**。HTTP 状态可通过 `errors.As` 提取 `*httpclient.StatusError`，网络与 context 错误保留错误链。
- 不自动重试或跟随 HTTP 重定向，不做本地缓存。权限、限流和数据可用范围由服务端决定。结果在内存中汇总，大范围分钟线建议调用方分段查询。

## 最小示例

以下是函数内片段，`ctx` 为调用方 context，`apiKey` 为 Massive API key：

```go
c, err := massive.New(massive.Config{
    APIKey: apiKey,
    ProxyURL: "http://127.0.0.1:7890",
})
if err != nil {
    return err
}
defer c.CloseIdleConnections()

bars, err := c.Daily(ctx, "AAPL", "2024-01-01", "2024-01-31", true)
if err != nil {
    return err
}
minutes, err := c.Minute(ctx, "AAPL", "2024-01-02", "2024-01-02", false)
if err != nil {
    return err
}
fmt.Println(len(bars), len(minutes))
```

包路径：`github.com/ekk1/mygo/utils/massive`。

## 并发与生命周期

客户端创建后配置不变，可复用并并发请求；分页状态与结果均归单次调用所有。调用方若共享并修改返回值，需要自行同步。`CloseIdleConnections` 释放空闲连接，不中断正在执行的请求。

官方文档：[股票聚合接口](https://massive.com/docs/rest/stocks/aggregates/custom-bars)、[鉴权](https://massive.com/docs/rest/quickstart)。
