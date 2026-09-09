# utils

存放可复用的 Go 工具包，每个子目录是一个独立的 Go package。新增能力前先查本索引，使用模块前先读对应的 README。

## 模块索引

| 模块与接口文档 | 一句话用途 |
| --- | --- |
| [logutil](logutil/README.md) | 像 fmt.Print 一样直接打印日志，支持四个级别、统一格式和并发安全。 |
| [executil](executil/README.md) | 执行完整 Bash 命令或管理实时输出的后台进程。 |
| [fileutil](fileutil/README.md) | 列出、读写和筛选文件，文件系统错误统一 panic。 |
| [strutil](strutil/README.md) | 生成随机字符串并识别常见换行符切分内容。 |
| [waiter](waiter/README.md) | 按间隔检查条件，成功返回 nil，超时返回带消息的错误。 |

## 使用与维护

- 通过 `github.com/ekk1/mygo/utils/<模块名>` 引用，例如 `github.com/ekk1/mygo/utils/logutil`。
- 每个模块的 README 列出公开接口、最小示例和使用约定；也可运行 `go doc ./utils/logutil` 查看 Go doc。
- 新增模块时，在上表添加一行并提供模块 README；接口变更时同步模块文档。
- 保持接口简单，优先复用，不够用时再扩展。完整开发流程见 [AGENTS.md](../AGENTS.md)。
