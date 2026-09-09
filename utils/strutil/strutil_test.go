package strutil

import (
	"reflect"
	"strings"
	"testing"
)

func TestLines(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want []string
	}{
		{"", []string{}}, {"a", []string{"a"}}, {"a\r\nb\rc\n", []string{"a", "b", "c"}},
		{"\n\r\n\r", []string{"", "", ""}}, {"a\n\nb", []string{"a", "", "b"}},
	} {
		if got := Lines(tt.in); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Lines(%q)=%q, want %q", tt.in, got, tt.want)
		}
	}
}
func TestRandom(t *testing.T) {
	for _, n := range []int{0, 1, 1000} {
		s := Random(n)
		if len(s) != n {
			t.Fatalf("length=%d want %d", len(s), n)
		}
		if strings.ContainsFunc(s, func(r rune) bool { return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') }) {
			t.Fatal("invalid alphabet")
		}
	}
	defer func() {
		if recover() == nil {
			t.Error("negative length should panic")
		}
	}()
	Random(-1)
}
