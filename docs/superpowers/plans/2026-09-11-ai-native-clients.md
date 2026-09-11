# 四家 AI 原生客户端实现计划

> **For agentic workers:** 使用 superpowers:executing-plans 按任务执行；步骤以复选框跟踪。OpenAI 阶段已完成；按用户要求不接入 OpenAI 视频。其余三家保留后续待办。

**Goal:** 先完成 OpenAI，再接入 Anthropic、Google Gemini、xAI；提供原生生成、请求内工具、Files、Batch、OpenAI 容器，以及 OpenAI/Google/xAI 的非 Live 图片和语音能力，以及 Google/xAI 视频能力。

**Architecture:** 四家各自一个 utils 包，各自维护原生请求、响应、事件和资源方法，不定义统一 Provider、Message 或生成接口。复用 httpclient 的连接池、代理和证书配置；共同的传输、SSE 和调试落盘细节放在 utils/internal/aihttp，不承担厂商参数转换。

**Tech Stack:** Go 1.24+，标准库，仓库内 utils/httpclient；不安装依赖或 SDK。

**Spec:** 本文件“范围与行为约定”是本轮更新后的设计依据；移除 MCP 和检索资源管理，新增独立多模态生成，明确 OpenAI 为首个完整交付阶段。

## 全局约束

- 仅使用 Go 标准库和仓库内的 utils 包，不引入第三方 Go 依赖。
- 全仓库共用根目录的 go.mod。通用包放在 utils/<功能包>/。
- 不定义跨厂商统一 AI 抽象，也不让 xAI 依赖 openai 包的公开类型。
- 所有客户端支持 HTTP 代理，行为参考 utils/massive/README.md。
- 所有特色工具按请求显式开启；关闭即省略，不能偷偷加入工具或 beta header。
- debug 开启时，每一次实际 HTTP 请求都留下 request 和 response 日志。
- 图片、视频、语音支持普通 HTTP 请求及提交后查询结果；保留单次请求的服务端流式输出，不接入 Live/Realtime、WebSocket/WebRTC 双向实时会话。
- 导出接口、默认值、错误和并发行为在模块 README 中完整说明，导出符号保留 Go doc。
- 实际新增模块在实施时加入 utils/README.md。

## 范围与行为约定

### 1. 功能边界

| 包 | 核心生成 | 请求内能力 | 资源接口 |
| --- | --- | --- | --- |
| utils/openai | Chat Completions、Responses；普通与 SSE 流式；Images、Audio | 多模态输入、function calling、结构化输出、reasoning、service_tier、缓存参数、响应续接与后台处理；Web Search、Image Generation、Code Interpreter、原生 hosted shell 配置 | Files；Responses 获取/取消/删除及输入项；Containers 和容器文件；Batch |
| utils/anthropic | Messages、Count Tokens；普通与 SSE 流式 | 多模态内容、tool use/result、thinking、effort、prompt caching、service_tier、上下文管理；Web Search、Web Fetch、Code Execution、container 续用；原生客户端工具声明与结果回传 | Files；Message Batches |
| utils/gemini | generateContent、streamGenerateContent、Count Tokens、Interactions 及其原生流式；Gemini/Imagen 图片、Veo/Gemini 视频、TTS、音频理解与转写 | 多模态内容及输出配置、function calling、thinking、结构化输出、safetySettings、缓存引用；Google Search、URL Context、Code Execution；保留 thought signature、grounding 与 usage | Files；Batch；Interactions 生命周期；视频长任务查询和产物下载 |
| utils/xai | Chat Completions、Responses；普通与 SSE 流式；独立 Images、Videos、HTTP TTS/STT | 多模态输入、function calling、结构化输出、推理与服务档位原生参数；Web Search、X Search、Code Interpreter、Image Generation、文件附件 | Files；Batch；已存储 Responses 的官方生命周期方法；视频任务和生成产物；可用音色查询 |

明确移除 MCP、OpenAI Vector Stores、Gemini File Search Stores、xAI Collections 及相关 RAG 管理和专用工具封装。上传文件 ID/URI 直接参与生成请求仍在范围内。

核心请求中官方支持的字段尽量完整保留，包括工具选择、并行调用、结果包含项、缓存和多模态设置。不同 endpoint 使用各自的字段，不能把 Responses 的工具直接塞给 Chat Completions。

