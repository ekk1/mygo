package webui

import (
	"bytes"
	"encoding/xml"
	"io"
	"math"
	"strings"
	"testing"
)

func chartHTML(t *testing.T, candles []Candle, lines ...ChartLine) string {
	t.Helper()
	n, err := CandlestickChart(candles, lines...)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := Render(&b, Page{Body: n}); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestChartScaleEscapingAndGaps(t *testing.T) {
	candles := []Candle{{Label: `</text><script>{{.}}</script>`, Open: 10, High: 20, Low: 0, Close: 15}, {Open: 15, High: 20, Low: 0, Close: 10}, {Open: 10, High: 20, Low: 0, Close: 10}}
	output := chartHTML(t, candles, ChartLine{Name: `<SMA>`, Values: []float64{30, math.NaN(), 30}})
	start, end := strings.Index(output, "<svg"), strings.Index(output, "</svg>")
	if start < 0 || end < 0 {
		t.Fatal("missing SVG")
	}
	if strings.Contains(output, "<script>{{.}}") || !strings.Contains(output, "&lt;SMA&gt;") {
		t.Fatal("unsafe chart labels")
	}
	decoder := xml.NewDecoder(strings.NewReader(output[start : end+6]))
	var paths []string
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if el, ok := token.(xml.StartElement); ok && el.Name.Local == "path" {
			for _, a := range el.Attr {
				if a.Name.Local == "d" {
					paths = append(paths, a.Value)
				}
			}
		}
	}
	// The overlay extends the scale to 30; gaps must start new subpaths.
	if len(paths) < 3 {
		t.Fatal("missing candle/line paths")
	}
	line := paths[len(paths)-1]
	if strings.Count(line, "M") != 2 || strings.Contains(line, "L") {
		t.Fatalf("gap bridged: %s", line)
	}
	if strings.Contains(output, "NaN") || strings.Contains(output, "Inf") {
		t.Fatal("invalid coordinates")
	}
	// All candle and line points use the same mapping, including padding.
	if !strings.Contains(output, ">31.5</text>") || !strings.Contains(output, ">-1.5</text>") {
		t.Fatal("overlay excluded from scale")
	}
}

func TestChartEdges(t *testing.T) {
	for _, candles := range [][]Candle{nil, {{Open: 0, High: 0, Low: 0, Close: 0}}, {{Open: -4, High: -2, Low: -5, Close: -3}}, {{Open: 1e-10, High: 2e-10, Low: 0, Close: 1e-10}}} {
		output := chartHTML(t, candles)
		if strings.Contains(output, "NaN") || strings.Contains(output, "Inf") {
			t.Fatal("nonfinite output")
		}
	}
	good := []Candle{{Open: 1, High: 2, Low: 0, Close: 1}}
	for _, c := range []Candle{{Open: 3, High: 2, Low: 0, Close: 1}, {Open: 1, High: 2, Low: 3, Close: 1}, {Open: 1, High: math.Inf(1), Low: 0, Close: 1}, {Open: math.NaN()}, {High: math.MaxFloat64, Low: -math.MaxFloat64}} {
		if _, err := CandlestickChart([]Candle{c}); err == nil {
			t.Errorf("accepted invalid candle: %+v", c)
		}
	}
	for _, values := range [][]float64{nil, {1, 2}, {math.Inf(1)}} {
		if _, err := CandlestickChart(good, ChartLine{Values: values}); err == nil {
			t.Errorf("accepted line %v", values)
		}
	}
}

func TestChartNodeImmutable(t *testing.T) {
	c := []Candle{{Open: 1, High: 2, Low: 0, Close: 1}}
	l := []ChartLine{{Name: "SMA", Values: []float64{1}}}
	n, err := CandlestickChart(c, l...)
	if err != nil {
		t.Fatal(err)
	}
	var before, after bytes.Buffer
	if err := Render(&before, Page{Body: n}); err != nil {
		t.Fatal(err)
	}
	c[0].High = 99
	l[0].Values[0] = 99
	l[0].Name = "changed"
	if err := Render(&after, Page{Body: n}); err != nil {
		t.Fatal(err)
	}
	if before.String() != after.String() {
		t.Fatal("retained mutable input")
	}
}

func TestChartTickPrecisionAndExtremeRange(t *testing.T) {
	output := chartHTML(t, []Candle{{Open: 100000, High: 100000.01, Low: 99999.99, Close: 100000}})
	if strings.Count(output, ">100000</text>") > 1 {
		t.Fatal("different prices have identical axis labels")
	}
	output = chartHTML(t, []Candle{{Open: -1e308, High: 0, Low: -1e308, Close: 0}})
	if strings.Contains(output, "Inf") || strings.Contains(output, "NaN") {
		t.Fatal("finite range rendered nonfinite ticks")
	}
}

func TestChartVolumeAndTooltip(t *testing.T) {
	candles := []Candle{{Label: `<x>&"`, Open: 10, High: 20, Low: 0, Close: 15, Volume: 100}, {Open: 15, High: 20, Low: 0, Close: 10, Volume: 50}}
	output := chartHTML(t, candles)
	if !strings.Contains(output, `class="chart-volume"`) || !strings.Contains(output, `height="72.500"`) || !strings.Contains(output, `height="36.250"`) {
		t.Fatal("volume bars do not share an independent linear scale")
	}
	if !strings.Contains(output, ">21</text>") || !strings.Contains(output, ">-1</text>") {
		t.Fatal("volume changed price scale")
	}
	if strings.Index(output, `class="chart-volume"`) > strings.Index(output, `class="chart-up"`) {
		t.Fatal("volume covers candles")
	}
	if strings.Count(output, `class="chart-hit"`) != 2 || !strings.Contains(output, "O: 10\nH: 20\nL: 0\nC: 15\nV: 100") {
		t.Fatal("missing per-candle OHLCV")
	}
	if strings.Contains(output, `<title><x>`) || !strings.Contains(output, "&lt;x&gt;&amp;") {
		t.Fatal("unsafe tooltip label")
	}
	for _, volume := range []float64{-1, math.NaN(), math.Inf(1)} {
		candles[0].Volume = volume
		if _, err := CandlestickChart(candles); err == nil {
			t.Errorf("accepted invalid volume %v", volume)
		}
	}
	candles[0].Volume = math.MaxFloat64
	candles[1].Volume = 0
	output = chartHTML(t, candles)
	if strings.Contains(output, "Inf") || strings.Contains(output, "NaN") {
		t.Fatal("volume overflow")
	}
	candles[0].Volume = 0
	output = chartHTML(t, candles)
	if strings.Contains(output, `class="chart-volume"`) || !strings.Contains(output, "V: 0") {
		t.Fatal("zero volume must keep tooltip without drawing bars")
	}
}

func TestChartLastCloseLine(t *testing.T) {
	output := chartHTML(t, []Candle{{Open: 10, High: 20, Low: 0, Close: 10}, {Open: 10, High: 20, Low: 0, Close: 15}})
	// Scale is -1..21, so close 15 maps to y=99.091.
	if !strings.Contains(output, `class="chart-close" d="M86.000 99.091H776"`) {
		t.Fatal("missing last close line at price scale")
	}
	if strings.Index(output, `class="chart-close"`) > strings.Index(output, `class="chart-up"`) {
		t.Fatal("close line must be behind candle bodies")
	}
	if strings.Contains(chartHTML(t, nil), `class="chart-close"`) {
		t.Fatal("empty chart has no close")
	}
}
