# workbench 开发说明

运行方式、页面能力、存储约定和测试命令见 [README](README.md)。这里记录页面与请求处理的内部约定。

## 代码职责

- `pages.go` 和 `assets/workbench.js`：工作台外壳、profile、设置与日志。
- `assets/ai-pages.js`：各家独立参数与生成页面；`assets/resources.js`：资源列表与操作。
- `native.go`：按服务商构建原生请求，发送和预览复用同一构造逻辑。
- `native_curl.go`：从同一请求描述导出 curl，保留原文并用环境变量引用凭据与本地文件。
- `native_transport.go`：HTTP、代理、日志、SSE、二进制下载及 Gemini 分阶段上传。
- `conversation*.go`：原生历史分支、流式转发与结果持久化。
- `config.go`、`store.go`：配置版本、凭据与会话存储。

## 页面交互约定

复用 `workbench.js` 中的字段、按钮、弹窗和结果展示；厂商参数与请求仍由各自 builder 处理。生成页遵循模型、输入、参数、结果的布局，参数宽屏侧置、窄屏后置。字段间距由 workbench 样式统一覆盖 webui 的独立表单默认值。

异步操作使用 `W.action` 或 `W.lock` 恢复控件状态，弹窗提交期间同时设置 `setBusy`。`W.confirm` 返回 Promise，操作成功才关闭，失败留在弹窗内。不要在 await 之后读取 event.currentTarget，也不要把节点数组直接传给 replaceChildren；后者需要展开数组。

模型、资源、日志和会话列表仅由刷新、分页或重试按钮读取；进入页面、打开选择框或操作完成都不隐式重拉列表。列表使用当前标签页的 sessionStorage 缓存，通过 `W.readCache` / `W.writeCache` 容忍存储不可用；无缓存与已读取的空列表分开显示。模型和资源缓存绑定配置 revision，换 key 后不沿用旧账号目录；资源还按 profile、base URL、类型及容器隔离，分页成功后才提交数据、游标和历史，读取期间禁用行操作。会话列表只缓存摘要，保存和管理操作使用响应更新本地摘要。

带 attachment 的下载先按 Blob 读取，不能按 JSON 或聊天逐行协议重新解码。文字显示当前祖先路径，流式失败保留部分输出和输入草稿。

对话正文默认展开，用消息按钮切换正文与摘要；折叠状态在当前页面按会话和消息 ID 记忆，不参与请求构造。`W.result(value,{lazyMedia:true})` 为聊天媒体生成占位，首次展开才解码；`W.jsonDetails` 首次展开才序列化 JSON。同一消息对象复用 DOM，避免选择续接点时重建媒体。历史容器统一滚动，消息保持内容高度，不允许收缩裁切；输入区不参与历史滚动，超长状态或错误提示单独滚动。复用结果展示时去掉重复正文，并保留媒体和复制操作。

应用样式通过 `webui.Page.Styles` 随 HTML 加载，不能等应用 JS 执行后才插入。请求预览统一调用 `W.showPreview`，在异步操作前记录触发按钮，关闭后恢复焦点；资源编辑器可以在自身弹窗上再打开预览。

## 本地请求约定

`POST /api/native/{vendor}/{operation}` 的 JSON 包装为 `{"provider_id":"…","params":{…},"save_response":false}`，其中 save_response 可省略。multipart 使用同名文本字段及原生文件字段。profile 必须属于 URL 中的服务商；页面携带 `revision` 查询参数，与凭据从同一个配置快照校验。

`?preview=1` 返回 method、url、content_type、body、files 和 curl，不创建日志或调用上游。导出失败时保留请求预览并返回 curl_error。Gemini resumable 上传另返回两个阶段的 requests；第二阶段 URL 是上游启动请求返回的同源地址，curl 与实际传输共用阶段 header 构造。curl 测试通过本地 HTTP 服务核对真实 Bash/curl 发送的字节、文件顺序、转义、长字段与失败清理。

仅用于路由的 file_id、container_id、batch_id、video_id 从请求体移除；Gemini 的 model 和资源 name 按操作放入路径。分页保留各家原生字段：OpenAI after、Anthropic after_id、Gemini pageToken/pageSize、xAI pagination_token。不要在传输层统一这些字段。

