# waiter

在指定时间内重复检查 bool 条件，成功返回 nil，超时返回包含场景消息的 error。

## 接口

```go
func Wait(timeout, interval time.Duration, condition func() bool, message string) error
```

- 立即检查一次；返回 false 后等待 interval 再检查，直到 true 或超时。timeout 包含条件执行耗时和等待耗时；剩余时间不足 interval 时只等待剩余时间。
- 超时错误包含 message 和 timeout，并包装 `context.DeadlineExceeded`，可用 `errors.Is` 判断。
- timeout、interval 必须大于零，condition 不能为 nil，否则返回包含 message 的参数错误，且不执行条件函数。message 可以为空。
- 同步调用条件函数，不会产生后台 goroutine，也不会重叠调用；条件函数应快速返回。无法中断阻塞中的函数，因此实际返回可能晚于 timeout；截止时间之后才返回 true 仍视为超时。条件函数 panic 原样向上传播。
- 包本身无共享状态，可并发调用；条件函数访问的共享数据由调用方保证同步。

## 示例

```go
err := waiter.Wait(5*time.Second, 100*time.Millisecond, func() bool {
    _, err := os.Stat("ready.flag")
    return err == nil
}, "等待 ready.flag")
if err != nil {
    fmt.Println(err)
}
```

导入 `github.com/ekk1/mygo/utils/waiter`，以及标准库 `time`、`os`、`fmt`。
