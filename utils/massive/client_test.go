package massive

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

func TestBarsAndPagination(t *testing.T) {
	for _, span := range []string{"day", "minute"} {
		for _, adjusted := range []bool{false, true} {
			t.Run(fmt.Sprint(span, adjusted), func(t *testing.T) {
				calls := 0
				c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
					calls++
					if r.Header.Get("Authorization") != "Bearer secret" || r.URL.Query().Has("apiKey") {
						t.Errorf("authentication: %v", r)
					}
					if calls == 1 {
						if r.URL.Path != "/v2/aggs/ticker/AAPL/range/1/"+span+"/2024-01-02/2024-01-03" {
							t.Errorf("path: %s", r.URL.Path)
						}
						q := r.URL.Query()
						if q.Get("adjusted") != fmt.Sprint(adjusted) || q.Get("sort") != "asc" || q.Get("limit") != "50000" {
							t.Errorf("query: %v", q)
						}
						fmt.Fprintf(w, `{"status":"OK","results":[{"t":1704240000000,"o":100,"h":110,"l":90,"c":105,"v":120.5,"vw":102.5,"n":7,"otc":true}],"next_url":"http://%s/next?cursor=abc"}`, r.Host)
					} else {
						if calls > 2 || r.URL.Path != "/next" || r.URL.Query().Get("cursor") != "abc" {
							t.Errorf("pagination: %v", r)
						}
						fmt.Fprint(w, `{"status":"OK","results":[{"t":1704153600000,"c":99}]}`)
					}
				})
				get := c.Daily
				if span == "minute" {
					get = c.Minute
				}
				bars, err := get(context.Background(), "AAPL", "2024-01-02", "2024-01-03", adjusted)
				if err != nil {
					t.Fatal(err)
				}
				if calls != 2 || len(bars) != 2 {
					t.Fatalf("calls=%d bars=%v", calls, bars)
				}
				b := bars[1]
				if b.Timestamp != 1704240000000 || b.Open != 100 || b.High != 110 || b.Low != 90 || b.Close != 105 || b.Volume != 120.5 || b.VWAP != 102.5 || b.Transactions != 7 || !b.OTC {
					t.Fatalf("bar: %+v", b)
				}
			})
		}
	}
}

func TestErrorsAndEmpty(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		wantErr    bool
	}{
		{"empty", `{"status":"OK"}`, 200, false}, {"delayed", `{"status":"DELAYED","results":[]}`, 200, false}, {"api error", `{"status":"ERROR","error":"denied"}`, 200, true}, {"null", "null", 200, true}, {"blank", "", 200, true}, {"malformed", "{", 200, true}, {"unauthorized", "denied", 401, true}, {"rate limited", "busy", 429, true},
		{"foreign page", `{"status":"OK","next_url":"https://evil.invalid/page"}`, 200, true}, {"loop", `{"status":"OK","next_url":"/loop"}`, 200, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls > 3 {
					w.WriteHeader(500)
					return
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			})
			bars, err := c.Daily(context.Background(), "A", "2024-01-01", "2024-01-01", true)
			if (err != nil) != tc.wantErr || (err != nil && bars != nil) {
				t.Fatalf("bars=%v err=%v", bars, err)
			}
			if calls > 2 {
				t.Errorf("pagination loop not stopped: %d", calls)
			}
			if tc.status >= 400 {
				var se *httpclient.StatusError
				if !errors.As(err, &se) || se.Response.StatusCode != tc.status {
					t.Fatalf("status: %v", err)
				}
			}
		})
	}
}

func TestLaterPageFailure(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/next" {
			w.WriteHeader(429)
			return
		}
		fmt.Fprint(w, `{"status":"OK","results":[{"t":1}],"next_url":"/next"}`)
	})
	bars, err := c.Minute(context.Background(), "A", "2024-01-01", "2024-01-01", false)
	if err == nil || bars != nil {
		t.Fatalf("partial result: %v %v", bars, err)
	}
}

func TestValidationCancellationAndConcurrency(t *testing.T) {
	for _, cfg := range []Config{{}, {APIKey: " \t"}, {APIKey: "x", ProxyURL: "invalid"}, {APIKey: "x", Timeout: -1}} {
		if _, err := New(cfg); err == nil {
			t.Fatalf("accepted %+v", cfg)
		}
	}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"status":"OK"}`) })
	for _, v := range [][3]string{{"", "2024-01-01", "2024-01-02"}, {"../bad", "2024-01-01", "2024-01-02"}, {"A", "2024-02-30", "2024-03-01"}, {"A", "2024-02-02", "2024-02-01"}, {"A", "", ""}} {
		if _, err := c.Daily(context.Background(), v[0], v[1], v[2], true); err == nil {
			t.Errorf("accepted %v", v)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Minute(ctx, "A", "2024-01-01", "2024-01-01", true); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Daily(context.Background(), "A", "2024-01-01", "2024-01-01", false); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}

func TestProxy(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Host != "upstream.invalid" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("proxy: %v", r)
		}
		fmt.Fprint(w, `{"status":"OK"}`)
	}))
	defer proxy.Close()
	c, err := New(Config{APIKey: "secret", ProxyURL: proxy.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseIdleConnections()
	c.baseURL = "http://upstream.invalid"
	if _, err := c.Daily(context.Background(), "A", "2024-01-01", "2024-01-01", false); err != nil {
		t.Fatal(err)
	}
}
