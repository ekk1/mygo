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
- `tasks.go`：任务持久化、独立执行、原始流订阅与取消。
- `asset*.go`：资产 API、原生附件解析、媒体捕获与旧会话导入；通用文件存储在 `utils/assetstore`。
- `assets/library.js` / `library.css`：全局资产和任务页面、精选选择器、持久化媒体展示。

## 页面交互约定

复用 `workbench.js` 中的字段、按钮、弹窗和结果展示；厂商参数与请求仍由各自 builder 处理。生成页遵循模型、输入、参数、结果的布局，参数宽屏侧置、窄屏后置。字段间距由 workbench 样式统一覆盖 webui 的独立表单默认值。

异步操作使用 `W.action` 或 `W.lock` 恢复控件状态，弹窗提交期间同时设置 `setBusy`。`W.confirm` 返回 Promise，操作成功才关闭，失败留在弹窗内。不要在 await 之后读取 event.currentTarget，也不要把节点数组直接传给 replaceChildren；后者需要展开数组。

模型、资源、日志、会话、资产和任务列表仅由刷新、分页或重试按钮读取；进入页面、打开选择框或操作完成都不隐式重拉列表。列表使用当前标签页的 sessionStorage 缓存，通过 `W.readCache` / `W.writeCache` 容忍存储不可用；无缓存与已读取的空列表分开显示。模型和资源缓存绑定配置 revision，换 key 后不沿用旧账号目录；资源还按 profile、base URL、类型及容器隔离，分页成功后才提交数据、游标和历史，读取期间禁用行操作。会话列表只缓存摘要，保存和管理操作使用响应更新本地摘要。

资产选择复用 `W.assetControl` / `W.pickAssets`，管理页以外不提供本地文件输入。`W.assetResults(ids,{lazyMedia:true})` 在展开前不读取缺失元信息或媒体；生成页直接展示保存的资产。原生结果通过 `W.result(value,{skipMedia:true})` 保留文字与 JSON，避免重复解码已入库的媒体。

带 attachment 的下载先按 Blob 读取，不能按 JSON 或聊天逐行协议重新解码。

对话正文默认展开，用消息按钮切换正文与摘要；折叠状态在当前页面按会话和消息 ID 记忆，不参与请求构造。`W.result(value,{lazyMedia:true})` 为聊天媒体生成占位，首次展开才解码；`W.jsonDetails` 首次展开才序列化 JSON。同一消息对象复用 DOM，避免选择续接点时重建媒体。历史容器统一滚动，消息保持内容高度，不允许收缩裁切；输入区不参与历史滚动，超长状态或错误提示单独滚动。复用结果展示时去掉重复正文，并保留媒体和复制操作。

应用样式通过 `webui.Page.Styles` 随 HTML 加载，不能等应用 JS 执行后才插入。请求预览统一调用 `W.showPreview`，在异步操作前记录触发按钮，关闭后恢复焦点；资源编辑器可以在自身弹窗上再打开预览。

## 本地请求约定

`POST /api/native/{vendor}/{operation}` 的 JSON 包装为 `{"provider_id":"…","params":{…},"save_response":false}`，其中 save_response 可省略。可附加 `asset_ids:{"image":["资产 ID"]}`；键为 attachment（聊天图片）、image（编辑原图）、mask（PNG 蒙版）、file（转写或云端上传）。multipart 使用同名文本字段及原生文件字段，asset_ids 是 JSON 文本。profile 必须属于 URL 中的服务商；页面携带 `revision` 查询参数，与凭据从同一个配置快照校验。

`?preview=1` 返回 method、url、content_type、body、files 和 curl，不创建日志或调用上游。导出失败时保留请求预览并返回 curl_error。Gemini resumable 上传另返回两个阶段的 requests；第二阶段 URL 在实际发送时由上游启动请求返回，预览中使用占位，curl 与传输共用阶段 header 构造。

curl 的 JSON 正文通过标准输入发送；multipart 长字段写入私有临时文件，退出时清理，避免命令行参数长度限制。Gemini 导出脚本核对文件大小并限制上传地址同源。本地 HTTP 测试核对真实 Bash/curl 发送的字节、文件顺序、转义和失败清理。

仅用于路由的 file_id、container_id、batch_id、video_id 从请求体移除；Gemini 的 model 和资源 name 按操作放入路径。分页保留各家原生字段：OpenAI after、Anthropic after_id、Gemini pageToken/pageSize、xAI pagination_token。不要在传输层统一这些字段。

生成页向原生端点传 `?background=1&feature=image` 等功能名，立即以 202 返回 `{task}`。文字使用 `POST /api/sessions/{id}/native` 并传 `background:true`；非流式以 202 返回 `{task,session}`，流式首行 started 返回任务和待生成会话，event 转发事件，done 返回已保存的 task 与 session。新会话预览使用 id=new，不创建会话。旧的同步本地 API 调用方式仍可用，浏览器生成页统一使用后台模式。

`GET /api/tasks` 返回不含 result 的摘要，可按 provider_id / feature 过滤；`GET /api/tasks/{id}` 返回完整结果；`POST /api/tasks/{id}/cancel` 提交取消，响应可能仍是 running，需读取最终状态；`DELETE /api/tasks/{id}` 只删除结束记录。前端只在提交当前任务时观察详情，恢复页面不重接流、不轮询列表；延迟详情响应必须核对原任务及会话仍被选中。

