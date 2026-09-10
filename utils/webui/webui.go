// Package webui 用 Go 组合服务端 HTML 页面，提供原生表单友好的主题与请求工具。
package webui

import (
	"bytes"
	"fmt"
	"html/template"
	"io"
	"maps"
	"path"
	"slices"
	"strconv"
	"strings"
)

// Attrs 是元素的字符串属性；布尔属性以空字符串表示真，省略表示假。
type Attrs map[string]string

// Node 是不可变的 HTML 片段。零值为空，可在多个 goroutine 中复用。
type Node struct {
	element  bool
	tag      string
	text     string
	attrs    Attrs
	children []Node
}

// Text 创建文本节点，内容在渲染时自动转义。
func Text(value string) Node { return Node{text: value} }

// El 创建 HTML 元素并复制 attrs 和 children；无效结构在 Render 时返回错误。
func El(tag string, attrs Attrs, children ...Node) Node {
	return Node{element: true, tag: tag, attrs: maps.Clone(attrs), children: slices.Clone(children)}
}

// Group 将多个节点组合为不带外层标签的片段。
func Group(children ...Node) Node { return Node{children: slices.Clone(children)} }

// Page 描述完整页面。Lang 默认 zh-CN，AssetPrefix 默认 /webui。
// AssetPrefix 必须是本地绝对路径，应用需在对应路径挂载 Assets。
type Page struct {
	Title       string
	Lang        string
	AssetPrefix string
	// Theme 是初始配色：rose（默认）、sand、sage 或 dusk。
	Theme string
	// Scripts 是应用自己的本地绝对脚本路径，按顺序在内置 JS 后 defer 加载。
	Scripts []string
	Body    Node
}

// Render 生成完整 HTML 后写入 w；结构或模板错误不会写入内容，写入错误原样返回。
// 不设置 HTTP 响应头或状态。相同页面可并发渲染到不同 writer。
func Render(w io.Writer, p Page) error {
	prefix := p.AssetPrefix
	if prefix == "" {
		prefix = "/webui"
	}
	if !localPath(prefix) {
		return fmt.Errorf("webui: invalid asset prefix %q", prefix)
	}
	prefix = strings.TrimSuffix(prefix, "/")
	lang := p.Lang
	if lang == "" {
		lang = "zh-CN"
	}
	theme := p.Theme
	if theme == "" {
		theme = "rose"
	}
	if theme != "rose" && theme != "sand" && theme != "sage" && theme != "dusk" {
		return fmt.Errorf("webui: invalid theme %q", theme)
	}
	b := builder{}
	b.source.WriteString(`<!doctype html><html lang="` + b.value(lang) + `"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>` + b.value(p.Title) + `</title><link rel="stylesheet" href="` + b.value(prefix+"/webui.css") + `"><script defer src="` + b.value(prefix+"/webui.js") + `"></script>`)
	b.source.WriteString(`<link rel="stylesheet" data-webui-theme-sheet="` + b.value(theme) + `" href="` + b.value(prefix+"/themes/"+theme+".css") + `"><script defer src="` + b.value(prefix+"/theme.js") + `"></script>`)
	for _, src := range p.Scripts {
		if !localPath(src) {
			return fmt.Errorf("webui: invalid script path %q", src)
		}
		b.source.WriteString(`<script defer src="` + b.value(src) + `"></script>`)
	}
	b.source.WriteString("</head><body>")
	if err := b.node(p.Body); err != nil {
		return err
	}
	b.source.WriteString("</body></html>")
	tmpl, err := template.New("page").Parse(b.source.String())
	if err != nil {
		return fmt.Errorf("webui: parse: %w", err)
	}
	var output bytes.Buffer
	if err = tmpl.Execute(&output, b.values); err != nil {
		return fmt.Errorf("webui: render: %w", err)
	}
	_, err = w.Write(output.Bytes())
	return err
}

func localPath(s string) bool {
	if !strings.HasPrefix(s, "/") || strings.HasPrefix(s, "//") || strings.ContainsAny(s, "\\?#%\"'<> \t\r\n") {
		return false
	}
	if s != "/" && path.Clean(s) != strings.TrimSuffix(s, "/") {
		return false
	}
	for _, c := range s {
		if c < 32 || c == 127 {
			return false
		}
	}
	return true
}

// The template contains only validated structure and index actions. All caller
// values remain strings so html/template applies context-sensitive escaping.
type builder struct {
	source strings.Builder
	values []string
}

func (b *builder) value(s string) string {
	b.values = append(b.values, s)
	return "{{index . " + strconv.Itoa(len(b.values)-1) + "}}"
}

const elements = " a abbr address article aside audio b bdi bdo blockquote br button canvas caption cite code col colgroup data datalist dd del details dfn dialog div dl dt em fieldset figcaption figure footer form h1 h2 h3 h4 h5 h6 header hgroup hr i img input ins kbd label legend li main map mark menu meter nav ol optgroup option output p picture pre progress q rp rt ruby s samp search section select slot small source span strong sub summary sup table tbody td template textarea tfoot th thead time tr track u ul var video wbr "
const voidElements = " br col hr img input source track wbr "

func (b *builder) node(n Node) error {
	if !n.element {
		if n.text != "" {
			b.source.WriteString(b.value(n.text))
		}
		for _, c := range n.children {
			if err := b.node(c); err != nil {
				return err
			}
		}
		return nil
	}
	if strings.ContainsAny(n.tag, " \t\r\n") || !strings.Contains(elements, " "+n.tag+" ") {
		return fmt.Errorf("webui: unsupported element %q", n.tag)
	}
	isVoid := strings.Contains(voidElements, " "+n.tag+" ")
	if isVoid && len(n.children) > 0 {
		return fmt.Errorf("webui: void element %q cannot have children", n.tag)
	}
	b.source.WriteString("<" + n.tag)
	for _, key := range slices.Sorted(maps.Keys(n.attrs)) {
		if !validAttr(key) {
			return fmt.Errorf("webui: unsupported attribute %q", key)
		}
		b.source.WriteString(" " + key + `="` + b.value(n.attrs[key]) + `"`)
	}
	b.source.WriteString(">")
	// HTML parsers discard one leading newline in these elements.
	if n.tag == "textarea" || n.tag == "pre" {
		b.source.WriteByte('\n')
	}
	if isVoid {
		return nil
	}
	for _, c := range n.children {
		if err := b.node(c); err != nil {
			return err
		}
	}
	b.source.WriteString("</" + n.tag + ">")
	return nil
}

func validAttr(s string) bool {
	if s == "" || strings.HasPrefix(s, "on") || s == "style" || s == "srcdoc" {
		return false
	}
	for i, c := range s {
		if c >= 'a' && c <= 'z' {
			continue
		}
		if i > 0 && (c >= '0' && c <= '9' || c == '-' || c == '_') {
			continue
		}
		return false
	}
	return true
}
