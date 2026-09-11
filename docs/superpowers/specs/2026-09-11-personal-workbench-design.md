# 个人工作台设计记录（归档）

本设计已于 2026-09-11 实现，保留架构取舍，不作为执行或授权指令。当前用法以 [workbench README](../../../cmd/workbench/README.md) 为准，验收状态见 [交付记录](../plans/2026-09-11-personal-workbench.md)。

以 cmd/workbench 交付单个可构建二进制，复用 httpserver 和 webui。

## 配置与模型

Provider 保存名称、OpenAI 原生 API 前缀、API key、HTTP 代理及发现的完整模型目录。管理页允许发现模型，也能手工填写代理未列出的模型。逻辑模型有独立 ID、显示名、精选开关，以及有顺序的 provider/model/protocol 映射。首条映射为默认线路，不自动重试或故障切换。对话页面只可选择精选且有效映射的逻辑模型，不让调用方绕过映射传原始模型名；管理页可看到全部逻辑模型及各 provider 目录。密钥只提交更新，GET 返回 has_key，空 key 保留原值，显式 clear_key 删除。

## 对话

消息节点含 ID、parent_id、角色、文本、逻辑模型/实际线路快照、原生响应输出、状态与时间。会话内从选定 parent 发送；每个节点可折叠，任意节点可复制祖先路径到新会话形成分支，原会话不变。网页流式接收文字，失败和取消保存已产生内容并允许查日志。只按祖先路径构造上下文，不能包含另一分支。生成时不持有配置/其他会话锁；同一会话禁止并发生成。模型改变后仅跨线路复用普通文本，不把旧厂商工具 ID 传到新线路。

Responses 支持 web/image/exec 工具开关，工具 type/model 可编辑，exec 支持自动容器或指定容器及文件 ID；提供原生 JSON 参数编辑器。Chat Completions 使用独立原生请求 schema，不注入 Responses tools。核心请求之外的生命周期及图片/语音方法由原生操作页面提供。

## 资源与日志

Files、Containers、Batch 分页页面各自选择资源归属 provider，提供所有已有 CRUD、上传、内容下载、作业取消方法及原生参数。原生操作页提供 Responses 生命周期/token counting/compact、图片与非实时音频，支持 multipart 上传、JSON 响应和媒体下载；不新增视频等未实现范围。

每次发往 provider 的 HTTP 请求强制记录 request metadata/body，response metadata 始终保存，response body 开关决定是否落盘（默认关）；不能先保存再删除冒充关闭。日志页列出操作、时间、provider、状态并查看原始 request/response，敏感认证元信息脱敏，正文保持原样。日志为逐操作独立目录，不放入配置或会话 KV。

## 存储与服务

config.json 为独立 kv.DB，每个 sessions/<id>.json 为独立 kv.DB，会话目录扫描获取索引；一次写会话不保存 config 或其他会话。修改先保存临时状态成功再发布到内存，磁盘失败不返回成功。进程内保证并发安全，同一数据目录只运行一个实例（排他锁）。使用 kv 原子替换能力，不承诺 fsync。数据目录 0700，配置/会话/日志文件 0600。

浏览器不获得明文 key。默认本地使用；非回环监听要求 WORKBENCH_PASSWORD，复用 BasicAuth；所有写入验证同源 Origin（无 Origin 的 CLI 需要 X-Workbench-Request: 1），防止跨站页面调用本机接口。数据与日志不作为静态目录发布。响应和模型文本通过 textContent/webui 转义，不渲染任意 HTML。
