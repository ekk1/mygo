# AI 原生客户端：交付记录与后续计划

状态更新于 2026-09-11：OpenAI 已交付；Anthropic、Google Gemini、xAI 尚未实施。本文保留后续范围与验收任务，不作为自动执行、安装或提交授权。当前已实现接口以 [OpenAI README](../../../utils/openai/README.md) 为准。

## 已交付：OpenAI

`utils/openai` 提供 Chat Completions、Responses、请求内工具、Images、非实时 Audio、Files、Containers 和 Batch；`utils/internal/aihttp` 复用 httpclient，处理传输、SSE 和逐请求日志。工作台随后增加了模型目录查询与可选响应正文日志。

已通过本地 httptest 协议、代理、错误边界与并发测试，以及全仓 fmt/test/vet/build/race。未调用真实计费 API；模型兼容性和云端 Office 文件处理效果未经在线验证。当前用法、默认值、工具版本及示例仅在模块 README 维护。

## 后续共同约定

- 四家各自维护原生请求、响应、事件和资源方法，不定义跨厂商 Provider/Message/生成抽象；xAI 不依赖 OpenAI 公开类型。只使用标准库和已有 utils，按实际需要复用内部传输。
- 核心请求尽量保留原生字段、多态内容和未知返回字段；可选标量区分省略与 false/0，扩展字段冲突报错。不同 endpoint 不混用 schema。
- 主模型、工具 type/版本/name、图片工具 model、API version 和 beta header 分别配置，支持覆盖和省略；工具只在显式加入请求后启用。没有原生版本字段的工具不额外加字段。
- 所有实际请求（含 SSE、上传、下载、任务查询）支持 HTTP 代理，行为沿用 [httpclient](../../../utils/httpclient/README.md)。BaseURL 支持第三方代理前缀；不自动换模型、降级或重试计费请求。
- Debug 默认关闭，开启后逐 HTTP 请求保存 request/response 元信息和正文；允许显式省略响应正文。流式落盘、认证元信息脱敏，上传和生成正文原样保留；日志失败返回错误，并发请求独立记录。工作台始终记录请求的策略由 cmd 层实现。
- Files 支持官方允许的上传、分页、查询、删除、下载及 ID/URI 复用；分阶段上传的每个请求均走代理与日志。保留处理中、到期与错误信息，不自动重传或清理资源。
- 下载使用 io.Writer，签名媒体 URL 不携带厂商认证。请求超时与整个工作流的 context 分开；配置创建后只读，可并发复用。
- Batch 保持各家原生作业格式和逐条结果，不统一 schema，不假定所有多模态 endpoint 可批处理。Anthropic 不伪造 flex 档位，折扣异步请求通过 Message Batches 接入。
- 非实时多模态包括一次请求返回 JSON/媒体、单向流式响应、提交异步任务后查询下载。视频轮询与折扣 Batch 分开；等待受 context 控制，取消等待不自动取消云端任务，不重复生成。
- 不实现 OpenAI 视频、Live/Realtime、MCP、Vector Stores/File Search Stores/Collections、微调、组织管理或本地工具自动执行器；Google/xAI 视频仍在后续范围。

## 实施前需重新核对

后续顺序为 Anthropic → Gemini → xAI，每家独立验收。工具版本、模型候选、endpoint、Files 留存/收费、Batch 支持范围和执行环境限制都可能变化；原计划中的具体版本及费用不作为当前保证。实施时查询官方 REST schema，记录日期和支持限制，再确定包内默认常量。

- Anthropic：Messages、thinking/caching、Web Search/Web Fetch/Code Execution、container 续用、Files 与 Message Batches。
- Gemini：generateContent 与 Interactions 分开；Google Search、URL Context、Code Execution；Files、Batch；Gemini/Imagen 图片、Veo 及官方支持的视频生成/编辑、TTS、音频理解与转写。
- xAI：Chat Completions 与 Responses、Web Search/X Search/Code Interpreter/Image Generation、Files、Batch、独立图片/视频与 HTTP TTS/STT。不能把网页版环境规格当作 API 承诺，也不预设独立 Containers API。

## 后续任务

以下路径和方法拆分为计划，只有实现后才加入 [模块索引](../../../utils/README.md)。端点和字段名称仍需按上述约定复核。

### 任务 1：Anthropic Messages、Files 与 Batch

创建 utils/anthropic/{client.go,messages.go,tools.go,files.go,batches.go,README.md} 及对应 *_test.go；增加 utils/README.md 的 anthropic 行。

