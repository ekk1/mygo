# workbench

使用本仓库 httpserver、webui、kv 和 openai 的个人工作台。页面、脚本和样式编译进单个 Go 二进制，无前端构建步骤或运行时 SDK。

## 构建与运行

在仓库根目录使用 Go 1.24 或更高版本：

```sh
go build -o ./bin/workbench ./cmd/workbench
./bin/workbench -addr 127.0.0.1:8090 -data-dir ./workbench-data
```

打开 `http://127.0.0.1:8090`。所有相对路径基于启动程序时的工作目录，换目录运行时建议使用绝对 `-data-dir`。

| 参数/环境变量 | 默认值 | 用途 |
| --- | --- | --- |
| `-addr` | `127.0.0.1:8090` | HTTP 监听地址 |
| `-data-dir` | `./workbench-data` | 配置、会话与日志目录 |
| `WORKBENCH_PASSWORD` | 空 | 可选访问密码；非回环监听时必需，用户名固定为 `workbench` |

跨机器访问可使用 SSH 本地端口转发，保持服务监听回环地址；也可设置密码并监听 `0.0.0.0:8090` 供可信网络访问。原生 HTTP 服务不自行配置 TLS，同源检查不信任转发头，不能直接置于终止 TLS 的反向代理后。未设密码的本地模式仅接受 `localhost`/回环 IP 的 Host。

## 首次设置与模型映射

1. 打开“设置”，添加服务商，填写 API key、API 前缀和 HTTP 代理，保存。官方 API 前缀为 `https://api.openai.com/v1`；代理可填自己的 OpenAI 兼容地址。
2. 点击“发现全部模型”读取该服务商的原生 `/models` 目录。列表不存在时也能手动填写实际模型名。
3. 添加逻辑模型，填写你喜欢的显示名称，再建立一条或多条“服务商 → 实际模型名 → Responses/Chat Completions 协议”线路。
4. 勾选“在对话页精选展示”。工作台只能选择这些有映射的精选模型。管理页保留全部逻辑模型和各服务商的完整目录。

同一逻辑模型可以映射到多个服务商，第一条线路为默认线路，可调整顺序。客户端不自动切换、重试或降级，失败也不会改用另一条计费线路。API key 保存在本机配置文件，浏览器 GET 不会读到已保存明文；编辑留空保留原 key，显式删除才清空。配置有版本检查，其他页面已更新时会拒绝覆盖，需重新载入后修改。

`ProxyURL` 留空使用环境代理，`-` 表示直连，支持 http/https/socks5/socks5h。对话、资源、上传、下载和模型发现都使用相应服务商的代理配置。每次上游请求默认 10 分钟超时。

## 对话与分支

“对话”支持单次 HTTP 流式输出。每条消息可折叠；“从这里继续”选择下一次请求的父节点，“复制为新会话”复制到该节点为止的祖先路径，原会话不改变。另一分支的消息不会进入本次上下文。会话可重命名、删除，删除只影响本地会话，不删除 OpenAI 云端资源。

Responses 模型可以勾选联网搜索、图片生成和代码执行，展开高级设置可修改工具 type、图片工具 model、容器 ID、系统指令和原生 JSON 参数。文件 ID 可反复引用；留空容器 ID 时使用自动环境，填写现有容器 ID 时续用该环境。图片结果直接预览，执行结果和文件引用保留在折叠的原生输出中，可在容器页面下载产物。

Chat Completions 映射使用它自己的原生请求结构，Responses 工具开关禁用；Chat 原生搜索、音频、函数等参数可通过 JSON options 传入，完整结果亦可在原生操作页调用。聊天窗口显示第一条文本 choice；多 choice、音频等完整原生数据请使用原生操作页。

JSON options 保留 native 字段、false 和 0；不能覆盖工作台维护的 model/input/messages/stream/conversation/previous_response_id。选中的工具与同名原生 tools 冲突会报错；需要自定义完整 tools 时关闭便捷工具开关。模型/工具参数支持与否由实际服务商校验。

只有同一个服务商、同一实际模型、同一协议的完整 Responses 输出才复用原生工具内容。更换线路时用普通文本续接，避免传递旧工具 ID；已上传的文件归属原服务商，不会自动重传。取消或失败会保存已收到的文字和错误；同一会话一次只生成一个回答，其他会话与配置独立使用。

## 资源和原生操作

“文件”“容器”“批处理”各自提供所属服务商选择、参数表单、完整原生 JSON、上传或下载结果。分页是一次一页；列表返回的 `last_id` 可用于下一次 `after`。

- Files：上传、列表、详情、删除、内容下载；上传 purpose 与到期设置遵循原生协议。
- Containers：创建、列表、详情、删除，内存/到期/网络策略等原生参数，以及文件引用、上传、列表、详情、删除、产物下载。
- Batch：创建、列表、查询和取消。先在 Files 上传 purpose=batch 的 JSONL，再用其 ID 创建任务；输出与错误文件 ID 通过 Files 下载。没有自动轮询或自动重复提交。
- 原生操作：Responses 的生成/流式、查询/取消/删除、输入项、token counting/compact；Chat 普通/流式；图片生成/编辑/流式；语音合成、转写、翻译及非实时流式。

