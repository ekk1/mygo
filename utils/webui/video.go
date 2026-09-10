package webui

import (
	"fmt"
	"net/url"
	"strings"
	"unicode"
)

// VideoPositionControls 创建绑定 videoID 的保存/恢复按钮及状态提示。
// saveURL 接收 POST JSON {"seconds":数字}；restoreURL 的 GET 返回同结构或 null。
// 地址必须是同源绝对路径，可带查询参数。按钮仅在启用 JS 后显示。
func VideoPositionControls(videoID, saveURL, restoreURL string) (Node, error) {
	if videoID == "" || strings.IndexFunc(videoID, unicode.IsSpace) >= 0 {
		return Node{}, fmt.Errorf("webui: invalid video ID")
	}
	for _, endpoint := range []string{saveURL, restoreURL} {
		u, err := url.Parse(endpoint)
		if err != nil || u.IsAbs() || u.Host != "" || u.Fragment != "" || strings.ContainsAny(endpoint, "#\\\r\n\t ") || !localPath(u.Path) {
			return Node{}, fmt.Errorf("webui: invalid video position URL %q", endpoint)
		}
	}
	return El("div", Attrs{"class": "actions video-position", "data-webui-video": videoID, "data-save-url": saveURL, "data-restore-url": restoreURL, "hidden": ""},
		El("button", Attrs{"type": "button", "data-video-action": "save"}, Text("保存位置")),
		El("button", Attrs{"type": "button", "class": "secondary", "data-video-action": "restore"}, Text("恢复位置")),
		El("span", Attrs{"role": "status", "class": "muted"}),
	), nil
}
