# AI 通用客户端待办

工作台已在 `cmd/workbench` 直接接入四家的原生 HTTP 接口；可供其他 Go 程序复用的包目前只有 [utils/openai](../utils/openai/README.md)。以下待办指独立 utils 包，不表示工作台尚未接入这些服务商。

| 待办包 | 待实现范围 |
| --- | --- |
| Anthropic | Messages、thinking/caching、搜索与执行工具、文件与容器引用、Message Batches、token counting。 |
| Gemini | generateContent 与 Interactions、搜索与执行工具、Files 与 Batch、Gemini/Imagen 图片、Veo 视频任务、TTS 与音频理解。 |
| xAI | Responses 与 Chat、Web/X Search 与执行/图片工具、Files 与 Batch、图片生成/编辑、视频生成/编辑/延长、HTTP TTS/STT 与音色查询。 |

各家独立维护原生请求、响应和事件，不增加跨厂商生成接口。先评估工作台已有实现中可复用的能力，再确定包内接口；不提前固定文件拆分或默认模型。

实现时重新核对官方 schema、工具版本和支持限制，保留未知返回字段与显式 false/0。覆盖代理、日志、流式错误、文件复用、分页、任务取消和媒体下载；不得自动重试计费请求，取消等待也不得自动取消云端任务。

完成后按 [开发约定](../AGENTS.md) 补齐模块 README、Go doc、模块索引和必要测试。本地假服务验证与真实账号联调分别说明。Live/Realtime、MCP、RAG 资源管理、微调、组织管理和本地工具自动执行器不在此待办范围。