图片生成/编辑、视频生成/编辑及非实时语音是正式范围，既包含核心生成请求内的能力，也包含厂商独立 endpoint。暂不接入 Live/Realtime、双向语音/视频会话、微调、组织管理、本地 agent 执行器或显式缓存资源管理。客户端工具的声明、调用事件和结果回传属于生成协议，执行本地程序或自动工具循环由调用方负责。

### 2. 参数与工具版本

- 主模型由调用方传字符串，不写死模型名单。
- 各家 Request/Response 使用原生字段和 JSON 名称；常用字段有明确类型。多态内容、工具和未来字段允许 json.RawMessage / 扩展字段，返回值保留原始 JSON。
- 可选标量能区分“省略”和显式 false/0；扩展字段与已设置类型字段冲突时返回错误，避免隐式覆盖。
- 提供工具构造函数及公开配置：默认配置开启工具，工具不存在则关闭。工具 type、版本、name、工具自身 model 均按官方 schema 暴露可覆盖字符串，不限制为封闭枚举。
- 默认版本使用包内常量；仅工具被开启后才应用。调用方可以显式指定旧版、新版或代理别名，显式传入的原生工具 JSON 不再注入默认字段。
- 图片工具的 model 与顶层生成 model 分开；允许使用默认图片模型、自定义模型，或通过原生配置省略工具 model 以兼容代理。
- 独立图片/视频/音频请求也保留原生 model、voice/voice_id、格式、尺寸、时长及厂商特色参数，字段适用性以各 endpoint 为准；预设常量仅便于调用，不封闭模型或音色列表。
- 不给官方没有版本/model 字段的工具凭空添加字段；Beta/API version 与工具 type 分开配置。
- 不自动猜测模型兼容性，不在失败后悄悄换工具、降级或重复计费请求。

2026-09-11 核对的默认值候选，实施时再次核对官方 REST schema 与模型兼容说明：

