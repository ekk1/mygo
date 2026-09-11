# Personal Workbench Implementation Plan

> **For agentic workers:** use superpowers:subagent-driven-development. User explicitly requests implementation and push without intermediate approval.

**Goal:** 提供完整可运行的个人 OpenAI 工作台基础。
**Architecture:** cmd/workbench 内按存储、配置、聊天、资源与页面分文件；仅复用 utils，不引入跨厂商客户端抽象。
**Tech Stack:** Go 1.24、标准库、原生浏览器 JS、已有 httpserver/webui/kv/openai。
**Spec:** ../specs/2026-09-11-personal-workbench-design.md

## Global Constraints
- 复用已有 Go/Node/Chromium；用户后续明确授权 27 个 Debian 浏览器依赖包，已下载解包到任务临时目录。测试本身禁止下载。
- 工作在 /tmp/mygo-workbench，完成后合入 main 并推送 origin/main。
- Go 测试与浏览器仅使用本地假服务；日志必须写入成功才能执行请求。

## 任务 1：存储、配置、聊天与服务（主代理）
- [x] 在 store_test.go 编写分库、重启、映射校验、回滚测试，先运行失败。
- [x] 实现 store.go/config.go：Config、Provider、Model、Route；Session、Message；事务保存独立 KV。
- [x] app.go/main.go 注册 httpserver、webui 和 JSON API，监听与退出、目录锁、同源校验。
- [x] chat_test.go 通过 httptest 验证分支仅传祖先、映射不可绕过、工具切换、失败保存和并发冲突，再实现 chat.go。

## 任务 2：原生客户端扩展及资源接口（子代理）
- [x] tests 验证 request-only debug 不创建 response.body，保留 request；实现 openai.Config.DebugOmitResponseBody，维护文档。
- [x] 添加 ListModels，保留官方 raw model metadata，通过已有 transport 走代理/debug。
- [x] resources.go/resource_ops.go 实现统一操作入口（仅工作台 HTTP 路由，内部调用明确的 OpenAI 方法），覆盖已有公开操作清单；增加资源与 multipart 请求测试。

## 任务 3：浏览器工作台（子代理）
- [x] pages.go 使用 webui.Render 生成导航与页面骨架，embed 应用 CSS/JS。
- [x] 配置页 provider/key/catalog、逻辑模型和多线路；工作台会话/模型/工具/参数/折叠/分支/流式。
- [x] 资源页 Files、Containers、Batch 与原生操作表单；日志页 metadata/body 查看和响应记录开关。
- [x] 6 项浏览器测试通过，覆盖真实页面交互、资源上传下载、原生 JSON、四套主题明暗模式与手机布局；桌面/手机截图已目视检查。

## 任务 4：集成审查与发布
- [x] 独立代码审查并修复实际缺陷；补充 cmd/workbench/README.md、根 README 和 .gitignore。
- [x] GOTOOLCHAIN=local GOPROXY=off go fmt ./...; go test ./...; go vet ./...; go build -o ./bin/ ./...; go test -race ./...。
- [x] Node 请求测试通过；浏览器尝试启动失败，已记录依赖限制，未伪报通过。
- [x] 取得安装授权后补跑浏览器交互与目视验收，保持 sandbox 开启，退出和清理成功。
- [x] 已检查差异，提交实现 055563b，fast-forward main，push origin main 并核对远端提交；包含前置 OpenAI 客户端提交 e6df0cf。

## 交付说明

实现覆盖逻辑模型多线路映射、原生对话工具、分支/折叠、38 项资源操作、强制请求日志、可选响应正文与独立 KV。审查修复了退出等待、原生上下文跨模型/取消恢复、删除竞态、JSON 编码状态、原生扩展字段丢失、界面草稿丢失等缺陷。Go 和 JS 请求验证通过；用户授权依赖后，6 项浏览器测试和截图目视验收也已完成，修复了消息 null、输入区遮挡、会话按钮溢出及深色/手机可读性问题。
