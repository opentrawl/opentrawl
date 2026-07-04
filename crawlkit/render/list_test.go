package render

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestWriteListFull(t *testing.T) {
	t.Setenv("COLUMNS", "80")
	var buf bytes.Buffer
	err := WriteList(&buf, List{
		Heading: "Bookmarks: showing 2 of 398, newest first.",
		Hints: []string{
			"Open: birdcrawl open REF",
			"More: birdcrawl bookmarks --limit 40",
		},
		Items: []ListItem{
			{
				Time:  time.Date(2026, 7, 4, 9, 30, 0, 0, time.Local),
				Who:   "Ada 🙂",
				Where: "lab",
				Ref:   "a1",
				Text:  "First message with emoji.",
			},
			{
				Time:  time.Date(2026, 7, 4, 10, 5, 0, 0, time.Local),
				Who:   "Lin 你好",
				Where: "office",
				Ref:   "b2",
				Text:  "Second message for release notes.",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"Bookmarks: showing 2 of 398, newest first.",
		"Open: birdcrawl open REF",
		"More: birdcrawl bookmarks --limit 40",
		"",
		"date              who       where   ref  text",
		"2026-07-04 09:30  Ada 🙂    lab     a1   First message with emoji.",
		"2026-07-04 10:05  Lin 你好  office  b2   Second message for release notes.",
		"",
	}, "\n")
	assertGolden(t, buf.String(), want)
	assertNoTrailingSpaces(t, buf.String())
}

func TestWriteListOmitsEmptyColumns(t *testing.T) {
	t.Setenv("COLUMNS", "60")
	var buf bytes.Buffer
	err := WriteList(&buf, List{
		Heading: "Notes: showing 1.",
		Items: []ListItem{{
			Text: "One plain record.",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"Notes: showing 1.",
		"",
		"text",
		"One plain record.",
		"",
	}, "\n")
	assertGolden(t, buf.String(), want)
}

func TestWriteListClampsText(t *testing.T) {
	t.Setenv("COLUMNS", "28")
	var buf bytes.Buffer
	err := WriteList(&buf, List{
		Heading:   "Search: showing 1.",
		ClampText: 2,
		Items: []ListItem{{
			Ref:  "r1",
			Text: "alpha beta gamma delta epsilon zeta eta theta iota kappa",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"Search: showing 1.",
		"",
		"ref  text",
		"r1   alpha beta gamma delta",
		"     epsilon zeta eta theta…",
		"",
	}, "\n")
	assertGolden(t, buf.String(), want)
}

func TestWriteListEmpty(t *testing.T) {
	var buf bytes.Buffer
	err := WriteList(&buf, List{
		Heading: "Bookmarks: showing 0 of 398.",
		Hints:   []string{"Sync: birdcrawl sync"},
		Empty:   "No bookmarks archived yet. Run 'birdcrawl sync'.",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"No bookmarks archived yet. Run 'birdcrawl sync'.",
		"",
	}, "\n")
	assertGolden(t, buf.String(), want)
}

func TestShortLocalTime(t *testing.T) {
	if got := ShortLocalTime(time.Time{}); got != "" {
		t.Fatalf("ShortLocalTime(zero) = %q, want empty", got)
	}
	when := time.Date(2026, 7, 4, 9, 30, 0, 0, time.Local)
	if got := ShortLocalTime(when); got != "2026-07-04 09:30" {
		t.Fatalf("ShortLocalTime = %q, want %q", got, "2026-07-04 09:30")
	}
}

func assertGolden(t *testing.T, got string, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}
}

func assertNoTrailingSpaces(t *testing.T, output string) {
	t.Helper()
	for lineNo, line := range strings.Split(strings.TrimRight(output, "\n"), "\n") {
		if strings.HasSuffix(line, " ") {
			t.Fatalf("line %d has trailing spaces: %q", lineNo+1, line)
		}
	}
}