- [ ] 核对 Messages、工具版本、Files/beta headers 的 REST schema；记录新旧版本差异和不支持的组合。
- [ ] 为鉴权/version、原生内容块、thinking/caching、工具版本覆盖、工具关闭、pause_turn、SSE 错误和 token counting 编写失败测试，再实现。
- [ ] 为 container_upload→再次引用 container→下载产物、Files 分页/删除和 Message Batches 结果流编写失败测试，再实现。
- [ ] README 列出所有导出接口，给出重复研究同一文件、搜索/执行开关、Batch 与代理示例。
- [ ] 执行 GOTOOLCHAIN=local GOPROXY=off go test -race ./utils/anthropic ./utils/internal/aihttp。

### 任务 2：Gemini 原生生成、多模态、Files 与 Batch

创建 utils/gemini/{client.go,content.go,interactions.go,tools.go,files.go,images.go,videos.go,audio.go,batches.go,README.md} 及对应 *_test.go；增加 utils/README.md 的 gemini 行。

- [ ] 分别核对 generateContent、Interactions 的原生 schema 和能力，不共享混合请求结构。
- [ ] 为多模态 part、thought signature、grounding、工具开关、generationConfig、SSE、Interactions 续接和 token counting 编写失败测试，再实现。
- [ ] 为 resumable upload 两次请求均走代理且有日志、文件处理中/到期状态、文件 URI 复用、inline/文件 Batch 和输出下载编写失败测试，再实现。
- [ ] 为 Gemini 图片生成/编辑及 Imagen 原生生成编写请求/结果测试，再实现；保留混合文本/图片、thought signature、安全拦截和输出配置。
- [ ] 为 Veo predictLongRunning、operation 查询、参考图/首尾帧/延长和视频下载编写失败测试，再实现；Gemini 原生视频生成/编辑按其生成协议单独验证。
- [ ] 为单人/多人 TTS、音频 inline/Files 输入、转写输出和 MIME/PCM 元数据编写失败测试，再实现；不把 Gemini 生成式转写伪装成独立 STT endpoint。
- [ ] README 说明实施时核对的 Files 留存期限、下载限制和两种生成协议差异；例子覆盖搜索、执行、文件复用、Batch、图像、视频和非实时音频。
- [ ] 执行 GOTOOLCHAIN=local GOPROXY=off go test -race ./utils/gemini ./utils/internal/aihttp。

### 任务 3：xAI 原生生成、服务端工具、多模态、Files 与 Batch

创建 utils/xai/{client.go,chat.go,responses.go,tools.go,files.go,images.go,videos.go,audio.go,batches.go,README.md} 及对应 *_test.go；增加 utils/README.md 的 xai 行。

- [ ] 核对 xAI REST 的工具字段、Files 与 Batch 路径；确认 code_interpreter 和 image_generation 当前参数，不照抄 OpenAI container/model 字段。
- [ ] 为 Web Search/X Search 过滤配置、执行/图片工具开关、工具输出事件、文件附件复用与工具原生参数覆盖编写失败测试，再实现。
- [ ] 为 Batch 创建/请求追加/文件提交/查询/取消/结果下载、Files 分页和错误保留编写失败测试，再实现。
- [ ] 为独立图片生成/编辑、多参考图、格式/比例、返回 URL/base64 编写失败测试，再实现。
- [ ] 为 /videos/generations、/videos/edits、/videos/extensions 和 /videos/{request_id} 状态查询编写失败测试，再实现；覆盖生成一次、轮询取消、失败/过期和签名 URL 下载。
- [ ] 为 HTTP /tts、/stt 及音色列表编写失败测试，再实现；覆盖音频字节、语言/voice_id、词级时间戳和多声道结果，不连接 WebSocket。
- [ ] README 给出执行 Python、图片/视频、TTS/STT 的最小例子，并说明官方未承诺执行环境硬件规格；明确可用文件访问行为。
- [ ] 执行 GOTOOLCHAIN=local GOPROXY=off go test -race ./utils/xai ./utils/internal/aihttp。

### 任务 4：接口与全仓验收

- [ ] 检查四家核心请求 schema 覆盖表、所有导出符号 Go doc、模块 README 和索引链接；确认没有新增 MCP/RAG 管理或统一 Provider。
- [ ] 每家验证最小生成、同一文件二次引用、搜索开关、原生版本覆盖、Batch、代理、debug；OpenAI/Google/xAI 额外验证图片生成/编辑、TTS 和音频转写；Google/xAI 额外验证视频任务/下载。本地假服务验证协议，真实厂商联调与退役接口状态单独标明。
- [ ] 使用已安装工具，设置 GOTOOLCHAIN=local 和 GOPROXY=off，避免验证隐式下载；如本地版本不能满足要求，按 AGENTS.md 的安装确认规则处理。
- [ ] 执行 go fmt ./...、go test ./...、go vet ./...、go build -o ./bin/ ./...、go test -race ./...，检查实际结果后报告。
