package tiingo

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/ekk1/mygo/utils/httpclient"
)

func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	s := httptest.NewServer(handler)
	t.Cleanup(s.Close)
	c, err := New(Config{APIKey: "secret", ProxyURL: "-"})
	if err != nil {
		t.Fatal(err)
	}
	c.baseURL = s.URL
	t.Cleanup(c.CloseIdleConnections)
	return c
}

func TestEOD(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/tiingo/daily/BRK.B/prices" || r.Header.Get("Authorization") != "Token secret" {
			t.Errorf("bad request: %v", r)
		}
		q := r.URL.Query()
		if q.Get("startDate") != "2024-01-02" || q.Get("endDate") != "2024-01-03" || q.Get("resampleFreq") != "daily" || q.Get("token") != "" {
			t.Errorf("query: %v", q)
		}
		fmt.Fprint(w, `[{"date":"2024-01-03T00:00:00Z","open":100,"high":110,"low":90,"close":105,"volume":120,"adjOpen":50,"adjHigh":55,"adjLow":45,"adjClose":52.5,"adjVolume":240,"divCash":0.5,"splitFactor":2},{"date":"2024-01-02T00:00:00Z","close":99}]`)
	})
	bars, err := c.EOD(context.Background(), "BRK.B", "2024-01-02", "2024-01-03")
	if err != nil {
		t.Fatal(err)
	}
	if len(bars) != 2 {
		t.Fatalf("bars: %+v", bars)
	}
	b := bars[1]
	if b.Date.Format("2006-01-02") != "2024-01-03" || b.Open != 100 || b.High != 110 || b.Low != 90 || b.Close != 105 || b.Volume != 120 || b.AdjOpen != 50 || b.AdjHigh != 55 || b.AdjLow != 45 || b.AdjClose != 52.5 || b.AdjVolume != 240 || b.DivCash != 0.5 || b.SplitFactor != 2 {
		t.Fatalf("bar: %+v", b)
	}
}

func TestErrorsAndEmpty(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		wantErr    bool
	}{
		{"empty", "[]", 200, false}, {"null", "null", 200, true}, {"blank", "", 200, true}, {"malformed", "[", 200, true}, {"date", `[{"date":"bad"}]`, 200, true}, {"unauthorized", `{"detail":"bad token"}`, 401, true}, {"rate limited", "busy", 429, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); fmt.Fprint(w, tc.body) })
			bars, err := c.EOD(context.Background(), "AAPL", "2024-01-02", "2024-01-02")
			if (err != nil) != tc.wantErr {
				t.Fatalf("bars=%v err=%v", bars, err)
			}
			if tc.status >= 400 {
				var se *httpclient.StatusError
				if !errors.As(err, &se) || se.Response.StatusCode != tc.status {
					t.Fatalf("status error: %v", err)
				}
			}
		})
	}
}

func TestValidationCancellationAndConcurrency(t *testing.T) {
	for _, cfg := range []Config{{}, {APIKey: " \t"}, {APIKey: "x", ProxyURL: "invalid"}, {APIKey: "x", Timeout: -1}} {
		if _, err := New(cfg); err == nil {
			t.Fatalf("accepted %+v", cfg)
		}
	}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "[]") })
	for _, v := range [][3]string{{"", "2024-01-01", "2024-01-02"}, {"../bad", "2024-01-01", "2024-01-02"}, {"A", "2024-02-30", "2024-03-01"}, {"A", "2024-02-02", "2024-02-01"}, {"A", "", ""}} {
		if _, err := c.EOD(context.Background(), v[0], v[1], v[2]); err == nil {
			t.Errorf("accepted %v", v)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.EOD(ctx, "A", "2024-01-01", "2024-01-01"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.EOD(context.Background(), "A", "2024-01-01", "2024-01-01"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}

func TestProxy(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Host != "upstream.invalid" || r.Header.Get("Authorization") != "Token secret" {
			t.Errorf("proxy request: %v", r)
		}
		fmt.Fprint(w, "[]")
	}))
	defer proxy.Close()
	c, err := New(Config{APIKey: "secret", ProxyURL: proxy.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseIdleConnections()
	c.baseURL = "http://upstream.invalid"
	if _, err := c.EOD(context.Background(), "A", "2024-01-01", "2024-01-01"); err != nil {
		t.Fatal(err)
	}
}
