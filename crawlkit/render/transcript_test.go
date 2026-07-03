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
