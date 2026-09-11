# mygo

一个仅使用 Go 标准库的通用 monorepo，用于积累可复用的工具包和独立命令行小工具。

开发前先读 [开发约定](AGENTS.md) 和 [utils 模块索引](utils/README.md)，再按需查看具体模块的接口文档。

## 环境

Go 1.24 或更高版本。整个仓库使用单个 Go module：`github.com/ekk1/mygo`。

## 目录结构

```text
.
├── cmd/
│   ├── hello/            # 最小命令示例
│   ├── workbench/        # 个人 AI 工作台：模型、对话、资源与日志
│   └── webui-demo/       # 页面、表单和配色示例
├── utils/
│   ├── <模块>/           # 实现、测试和模块 README
│   └── README.md         # 唯一模块索引
├── docs/superpowers/      # 设计记录、已完成归档与后续计划
├── bin/                  # 构建与截图产物，已忽略
├── AGENTS.md             # 开发、安装与验证约定
├── go.mod
└── README.md
```

## 常用命令

以下命令均在仓库根目录运行：

```sh
# 运行命令示例或 webui 示例
go run ./cmd/hello
go run ./cmd/webui-demo

# 构建指定工具，产物放入已忽略的 bin 目录
go build -o ./bin/hello ./cmd/hello

# 安装指定工具到 GOBIN（未设置时使用 GOPATH/bin）
go install ./cmd/hello

# 格式化、静态检查、测试和编译整个仓库
go fmt ./...
go vet ./...
go test ./...
go build -o ./bin/ ./...

# 涉及共享状态或并发时
go test -race ./...
```

webui 的原生 JS 和浏览器测试需要单独执行，完整流程见 [webui 开发与测试流程](utils/webui/README.md#开发与测试流程)。

## 个人工作台

```sh
go build -o ./bin/workbench ./cmd/workbench
./bin/workbench -addr 127.0.0.1:8090 -data-dir ./workbench-data
```

打开工作台的“设置”页面配置服务商和精选模型映射。完整用法、跨机访问、日志和存储说明见 [workbench 文档](cmd/workbench/README.md)。

## 设计与计划

已完成功能的用法维护在各 README，已完成计划归档，未实施需求继续保留为待办。[AI 客户端计划](docs/superpowers/plans/2026-09-11-ai-native-clients.md)中 OpenAI 已交付，Anthropic、Gemini、xAI 为后续待办；[工作台计划](docs/superpowers/plans/2026-09-11-personal-workbench.md)已归档。
