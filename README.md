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
│   └── webui-demo/       # 页面、表单和配色示例
├── utils/
│   ├── <模块>/           # 实现、测试和模块 README
│   └── README.md         # 唯一模块索引
├── docs/superpowers/plans/ # 已归档的实现记录
├── bin/                  # 构建与截图产物，已忽略
├── AGENTS.md             # 开发、安装与验证约定
├── go.mod
└── README.md
```

命令入口放在 `cmd/<工具名>/`，可复用代码放在 `utils/<模块>/`。包依赖和接口维护要求见 [开发约定](AGENTS.md)，现有模块见 [模块索引](utils/README.md)。

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
