package main

import (
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/ekk1/mygo/utils/webui"
)

const demoCandleCount = 120

func demoChartDate(index int) time.Time { return time.Date(2026, 1, 1+index, 0, 0, 0, 0, time.UTC) }

// End is exclusive for slicing; the form uses inclusive calendar dates.
func chartDateWindow(query url.Values) (start, end int, err error) {
	bars, _ := strconv.Atoi(query.Get("bars"))
	if bars != 20 && bars != 60 && bars != 120 {
		bars = 60
	}
	start, end = demoCandleCount-bars, demoCandleCount
	if query.Has("start") || query.Has("end") {
		first, e1 := time.Parse(time.DateOnly, query.Get("start"))
		last, e2 := time.Parse(time.DateOnly, query.Get("end"))
		if e1 != nil || e2 != nil {
			return 0, 0, fmt.Errorf("请输入有效的开始和结束日期。")
		}
		if first.After(last) {
			return 0, 0, fmt.Errorf("开始日期不能晚于结束日期。")
		}
		if first.Before(demoChartDate(0)) || last.After(demoChartDate(demoCandleCount-1)) {
			return 0, 0, fmt.Errorf("示例日期范围为 2026-01-01 至 2026-04-30。")
		}
		start = int(first.Sub(demoChartDate(0)) / (24 * time.Hour))
		end = int(last.Sub(demoChartDate(0))/(24*time.Hour)) + 1
	}
	step := 1
	if query.Has("step") {
		switch query.Get("step") {
		case "1":
		case "5":
			step = 5
		default:
			return 0, 0, fmt.Errorf("移动步长必须为 1 或 5。")
		}
	}
	width := end - start
	switch query.Get("move") {
	case "left":
		start = max(0, start-step)
		end = start + width
	case "right":
		end = min(demoCandleCount, end+step)
		start = end - width
	case "prev":
		start = max(0, start-width)
		end = start + width
	case "next":
		end = min(demoCandleCount, end+width)
		start = end - width
	case "":
	default:
		return 0, 0, fmt.Errorf("移动方向无效。")
	}
	return start, end, nil
}

func chartDateControls(query url.Values, start, end int, rangeErr error) webui.Node {
	first, last := demoChartDate(start).Format(time.DateOnly), demoChartDate(end-1).Format(time.DateOnly)
	var notice webui.Node
	if rangeErr != nil {
		first, last = query.Get("start"), query.Get("end")
		notice = webui.El("p", webui.Attrs{"class": "notice", "role": "alert", "id": "chart-date-error"}, webui.Text(rangeErr.Error()+" 暂显示最近 60 根。"))
	}
	dateInput := func(id, label, value string) webui.Node {
		attrs := webui.Attrs{"type": "date", "id": id, "name": id, "value": value, "min": "2026-01-01", "max": "2026-04-30", "required": ""}
		if rangeErr != nil {
			attrs["aria-invalid"] = "true"
			attrs["aria-describedby"] = "chart-date-error"
		}
		return webui.El("div", nil, webui.El("label", webui.Attrs{"for": id}, webui.Text(label)), webui.El("input", attrs))
	}
	previous := webui.Attrs{"type": "submit", "name": "move", "value": "prev", "class": "secondary"}
	next := webui.Attrs{"type": "submit", "name": "move", "value": "next", "class": "secondary"}
	if start == 0 || rangeErr != nil {
		previous["disabled"] = ""
	}
	if end == demoCandleCount || rangeErr != nil {
		next["disabled"] = ""
	}
	return webui.Group(notice,
		webui.El("form", webui.Attrs{"method": "get", "action": "/components"},
			webui.El("div", webui.Attrs{"class": "grid"}, dateInput("start", "开始日期", first), dateInput("end", "结束日期", last)),
			webui.El("div", webui.Attrs{"class": "actions"},
				webui.El("button", webui.Attrs{"type": "submit"}, webui.Text("应用日期")),
				webui.El("button", previous, webui.Text("← 前一段")), webui.El("button", next, webui.Text("后一段 →"))),
		),
		webui.El("p", webui.Attrs{"class": "field-help"}, webui.Text(fmt.Sprintf("当前 %s 至 %s，共 %d 根；按钮移动一个窗口；图表获得焦点后，← / → 平移 1 根，Shift + ← / → 平移 5 根。模拟数据含周末，范围为 2026-01-01 至 2026-04-30。", demoChartDate(start).Format(time.DateOnly), demoChartDate(end-1).Format(time.DateOnly), end-start))),
	)
}
