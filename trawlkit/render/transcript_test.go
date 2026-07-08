package render

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestWriteTranscriptAddsDaySeparators(t *testing.T) {
	rows := []TranscriptRow{
		{Time: time.Date(2025, 6, 10, 23, 50, 0, 0, time.UTC), Line: "23:50  Alice  earlier"},
		{Time: time.Date(2025, 6, 11, 0, 5, 0, 0, time.UTC), Line: "00:05  Bob    later\n"},
		{Time: time.Date(2025, 6, 11, 0, 6, 0, 0, time.UTC), Line: "00:06  Alice  done"},
	}
	var buf bytes.Buffer
	if err := WriteTranscript(&buf, rows); err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"— Tue 10 Jun 2025 —",
		"23:50  Alice  earlier",
		"— Wed 11 Jun 2025 —",
		"00:05  Bob    later",
		"00:06  Alice  done",
		"",
	}, "\n")
	if buf.String() != want {
		t.Fatalf("transcript:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func TestWriteTranscriptSkipsSeparatorsForMissingTimes(t *testing.T) {
	rows := []TranscriptRow{
		{Line: "unknown time"},
		{Time: time.Date(2025, 6, 10, 23, 50, 0, 0, time.UTC), Line: "known time"},
	}
	var buf bytes.Buffer
	if err := WriteTranscript(&buf, rows); err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"unknown time",
		"— Tue 10 Jun 2025 —",
		"known time",
		"",
	}, "\n")
	if buf.String() != want {
		t.Fatalf("transcript:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func TestWriteTranscriptWrapsPrefixedRows(t *testing.T) {
	t.Setenv("COLUMNS", "50")
	rows := []TranscriptRow{
		{
			Time:   time.Date(2025, 8, 9, 10, 58, 0, 0, time.UTC),
			Prefix: ">  2025-08-09 10:58  Alice: ",
			Text:   "Signal is nearly the same as WhatsApp except this sentence is deliberately long.",
		},
	}
	var buf bytes.Buffer
	if err := WriteTranscript(&buf, rows); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	for lineNo, line := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
		if width := DisplayWidth(line); width > 50 {
			t.Fatalf("line %d width = %d, want <= 50:\n%s", lineNo+1, width, got)
		}
	}
	if !strings.Contains(got, "\n                              same as") {
		if !hasIndentedTranscriptLine(got, "same as WhatsApp") {
			t.Fatalf("continuation line was not indented under the message text:\n%s", got)
		}
	}
}

func hasIndentedTranscriptLine(got string, text string) bool {
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, text) && strings.HasPrefix(line, " ") && !strings.Contains(line, "2025-") {
			return true
		}
	}
	return false
}
