package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/ekk1/mygo/utils/httpserver"
	"github.com/ekk1/mygo/utils/webui"
)

//go:embed components.js
var componentsJS string

func registerComponents(s *httpserver.Server) error {
	for _, route := range []struct {
		pattern string
		handler http.HandlerFunc
	}{
		{"GET /components", components},
		{"GET /components/chart", components},
		{"GET /components.js", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			if r.Method != "HEAD" {
				fmt.Fprint(w, componentsJS)
			}
		}},
		{"GET /components/position", loadPosition},
		{"POST /components/position", savePosition},
	} {
		if err := s.HandleFunc(route.pattern, route.handler); err != nil {
			return err
		}
	}
	return nil
}

// The demo stores one position per browser in an HttpOnly cookie, without shared state.
func savePosition(w http.ResponseWriter, r *http.Request) {
	var position struct {
		Seconds *float64 `json:"seconds"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	if err := decoder.Decode(&position); err != nil || position.Seconds == nil || math.IsNaN(*position.Seconds) || math.IsInf(*position.Seconds, 0) || *position.Seconds < 0 {
		http.Error(w, "Invalid position", 400)
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		http.Error(w, "Invalid position", 400)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "webui-demo-position", Value: strconv.FormatFloat(*position.Seconds, 'g', -1, 64), Path: "/components/position", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 30 * 24 * 60 * 60, Secure: r.TLS != nil})
	w.WriteHeader(http.StatusNoContent)
}

func loadPosition(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == "HEAD" {
		return
	}
	if cookie, err := r.Cookie("webui-demo-position"); err == nil {
		if seconds, err := strconv.ParseFloat(cookie.Value, 64); err == nil && !math.IsNaN(seconds) && !math.IsInf(seconds, 0) && seconds >= 0 {
			json.NewEncoder(w).Encode(map[string]float64{"seconds": seconds})
			return
		}
	}
	fmt.Fprintln(w, "null")
}

func components(w http.ResponseWriter, r *http.Request) {
	partial := r.URL.Path == "/components/chart"
	query := r.URL.Query()
	start, end, rangeErr := chartDateWindow(query)
	status := http.StatusOK
	if rangeErr != nil {
		if partial {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnprocessableEntity)
			if r.Method != "HEAD" {
				json.NewEncoder(w).Encode(map[string]string{"error": rangeErr.Error()})
			}
			return
		}
		start, end = demoCandleCount-60, demoCandleCount
		status = http.StatusUnprocessableEntity
	} else if query.Get("move") != "" && !partial {
		target := url.Values{"start": {demoChartDate(start).Format(time.DateOnly)}, "end": {demoChartDate(end - 1).Format(time.DateOnly)}}
		http.Redirect(w, r, "/components?"+target.Encode(), http.StatusSeeOther)
		return
	}
	// Calculate SMA before slicing so the selected window retains its warm-up history.
	candles := make([]webui.Candle, demoCandleCount)
	averages := make([]float64, demoCandleCount)
	sum := 0.0
	price := func(v float64) float64 { return math.Round(v*100) / 100 }
	for i := range candles {
		open := price(100 + float64(i)*.12 + math.Sin(float64(i)*.4)*4)
		close := price(open + math.Sin(float64(i)*1.7)*2)
		candles[i] = webui.Candle{Label: demoChartDate(i).Format("01-02"), Open: open, High: price(math.Max(open, close) + 1.3), Low: price(math.Min(open, close) - 1.1), Close: close}
		candles[i].Volume = float64(1000 + (i*137)%2400)
		sum += close
		if i >= 5 {
			sum -= candles[i-5].Close
		}
		averages[i] = math.NaN()
		if i >= 4 {
			averages[i] = sum / 5
		}
	}
	chart, err := webui.CandlestickChart(candles[start:end], webui.ChartLine{Name: "SMA 5", Values: averages[start:end]})
	if err != nil {
		http.Error(w, "Chart unavailable", 500)
		return
	}
	controls, err := webui.VideoPositionControls("demo-video", "/components/position", "/components/position")
	if err != nil {
		http.Error(w, "Video controls unavailable", 500)
		return
	}
	var ranges []webui.Node
	for _, n := range []int{20, 60, 120} {
		ranges = append(ranges, webui.El("button", webui.Attrs{"type": "submit", "name": "bars", "value": strconv.Itoa(n), "class": "secondary"}, webui.Text(fmt.Sprintf("最近 %d 根", n))))
	}
	chartURL := "/components"
	if query.Has("start") || query.Get("move") != "" {
		chartURL += "?" + url.Values{"start": {demoChartDate(start).Format(time.DateOnly)}, "end": {demoChartDate(end - 1).Format(time.DateOnly)}}.Encode()
	} else if query.Has("bars") {
		chartURL += "?" + url.Values{"bars": {query.Get("bars")}}.Encode()
	}
	chartContent := webui.El("div", webui.Attrs{"id": "chart-content", "data-url": chartURL},
		webui.El("h2", nil, webui.Text("K 线与均线")),
		webui.El("p", webui.Attrs{"class": "muted"}, webui.Text("模拟日线 · 白涨黑跌 · SMA 5 · 悬停查看 OHLCV")),
		chartDateControls(query, start, end, rangeErr), webui.El("hr", nil),
		webui.El("form", webui.Attrs{"method": "get", "action": "/components", "class": "actions"}, ranges...), chart)
	if partial {
		var output bytes.Buffer
		if err := webui.RenderFragment(&output, chartContent); err != nil {
			http.Error(w, "Chart unavailable", 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != "HEAD" {
			if _, err := output.WriteTo(w); err != nil {
				fmt.Println(err)
			}
		}
		return
	}
	var themes []webui.Node
	for _, theme := range []struct{ id, name string }{{"rose", "玫瑰灰"}, {"sand", "燕麦"}, {"sage", "鼠尾草"}, {"dusk", "暮色"}} {
		themes = append(themes, webui.El("button", webui.Attrs{"type": "button", "data-webui-theme": theme.id}, webui.Text(theme.name)))
	}
	page := webui.Page{Title: "视频与 K 线 · webui", Scripts: []string{"/components.js"}, Body: webui.Group(
		webui.El("header", nil, webui.El("a", webui.Attrs{"href": "/", "class": "brand"}, webui.Text("webui")),
			webui.El("div", webui.Attrs{"class": "theme-switcher", "data-webui-theme-controls": "", "hidden": "", "role": "group", "aria-label": "配色"}, themes...),
			webui.El("span", webui.Attrs{"data-webui-theme-status": "", "role": "status"})),
		webui.El("main", webui.Attrs{"class": "stack"},
			webui.El("h1", nil, webui.Text("视频与 K 线")),
			webui.El("section", webui.Attrs{"class": "card", "id": "chart-panel"}, chartContent,
				webui.El("p", webui.Attrs{"id": "chart-update-status", "role": "status", "class": "field-help", "hidden": ""})),
			webui.El("section", webui.Attrs{"class": "card"}, webui.El("h2", nil, webui.Text("手动记忆播放位置")),
				webui.El("label", webui.Attrs{"for": "video-file"}, webui.Text("选择本地视频")),
				webui.El("input", webui.Attrs{"id": "video-file", "type": "file", "accept": "video/*"}),
				webui.El("video", webui.Attrs{"id": "demo-video", "controls": "", "preload": "metadata", "aria-label": "示例视频"}), controls,
				webui.El("p", webui.Attrs{"class": "muted"}, webui.Text("视频只在本机播放，不上传。示例回调用 Cookie 保存一个位置；刷新后重新选择同一文件即可恢复，更换视频前请重新保存。"))),
		))}
	var output bytes.Buffer
	if err := webui.Render(&output, page); err != nil {
		http.Error(w, "Page unavailable", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if r.Method != "HEAD" {
		if _, err := output.WriteTo(w); err != nil {
			fmt.Println(err)
		}
	}
}
