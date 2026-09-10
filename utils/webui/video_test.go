package webui

import (
	"bytes"
	"strings"
	"testing"
)

func TestVideoPositionControls(t *testing.T) {
	n, err := VideoPositionControls(`movie:1`, `/position?id=a%20b&token=%22`, `/position?id=a%20b`)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := Render(&b, Page{Body: n, AssetPrefix: "/ui"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`data-webui-video="movie:1"`, `保存位置`, `恢复位置`, `type="button"`, `role="status"`, `/ui/video.js`, `&amp;token=%22`} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, url := range []string{"", "https://evil.test/", "//evil.test/", `/a\b`, "/a#b", "/a\nb", "javascript:alert(1)", "/a/../b"} {
		if _, err := VideoPositionControls("v", url, "/restore"); err == nil {
			t.Errorf("accepted save URL %q", url)
		}
		if _, err := VideoPositionControls("v", "/save", url); err == nil {
			t.Errorf("accepted restore URL %q", url)
		}
	}
	if _, err := VideoPositionControls("", "/save", "/restore"); err == nil {
		t.Fatal("accepted empty video ID")
	}
}
