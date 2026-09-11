# workbench 开发说明

运行方式、页面能力、存储约定和测试命令见 [README](README.md)。这里仅记录维护请求处理时需要注意的内部约定。

## 代码职责

- `pages.go` 和 `assets/workbench.js`：工作台外壳、profile、设置与日志。
- `assets/ai-pages.js`：各家独立参数与生成页面；`assets/resources.js`：资源列表与操作。
- `native.go`：按服务商构建原生请求，发送和预览复用同一构造逻辑。
- `native_transport.go`：HTTP、代理、日志、SSE、二进制下载及 Gemini 分阶段上传。
- `conversation*.go`：原生历史分支、流式转发与结果持久化。
- `config.go`、`store.go`：配置版本、凭据与会话存储。

## 本地请求约定

`POST /api/native/{vendor}/{operation}` 的 JSON 包装为 `{"provider_id":"…","params":{…},"save_response":false}`，其中 save_response 可省略。multipart 使用同名文本字段及原生文件字段。profile 必须属于 URL 中的服务商；页面携带 `revision` 查询参数，与凭据从同一个配置快照校验。

`?preview=1` 返回 method、url、content_type、body 和 files，不创建日志或调用上游。Gemini resumable 上传另返回两个阶段的 requests；第二阶段 URL 是上游启动请求返回的同源地址。

仅用于路由的 file_id、container_id、batch_id、video_id 从请求体移除；Gemini 的 model 和资源 name 按操作放入路径。分页保留各家原生字段：OpenAI after、Anthropic after_id、Gemini pageToken/pageSize、xAI pagination_token。不要在传输层统一这些字段。

文字使用 `POST /api/sessions/{id}/native`，流式响应为逐行 JSON，event 行转发上游事件，done 行返回已持久化会话。新会话预览使用 id=new；预览不创建会话。

## 流式与传输边界

SSE 必须收到协议终态才能判定成功：Responses 的 response.completed、Chat 的 [DONE] 或 finish_reason、Anthropic 的 message_stop、Gemini 的 finishReason。读取失败、错误事件或缺少终态时保留部分事件与错误；HTTP EOF 本身不是完成信号。

重建助手结果前先复制原始事件，避免修改原始记录或生成循环引用。中断时保存已收到的文字；跨模型不复用旧助手工具状态。修改这些行为时运行 conversation 与 native 测试。

请求禁止自动跟随重定向。日志的 request_bytes 记录传输实际读取的字节数，失败拨号或中断上传不能标成完整请求。认证信息脱敏不得破坏错误的 Unwrap；原始正文日志按用户设置保留。

Anthropic Messages 引用 Files 文档源时需要 Files beta header。Gemini 用户上传文件和生成结果文件的下载能力不同。OpenAI 音频转写可能返回 text/srt/vtt，不能强制按 JSON 解码。这些协议差异由 native 测试覆盖。