原生操作页保持服务商的完整请求参数，因此其中的 `model` 是实际服务商模型名；日常对话只通过精选逻辑模型。原生 SSE 操作收集完整事件后作为 JSON 展示，对话页则逐段显示文字。二进制响应提供下载。API 返回的外部媒体 URL 可以由调用方直接访问，工作台不提供任意 URL 转发。

资源操作只在点击执行后发起，不自动清理、翻页或运行本地程序。图片编辑允许 JSON 文件引用或 multipart 文件；不能混用两种方式。上传请求总计限制 300 MiB，超过 8 MiB 的 multipart 数据使用临时文件，结束后清理。缓冲 API 响应和原生流式事件的累计收集上限默认 256 MiB；媒体下载通过临时文件提供下载。原生操作中途失败时返回错误，已收集的部分事件不作为成功结果展示；需要排查完整接收过程时开启响应日志。

未接入 OpenAI 视频、Live/Realtime、MCP、Vector Stores，也未实现 Anthropic/Google/xAI 的原生客户端。

## 日志与存储

每次实际请求都开启请求记录，无法关闭。响应正文默认不记录，可在设置中修改默认值，也可按次覆盖。关闭时不会创建 `response.body`，而非保存后再删除；response metadata 仍保留状态、字节数等。聊天回复仍作为会话内容保存，与原始 HTTP 响应日志是两回事。

日志页可查操作、provider、时间、状态、request/response metadata 和正文；单个正文预览最多 256 KiB，截断时可下载完整记录。认证头和签名 URL 元信息脱敏，正文原样保留，包含提示词和上传文件。HTTP EOF 标志不表示模型完成；操作状态和会话错误应一并查看。日志写入失败会阻止请求或返回失败，不能据此认为服务端未执行并直接重试。

```text
workbench-data/
  config.json                 # 独立 config KV：provider、key、逻辑模型、日志默认值
  sessions/<session-id>.json   # 每个会话独立 KV：节点、父关系、输出与状态
  logs/<operation-id>/
    context.json              # 操作归属、时间与结果
    <request-id>/
      request.json
      request.body
      response.json
      response.body           # 仅开启响应日志时创建
  .lock/                      # 进程持有的排他目录锁
```

修改一个会话只保存它自己的 KV，不保存其他会话或 config。修改先保存成功再发布到内存，KV 使用同目录临时文件替换，不提供断电 fsync 保证。进程内支持并发，数据目录只能由一个进程使用。目录权限 0700、新写文件 0600；不要把数据目录纳入 Git 或静态网站。

正常退出会等待活动请求处理并保存，超时后取消连接再等待保存。异常终止后若 `.lock/` 留存，先确认没有运行实例，再删除这个锁目录；不要在另一个实例运行时移除。备份/跨机搬迁应先停止程序，复制整个数据目录。程序不会删除旧日志，需按自己的留存需求离线归档。

## 测试

Go 请求测试全部使用本地假 OpenAI 服务；不需要真实 API key：

```sh
go test ./cmd/workbench ./utils/openai ./utils/internal/aihttp
go test -race ./cmd/workbench
node --test utils/webui/request_test.cjs
```

浏览器测试使用已有 Node、Playwright 和 Chromium，不自动下载。先按 [webui 流程](../../utils/webui/README.md#开发与测试流程) 准备已获授权的测试环境，然后：

```sh
WORKBENCH_GO=go WORKBENCH_CHROMIUM=/usr/bin/chromium \
  node --test cmd/workbench/browser_test.cjs
```

脚本自行编译临时二进制。动态库不在系统搜索路径时，可用 `WORKBENCH_BROWSER_LIBS` 指向已安装浏览器库目录；Node 找不到 Playwright 时按现有环境设置 `NODE_PATH`。这些变量都只选择已有工具和库，不触发安装或下载。

测试脚本自行创建临时数据目录和本地假 provider，覆盖配置、目录发现、逻辑模型、对话、分支、日志、资源及手机布局。运行 Go 程序不需要这些浏览器测试工具。

### 本次验证记录（2026-09-11）

全仓 Go fmt/test/vet/build/race、JS 语法检查、webui 的 5 项请求测试通过，独立源码审查的阻塞问题已修复。未调用真实计费 API。浏览器 E2E 在启动钩子失败：现有 Chromium 缺少 `libcups.so.2`，另缺 Cairo/Pango 库，4 项浏览器测试均未执行到页面；桌面/手机目视验收也未完成。已请求安装授权但尚未取得，没有安装依赖或关闭 sandbox 绕过；可在具备上述测试环境的机器运行脚本补验。
