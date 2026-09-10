# kv

并发安全的内存 KV 数据库，支持 string、hash、list，并可将完整数据保存为 JSON。

## 接口

```go
type DB struct { /* 非导出字段；零值可用 */ }
var ErrNotFound error
var ErrWrongType error

func (db *DB) Set(key, value string)
func (db *DB) Get(key string) (string, error)
func (db *DB) Keys() []string
func (db *DB) Delete(keys ...string) int

func (db *DB) HSet(key, field, value string) error
func (db *DB) HGet(key, field string) (string, error)
func (db *DB) HGetAll(key string) (map[string]string, error)
func (db *DB) HKeys(key string) ([]string, error)

func (db *DB) RPush(key string, values ...string) (int, error)
func (db *DB) LPop(key string) (string, error)
func (db *DB) LRange(key string, start, stop int) ([]string, error)

func (db *DB) Save(path string) error
func (db *DB) Load(path string) error
```

- `Set` 设置字符串，覆盖 key 的原有类型和值。其余 string/hash/list 操作要求类型匹配，否则返回 `ErrWrongType`。
- `Get`、`HGet`、`LPop` 在 key 或字段不存在时返回 `ErrNotFound`，可用 `errors.Is` 判断；空字符串是合法值，不表示缺失。key、字段也允许空字符串。
- `Keys` 返回所有类型的 key，`HKeys` 返回 hash 的字段，均按字典序排列，不支持模式匹配。
- `Delete` 删除任意类型的 key，返回实际删除数量，忽略重复和不存在的 key。
- `HSet` 创建或覆盖一个字段；`HGetAll` 返回全部字段的副本。不存在的 hash，`HGetAll` / `HKeys` 返回空 map / 切片和 nil。
- `RPush` 按参数顺序追加元素并返回列表长度，不传值时只返回长度、不创建 key。`LPop` 弹出左侧元素，最后一个元素弹出后删除 key。
- `LRange` 返回区间副本，起止下标都包含在内；负下标从末尾计数，`0, -1` 取全部。越界裁剪，区间为空或 key 不存在时返回空切片和 nil。
- `Save` 手动保存整个数据库，`Load` 整体替换当前内容，不做合并。JSON 包含 `types`、`strings`、`hashes`、`lists` 四个表；加载校验类型与数据一致性，失败不修改当前内容。空对象 `{}` 表示空数据库，`null` 非法。
- 持久化使用标准库 JSON 字符串语义，适合 UTF-8 文本；无效 UTF-8 字节会被替换，不适合保存任意二进制字符串。
- 文件操作失败返回 error，文件不存在可用 `errors.Is(err, os.ErrNotExist)` 判断。不会自动创建父目录。保存先写同目录临时文件再重命名替换目标，新文件权限为 `0600`（受 umask 影响），替换后不保留旧权限。不执行 fsync，不保证断电持久性；替换的原子性遵循平台 `os.Rename` 语义。
- 每个 DB 独立持有数据，零值可用，无需构造函数或 Close，使用后不得复制。所有方法并发安全，返回的 map/切片可以独立修改；传入参数调用期间不得被并发修改。多次方法调用不构成事务。
- Save/Load 执行期间阻塞同一 DB 的其他操作。不同 DB 或进程共用文件时由调用方协调，无文件锁。
- 不自动保存，也不提供 TTL 或容量上限。需要隔离时使用多个 DB 和文件。

## 最小示例

导入 `github.com/ekk1/mygo/utils/kv`：

```go
var db kv.DB
// 恢复已有数据时调用 db.Load("data.json") 并处理错误。
db.Set("name", "demo")
name, err := db.Get("name")
if err != nil { return err }
_ = name
if err := db.HSet("user:1", "name", "Alice"); err != nil { return err }
if _, err := db.RPush("queue", "job1", "job2"); err != nil { return err }
job, err := db.LPop("queue")
if err != nil { return err }
_ = job
return db.Save("data.json")
```

示例片段放在返回 error 的函数中；需要独立数据库时另声明一个 `kv.DB`。
