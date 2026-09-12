# webui

用 Go 组合并渲染 HTML 页面，配套本地主题、视频位置按钮、SVG K 线图和原生请求工具；默认使用普通链接和表单。

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
    Styles      []string
    Scripts     []string
    Body        Node
}
func Render(w io.Writer, p Page) error
func RenderFragment(w io.Writer, n Node) error
func Assets() http.Handler
```

- `Text` 创建文本，`El` 创建元素，`Group` 组合没有外层标签的多个节点。`Node{}` 为空片段；列表可用 Go 循环构建后传入 `Group(nodes...)`。`El` 和 `Group` 复制传入的 map/slice。
- `Attrs` 的键使用小写 HTML 属性名，支持 `class`、`id`、`data-*`、`aria-*` 等。布尔属性用 `"required": ""` 表示开启，关闭时不添加键；`"disabled": "false"` 仍会禁用元素。表单输入值用 `value`，`textarea` 内容用 `Text`。
- `Page.Title` 是浏览器标题；`Lang` 默认 `zh-CN`；`AssetPrefix` 默认 `/webui`，可改为 `/assets/ui`，允许末尾 `/`，也可为 `/`。前缀必须是规范的本地绝对 URL 路径，不能有查询、片段、百分号、反斜线、空白或 `..` 段。资源由 `Assets` 单独挂载，`Render` 不注册路由。
- `Page.Theme` 指定初始配色：`rose`（默认，玫瑰灰）、`sand`（燕麦）、`sage`（鼠尾草）、`dusk`（暮色）。无效名称使 `Render` 返回错误且不写页面。浏览器已保存的有效选择优先于此初始值。前三套随系统切换明暗，暮色固定为深色。
- `Page.Scripts` 可填 `[]string{"/app.js"}`，在内置 JS 后按顺序 defer 加载应用脚本；路径遵守相同的本地路径规则，由应用另外注册资源路由。默认不加载应用脚本，不支持内联 JS。
- `Page.Styles` 可填 `[]string{"/app.css"}`，在 HTML head 中、内置主题之后按顺序加载应用样式，避免等 JS 执行后才应用布局。路径遵守相同的本地路径规则，由应用注册资源路由；默认不加载应用样式。
- `Render` 输出完整 HTML5 文档，自动加入 charset、viewport、本地 CSS 和 defer JS。先完整渲染再写入；非法标签、属性或模板错误返回 error，不写半截页面。writer 写入错误直接返回，可能已经写入部分字节。调用方设置 HTTP 状态和 `Content-Type: text/html; charset=utf-8`；需在写响应头前处理渲染错误时可先渲染到 `bytes.Buffer`。
- `RenderFragment` 只输出传入节点，不添加文档外壳、CSS 或 JS；转义、校验和错误行为同 `Render`。例如 `webui.RenderFragment(&buf, chart)` 可返回图表片段，由应用 JS 插入已加载资源的页面。
- `El` 支持常用语义化 HTML、表单、表格、媒体和 `details/dialog` 等，不支持自定义标签、SVG 或 `script/style/iframe/object/embed`，也不允许 `html/head/body` 等文档外壳标签。SVG 仅由下文的 `CandlestickChart` 在包内安全生成。名字不合法、内联 `on*`、`style`、`srcdoc` 属性及 void 元素的子节点会在渲染时返回错误。布局合法性（例如 `ul` 的子元素应该是 `li`）仍由调用方保证。
- 动态文本、属性和 URL 按上下文转义，不提供 Raw HTML。危险 URL 输出 `#ZgotmplZ`，不会返回 error。标签/属性名由应用代码决定；转义不代替业务校验、认证、CSRF 或授权。除下文的配色属性和视频组件属性外，`data-*` 不自动触发脚本。
- `Assets` 提供 `webui.css`、`webui.js`、`theme.js`、`video.js`、`chart.js` 和 `themes/{rose,sand,sage,dusk}.css`，支持 GET/HEAD 和 Range 请求；其他路径 404，其他方法 405。内置 JS 由 `Render` 自动 defer 加载。资源随程序打包，无外部服务或运行时磁盘依赖，使用 `Cache-Control: no-cache`。
- `Text` 在 `textarea` / `pre` 中的开头换行会保留；浏览器仍按 HTML 规则处理其他换行和空白。
- `Node` 构造后不可变，可并发复用；`Render` 和 `RenderFragment` 可并发写入各自的 writer。构造时不要并发修改输入 map/slice，渲染时不要并发修改 `Page` 或其 `Scripts` / `Styles`。`Assets` Handler 可并发使用。

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

运行 `go run ./cmd/webui-demo`，访问 `http://127.0.0.1:8080`；`-addr` 可改监听地址。首页预览表单使用 POST → 校验 → 303 → GET，错误返回 422 并保留输入，禁用 JS 也能使用。预览内容存放于 URL，不持久化，不要填写私密数据。

