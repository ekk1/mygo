# Massive / Tiingo 实现记录（归档）

2026-09-11 的已完成计划，仅保留历史背景，不作为当前开发指令或提交授权。

两个数据源各自提供独立客户端，复用 httpclient 的代理、超时和连接池。Massive 支持股票日线、1 分钟线及同源自动分页；Tiingo 返回 EOD 原始与调整后的行情。不引入第三方 Go 依赖。

实现时通过本地 HTTP 服务验证请求、错误、分页和并发行为，全仓库 fmt、test、vet、build、race 检查通过。使用 `GOTOOLCHAIN=local GOPROXY=off`，未下载工具与依赖，未使用真实 API key 联调。

当前接口、默认值、示例与官方依据以 [massive](../../../utils/massive/README.md)、[tiingo](../../../utils/tiingo/README.md) 文档为准；开发流程见 [AGENTS.md](../../../AGENTS.md)。
