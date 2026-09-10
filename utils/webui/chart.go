package webui

import (
	"fmt"
	"html"
	"html/template"
	"math"
	"strconv"
	"strings"
)

// Candle 是一根 K 线。Label 为横轴标签；按传入顺序等距绘制，不解析时间。
type Candle struct {
	Label                  string
	Open, High, Low, Close float64
	// Volume 为非负成交量；零值不绘制量柱，但仍在提示中显示。
	Volume float64
}

// ChartLine 是与 K 线逐项对齐的折线。NaN 表示缺失点（断线）；Name 用作图例。
type ChartLine struct {
	Name   string
	Values []float64
}

// CandlestickChart 生成自适应 SVG K 线图，可叠加均线等折线，不需要绘图 JS。
// 默认标出最后一根 K 的收盘价。价格与折线共用线性纵轴，成交量独立缩放；非法 OHLCV、长度不匹配或范围溢出返回 error。
// 空数据生成占位提示。构造后不保留输入切片，可并发复用返回的 Node。
func CandlestickChart(candles []Candle, lines ...ChartLine) (Node, error) {
	low, high := math.Inf(1), math.Inf(-1)
	maxVolume := 0.0
	include := func(v float64) { low = math.Min(low, v); high = math.Max(high, v) }
	for i, c := range candles {
		if !finite(c.Open) || !finite(c.High) || !finite(c.Low) || !finite(c.Close) || !finite(c.Volume) || c.Volume < 0 || c.Low > math.Min(c.Open, c.Close) || c.High < math.Max(c.Open, c.Close) {
			return Node{}, fmt.Errorf("webui: invalid candle %d", i)
		}
		include(c.Low)
		include(c.High)
		maxVolume = math.Max(maxVolume, c.Volume)
	}
	for i, l := range lines {
		if len(l.Values) != len(candles) {
			return Node{}, fmt.Errorf("webui: line %d length differs from candles", i)
		}
		for _, v := range l.Values {
			if math.IsNaN(v) {
				continue
			}
			if !finite(v) {
				return Node{}, fmt.Errorf("webui: invalid line %d value", i)
			}
			include(v)
		}
	}
	if len(candles) == 0 {
		return El("p", Attrs{"class": "empty muted"}, Text("暂无 K 线数据")), nil
	}
	span := high - low
	if !finite(span) {
		return Node{}, fmt.Errorf("webui: chart range overflow")
	}
	pad := span * .05
	if span == 0 {
		pad = math.Max(math.Abs(low)*.05, 1)
	}
	low -= pad
	high += pad
	span = high - low
	if !finite(low) || !finite(high) || !finite(span) || span <= 0 {
		return Node{}, fmt.Errorf("webui: chart range overflow")
	}
	// Keep small movements legible even when the absolute price is large.
	tickStep := math.Max(span/4, math.SmallestNonzeroFloat64)
	digits := max(4, min(17, int(math.Floor(math.Log10(math.Max(math.Abs(low), math.Abs(high))))-math.Floor(math.Log10(tickStep)))+2))
	var ticks [5]string
	left := 86.0
	for i := range ticks {
		ticks[i] = strconv.FormatFloat(high-span*(float64(i)/4), 'g', digits, 64)
		// Leave room for the larger labels used on narrow screens.
		left = math.Max(left, float64(len(ticks[i]))*15+16)
	}
	const top, right, height = 20.0, 776.0, 290.0
	step := (right - left) / float64(len(candles))
	x := func(i int) float64 { return left + (float64(i)+.5)*step }
	y := func(v float64) float64 { return top + (1-(v-low)/span)*height }
	num := func(v float64) string { return strconv.FormatFloat(v, 'f', 3, 64) }
	var svg strings.Builder
	svg.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 800 350" class="candlestick-chart" role="group" aria-label="K 线图"><title>K 线图；白色阳线、黑色阴线，淡灰成交量独立比例，悬停查看 OHLCV</title>`)
	for i, tick := range ticks {
		py := num(top + height*float64(i)/4)
		fmt.Fprintf(&svg, `<path class="chart-grid" d="M%s %sH776"/><text x="%s" y="%s" text-anchor="end" dominant-baseline="middle">%s</text>`, num(left), py, num(left-8), py, tick)
	}
	var up, down strings.Builder
	half := math.Min(step*.3, 12)
	for i, c := range candles {
		if c.Volume == 0 {
			continue
		}
		volumeHeight := (c.Volume / maxVolume) * height * .25
		fmt.Fprintf(&svg, `<rect class="chart-volume" x="%s" y="%s" width="%s" height="%s"/>`, num(x(i)-half), num(top+height-volumeHeight), num(half*2), num(volumeHeight))
	}
	fmt.Fprintf(&svg, `<path class="chart-close" d="M%s %sH776"><title>最新收盘价：%s</title></path>`, num(left), num(y(candles[len(candles)-1].Close)), strconv.FormatFloat(candles[len(candles)-1].Close, 'g', -1, 64))
	for i, c := range candles {
		p := &up
		if c.Close < c.Open {
			p = &down
		}
		px := x(i)
		a, b := y(math.Max(c.Open, c.Close)), y(math.Min(c.Open, c.Close))
		// SVG strokes the entire compound path after filling it. Keep wicks
		// outside the body so that their stroke cannot show through the fill.
		fmt.Fprintf(p, "M%s %sV%sM%s %sV%s", num(px), num(y(c.High)), num(a), num(px), num(b), num(y(c.Low)))
		// A doji remains a horizontal stroke at its actual price.
		if a == b {
			fmt.Fprintf(p, "M%s %sH%s", num(px-half), num(a), num(px+half))
			continue
		}
		fmt.Fprintf(p, "M%s %sH%sV%sH%sZ", num(px-half), num(a), num(px+half), num(b), num(px-half))
	}
	fmt.Fprintf(&svg, `<path class="chart-up" d="%s"/><path class="chart-down" d="%s"/>`, up.String(), down.String())
	for i, l := range lines {
		var p strings.Builder
		move := true
		for j, v := range l.Values {
			if math.IsNaN(v) {
				move = true
				continue
			}
			cmd := "L"
			if move {
				cmd = "M"
			}
			fmt.Fprintf(&p, "%s%s %s", cmd, num(x(j)), num(y(v)))
			move = false
		}
		fmt.Fprintf(&svg, `<path class="chart-line chart-line-%d" d="%s"><title>%s</title></path>`, i%3, p.String(), html.EscapeString(l.Name))
	}
	indices := []int{0}
	if len(candles) > 2 {
		indices = append(indices, len(candles)/2)
	}
	if len(candles) > 1 {
		indices = append(indices, len(candles)-1)
	}
	for _, i := range indices {
		anchor := "middle"
		if i == 0 {
			anchor = "start"
		} else if i == len(candles)-1 {
			anchor = "end"
		}
		label := []rune(candles[i].Label)
		if len(label) > 12 {
			label = append(label[:11], '…')
		}
		fmt.Fprintf(&svg, `<text x="%s" y="338" text-anchor="%s">%s</text>`, num(x(i)), anchor, html.EscapeString(string(label)))
	}
	// Whole-column hit areas make thin candles and volume bars easy to inspect.
	for i, c := range candles {
		tip := fmt.Sprintf("%s\nO: %s\nH: %s\nL: %s\nC: %s\nV: %s", c.Label, strconv.FormatFloat(c.Open, 'g', -1, 64), strconv.FormatFloat(c.High, 'g', -1, 64), strconv.FormatFloat(c.Low, 'g', -1, 64), strconv.FormatFloat(c.Close, 'g', -1, 64), strconv.FormatFloat(c.Volume, 'g', -1, 64))
		tip = html.EscapeString(strings.TrimSpace(tip))
		fmt.Fprintf(&svg, `<rect class="chart-hit" x="%s" y="20" width="%s" height="290" tabindex="0" role="img" aria-label="%s"><title>%s</title></rect>`, num(left+float64(i)*step), num(step), tip, tip)
	}
	svg.WriteString(`</svg>`)
	legend := []Node{El("span", nil, Text("白涨 / 黑跌 · 淡灰成交量"))}
	for i, l := range lines {
		legend = append(legend, El("span", Attrs{"class": fmt.Sprintf("chart-legend-line chart-line-%d", i%3)}, Text(l.Name)))
	}
	return El("figure", Attrs{"class": "chart"}, Node{svg: template.HTML(svg.String())}, El("div", Attrs{"class": "chart-tooltip", "role": "tooltip", "hidden": ""}), El("figcaption", Attrs{"class": "actions muted"}, legend...)), nil
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
