# webui

用 Go 组合并渲染 HTML 页面，配套多套本地主题和可选的原生请求工具；默认使用普通链接和表单。

## Go 接口

```go
type Attrs map[string]string
type Node struct { /* 私有字段 */ }
func Text(value string) Node
func El(tag string, attrs Attrs, children ...Node) Node
func Group(children ...Node) Node

type Page struct {
    Title       string
    Lang        string
    AssetPrefix string
    Theme       string
    Scripts     []string
    Body        Node
}
func Render(w io.Writer, p Page) error
func Assets() http.Handler
```

- `Text` 创建文本，`El` 创建元素，`Group` 组合没有外层标签的多个节点。`Node{}` 为空片段，适合条件渲染；列表用 Go 循环构建 `[]Node`，然后 `Group(nodes...)`。`El` 和 `Group` 复制传入的 map/slice。
- `Attrs` 的键使用小写 HTML 属性名，支持 `class`、`id`、`data-*`、`aria-*` 等。布尔属性用 `"required": ""` 表示开启，关闭时不添加键；`"disabled": "false"` 仍会禁用元素。表单输入值用 `value`，`textarea` 内容用 `Text`。
- `Page.Title` 是浏览器标题；`Lang` 默认 `zh-CN`；`AssetPrefix` 默认 `/webui`，可改为 `/assets/ui`，允许末尾 `/`，也可为 `/`。前缀必须是规范的本地绝对 URL 路径，不能有查询、片段、百分号、反斜线、空白或 `..` 段。资源由 `Assets` 单独挂载，`Render` 不注册路由。
- `Page.Theme` 指定初始配色：`rose`（默认，玫瑰灰）、`sand`（燕麦）、`sage`（鼠尾草）、`dusk`（暮色）。无效名称使 `Render` 返回错误且不写页面。浏览器已保存的有效选择优先于此初始值。前三套随系统切换明暗，暮色固定为深色。
- `Page.Scripts` 可填 `[]string{"/app.js"}`，在内置 JS 后按顺序 defer 加载应用脚本；路径遵守相同的本地路径规则，由应用另外注册资源路由。默认不加载应用脚本，不支持内联 JS。
- `Render` 输出完整 HTML5 文档，自动加入 charset、viewport、本地 CSS 和 defer JS。先完整渲染再写入；非法标签、属性或模板错误返回 error，不写半截页面。writer 写入错误直接返回，可能已经写入部分字节。调用方设置 HTTP 状态和 `Content-Type: text/html; charset=utf-8`；需在写响应头前处理渲染错误时可先渲染到 `bytes.Buffer`。
- 元素支持常用语义化 HTML、表单、表格、媒体和 `details/dialog` 等，不支持自定义标签、SVG 或 `script/style/iframe/object/embed`，也不允许 `html/head/body` 等文档外壳标签。名字不合法、内联 `on*`、`style`、`srcdoc` 属性及 void 元素的子节点会在渲染时返回错误。布局合法性（例如 `ul` 的子元素应该是 `li`）仍由调用方保证。
- 动态文本、属性和 URL 统一使用标准库 `html/template` 按上下文转义；不提供绕过转义的 Raw HTML。危险 URL 会输出 `#ZgotmplZ`，不会返回 error。标签/属性名由应用代码决定，不应交给用户选择；转义不代替业务校验、认证、CSRF 或授权。除下文约定的 `data-webui-theme*` 配色属性外，`data-*` 只是数据，不自动触发脚本。
- `Assets` 提供 `webui.css`、`webui.js`、`theme.js` 和 `themes/{rose,sand,sage,dusk}.css`，支持 GET/HEAD 和标准 Range 请求；其他路径 404，其他方法 405。资源通过 `go:embed` 打包，无 CDN、字体服务或运行时磁盘依赖；`Cache-Control: no-cache` 避免更新后使用过期资源。
- `Text` 在 `textarea` / `pre` 中的开头换行会保留；浏览器仍按 HTML 规则处理其他换行和空白。
- `Node` 构造后不可变，可并发复用；`Render` 无共享可变状态，多个 goroutine 可以渲染到各自的 writer。调用方不要在构造节点的同时修改输入 map/slice，也不要在渲染时并发修改同一个 `Page` 或其 `Scripts` 切片。`Assets` Handler 可并发使用。

## 最小示例

```go
package main

import (
    "bytes"
    "fmt"
    "net/http"

    "github.com/ekk1/mygo/utils/webui"
)

func main() {
    mux := http.NewServeMux()
    mux.Handle("/webui/", http.StripPrefix("/webui/", webui.Assets()))
    mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
        page := webui.Page{Title: "搜索", Body: webui.El("main", nil,
            webui.El("h1", nil, webui.Text("搜索笔记")),
            webui.El("form", webui.Attrs{"method": "get", "action": "/"},
                webui.El("label", webui.Attrs{"for": "q"}, webui.Text("关键词")),
                webui.El("input", webui.Attrs{"id": "q", "name": "q", "value": r.URL.Query().Get("q")}),
                webui.El("button", webui.Attrs{"type": "submit"}, webui.Text("搜索")),
            ),
        )}
        var output bytes.Buffer
        if err := webui.Render(&output, page); err != nil {
            http.Error(w, "页面生成失败", http.StatusInternalServerError)
            return
        }
        w.Header().Set("Content-Type", "text/html; charset=utf-8")
        if r.Method != http.MethodHead {
            if _, err := output.WriteTo(w); err != nil { fmt.Println(err) }
        }
    })
    if err := http.ListenAndServe("127.0.0.1:8080", mux); err != nil { fmt.Println(err) }
}
```

