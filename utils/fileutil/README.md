# fileutil

直接列出、读写、筛选文件，所有文件系统错误均 panic，由上层按需 recover。

## 接口

```go
func Files(dir string) []string
func Dirs(dir string) []string
func Read(path string) string
func Write(path, content string)
func Latest(dir string) string
func Grep(path string, keywords []string, once bool) []string
```

- `Files` / `Dirs` 列出指定目录的直接子项，不递归；分别选择普通文件和真实目录，排除符号链接、特殊文件。结果按名称字典序排列，返回 `filepath.Join(dir, name)` 路径；相对输入仍返回相对路径，没有匹配项返回空切片。
- `Read` 一次读取全部内容，按原始字节返回字符串，不转换编码或换行。
- `Write` 创建或截断文件；新文件权限为 `0644`（受 umask 影响），已有文件保留权限。不自动创建父目录，不保证原子写入。Read/Write 按操作系统规则跟随符号链接。
- `Latest` 在 `Files` 范围内按修改时间选取最新文件；同一时间取名称字典序最小者。没有普通文件时 panic，错误包装 `os.ErrNotExist`。
- `Grep` 从文件中返回包含任意关键字的行：大小写敏感、字面子串 OR 匹配，不解释正则表达式。同一行命中多个关键字只返回一次，保留原顺序，去掉换行符。`once=true` 只返回第一条匹配行。空关键字数组不匹配任何行；数组里的空字符串匹配任意行。
- Grep 复用 [strutil.Lines](../strutil/README.md)，支持 LF、CRLF、CR 和长行；全文件读入内存，once 仅提前停止匹配，不避免读取整个文件。
- 无共享可变状态；对同一路径的并发写入、读写以及目录变化由调用方协调，不提供文件锁或目录快照。

## 示例

```go
package main

import (
    "fmt"
    "os"
    "path/filepath"
    "github.com/ekk1/mygo/utils/fileutil"
)

func main() {
    dir, err := os.MkdirTemp("", "fileutil-")
    if err != nil { panic(err) }
    defer os.RemoveAll(dir)
    path := filepath.Join(dir, "app.log")
    fileutil.Write(path, "INFO ready\r\nERROR failed\n")
    fmt.Print(fileutil.Read(path))
    fmt.Println(fileutil.Files(dir), fileutil.Dirs(dir))
    fmt.Println(fileutil.Latest(dir))
    fmt.Println(fileutil.Grep(path, []string{"ERROR", "WARN"}, true))
}
```
