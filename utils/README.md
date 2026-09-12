# utils

存放可复用的 Go 工具包，每个子目录是一个独立的 Go package。新增能力前先查本索引，使用模块前先读对应的 README。

## 模块索引

| 模块与接口文档 | 一句话用途 |
| --- | --- |
| [logutil](logutil/README.md) | 像 fmt.Print 一样直接打印日志，支持四个级别、统一格式和并发安全。 |
| [executil](executil/README.md) | 执行完整 Bash 命令或管理实时输出的后台进程。 |
| [fileutil](fileutil/README.md) | 列出、读写和筛选文件，文件系统错误统一 panic。 |
| [strutil](strutil/README.md) | 生成随机字符串并识别常见换行符切分内容。 |
| [kv](kv/README.md) | 并发安全的内存 KV 数据库，支持 string、hash、list 和整库 JSON 保存加载。 |
| [assetstore](assetstore/README.md) | 并发安全地流式保存二进制资产及名称、收藏与来源元数据。 |
| [waiter](waiter/README.md) | 按间隔检查条件，成功返回 nil，超时返回带消息的错误。 |
| [openai](openai/README.md) | 原生调用 OpenAI 生成、图片、语音、Files、Containers 与 Batch，支持代理和逐请求日志。 |
| [httpclient](httpclient/README.md) | 配置代理和证书，一次调用完成 JSON/Form 请求及响应解码。 |
| [massive](massive/README.md) | 获取 Massive 股票日线和 1 分钟线，支持代理与自动分页。 |
| [tiingo](tiingo/README.md) | 获取 Tiingo EOD 原始与调整后日线，支持代理。 |
| [httpserver](httpserver/README.md) | 快速注册路由、中间件和静态目录，支持 HTTP、TLS 及 Unix socket。 |
| [webui](webui/README.md) | 用 Go 组合 HTML 页面，提供本地配色、视频位置按钮、SVG K 线图和请求辅助 JS。 |

## 使用与维护

- 通过 `github.com/ekk1/mygo/utils/<模块名>` 引用，例如 `github.com/ekk1/mygo/utils/logutil`。
- 接口、默认值和示例见各模块 README；也可运行 `go doc ./utils/<模块名>`。索引维护与开发流程见 [AGENTS.md](../AGENTS.md)。