`/components` 提供视频和图表示例（[源码](../../cmd/webui-demo/components.go)）：视频在本机播放，不上传；HttpOnly Cookie 保存一个位置（30 天）。刷新后重新选择同一文件即可手动恢复，更换视频需重新保存。实际应用应按用户和视频分别存储。

图表使用 2026-01-01 至 2026-04-30 的模拟日线（含周末）和 SMA 5。日期范围包含两端；按钮移动一个窗口，“最近 20／60／120 根”回到最新数据。Tab 或点击 K 线取得焦点后，← / → 平移 1 根，Shift + ← / → 平移 5 根，保持窗口宽度和焦点所在列，到边界停止。请求期间忽略方向键；日期输入保留原生键盘操作。

启用 JS 时只更新图表与日期控件，保留视频、主题、滚动位置和操作焦点，成功后更新网址并支持前进／后退。请求 15 秒超时；失败保留旧图和输入，显示提示。新请求或编辑日期会取消旧请求；历史导航失败或取消时，当前历史条目恢复为已显示图表的网址。禁用 JS 时使用普通 GET 导航，日期错误返回 422、保留输入并暂显示最近 60 根。

## 视频位置按钮

```go
func VideoPositionControls(videoID, saveURL, restoreURL string) (Node, error)
```

返回一组“保存位置”“恢复位置”按钮和可访问的状态提示，绑定页面上唯一 ID 对应的原生 `<video>`。不封装视频来源、播放控件或存储。`videoID` 不能为空或包含空白；两个地址必须是规范的同源绝对路径，可带查询参数，例如 `/api/videos/42/position?token=...`，不能带片段、外部主机或 `..` 段；无效参数返回 error。

```go
controls, err := webui.VideoPositionControls("lesson-video",
    "/api/videos/42/position", "/api/videos/42/position")
if err != nil { return err }
body := webui.Group(
    webui.El("video", webui.Attrs{
        "id": "lesson-video", "src": "/media/lesson.mp4",
        "controls": "", "preload": "metadata",
    }),
    controls,
)
// 把 body 交给 Page.Body，再使用 Render 输出页面。
```

回调由应用注册，两个地址可以相同：

| 操作 | 请求 | 成功响应 |
| --- | --- | --- |
| 保存 | POST，`Content-Type: application/json`，正文 `{"seconds":12.5}` | 任意 2xx，建议 204，响应正文忽略 |
| 恢复 | GET，`Accept: application/json`，不使用缓存 | `{"seconds":12.5}`；尚未保存返回 `null` 或 204/205 |

- 秒数允许小数；恢复值必须是有限且非负的 JSON 数字，超过视频时长时截到结尾。恢复等待媒体元数据及 seek 完成并校验实际位置，不自动调用 `play()`，直播或未知／零时长不支持恢复。视频服务建议支持 HTTP Range（例如标准库 `http.ServeContent` 或仓库的静态文件 Handler）；浏览器无法定位时显示失败，不误报成功。
- 只响应点击，不自动保存、自动恢复或监听播放进度。保存需要视频已加载元数据；无 JS 时按钮隐藏，原生视频仍可操作。
- 每组按钮一次只执行一个请求，操作期间保留键盘焦点并忽略重复点击。整个操作 15 秒超时，失败显示提示并可重试；视频切换时不把旧请求的结果应用到新来源。
- 请求携带同源凭据并拒绝重定向；应用负责回调的认证、CSRF 校验、秒数校验及持久化。库不维护服务端播放状态，回调可用数据库、文件或现有存储。

## K 线与叠加线

```go
type Candle struct {
    Label string
    Open, High, Low, Close float64
    Volume float64
}
type ChartLine struct {
    Name string
    Values []float64
}
func CandlestickChart(candles []Candle, lines ...ChartLine) (Node, error)
```

`CandlestickChart` 返回带图例的 SVG 节点，可组合进 `Page.Body` 或交给 `RenderFragment`。图形由服务端生成；范围选择和键盘平移由应用实现，库不绑定这些操作。

```go
candles := []webui.Candle{
    {Label: "09-01", Open: 10, High: 13, Low: 9, Close: 12, Volume: 1000},
    {Label: "09-02", Open: 12, High: 14, Low: 10, Close: 11, Volume: 600},
    {Label: "09-03", Open: 11, High: 15, Low: 10, Close: 14, Volume: 1500},
}
chart, err := webui.CandlestickChart(candles,
    webui.ChartLine{Name: "SMA 2", Values: []float64{math.NaN(), 11.5, 12.5}},
)
if err != nil { return err }
// chart 可直接组合到已有页面；math.NaN 需要导入 math。
```

