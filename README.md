# mygo

一个仅使用 Go 标准库的通用 monorepo，用于积累可复用的工具包和独立命令行小工具。

开发前先读 [开发约定](AGENTS.md) 和 [utils 模块索引](utils/README.md)，再按需查看具体模块的接口文档。

## 环境

Go 1.24 或更高版本。整个仓库使用单个 Go module：`github.com/ekk1/mygo`。

## 目录结构

```text
.
├── cmd/
│   └── hello/
│       └── main.go       # 最小可运行示例
├── utils/
│   ├── logutil/          # 统一格式、支持级别过滤的并发安全日志
│   └── README.md         # 工具包组织与依赖约定
├── .gitignore
├── AGENTS.md             # 开发原则与 agent 接手流程
├── go.mod
└── README.md
```

- `cmd/<工具名>/`：每个子目录对应一个可独立运行、构建和安装的命令，使用 `package main`。
- `utils/<功能包>/`：按功能组织可复用代码，供各个命令和其他 utils 包按需引用。
- 依赖方向为 `cmd → utils → 标准库`；utils 之间允许无环依赖。
- 仅使用标准库和仓库内包，不引入第三方 Go 依赖。新增工具和工具包共用根目录的 `go.mod`，无需额外的 `go.mod` 或 `go.work`。

## 常用命令

以下命令均在仓库根目录运行：

```sh
# 运行示例工具
go run ./cmd/hello

# 构建指定工具，产物放入已忽略的 bin 目录
go build -o ./bin/hello ./cmd/hello

# 安装指定工具到 GOBIN（未设置时使用 GOPATH/bin）
go install ./cmd/hello

# 格式化、静态检查、测试和编译整个仓库
go fmt ./...
go vet ./...
go test ./...
go build -o ./bin/ ./...
```

## 扩展方式

新增命令时，创建 `cmd/<工具名>/main.go`。将参数解析和终端交互留在命令中，适合复用的逻辑提取到 `utils/<功能包>/`。

新增工具包时，使用明确的功能名称，并从 `github.com/ekk1/mygo/utils/<功能包>` 引用。更多约定见 [utils/README.md](utils/README.md)。

目前 `hello` 用于验证项目结构；可用工具包统一列在 [utils 模块索引](utils/README.md)。
