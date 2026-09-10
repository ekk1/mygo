package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestFormRoundTrip(t *testing.T) {
	s, err := newServer()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, tc := range []struct {
		title  string
		status int
		want   string
	}{
		{"", 422, "请输入"}, {strings.Repeat("a", 81), 422, "80"}, {`<script>alert(1)</script>`, 303, ""},
	} {
		r := httptest.NewRequest("POST", "/preview", strings.NewReader(url.Values{"title": {tc.title}, "note": {"hello"}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), tc.want) {
			t.Fatal(w.Body.String())
		}
		if w.Code == 303 {
			get := httptest.NewRecorder()
			s.ServeHTTP(get, httptest.NewRequest("GET", w.Header().Get("Location"), nil))
			if get.Code != 200 || !strings.Contains(get.Body.String(), "&lt;script&gt;") || strings.Contains(get.Body.String(), "<script>alert") {
				t.Fatal(get.Body.String())
			}
		}
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/missing", nil))
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
	r := httptest.NewRequest("POST", "/preview", strings.NewReader("title="+strings.Repeat("a", 20000)))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatal(w.Code)
	}
}
