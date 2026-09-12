package webui

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestRender(t *testing.T) {
	p := Page{Title: `<hello>`, Body: Group(Node{}, El("main", nil,
		El("h1", Attrs{"class": "title", "data-note": `"<&`}, Text(`<script>alert(1)</script>`)),
		El("a", Attrs{"href": "javascript:alert(1)"}, Text("link")),
		El("input", Attrs{"required": "", "value": `{{.Secret}}`}),
	))}
	var b bytes.Buffer
	if err := Render(&b, p); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<!doctype html>", "&lt;hello&gt;", "&lt;script&gt;", "#ZgotmplZ", `required=""`, `{{.Secret}}`, `/webui/webui.css`, `/webui/webui.js`, `lang="zh-CN"`} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("missing %q in %s", want, b.String())
		}
	}
	if strings.Contains(b.String(), "<script>alert") {
		t.Fatal("unescaped text")
	}
}

func TestRenderFragment(t *testing.T) {
	var b bytes.Buffer
	if err := RenderFragment(&b, El("p", nil, Text("<hello>"))); err != nil {
		t.Fatal(err)
	}
	if b.String() != "<p>&lt;hello&gt;</p>" {
		t.Fatal(b.String())
	}
	b.Reset()
	if err := RenderFragment(&b, Group(Text("partial"), El("script", nil))); err == nil || b.Len() != 0 {
		t.Fatal("invalid fragment wrote partial output")
	}
	if err := RenderFragment(brokenWriter{}, Text("x")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}
}

func TestInvalidStructureDoesNotWrite(t *testing.T) {
	for _, n := range []Node{
		El("", nil),
		El(`div>{{.}}`, nil), El("div dl", nil), El("script", nil, Text("alert(1)")),
		El("div", Attrs{`x onclick`: "bad"}), El("div", Attrs{"onclick": "bad"}),
		El("div", Attrs{"style": "bad"}), El("iframe", Attrs{"srcdoc": "bad"}),
		El("input", nil, Text("invalid child")),
	} {
		var b bytes.Buffer
		if err := Render(&b, Page{Body: n}); err == nil {
			t.Error("expected invalid structure error")
		}
		if b.Len() != 0 {
			t.Error("partial output")
		}
	}
}

func TestLeadingNewline(t *testing.T) {
	for _, tag := range []string{"textarea", "pre"} {
		var b bytes.Buffer
		if err := Render(&b, Page{Body: El(tag, nil, Text("\nhello"))}); err != nil {
			t.Fatal(err)
		}
		// HTML parsing discards the first newline directly after these start tags.
		if !strings.Contains(b.String(), "<"+tag+">\n\nhello</"+tag+">") {
			t.Fatalf("missing parser padding: %q", b.String())
		}
	}
}