`GET /api/assets?favorite=1` 只列精选；`POST /api/assets` 接收 multipart 文件并返回资产数组；详情和 PATCH/DELETE 使用 `/api/assets/{id}`。PATCH 接受 name / favorite，内容地址 `/api/assets/{id}/content?download=1`。`POST /api/assets/import-sessions` 返回本次导入的资产数组，只读旧内联媒体，按会话保存引用，失败回滚当前会话新增资产。

附件构造依据：[OpenAI 图片输入](https://developers.openai.com/api/docs/guides/images-vision)、[Anthropic Vision](https://platform.claude.com/docs/en/build-with-claude/vision)、[Gemini 图片理解](https://ai.google.dev/gemini-api/docs/image-understanding)、[xAI 图片编辑](https://docs.x.ai/developers/model-capabilities/images/editing)。资产引用单独保存，不替换会话里的原生图片、推理或签名。

## 上下文回传

以下规则于 2026-09-12 对照官方文档核对，适用于工作台会话层；`utils/openai` 不自动管理历史。验证范围见 [测试说明](README.md#测试)。

| 协议 | 同模型、已完成消息的回传 |
| --- | --- |
| OpenAI Responses | 保留完整 output，包括 reasoning、加密内容、工具项和图片生成结果。显式传入 `previous_response_id` 或 `conversation` 时不再叠加本地历史。依据：[会话状态](https://developers.openai.com/api/docs/guides/conversation-state)、[推理](https://developers.openai.com/api/docs/guides/reasoning)、[图片工具](https://developers.openai.com/api/docs/guides/image-generation#multi-turn-image-generation)。 |
| OpenAI / xAI / 通用 Chat | 保留第一 choice 的助手内容及 tool/function 结果，去除输出专用 annotations，audio 转成输入允许的 `{id}`；流式 tool_calls 按 choice 和调用索引拼接参数。兼容站点扩展的 reasoning_content 原样保留，不并入普通文本。依据：[Chat 输入与输出 schema](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create)。 |
| Anthropic Messages | 保留 content blocks 的顺序及 thinking/signature、redacted thinking、server tools 与结果；流式拼回签名及工具参数。依据：[Thinking](https://platform.claude.com/docs/en/build-with-claude/thinking)、[工具结果](https://platform.claude.com/docs/en/agents-and-tools/tool-use/handle-tool-calls)。 |
| Gemini generateContent | 保留第一 candidate 的 content.parts，包括 thoughtSignature、工具调用/结果和 inlineData。thought:true 的文字不计入可见正文。依据：[Thought signatures](https://ai.google.dev/gemini-api/docs/generate-content/thought-signatures)。 |
| xAI Responses | 保留原生 output（包括图片生成结果），并合并 `include:["reasoning.encrypted_content"]` 以取得无状态续接所需内容；使用服务端历史引用时不重复本地历史。依据：[Advanced usage](https://docs.x.ai/developers/tools/advanced-usage)、[图片续接](https://docs.x.ai/developers/tools/image-generation#multi-turn-editing)。 |

OpenAI 当前文档说明 `store:false` / ZDR 的推理加密内容默认返回，因此不强行添加旧版 include。媒体 URL、文件 ID 和音频 ID 仍可能过期，本地完整保存不保证上游资源永久可用。

更换模型时过滤旧工具结果、签名和生成图片链，跳过纯结构空助手消息。中止和失败的结果不回传未闭合的工具或推理结构，只保留可见部分文本。历史折叠和预览截断都不参与请求构造。

当前没有客户端工具自动执行循环、Anthropic pause_turn 自动续跑或多 choice/candidate 分支选择；Realtime / Live 也没有页面。服务端工具由上游执行，具体模型能力、权限、beta 字段和文件/资源有效期仍由上游决定。

## 流式与传输边界

后台任务在落盘并持有输入后才执行，使用独立 context 和提交时的配置快照。HTTP 返回、浏览器断开或消费事件过慢只结束实时显示；事件通道有界，不反向阻塞模型。任务依次捕获资产、保存会话、保存最终状态；取消与最终保存串行。Shutdown 的接收门与 WaitGroup.Add 共锁，取消后等待任务保存，重启只恢复状态和结果，不重新发请求。

任务摘要与资产元信息可用于列表缓存；完整输出只在详情/会话中读取。会话最终保存失败时原生结果仍尝试保存在任务中并报告错误。媒体保存失败不会自动重新生成。资产媒体从允许的原生输出字段提取，跳过重复 native_events；二进制下载以资产 ID 替代私有临时路径。云端视频和 Batch 的执行周期仍由服务商管理。

SSE 必须收到协议终态才能判定成功：Responses 的 response.completed、Chat 的 [DONE] 或 finish_reason、Anthropic 的 message_stop、Gemini 的 finishReason。读取失败、错误事件或缺少终态时保留部分事件与错误；HTTP EOF 本身不是完成信号。

重建助手结果前先复制原始事件，避免修改原始记录或生成循环引用。修改流式或上下文行为时运行 conversation 与 native 测试。

原生 API 请求禁止自动跟随重定向。生成媒体下载最多跟随 4 次重定向，外部地址校验后固定实际连接 IP，保留原始 Host、TLS 主机名与签名路径；配置中的 provider 同源地址允许为本地服务。日志的 request_bytes 记录传输实际读取的字节数，失败拨号或中断上传不能标成完整请求。认证信息脱敏不得破坏错误的 Unwrap；原始正文日志按用户设置保留。

Anthropic Messages 引用 Files 文档源时需要 Files beta header。Gemini 用户上传文件和生成结果文件的下载能力不同。OpenAI 音频转写可能返回 text/srt/vtt，不能强制按 JSON 解码。这些协议差异由 native 测试覆盖。