- `Candle` 为 OHLCV（开、高、低、收、成交量）；按输入顺序等距排列，不排序、不解析时间，也不为休市日期留空。`Label` 用作首／中／末横轴标签，超过 12 个字符会截断；提示中保留完整标签，名称和标签安全转义。
- 所有主题及明暗模式都固定白色阳线（收盘高于开盘）、黑色阴线（收盘低于开盘），中性灰边线和影线保证可辨识；平盘用水平线显示。线性价格轴同时包含全部 K 线和叠加线，上下留 5% 余量，不强制从零开始。完全平价时留 `max(abs(price)*5%, 1)` 余量，支持零与负价格。图表随容器等比缩放，背景和文字随页面主题配色。
- 默认在传入数据最后一根 K 的收盘价处绘制细横线，随价格轴缩放，位于实体下方。
- `Volume` 必须是有限非负数，省略时为零。淡灰半透明量柱绘制在蜡烛后方，从绘图区底部向上延伸；当前数据的最大量占绘图区高度 25%，其余按独立线性比例缩放，不影响价格轴。零量不绘柱，提示仍显示 `V: 0`，不自动换算单位。
- 悬停整列、Tab 聚焦或触屏点击可查看完整标签和 `O/H/L/C/V`。左上角半透明提示不占布局空间；移出或失焦时隐藏，焦点在图表内时可按 Escape 隐藏。`chart.js` 支持后续插入或替换的图表；禁用 JS 时保留 SVG 原生悬停提示。
- `ChartLine.Values` 必须与 K 线等长且逐项对应；`math.NaN()` 表示缺失点并断线，单个孤立点不形成线段。`Name` 是图例文字，多个序列循环使用三种颜色。只画相邻点间的折线，不做平滑插值，避免曲线超出真实价格。
- SMA 等指标由调用方计算；切换显示范围时建议先计算均线再截取数据，保留窗口之前的预热数据。不传 `lines` 就只画 K 线。
- 空 K 线且各折线也为空时显示“暂无 K 线数据”。OHLCV 含 NaN／无穷值、负成交量、低价高于开收任一值、高价低于开收任一值、折线含无穷值、长度不匹配或价格范围溢出均返回 error。
- 不自动抽样或限制数量，密集数据请由调用方聚合。构造时读取数据，不保留输入切片；返回的 `Node` 可并发复用。

## CSS 用法

原生表单、输入框、按钮、表格、`details` 等直接获得样式。布局支持窄屏折行和键盘焦点；可用下列 class 组合页面。

| class | 用途 |
| --- | --- |
| `hero` / `eyebrow` | 页面引导区 / 小号强调文字 |
| `card` | 圆角、细边框内容块 |
| `grid` / `stack` | 自适应网格 / 纵向间隔 |
| `actions` | 按钮组，可换行 |
| `button` / `secondary` | 链接呈现为按钮 / 次要按钮 |
| `muted` / `badge` | 次要文字 / 主题强调色标签 |
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

用户点击切换配色时，颜色在 200ms 内渐变；自动恢复已保存配色时不播放动画。系统启用“减少动态效果”时直接切换。

## 可选 JS 接口

请求脚本 `webui.js` 提供 `window.webui`，自身不绑定事件、不拦截表单、不修改 DOM。独立的 `theme.js` 负责配色，`video.js` 绑定 `VideoPositionControls` 生成的按钮，`chart.js` 处理 K 线提示。自定义脚本用 `Page.Scripts` 加载；也可在已有页面中单独引用 Assets 提供的 JS。普通表单不需要自定义 JS。

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

浏览器测试使用 `/usr/bin/chromium` 和 Chromium sandbox，覆盖配色切换与恢复、视频回调与真实 seek、图表提示与键盘平移、局部更新与历史导航，以及窄屏、减少动态效果、无 JS 和错误恢复。视频测试用 Node 标准库生成 WAV，不下载媒体。

测试应全部通过，无失败或跳过，命令正常退出且退出码为 0。页面操作完成但浏览器清理失败，仍算测试失败。失败时先定位，不能通过忽略异常、关闭 sandbox 或擅自安装其他版本绕过。

### 4. 视觉检查与收尾

涉及 CSS、配色或布局时，在示例页面逐个切换主题，检查桌面和手机宽度下的空内容、已填写内容、长文本、焦点和明暗效果。自动化测试不能代替对布局和配色的目视检查；截图按需放在已忽略的 `bin/`，不是应用发布依赖。

测试结束后在终端 A 按 `Ctrl+C`，只停止本次启动的服务。浏览器测试正常退出会清理自己创建的临时目录；异常残留需要核实归属后处理，不清空共享缓存。最后执行 `git diff --check`，核对文档和改动范围，报告通过项及尚未解决的失败。纯文档变更只需核对命令、链接和内容，不要求重跑上述程序测试。

### 已验证版本与兼容处理

2026-09-10 验证：Debian Node 20.19.2、`node-playwright` 1.38.0+ds-3 和 Chromium 152.0.7977.82 的请求及浏览器测试通过。

该 Playwright 包有 `rimraf` 回调/Promise 接口不匹配问题。测试入口仅在版本为 1.38.0 且 `rimraf` 为三参数函数时，通过 `util.promisify` 适配并保留同步清理方法。适配只作用于测试进程，不修改系统包；测试验证真实目录清理，错误仍会使测试失败。此处依赖 Playwright 内部接口，Debian 修复后应复核并删除。
