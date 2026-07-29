package render

import (
	"bytes"
	"strings"
	"testing"
)

func TestWriteSearchSummaryTruncatesLongDisplayQuery(t *testing.T) {
	t.Setenv("COLUMNS", "80")
	var buf bytes.Buffer
	if err := WriteSearchSummary(&buf, strings.Repeat("q", 200), 0, 0); err != nil {
		t.Fatal(err)
	}
	got := strings.TrimRight(buf.String(), "\n")
	if !strings.Contains(got, "…") {
		t.Fatalf("summary did not truncate query:\n%s", got)
	}
	for lineNo, line := range strings.Split(got, "\n") {
		if width := DisplayWidth(line); width > 80 {
			t.Fatalf("line %d width = %d, want <= 80:\n%s", lineNo+1, width, got)
		}
	}
}

func TestWriteWrappedFieldUsesContinuationIndent(t *testing.T) {
	t.Setenv("COLUMNS", "32")
	var buf bytes.Buffer
	if err := WriteWrappedField(&buf, "Text", "alpha beta gamma delta epsilon zeta"); err != nil {
		t.Fatal(err)
	}
	got := strings.TrimRight(buf.String(), "\n")
	if !strings.Contains(got, "\n      epsilon") {
		t.Fatalf("wrapped field did not align continuation:\n%s", got)
	}
	for lineNo, line := range strings.Split(got, "\n") {
		if width := DisplayWidth(line); width > 32 {
			t.Fatalf("line %d width = %d, want <= 32:\n%s", lineNo+1, width, got)
		}
	}
}
