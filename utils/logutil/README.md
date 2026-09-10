# logutil

像 `fmt.Print` 一样直接打印日志，支持四个级别、统一格式和并发安全。

## 接口

| 接口 | 说明 |
| --- | --- |
| `type Level int` | 日志级别类型。 |
| `LevelDebug`、`LevelInfo`、`LevelWarn`、`LevelError` | 从低到高的四个级别，值依次为 0、1、2、3。 |
| `SetLevel(l Level)` | 设置全局最低输出级别，默认 `LevelInfo`。 |
| `Debug(args ...any)` | 打印 Debug 日志。 |
| `Info(args ...any)` | 打印 Info 日志。 |
| `Warn(args ...any)` | 打印 Warn 日志。 |
| `Error(args ...any)` | 打印 Error 日志，不退出进程。 |

四个打印函数均无返回值，参数拼接规则与 `fmt.Print` 相同，自动追加换行，所有级别都输出到 stdout。底层输出错误不返回给调用方。

## 使用示例

```go
package main

import "github.com/ekk1/mygo/utils/logutil"

func main() {
    logutil.Info("开始执行，任务=", "demo")
    logutil.Warn("准备重试，次数=", 2)
    logutil.Error("执行失败：", "连接超时")

    logutil.SetLevel(logutil.LevelDebug)
    logutil.Debug("调试信息：", 42)
}
```

统一格式（本地时间）：

```text
[2026-09-09 18:30:00] [INFO] 开始执行，任务=demo
```

## 使用约定

只打印设定级别及以上的日志，`SetLevel` 对整个进程中的 logutil 调用生效。

内部通过互斥锁保护级别和输出，可以并发修改级别、打印日志。
消息中的换行原样保留；需要格式化字符串时可传入 `fmt.Sprintf(...)` 的结果。