func TestNodeCopiesInputs(t *testing.T) {
	attrs := Attrs{"id": "original"}
	children := []Node{Text("original")}
	n := El("div", attrs, children...)
	attrs["id"] = "changed"
	children[0] = Text("changed")
	var b bytes.Buffer
	if err := Render(&b, Page{Body: n}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "changed") {
		t.Fatal("node retains mutable caller inputs")
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestRenderOptionsAndErrors(t *testing.T) {
	var b bytes.Buffer
	if err := Render(&b, Page{Lang: "en", AssetPrefix: "/static/ui/"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), `lang="en"`) || !strings.Contains(b.String(), `/static/ui/webui.css`) {
		t.Fatal(b.String())
	}
	for _, prefix := range []string{"https://example.com", "//evil", "/foo?bar", "/a/../b", `/a\b`, "/a%2fb"} {
		b.Reset()
		if err := Render(&b, Page{AssetPrefix: prefix}); err == nil || b.Len() != 0 {
			t.Errorf("invalid prefix accepted: %q", prefix)
		}
	}
	if err := Render(brokenWriter{}, Page{}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("writer error: %v", err)
	}
}

func TestConcurrentRender(t *testing.T) {
	p := Page{Body: El("p", nil, Text("shared"))}
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var b bytes.Buffer
			if err := Render(&b, p); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}

func TestLocalScripts(t *testing.T) {
	var b bytes.Buffer
	if err := Render(&b, Page{Scripts: []string{"/app.js", "/extra.js"}}); err != nil {
		t.Fatal(err)
	}
	builtin, app, extra := strings.Index(b.String(), "/webui/webui.js"), strings.Index(b.String(), `defer src="/app.js"`), strings.Index(b.String(), `defer src="/extra.js"`)
	if builtin < 0 || app < builtin || extra < app {
		t.Fatal(b.String())
	}
	for _, s := range []string{"https://evil.test/a.js", "//evil/a.js", "/../evil.js", `/a" onload="bad`} {
		b.Reset()
		if err := Render(&b, Page{Scripts: []string{s}}); err == nil || b.Len() != 0 {
			t.Fatalf("invalid script accepted: %q", s)
		}
	}
}

func TestLocalStyles(t *testing.T) {
	var b bytes.Buffer
	if err := Render(&b, Page{Styles: []string{"/app.css", "/extra.css"}, Scripts: []string{"/app.js"}}); err != nil {
		t.Fatal(err)
	}
	output := b.String()
	base, app, extra, script := strings.Index(output, "/webui/themes/rose.css"), strings.Index(output, `rel="stylesheet" href="/app.css"`), strings.Index(output, `rel="stylesheet" href="/extra.css"`), strings.Index(output, `defer src="/app.js"`)
	if base < 0 || app < base || extra < app || script < extra || extra > strings.Index(output, "</head>") {
		t.Fatal(output)
	}
	for _, href := range []string{"https://evil.test/a.css", "//evil/a.css", "/../a.css", `/a" onload="bad`} {
		b.Reset()
		if err := Render(&b, Page{Styles: []string{href}}); err == nil || b.Len() != 0 {
			t.Fatalf("invalid stylesheet accepted: %q", href)
		}
	}
}

func TestAssets(t *testing.T) {
	h := http.StripPrefix("/ui/", Assets())
	for _, tc := range []struct {
		method, path string
		status       int
		contentType  string
	}{
		{"GET", "/ui/webui.css", 200, "text/css"}, {"GET", "/ui/webui.js", 200, "javascript"},
		{"GET", "/ui/theme.js", 200, "javascript"},
		{"GET", "/ui/video.js", 200, "javascript"},
		{"GET", "/ui/chart.js", 200, "javascript"},
		{"GET", "/ui/themes/rose.css", 200, "text/css"},
		{"GET", "/ui/themes/sand.css", 200, "text/css"},
		{"GET", "/ui/themes/sage.css", 200, "text/css"},
		{"GET", "/ui/themes/dusk.css", 200, "text/css"},
		{"GET", "/ui/themes/unknown.css", 404, ""},
		{"HEAD", "/ui/webui.css", 200, "text/css"}, {"GET", "/ui/", 404, ""}, {"GET", "/ui/missing", 404, ""},
		{"POST", "/ui/webui.css", 405, ""},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != tc.status {
			t.Errorf("%s %s: %d", tc.method, tc.path, w.Code)
		}
		if !strings.Contains(w.Header().Get("Content-Type"), tc.contentType) {
			t.Error(w.Header())
		}
		if tc.method == "HEAD" && w.Body.Len() != 0 {
			t.Error("HEAD body")
		}
	}
}

func TestPageTheme(t *testing.T) {
	for _, theme := range []string{"", "rose", "sand", "sage", "dusk"} {
		var b bytes.Buffer
		if err := Render(&b, Page{Theme: theme, AssetPrefix: "/ui"}); err != nil {
			t.Fatal(err)
		}
		if theme == "" {
			theme = "rose"
		}
		if !strings.Contains(b.String(), `href="/ui/themes/`+theme+`.css"`) || !strings.Contains(b.String(), `/ui/theme.js`) {
			t.Fatal(b.String())
		}
	}
	var b bytes.Buffer
	if err := Render(&b, Page{Theme: "../bad"}); err == nil || b.Len() != 0 {
		t.Fatal("invalid theme accepted")
	}
}