仓库示例复用 `httpserver`，运行 `go run ./cmd/webui-demo`，访问 `http://127.0.0.1:8080`；`-addr` 可改监听地址。示例使用 POST → 服务端校验 → 303 → GET，校验错误返回 422 并保留输入，禁用 JS 也能使用。预览内容存放于 URL，不持久化，不要填写私密数据。

## CSS 用法

默认玫瑰灰随系统切换明暗，另有燕麦、鼠尾草和固定深色的暮色。主题统一采用柔和圆角和轻阴影，编辑区、输入框和预览正文使用同系底色。窄屏自动折行，支持键盘焦点和减少动态效果偏好。原生 `form`、`label`、输入框、按钮、表格、`details` 等直接获得样式。

| class | 用途 |
| --- | --- |
| `hero` / `eyebrow` | 页面引导区 / 小号强调文字 |
| `card` | 圆角、细边框内容块 |
| `grid` / `stack` | 自适应网格 / 纵向间隔 |
| `actions` | 按钮组，可换行 |
| `button` / `secondary` | 链接呈现为按钮 / 次要按钮 |
| `muted` / `badge` | 次要文字 / 粉色标签 |
| `notice` | 提示块；错误可搭配 `role="alert"` |
| `table-scroll` | 包住宽表格，允许横向滚动 |
| `brand` / `quiet-link` | 简洁站点名称 / 次要操作链接 |
| `workspace` / `editor` / `preview` | 左侧编辑、右侧预览的分栏布局，窄屏上下排列 |
| `section-title` / `field-help` | 分栏标题 / 字段补充说明 |
| `document` / `prose` / `empty` | 预览正文容器 / 保留换行的正文 / 空内容说明 |

主题变量定义在 `:root`：`--bg`、`--surface`、`--soft`、`--text`、`--muted`、`--line`、`--accent`、`--hover`、`--tint`、`--shadow`、`--radius`，以及输入底色 `--field`、预览纸张底色 `--paper`、主按钮文字色 `--button-text`。布局在 `webui.css`，配色在各自主题 CSS 中。

## 本地配色切换

`Render` 自动引用当前主题 CSS 与 `theme.js`。页面需要切换按钮时，使用下面的 HTML 结构（可用 `El` 构建）。不添加按钮时仍会恢复浏览器已保存的配色。

```html
<div class="theme-switcher" data-webui-theme-controls role="group" aria-label="配色" hidden>
  <button type="button" data-webui-theme="rose" aria-pressed="true">玫瑰灰</button>
  <button type="button" data-webui-theme="sand" aria-pressed="false">燕麦</button>
  <button type="button" data-webui-theme="sage" aria-pressed="false">鼠尾草</button>
  <button type="button" data-webui-theme="dusk" aria-pressed="false">暮色</button>
</div>
<span class="theme-status" data-webui-theme-status role="status"></span>
```

JS 启用后显示按钮；未启用时使用 Go 指定的初始配色，普通表单仍可使用。选择通过同源 `localStorage` 的 `webui-theme` 键保存，刷新或表单跳转后恢复；无效保存值忽略，存储不可用时仍能切换。加载新 CSS 时用 `aria-disabled` 标记按钮并忽略重复点击，保持键盘焦点；成功后才替换旧样式并保存；失败或 10 秒超时保留当前样式，显示提示并允许重试。只请求本地 CSS，不发送业务请求，不上传偏好。

## 可选 JS 接口

请求脚本加载完成后提供 `window.webui`，不绑定事件、不拦截表单、不自动修改 DOM。独立的 `theme.js` 只负责主题 CSS 和配色按钮。自定义脚本用 `Page.Scripts` 加载；也可在已有页面中单独引用 Assets 提供的 JS。普通表单不需要自定义 JS。

```js
webui.request(url, options?)      // Promise<Response>
webui.json(url, options?)         // Promise<解析后的 JSON | null>
webui.form(url, formData, options?) // Promise<Response>
```

