package render

import (
	"reflect"
	"testing"
)

func TestDisplayWidth(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		want  int
	}{
		{name: "ASCII", value: "plain", want: 5},
		{name: "wide CJK runes", value: "你好", want: 4},
		{name: "emoji", value: "🙂", want: 2},
		{name: "combining marks", value: "e\u0301", want: 1},
		{name: "tabs", value: "a\tb", want: 6},
		{name: "empty input", value: "", want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := DisplayWidth(tc.value); got != tc.want {
				t.Fatalf("DisplayWidth(%q) = %d, want %d", tc.value, got, tc.want)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		width int
		want  string
	}{
		{name: "ASCII", value: "alpha beta", width: 8, want: "alpha b…"},
		{name: "wide CJK runes", value: "你好世界", width: 5, want: "你好…"},
		{name: "emoji", value: "go🙂lang", width: 5, want: "go🙂…"},
		{name: "combining marks", value: "cafe\u0301 au lait", width: 6, want: "cafe\u0301…"},
		{name: "tabs", value: "a\tb", width: 5, want: "a…"},
		{name: "exact width", value: "abcdef", width: 6, want: "abcdef"},
		{name: "width minus one", value: "abcdef", width: 5, want: "abcd…"},
		{name: "mid wide rune", value: "ab界cd", width: 4, want: "ab…"},
		{name: "empty input", value: "", width: 5, want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Truncate(tc.value, tc.width)
			if got != tc.want {
				t.Fatalf("Truncate(%q, %d) = %q, want %q", tc.value, tc.width, got, tc.want)
			}
			if width := DisplayWidth(got); width > tc.width {
				t.Fatalf("Truncate(%q, %d) width = %d, want <= %d", tc.value, tc.width, width, tc.width)
			}
		})
	}
}

func TestWrap(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		width int
		want  []string
	}{
		{name: "ASCII", value: "alpha beta gamma", width: 10, want: []string{"alpha beta", "gamma"}},
		{name: "wide CJK runes", value: "你好世界", width: 4, want: []string{"你好", "世界"}},
		{name: "emoji", value: "go🙂lang", width: 4, want: []string{"go🙂", "lang"}},
		{name: "combining marks", value: "cafe\u0301 au lait", width: 6, want: []string{"cafe\u0301", "au", "lait"}},
		{name: "tabs", value: "a\tb", width: 6, want: []string{"a    b"}},
		{name: "empty input", value: "", width: 10, want: []string{""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Wrap(tc.value, tc.width)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Wrap(%q, %d) = %#v, want %#v", tc.value, tc.width, got, tc.want)
			}
			assertLineWidths(t, got, tc.width)
		})
	}
}

func TestWrapWithIndent(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		width int
		want  []string
	}{
		{name: "ASCII", value: "alpha beta gamma", width: 10, want: []string{"X: alpha", "   beta", "   gamma"}},
		{name: "wide CJK runes", value: "你好世界", width: 7, want: []string{"X: 你好", "   世界"}},
		{name: "emoji", value: "go🙂lang", width: 7, want: []string{"X: go🙂", "   lang"}},
		{name: "combining marks", value: "cafe\u0301 au", width: 8, want: []string{"X: cafe\u0301", "   au"}},
		{name: "tabs", value: "a\tb", width: 9, want: []string{"X: a    b"}},
		{name: "empty input", value: "", width: 10, want: []string{"X:"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := WrapWithIndent("X: ", tc.value, tc.width, "")
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("WrapWithIndent(%q, %q, %d, %q) = %#v, want %#v", "X: ", tc.value, tc.width, "", got, tc.want)
			}
			assertLineWidths(t, got, tc.width)
		})
	}
}

func assertLineWidths(t *testing.T, lines []string, width int) {
	t.Helper()
	for _, line := range lines {
		if got := DisplayWidth(line); got > width {
			t.Fatalf("line %q width = %d, want <= %d", line, got, width)
		}
	}
}