文字使用 `POST /api/sessions/{id}/native`，流式响应为逐行 JSON，event 行转发上游事件，done 行返回已持久化会话。新会话预览使用 id=new；预览不创建会话。

## 上下文回传

以下为 2026-09-12 按官方文档核对的当前实现；单元测试和本地假服务覆盖请求形态与流式重组，尚未用真实账号逐项联调。

| 协议 | 同模型、已完成消息的回传 |
| --- | --- |
| OpenAI Responses | 保留完整 output，包括 reasoning、加密内容、工具项和图片生成结果。显式传入 `previous_response_id` 或 `conversation` 时不再叠加本地历史。依据：[会话状态](https://developers.openai.com/api/docs/guides/conversation-state)、[推理](https://developers.openai.com/api/docs/guides/reasoning)、[图片工具](https://developers.openai.com/api/docs/guides/image-generation#multi-turn-image-generation)。 |
| OpenAI / xAI / 通用 Chat | 保留第一 choice 的助手内容及 tool/function 结果，去除输出专用 annotations，audio 转成输入允许的 `{id}`；流式 tool_calls 按 choice 和调用索引拼接参数。兼容站点扩展的 reasoning_content 原样保留，不并入普通文本。依据：[Chat 输入与输出 schema](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create)。 |
| Anthropic Messages | 保留 content blocks 的顺序及 thinking/signature、redacted thinking、server tools 与结果；流式拼回签名及工具参数。依据：[Thinking](https://platform.claude.com/docs/en/build-with-claude/thinking)、[工具结果](https://platform.claude.com/docs/en/agents-and-tools/tool-use/handle-tool-calls)。 |
| Gemini generateContent | 保留第一 candidate 的 content.parts，包括 thoughtSignature、工具调用/结果和 inlineData。thought:true 的文字不计入可见正文。依据：[Thought signatures](https://ai.google.dev/gemini-api/docs/generate-content/thought-signatures)。 |
| xAI Responses | 保留原生 output（包括图片生成结果），并合并 `include:["reasoning.encrypted_content"]` 以取得无状态续接所需内容；使用服务端历史引用时不重复本地历史。依据：[Advanced usage](https://docs.x.ai/developers/tools/advanced-usage)、[图片续接](https://docs.x.ai/developers/tools/image-generation#multi-turn-editing)。 |

OpenAI 当前文档说明 `store:false` / ZDR 的推理加密内容默认返回，因此不强行添加旧版 include。媒体 URL、文件 ID 和音频 ID 仍可能过期，本地完整保存不保证上游资源永久可用。

更换模型时沿用已有产品策略：保留用户普通输入和附件、助手可见文本，过滤失去调用前项的工具结果并跳过纯结构空助手消息。中止和失败的结果不回传未闭合的工具或推理结构，只保留可见部分文本。历史折叠和预览截断都不会改变这些规则。

当前没有客户端工具自动执行循环、Anthropic pause_turn 自动续跑或多 choice/candidate 分支选择；Realtime / Live 也没有页面。服务端工具由上游执行，具体模型能力、权限、beta 字段和文件/资源有效期仍由上游决定。

## 流式与传输边界

SSE 必须收到协议终态才能判定成功：Responses 的 response.completed、Chat 的 [DONE] 或 finish_reason、Anthropic 的 message_stop、Gemini 的 finishReason。读取失败、错误事件或缺少终态时保留部分事件与错误；HTTP EOF 本身不是完成信号。

重建助手结果前先复制原始事件，避免修改原始记录或生成循环引用。中断时保存已收到的文字；跨模型不复用旧助手工具状态。修改这些行为时运行 conversation 与 native 测试。

请求禁止自动跟随重定向。日志的 request_bytes 记录传输实际读取的字节数，失败拨号或中断上传不能标成完整请求。认证信息脱敏不得破坏错误的 Unwrap；原始正文日志按用户设置保留。

Anthropic Messages 引用 Files 文档源时需要 Files beta header。Gemini 用户上传文件和生成结果文件的下载能力不同。OpenAI 音频转写可能返回 text/srt/vtt，不能强制按 JSON 解码。这些协议差异由 native 测试覆盖。