- `request` 使用原生 `fetch`，`options` 是原生 RequestInit（含 `signal`、headers、credentials 等）。非 2xx 拒绝并在 `error.response` 保留原始 Response，响应体未被读取。网络错误和取消错误原样传递；不自动重试，无额外超时，原生默认凭据策略为 same-origin。
- `json` 默认 GET，添加 `Accept: application/json`（已有时保留）；提供 `body` 时将 JS 值 JSON 编码，并设置 JSON Content-Type，发送数据时显式设置 `method: "POST"` 等。204/205 返回 null，其他成功响应调用 `response.json()`，无效或空 JSON 拒绝。不修改传入的 options。
- `form` 接受 `FormData`，默认 POST；非 GET/HEAD 发送 multipart，自动去掉传入的 Content-Type，由浏览器生成 boundary。GET/HEAD 将字段追加到 URL，保留既有查询和重复字段，文件字段使用文件名，无请求体。原生页面提交优先使用 `<form>`；只有明确需要 fetch 时才调用它。fetch 跟随重定向但不会导航当前页面。

```js
// 放在 Page.Scripts 加载的外部 JS 中；普通表单无需这些代码。
async function preview(form) {
  try {
    const result = await webui.json("/api/preview", {
      method: "POST", body: { title: "新想法" }
    });
    const response = await webui.form("/preview", new FormData(form));
    // 由应用自行消费 result / response，不自动渲染。
  } catch (error) {
    console.error(error.response ? await error.response.text() : error.message);
  }
}
```

SSE 暂不封装：实时推送可由应用使用标准 HTTP 流和原生 `EventSource`，不影响普通页面及表单。

## 开发与测试流程

以下命令均在仓库根目录执行。Node、Playwright 和 Chromium 仅用于测试，运行或发布 Go 应用不需要它们。

测试工具从 Debian 官方仓库获取，安装须先按 [开发约定](../../AGENTS.md#工具安装与来源信任) 取得用户确认，再由用户执行：

```sh
apt-get install --no-install-recommends nodejs node-playwright chromium chromium-sandbox
```

### 1. 检查工具

```sh
node --version
chromium --version
dpkg-query -W nodejs node-playwright chromium chromium-sandbox
node -p 'require.resolve("playwright")'
```

Playwright 应来自系统目录（Debian 通常为 `/usr/share/nodejs/playwright/index.js`）。缺少工具时先报告并取得安装确认，不运行 npm、npx、pip 或 `playwright install` 补装。

### 2. Go 与请求测试

```sh
go fmt ./...
go test ./...
go vet ./...
go build -o ./bin/ ./...
node --test utils/webui/request_test.cjs
```

涉及共享状态或并发时再执行 `go test -race ./...`。只定位 webui 问题时可先运行 `go test ./utils/webui ./cmd/webui-demo`，提交 Go 变更前仍需完成全仓库检查。`go test` 不会自动执行 `.cjs` 测试，JS 相关改动须单独运行对应命令。

### 3. 浏览器测试

终端 A 启动独立测试服务，等待显示监听地址后再进行下一步：

```sh
./bin/webui-demo -addr 127.0.0.1:18080
```

终端 B 执行：

```sh
node --test utils/webui/theme_browser_test.cjs
```

若端口已被占用，为本次测试选择其他端口，不结束已有服务。例如终端 A 使用 `-addr 127.0.0.1:18081`，终端 B 使用：

```sh
WEBUI_TEST_URL=http://127.0.0.1:18081 node --test utils/webui/theme_browser_test.cjs
```

测试显式启动 `/usr/bin/chromium` 并启用 Chromium sandbox。覆盖真实临时目录的异步/同步清理、四套主题替换、刷新/提交后的恢复、加载失败重试、存储禁用、键盘焦点、手机宽度、深色偏好及无 JS 表单。

测试应全部通过，无失败或跳过，命令正常退出且退出码为 0。页面操作完成但浏览器清理失败，仍算测试失败。失败时先定位，不能通过忽略异常、关闭 sandbox 或擅自安装其他版本绕过。

### 4. 视觉检查与收尾

涉及 CSS、配色或布局时，在示例页面逐个切换主题，检查桌面和手机宽度下的空内容、已填写内容、长文本、焦点和明暗效果。自动化测试不能代替对布局和配色的目视检查；截图按需放在已忽略的 `bin/`，不是应用发布依赖。

测试结束后在终端 A 按 `Ctrl+C`，只停止本次启动的服务。浏览器测试正常退出会清理自己创建的临时目录；异常残留需要核实归属后处理，不清空共享缓存。最后执行 `git diff --check`，核对文档和改动范围，报告通过项及尚未解决的失败。纯文档变更只需核对命令、链接和内容，不要求重跑上述程序测试。

### 已验证版本与兼容处理

2026-09-10 验证：Debian Node 20.19.2、`node-playwright` 1.38.0+ds-3 和 Chromium 152.0.7977.82 的请求及浏览器测试通过。

该 Playwright 包的退出清理存在 `rimraf` 回调/Promise 接口不匹配。测试入口仅在版本为 1.38.0 且 `rimraf` 为三参数函数时，用 Node 标准库 `util.promisify` 适配其异步接口，并保留同步清理方法。适配只作用于测试进程内存，不修改系统包、不新增依赖、不忽略清理错误；回归测试验证真实目录的异步/同步删除，以及完整浏览器流程正常退出。此兼容代码依赖 Playwright 内部接口，Debian 修复后应复核并删除。
