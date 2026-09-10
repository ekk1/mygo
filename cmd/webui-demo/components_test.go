package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestComponentsAndPosition(t *testing.T) {
	s, err := newServer()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, path := range []string{"/components", "/components?bars=20", "/components?bars=invalid"} {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || !strings.Contains(w.Body.String(), "<svg") || !strings.Contains(w.Body.String(), "SMA 5") {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/components/position", nil))
	if strings.TrimSpace(w.Body.String()) != "null" {
		t.Fatal(w.Body.String())
	}
	for _, body := range []string{`{"seconds":-1}`, `{"seconds":"2"}`, `{}`, `{"seconds":null}`, `{"seconds":2} {}`, `{"seconds":1e999}`, strings.Repeat("x", 2000)} {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("POST", "/components/position", strings.NewReader(body)))
		if w.Code != http.StatusBadRequest {
			t.Errorf("accepted %s: %d", body, w.Code)
		}
	}
	w = httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("POST", "/components/position", strings.NewReader(`{"seconds":12.5}`)))
	if w.Code != 204 {
		t.Fatal(w.Code)
	}
	request := httptest.NewRequest("GET", "/components/position", nil)
	for _, c := range w.Result().Cookies() {
		request.AddCookie(c)
	}
	w = httptest.NewRecorder()
	s.ServeHTTP(w, request)
	if !strings.Contains(w.Body.String(), `"seconds":12.5`) {
		t.Fatal(w.Body.String())
	}
}

func TestChartDateWindow(t *testing.T) {
	for _, tc := range []struct {
		query      string
		start, end int
		invalid    bool
	}{
		{"", 60, 120, false}, {"bars=20", 100, 120, false},
		{"start=2026-02-01&end=2026-02-10", 31, 41, false},
		{"start=2026-01-01&end=2026-01-01", 0, 1, false},
		{"start=2026-04-30&end=2026-04-30", 119, 120, false},
		{"start=2026-02-01&end=2026-02-10&move=left", 30, 40, false},
		{"start=2026-02-01&end=2026-02-10&move=right", 32, 42, false},
		{"start=2026-01-01&end=2026-01-10&move=left", 0, 10, false},
		{"bars=20&move=right", 100, 120, false},
		{"bars=20&move=left&step=5", 95, 115, false},
		{"start=2026-02-01&end=2026-02-10&move=right&step=5", 36, 46, false},
		{"start=2026-01-03&end=2026-01-12&move=left&step=5", 0, 10, false},
		{"start=2026-04-09&end=2026-04-28&move=right&step=5", 100, 120, false},
		{"bars=20&move=left&step=0", 0, 0, true},
		{"bars=20&move=left&step=oops", 0, 0, true},
		{"start=2026-02-01&end=2026-02-10&move=prev", 21, 31, false},
		{"start=2026-02-01&end=2026-02-10&move=next", 41, 51, false},
		{"start=2026-01-05&end=2026-01-14&move=prev", 0, 10, false},
		{"start=2026-04-15&end=2026-04-24&move=next", 110, 120, false},
		{"start=2026-02-10&end=2026-02-01", 0, 0, true},
		{"start=2026-02-30&end=2026-03-10", 0, 0, true},
		{"start=2025-12-31&end=2026-01-10", 0, 0, true},
		{"start=2026-04-20&end=2026-05-01", 0, 0, true},
		{"start=&end=", 0, 0, true}, {"start=2026-01-01", 0, 0, true},
	} {
		q, err := url.ParseQuery(tc.query)
		if err != nil {
			t.Fatal(err)
		}
		start, end, err := chartDateWindow(q)
		if tc.invalid {
			if err == nil {
				t.Errorf("accepted %q", tc.query)
			}
			continue
		}
		if err != nil || start != tc.start || end != tc.end {
			t.Errorf("%q: got (%d,%d,%v), want (%d,%d)", tc.query, start, end, err, tc.start, tc.end)
		}
	}
}

func TestChartDateRequests(t *testing.T) {
	s, err := newServer()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/components?start=2026-02-01&end=2026-02-10", nil))
	if w.Code != 200 || strings.Count(w.Body.String(), `class="chart-hit"`) != 10 || !strings.Contains(w.Body.String(), `value="2026-02-01"`) {
		t.Fatal("date range not applied", w.Code)
	}
	w = httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/components?start=2026-02-01&end=2026-02-10&move=prev", nil))
	if w.Code != 303 || w.Header().Get("Location") != "/components?end=2026-01-31&start=2026-01-22" {
		t.Fatal("wrong movement", w.Code, w.Header())
	}
	w = httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/components?start=2026-03-01&end=2026-02-01", nil))
	if w.Code != 422 || !strings.Contains(w.Body.String(), `role="alert"`) || !strings.Contains(w.Body.String(), `value="2026-03-01"`) {
		t.Fatal("invalid input not preserved", w.Code)
	}
}

func TestChartPartialResponse(t *testing.T) {
	s, err := newServer()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/components/chart?start=2026-02-01&end=2026-02-10&move=prev", nil))
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, `id="chart-content"`) || strings.Count(body, `class="chart-hit"`) != 10 || !strings.Contains(body, `value="2026-01-22"`) {
		t.Fatalf("wrong partial: %d %s", w.Code, body)
	}
	for _, unwanted := range []string{"<!doctype", "<head>", "<script", "<video", "video-file"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("partial includes %q", unwanted)
		}
	}
	w = httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/components/chart?start=2026-03-01&end=2026-02-01", nil))
	if w.Code != 422 || !strings.Contains(w.Body.String(), `"error"`) || strings.Contains(w.Body.String(), "<svg") {
		t.Fatal("invalid partial should return error without replacing chart", w.Code)
	}
}
