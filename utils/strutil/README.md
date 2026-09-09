# strutil

生成简单随机字符串，或识别常见换行符并切分内容。

## 接口

```go
func Random(n int) string
func Lines(s string) []string
```

- `Random` 返回长度为 n 的 ASCII 大小写字母和数字字符串。n 为 0 返回空串，负数 panic。使用标准库 `math/rand/v2`，并发安全，无需手动设置种子；不保证唯一性，不用于密码、令牌等安全用途。
- `Lines` 识别 LF (`\n`)、CRLF (`\r\n`) 和 CR (`\r`)，可以混用；返回不含换行符的各行。保留内部空行；末尾换行不会额外产生一个空行。空输入返回空切片。无 Scanner 长行限制，不改变其他空白或编码，无共享状态。

## 示例

```go
id := strutil.Random(12)
lines := strutil.Lines("first\r\nsecond\rthird\n")
// lines 为 []string{"first", "second", "third"}
_ = id
_ = lines
```

导入路径：`github.com/ekk1/mygo/utils/strutil`。
