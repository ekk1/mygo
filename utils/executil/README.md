# executil

用完整 Bash 命令同步执行或启动后台进程，收集 stdout/stderr 并查询进程状态。

## 接口

```go
func Run(command string) (string, error)
func RunEnv(command string, env map[string]string) (string, error)
func Start(command string, onOutput func(string)) (*Process, error)
func StartEnv(command string, env map[string]string, onOutput func(string)) (*Process, error)
type Process struct { /* 非导出字段 */ }
func (p *Process) PID() int
func (p *Process) Running() bool
func (p *Process) Wait() error
func (p *Process) Stop() error
```

- `Run` 使用 `bash -c`，支持管道、重定向、引号和命令串，返回合并的全部输出；启动失败或非零退出返回 error，失败前的输出仍保留。输出完整存入内存。
- `RunEnv` / `StartEnv` 在继承当前进程环境的基础上覆盖同名变量；nil 等同不额外设置。不修改父进程环境，变量名不得为空、含 `=` 或 NUL，值不得含 NUL。调用期间不要并发修改传入的 map。
- `Start` / `StartEnv` 启动成功立即返回，之后自动等待并回收进程。通过回调实时接收合并的 stdout/stderr；nil 回调丢弃输出。未缓存历史输出，需要时调用方自行累积。
- 回调按输出块串行调用，块可能包含多行、半行或不完整的 UTF-8 字符；跨流次序取决于进程和系统调度。回调必须尽快返回，慢回调会阻塞输出读取；不要在回调内调用 `Wait`，否则会死锁。回调 panic 不会被捕获。
- `PID` 返回启动的 shell PID。`Running` 表示仍在等待进程退出或输出处理完成。`Wait` 等待输出处理结束后返回退出错误，可以重复调用；成功返回 nil，非零退出或被杀死返回 error。
- `Stop` 强制终止进程但不等待；Unix 上终止同一进程组，其他系统只终止直接进程。已经完成时返回 nil。另建进程组的后代不在终止范围内；调用 `Wait` 确认结束。自行后台化且持有输出描述符的子进程可能延长 `Wait`。
- 所有 Process 方法并发安全；只通过 `Start` / `StartEnv` 创建，不复制实例，不使用零值。回调中维护的数据若被其他 goroutine 读取，需要调用方同步；`Wait` 返回后可直接读取回调结果。
- 需要 PATH 中有 Bash，工作目录继承调用进程。这里的后台指异步执行，不是脱离父进程的守护进程。命令字符串会执行 shell 语法，动态数据可通过 env 传入并在命令中引用。

## 示例

```go
package main

import (
    "fmt"
    "github.com/ekk1/mygo/utils/executil"
)

func main() {
    out, err := executil.RunEnv(`printf '%s\n' "$NAME" | tr a-z A-Z`, map[string]string{"NAME": "hello"})
    fmt.Print(out)
    if err != nil { panic(err) }

    p, err := executil.Start("for i in 1 2 3; do echo $i; sleep 1; done", func(chunk string) {
        fmt.Print(chunk)
    })
    if err != nil { panic(err) }
    fmt.Println("PID:", p.PID())
    if err := p.Wait(); err != nil { panic(err) }
}
```