| 能力 | 默认值与可覆盖位置 | 依据 |
| --- | --- | --- |
| OpenAI Responses Web Search | type = web_search；允许改为官方旧版本或代理支持的原生类型 | [Web Search](https://developers.openai.com/api/docs/guides/tools-web-search) |
| OpenAI Responses Image Generation | type = image_generation；model = gpt-image-2.5-flare，偏生成用途；可改 gpt-image-2.5-sunburst 等用于编辑 | [Image Generation](https://developers.openai.com/api/docs/guides/tools-image-generation) |
| Anthropic Web Search | type = web_search_20260318 | [Tool Reference](https://platform.claude.com/docs/en/agents-and-tools/tool-use/tool-reference) |
| Anthropic Web Fetch | type = web_fetch_20260318 | 同上 |
| Anthropic Code Execution | type = code_execution_20260521 | 同上；旧版本仍可能是旧模型所需版本 |
| xAI Responses | web_search、x_search、code_interpreter、image_generation；不因与 OpenAI 同名而复制其参数 | [Code Execution](https://docs.x.ai/developers/tools/code-execution)、[Image Generation](https://docs.x.ai/developers/tools/image-generation) |

上述“最新”指核对当日文档中的版本，运行时不联网更新常量。Google 的 generateContent 和 Interactions 工具 schema 分开维护，不给没有版本参数的工具加版本号。

### 3. Files 与执行环境

- Files 提供上传、分页列表、元信息、删除，以及官方允许的内容下载；支持文件 ID/URI 反复引用，保留状态、到期时间、purpose 等原生信息。
- 上传使用流式 I/O；Google resumable upload 的开始和传输是两个实际请求，均需经过代理与 debug。文件处理中状态和失败原因暴露给调用方。
- 下载写入 io.Writer，避免把大文件全部载入内存；不能把任意 API 返回 URL 当作可信地址转发认证头。
- 不自动删除用户文件、重传过期文件或创建收费容器；所有资源操作由调用方显式发起。
- OpenAI 完整覆盖创建/获取/列举/删除 container、配置自动 container 或引用已有 container、绑定/上传/列举/获取/删除容器文件、下载产物。支持官方提供的内存/到期配置，保留原生资源生命周期。
- OpenAI 工作流示例：上传 xlsx → 在 Responses 中启用执行工具并引用文件 → 再次请求复用环境 → 从结果提取容器产物并下载 xlsx/pptx。具体文件处理库和实际能力由云端环境提供，不在本仓库实现 Office 库。
- Anthropic 支持 container_upload 输入、container 续用、执行输出文件 ID 和 Files 下载，按原生协议处理 pause_turn。
- xAI API 已确认支持 Responses code_interpreter，在有常用 Python 库的沙箱中执行。官方工具页仅说明时间、内存和文件 I/O 限制，没有承诺 CPU/内存规格或网页版环境等价性；本计划不虚构独立 Containers API。

费用/留存事实只写文档，不编码为业务规则：

- [Gemini Files](https://ai.google.dev/gemini-api/docs/files)：Files API 免费，上传文件保存 48 小时，不能通过它下载用户上传的原文件；可复用不等于永久保存。
- [Anthropic Files](https://platform.claude.com/docs/en/build-with-claude/files)：文件操作免费，文件内容参与 Messages 时按输入 token 计费。
- 不把“Files 免费”推广成四家永久免费或推理免费；Files、提示缓存、向量存储、云端执行分别说明，OpenAI/xAI 的当前费用与有效期在各模块文档实施时按官方资料核对。

### 4. 客户端、代理与错误

各包提供 New(Config) (*Client, error)、CloseIdleConnections()；Config 至少包含 APIKey、BaseURL、ProxyURL、Timeout、Headers、Debug、DebugDir。认证/version/beta 额外选项按各家协议定义。

- ProxyURL 与 Massive 一致：空值遵循 HTTP_PROXY/HTTPS_PROXY/NO_PROXY；"-" 直连；显式 http/https/socks5/socks5h，支持代理认证。生成、SSE、文件、容器、Batch、图片/视频/语音、任务轮询和媒体下载所有请求使用同一套连接配置。
- BaseURL 默认官方地址，可设第三方代理和路径前缀；不自动跨厂商转换协议。初始化校验 scheme/host，并正确拼接、转义路径和查询参数。
- 默认每次 HTTP 请求超时 10 分钟，适应生成和执行；可设置 Timeout，整个工作流及等待由 context 限制。Batch 提交后直接返回作业，不用连接等待作业完成。
- 配置创建时复制，客户端创建后只读，可并发复用；调用方不并发修改同一请求/结果对象。
- 网络/context 错误保留错误链，非 2xx 保留状态、headers、原始 body、厂商 request ID。HTTP 200 内的原生错误事件/工具失败同样保留并暴露。
- 默认不自动重试或跟随重定向；分阶段上传和产物下载单独按官方 URL 规则处理，认证不能泄漏到外部 host。

### 5. Debug 落盘

- Debug=false 不创建日志。Debug=true 时 DebugDir 为空使用 ./ai-debug，文档明确其中包含请求正文和文件内容。
- 每次实际 HTTP 请求分配唯一 ID 和独立目录（目录 0700，文件 0600）；记录 method、脱敏 URL/headers、开始结束时间、状态和错误。
- request.body、response.body 保存应用层实际传输内容（JSON、multipart、二进制或 SSE）；认证头、API key 查询参数和认证信息脱敏，不静默删掉正文。
- 通过流式复制随收发写入，不把完整文件或流保存在内存；区分成功 EOF、取消、中断、读取失败和未收到响应。
- SSE 原始事件逐段落盘，不能仅记录最终拼接文本。上传失败记录已发部分；没有 HTTP response 时保留 response 元信息并写明原因。
- 日志创建/写入/关闭失败返回错误；如果服务端已经执行，错误及部分结果应保留，不能自动重试。
- 并发请求不覆盖、不交错；重定向禁止，未来任何显式重试都需要独立日志 ID。

### 6. Batch

四家分别实现官方异步 Batch 的提交、分页列表、查询、取消、结果读取/下载和官方允许的删除；保留 custom_id、逐条错误、usage、到期/取消状态，不统一作业 schema。

- OpenAI 支持 JSONL 文件提交，原生 endpoint 与 completion_window 参数。
- Anthropic 使用 Message Batches 原生请求列表和结果 JSONL；service_tier 不伪造 flex。[Service tiers](https://platform.claude.com/docs/en/api/service-tiers)、[Batch](https://platform.claude.com/docs/en/build-with-claude/batch-processing)。
- Gemini 支持 inline 和文件输入、异步操作状态；当前文档只支持 generateContent Batch，不实现 Interactions Batch。[Batch](https://ai.google.dev/gemini-api/docs/batch-api)。
- xAI 保留它的作业和请求追加流程，以及官方 JSONL Files 提交流程；不直接套 OpenAI 的请求路径。[Batch](https://docs.x.ai/developers/advanced-api-usage/batch-api)。

### 7. 非 Live 多模态

“请求返回结果”包含三种调用：一次 POST 返回 JSON/媒体；一次 POST 的单向流式响应；POST 提交异步任务后通过 GET 查询并下载。异步视频任务与折扣 Batch 是两个不同概念，不把视频轮询做成统一 Batch 抽象，也不要求调用方搭建 webhook 服务。

| 厂商 | 图片 | 视频 | 语音/音频 |
| --- | --- | --- | --- |
| OpenAI | Images 原生生成与编辑，支持参考图、mask、格式、尺寸、质量、背景与原生流式参数；保留 Responses 图片工具 | 本阶段不接入（用户明确暂缓） | Audio speech 文本转语音、transcriptions 文件转写、translations 文件翻译；Chat Completions 支持的音频输入/输出；保留音色、指令、格式、语言、时间戳和说话人信息等适用参数 |
| Google Gemini | Gemini 图片生成/编辑及 Imagen 独立生成；保留参考图、输出模态、尺寸/比例和安全参数 | Veo 长任务，以及 Gemini 原生协议支持的视频生成/编辑；保留参考图、首尾帧、延长、时长、比例、分辨率和音频配置等适用参数 | Gemini TTS 单人/多人语音；音频文件输入后的理解、转写与翻译通过生成接口实现；保留语音/说话人配置和输出音频 MIME 信息 |
| xAI | Images 原生生成/编辑、多参考图及 Responses 图片工具 | 文生视频、图生视频、参考素材、视频编辑/延长；提交、查询和产物下载 | POST /v1/tts 文本转语音、POST /v1/stt 文件转写及可用音色查询；保留语言、音色、音频格式和词级/多声道转写信息 |

官方依据（2026-09-11 核对）：

- OpenAI：[Images](https://developers.openai.com/api/docs/guides/image-generation)、[Videos](https://developers.openai.com/api/docs/guides/video-generation)、[TTS](https://developers.openai.com/api/docs/guides/text-to-speech)、[文件转写/翻译](https://developers.openai.com/api/docs/guides/speech-to-text)。Audio translations 当前输出英文，不包装成任意目标语言翻译。
- Google：[Gemini 图片](https://ai.google.dev/gemini-api/docs/image-generation)、[Imagen](https://ai.google.dev/gemini-api/docs/imagen)、[视频入口](https://ai.google.dev/gemini-api/docs/video)、[Veo](https://ai.google.dev/gemini-api/docs/veo)、[TTS](https://ai.google.dev/gemini-api/docs/speech-generation)、[音频理解](https://ai.google.dev/gemini-api/docs/audio)。Veo REST 按专门文档中的 predictLongRunning 和 operation 查询实现，不从概览推断为 generateContent 的同一 schema。
- xAI：[Images](https://docs.x.ai/developers/model-capabilities/images/generation)、[Videos](https://docs.x.ai/developers/model-capabilities/video/generation)、[TTS](https://docs.x.ai/developers/model-capabilities/audio/text-to-speech)、[STT REST](https://docs.x.ai/developers/rest-api-reference/inference/speech-to-text)。Images/Videos/Voice 使用各自原生 endpoint；相同名词不意味着兼容 OpenAI 的路径或参数。

**OpenAI 视频：** 用户已明确暂不接入。OpenAI 阶段不创建 videos.go，不包含视频相关方法和测试。Google/xAI 视频仍保留在后续阶段。

媒体 I/O 与等待约定：

- 请求原生字段传入图片/音频/视频或文件 ID/URL；不能假定每个 endpoint 都能使用 Files ID，需按实际 schema 提供 multipart、inline data 或 URL。
- 图片返回的 base64/URL、音频字节、视频 ID/operation/request_id、媒体元数据和原生错误完整保留；二进制下载写入 io.Writer。
- 不自动安装 ffmpeg、编解码库或调用本地转码程序；返回官方格式和 MIME，PCM 输出同时说明采样率等信息。
- 各家公开提交、单次查询和下载方法，并提供接受 context 的等待方法。等待默认间隔 5 秒，可配置正间隔；context 控制总期限，取消只停止等待，不擅自删除/取消云端任务。
- 生成只提交一次。轮询处理厂商原生成功/失败/过期/取消状态；失败返回任务 ID 与原始错误，网络错误不自动重新创建任务。未知状态返回包含原始响应的错误，避免无限等待。
- 媒体下载与每次状态查询都遵守代理和 debug 约定；签名 URL 不附带厂商 Authorization，日志元信息脱敏签名查询参数。过期下载链接返回明确错误，不重新生成收费内容。
- 不将所有多模态 endpoint 宣称为可 Batch；按各家 Batch 文档逐一记录支持情况和请求格式。

## 文件布局与执行顺序

**第一阶段只交付 OpenAI**：所需传输和 debug → 核心生成/工具 → Files/Containers → 图片 → 语音 → Batch → OpenAI 与全仓验收。共用传输随 OpenAI 实际需求实现，不先做覆盖四家的框架。

OpenAI 阶段完成后，再依次推进 Anthropic → Gemini → xAI，每家独立验收并运行全仓检查。按以下拆分实施 OpenAI，后续厂商保留待办。

### 任务 1：共用传输与逐请求日志

创建 utils/internal/aihttp/{client.go,debug.go,sse.go,client_test.go}。复用 utils/httpclient.New 返回的标准 http.Client，不把现有 JSON 方法的 16 MiB 读取方式用于文件和 SSE。

- [x] 用 httptest 编写代理转发、BaseURL 前缀、取消、HTTP 错误、并发日志、不完整流和日志写入失败用例，先确认失败。
- [x] 实现共享 HTTP 传输、受控响应读取、原始 SSE 事件解析和流式日志；原生 AI 数据模型留在厂商包。
- [x] 用拆分写入、多行 data、CRLF、超过 Scanner 默认长度的事件、EOF 和错误事件验证 SSE。
- [x] 执行 GOTOOLCHAIN=local GOPROXY=off go test -race ./utils/internal/aihttp；全部通过才进入厂商实现。

### 任务 2：OpenAI 第一阶段完整交付

创建 utils/openai/{client.go,chat.go,responses.go,tools.go,files.go,containers.go,images.go,audio.go,batches.go,README.md} 及对应 *_test.go；增加 utils/README.md 的 openai 行。

#### 2.1 核心生成与请求内工具

- [x] 核对 Chat Completions/Responses REST schema，建立请求字段与测试覆盖表；核对 hosted shell 与 Containers 是否共享资源，保持其原生配置。
- [x] 为普通/流式生成、工具默认值/版本覆盖/完全省略、显式 false/0、原始字段保留编写失败测试，再实现。
- [x] 验证工具执行输出、引用、usage、后台状态、续接 ID 和 HTTP 200 内错误均保留；用本地代理确认普通/SSE 都经过配置代理。

#### 2.2 Files 与云端执行

- [x] 为文件上传/分页/复用/删除和 container 创建/续用/文件引用/产物下载编写失败测试，再实现。
- [x] 验证 multipart 与下载中断均有成对日志、原始文件名转义正确、同一个上传 ID 可以用于第二次请求。
- [x] 完成 xlsx→云端执行→复用环境→下载 pptx 的可编译示例，并在 README 链接。

#### 2.3 独立图片生成/编辑

- [x] 核对 /images/generations、/images/edits 的 JSON/multipart 和流式 schema；为参考图、mask、模型/格式/尺寸参数和可选字段省略编写失败测试。
- [x] 实现 Images 方法，保留多张图片、base64/URL、usage 与原生错误；覆盖原生流式部分图片和最终结果。
- [x] 验证大图片响应不受普通小 JSON 默认上限误截断，媒体读取上限可配置；新增生成和编辑示例。

#### 2.4 非实时语音

- [x] 为 /audio/speech 的 JSON→二进制响应、/audio/transcriptions 与 /audio/translations 的 multipart 请求编写失败测试。
- [x] 实现 TTS、文件转写、翻译和原生响应格式，支持 context、下载写入错误、时间戳/说话人字段及已完成录音的单向流式转写。
- [x] 增加 Chat Completions 音频输入/输出的协议测试，README 给出文本→音频文件、录音→文字的最小用法，并明确不建立实时会话。

#### 2.5 Batch

- [x] 为 JSONL 提交、分页、查询、取消、输出/错误文件下载及逐条失败编写失败测试，再实现。
- [x] 保留 Batch 原生 endpoint 与 JSONL 文件提交格式；文档明确不假定所有多模态 endpoint 可批处理，服务端校验模型和端点支持。

#### 2.6 OpenAI 阶段验收

- [x] 完成所有导出接口文档和普通生成、流式、搜索、图片、语音、云端执行、Batch、代理/debug 示例。
- [x] 执行 GOTOOLCHAIN=local GOPROXY=off go test -race ./utils/openai ./utils/internal/aihttp。
- [x] 执行任务 6 的全仓格式化、测试、vet、build 和 race；区分本地协议验证与真实云端联调，完成 OpenAI 阶段后再进入下一家。

### 任务 3：Anthropic Messages、Files 与 Batch

创建 utils/anthropic/{client.go,messages.go,tools.go,files.go,batches.go,README.md} 及对应 *_test.go；增加 utils/README.md 的 anthropic 行。

- [ ] 核对 Messages、工具版本、Files/beta headers 的 REST schema；记录新旧版本差异和不支持的组合。
- [ ] 为鉴权/version、原生内容块、thinking/caching、工具版本覆盖、工具关闭、pause_turn、SSE 错误和 token counting 编写失败测试，再实现。
- [ ] 为 container_upload→再次引用 container→下载产物、Files 分页/删除和 Message Batches 结果流编写失败测试，再实现。
- [ ] README 列出所有导出接口，给出重复研究同一文件、搜索/执行开关、Batch 与代理示例。
- [ ] 执行 GOTOOLCHAIN=local GOPROXY=off go test -race ./utils/anthropic ./utils/internal/aihttp。

### 任务 4：Gemini 原生生成、多模态、Files 与 Batch

创建 utils/gemini/{client.go,content.go,interactions.go,tools.go,files.go,images.go,videos.go,audio.go,batches.go,README.md} 及对应 *_test.go；增加 utils/README.md 的 gemini 行。

- [ ] 分别核对 generateContent、Interactions 的原生 schema 和能力，不共享混合请求结构。
- [ ] 为多模态 part、thought signature、grounding、工具开关、generationConfig、SSE、Interactions 续接和 token counting 编写失败测试，再实现。
- [ ] 为 resumable upload 两次请求均走代理且有日志、文件处理中/到期状态、文件 URI 复用、inline/文件 Batch 和输出下载编写失败测试，再实现。
- [ ] 为 Gemini 图片生成/编辑及 Imagen 原生生成编写请求/结果测试，再实现；保留混合文本/图片、thought signature、安全拦截和输出配置。
- [ ] 为 Veo predictLongRunning、operation 查询、参考图/首尾帧/延长和视频下载编写失败测试，再实现；Gemini 原生视频生成/编辑按其生成协议单独验证。
- [ ] 为单人/多人 TTS、音频 inline/Files 输入、转写输出和 MIME/PCM 元数据编写失败测试，再实现；不把 Gemini 生成式转写伪装成独立 STT endpoint。
- [ ] README 说明 Files 48 小时期限、下载限制和两种生成协议差异；例子覆盖搜索、执行、文件复用、Batch、图像、视频和非实时音频。
- [ ] 执行 GOTOOLCHAIN=local GOPROXY=off go test -race ./utils/gemini ./utils/internal/aihttp。

### 任务 5：xAI 原生生成、服务端工具、多模态、Files 与 Batch

创建 utils/xai/{client.go,chat.go,responses.go,tools.go,files.go,images.go,videos.go,audio.go,batches.go,README.md} 及对应 *_test.go；增加 utils/README.md 的 xai 行。

- [ ] 核对 xAI REST 的工具字段、Files 与 Batch 路径；确认 code_interpreter 和 image_generation 当前参数，不照抄 OpenAI container/model 字段。
- [ ] 为 Web Search/X Search 过滤配置、执行/图片工具开关、工具输出事件、文件附件复用与工具原生参数覆盖编写失败测试，再实现。
- [ ] 为 Batch 创建/请求追加/文件提交/查询/取消/结果下载、Files 分页和错误保留编写失败测试，再实现。
- [ ] 为独立图片生成/编辑、多参考图、格式/比例、返回 URL/base64 编写失败测试，再实现。
- [ ] 为 /videos/generations、/videos/edits、/videos/extensions 和 /videos/{request_id} 状态查询编写失败测试，再实现；覆盖生成一次、轮询取消、失败/过期和签名 URL 下载。
- [ ] 为 HTTP /tts、/stt 及音色列表编写失败测试，再实现；覆盖音频字节、语言/voice_id、词级时间戳和多声道结果，不连接 WebSocket。
- [ ] README 给出执行 Python、图片/视频、TTS/STT 的最小例子，并说明官方未承诺执行环境硬件规格；明确可用文件访问行为。
- [ ] 执行 GOTOOLCHAIN=local GOPROXY=off go test -race ./utils/xai ./utils/internal/aihttp。

### 任务 6：接口与全仓验收

- [ ] 检查四家核心请求 schema 覆盖表、所有导出符号 Go doc、模块 README 和索引链接；确认没有新增 MCP/RAG 管理或统一 Provider。
- [ ] 每家验证最小生成、同一文件二次引用、搜索开关、原生版本覆盖、Batch、代理、debug；OpenAI/Google/xAI 额外验证图片生成/编辑、TTS 和音频转写；Google/xAI 额外验证视频任务/下载。本地假服务验证协议，真实厂商联调与退役接口状态单独标明。
- [ ] 使用已安装工具，设置 GOTOOLCHAIN=local 和 GOPROXY=off，避免验证隐式下载；如本地版本不能满足要求，按 AGENTS.md 的安装确认规则处理。
- [ ] 执行 go fmt ./...、go test ./...、go vet ./...、go build -o ./bin/ ./...、go test -race ./...，检查实际结果后报告。

## 本轮计划检查

- 已保留原始需求中的四家、核心生成、Files、Batch、debug、第三方代理兼容和 OpenAI 执行环境。
- 已按本轮要求移除 MCP 和各类向量/检索资源管理，并补充所有请求统一走 HTTP 代理的约定。
- 已区分主模型、工具 type 版本、图片工具模型和 API/beta version；当前默认值有官方出处，允许覆盖和省略。
- 已新增 OpenAI/Google/xAI 独立图片和非实时语音，以及 Google/xAI 视频；保留单次请求流式响应，排除 Live/Realtime 双向会话。
- OpenAI 是第一阶段完整交付，不等待其余三家；用户已明确暂缓 OpenAI 视频，相关实现任务已移除。
- 本次实施 OpenAI；不安装依赖，不调用计费 API。

## OpenAI 阶段交付记录

- 已实现 `utils/openai` 和内部传输 `utils/internal/aihttp`，更新模块索引、全部导出接口文档及示例。没有跨厂商生成抽象或第三方依赖。
- 独立审查发现并修复原生输出二次编码丢失字段、debug 提前结束上传记录、签名 Location 脱敏三个问题；复现用例和复审通过。
- 已通过全仓 `go fmt ./...`、`go test ./...`、`go vet ./...`、`go build -o ./bin/ ./...` 和 `go test -race ./...`。使用已有 Go 1.24，设置 `GOTOOLCHAIN=local GOPROXY=off`，未安装工具。
- 验证为本地 httptest 协议和错误边界测试；未调用真实 OpenAI API。实际模型兼容性、云端 Office 文件执行效果和计费未作在线验证。
- 其余三家尚未实施，任务 6 的四家联合验收保留待办；OpenAI 视频暂缓。

复验说明：放回原工作目录后，并行运行检查时既有 `TestServeShutdownAndConcurrentRegistration` 出现 1 秒 Shutdown 超时；该文件未改动。单独连续 5 次通过，随后串行运行全仓 `go test ./...` 与 `go test -race ./...` 均通过。保留此偶发超时记录，未为本次 AI 接入调整既有 HTTP server。
