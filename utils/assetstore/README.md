# assetstore

并发安全地保存二进制资产与名称、收藏和来源信息，支持有上限的流式导入。

## 接口

```go
const DefaultMaxBytes int64 = 300 << 20
var ErrNotFound error
var ErrTooLarge error
type Source map[string]string
type Asset struct {
    ID, Name, ContentType string
    Size int64
    Favorite bool
    CreatedAt string
    Source Source
}
type Store struct { /* 私有字段 */ }
func Open(dir string) (*Store, error)
func (s *Store) Add(name, contentType string, source Source, reader io.Reader, maxBytes int64) (Asset, error)
func (s *Store) List(favoritesOnly bool) []Asset
func (s *Store) Get(id string) (Asset, error)
func (s *Store) OpenContent(id string) (Asset, *os.File, error)
func (s *Store) Update(id string, name *string, favorite *bool) (Asset, error)
func (s *Store) Delete(id string) error
```

- `Open` 创建目录或将已有目录权限收紧到 0700，再恢复已提交数据；损坏的已提交资产返回错误，未完成的临时目录不进入索引。目录必须由调用方保护，只允许一个实例/进程使用。
- `Add` 边读取边写磁盘；`maxBytes=0` 使用 300 MiB 默认上限，正数使用指定字节数。负数、`math.MaxInt64` 或 nil reader 返回参数错误，超限返回 `ErrTooLarge`，读取或写入失败不发布资产。每次创建独立 ID，默认未收藏，相同内容也保留各自来源。
- 名称只用作显示，去掉目录前缀和控制字符，最长 240 UTF-8 字节；导入时空名使用 `asset`。磁盘路径只使用内部随机 ID。内容类型优先采用标准库嗅探；无法识别的二进制可使用传入 MIME 类型，Ogg 保留传入的 audio/ogg 或 video/ogg 分类；MP4/WebM 容器可分别保留 audio/mp4、audio/webm 提示，避免纯音频被归为视频。调用方仍需限制 HTTP 内联展示的类型。
- `Source` 是应用自定的来源键值，可省略，不应含密钥。`Add` 复制传入 map，所有返回值包含独立的来源副本；传入 map 在调用期间不得被并发修改。
- `List` 按创建时间从新到旧返回独立快照；`true` 只列收藏。`Get` 返回元数据快照。`CreatedAt` 为 UTC RFC3339Nano 字符串，`Size` 是原始字节数。
- `OpenContent` 同时取得元数据并打开内容，调用方必须关闭文件；已打开文件与并发删除的关系遵循操作系统语义。
- `Update` 的 nil 参数保持原值；空名称返回错误。先落盘成功再发布元数据。`Delete` 先原子移出索引目录，再删除内容；最后清理失败时返回错误，但资产已不可见。
- 无效、缺失或已删除 ID 返回 `ErrNotFound`，可用 `errors.Is` 判断；其他文件系统错误原样返回。方法可并发调用，单次调用原子，多次调用不组成事务；Store 不可复制。
- 元数据复用 `kv` 的同目录临时文件替换，新增资产通过目录重命名提交；新文件 0600，不执行 fsync，不保证突然断电的持久性。不存在 Close 或后台线程。

## 最小示例

在返回 `error` 的函数中使用；需导入 `github.com/ekk1/mygo/utils/assetstore`、`os` 和 `fmt`：

```go
s, err := assetstore.Open("./data/assets")
if err != nil { return err }
f, err := os.Open("photo.png")
if err != nil { return err }
defer f.Close()
a, err := s.Add("photo.png", "image/png", assetstore.Source{"kind": "upload"}, f, 0)
if err != nil { return err }
favorite := true
if _, err = s.Update(a.ID, nil, &favorite); err != nil { return err }
for _, item := range s.List(true) { fmt.Println(item.ID, item.Name) }
return nil
```
