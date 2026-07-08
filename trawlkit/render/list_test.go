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
			"Open: trawl twitter open REF",
			"More: trawl twitter bookmarks --limit 40",
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
		"Open: trawl twitter open REF",
		"More: trawl twitter bookmarks --limit 40",
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

// TestWriteListClampMarksFullWidthCut is the TRAWL-119 tripwire: when the
// last shown line of a clamped cell exactly fills the text column, the cut
// still ends in the ellipsis marker — a full-width line must never read as a
// complete, un-truncated cell. The text column here is 23 wide, so each
// eleven-char pair fills a line exactly and the second line is clamped at the
// boundary.
func TestWriteListClampMarksFullWidthCut(t *testing.T) {
	t.Setenv("COLUMNS", "28")
	var buf bytes.Buffer
	err := WriteList(&buf, List{
		Heading:   "Search: showing 1.",
		ClampText: 2,
		Items: []ListItem{{
			Ref:  "r1",
			Text: "aaaaaaaaaaa bbbbbbbbbbb ccccccccccc ddddddddddd eeeeeeeeeee",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"Search: showing 1.",
		"",
		"ref  text",
		"r1   aaaaaaaaaaa bbbbbbbbbbb",
		"     ccccccccccc dddddddddd…",
		"",
	}, "\n")
	assertGolden(t, buf.String(), want)
	assertNoTrailingSpaces(t, buf.String())
}

// TestWriteListClampWideRuneTokenSingleMarker: an elided wide-rune (CJK)
// token can already end in the ellipsis and still sit under the column
// budget, because two-cell runes pack unevenly. When that line is also the
// clamp cut, exactly one marker must show — never "……".
func TestWriteListClampWideRuneTokenSingleMarker(t *testing.T) {
	t.Setenv("COLUMNS", "16")
	var buf bytes.Buffer
	err := WriteList(&buf, List{
		Heading:   "Search: showing 1.",
		ClampText: 1,
		Items: []ListItem{{
			Text: "字字字字字字字字字字 tail",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"Search: showing 1.",
		"",
		"text",
		"字字字字字字字…",
		"",
	}, "\n")
	assertGolden(t, buf.String(), want)
}

// TestWriteListDateOnlyRows is the TRAWL-100 tripwire: a row carrying
// a calendar date (an all-day event) renders the date alone — never a
// fake 00:00 — and a list of only dates does not pay for a time column.
func TestWriteListDateOnlyRows(t *testing.T) {
	t.Setenv("COLUMNS", "80")
	var buf bytes.Buffer
	err := WriteList(&buf, List{
		Heading: "Events: showing 2 of 2.",
		Items: []ListItem{
			{
				Time:     time.Date(2026, 3, 16, 0, 0, 0, 0, time.Local),
				DateOnly: true,
				Ref:      "73kbb",
				Text:     "Birthday Michiel",
			},
			{
				Time: time.Date(2026, 3, 14, 14, 0, 0, 0, time.Local),
				Ref:  "5aqby",
				Text: "Birthday party",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"Events: showing 2 of 2.",
		"",
		"date              ref    text",
		"2026-03-16        73kbb  Birthday Michiel",
		"2026-03-14 14:00  5aqby  Birthday party",
		"",
	}, "\n")
	assertGolden(t, buf.String(), want)

	buf.Reset()
	err = WriteList(&buf, List{
		Heading: "Events: showing 1 of 1.",
		Items: []ListItem{{
			Time:     time.Date(2026, 3, 16, 0, 0, 0, 0, time.Local),
			DateOnly: true,
			Ref:      "73kbb",
			Text:     "Birthday Michiel",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want = strings.Join([]string{
		"Events: showing 1 of 1.",
		"",
		"date        ref    text",
		"2026-03-16  73kbb  Birthday Michiel",
		"",
	}, "\n")
	assertGolden(t, buf.String(), want)
}

// TestWriteListSourceColumn is the TRAWL-102 tripwire: a row can name
// its source (the federated trawl view) and the column sits between
// date and who. Rows without a source stay source-free lists.
func TestWriteListSourceColumn(t *testing.T) {
	t.Setenv("COLUMNS", "80")
	var buf bytes.Buffer
	err := WriteList(&buf, List{
		Heading: "Search \"boat\": showing 2 of 2, newest first.",
		Items: []ListItem{
			{
				Time:   time.Date(2026, 5, 14, 9, 12, 0, 0, time.Local),
				Source: "Messages",
				Who:    "Alice",
				Ref:    "t7k3f",
				Text:   "the boat trip is on Saturday",
			},
			{
				Time:   time.Date(2026, 5, 12, 10, 0, 0, 0, time.Local),
				Source: "Telegram",
				Who:    "Bob",
				Ref:    "q8n4c",
				Text:   "book the boat before June",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"Search \"boat\": showing 2 of 2, newest first.",
		"",
		"date              source    who    ref    text",
		"2026-05-14 09:12  Messages  Alice  t7k3f  the boat trip is on Saturday",
		"2026-05-12 10:00  Telegram  Bob    q8n4c  book the boat before June",
		"",
	}, "\n")
	assertGolden(t, buf.String(), want)
}

func TestWriteListCalendarColumn(t *testing.T) {
	t.Setenv("COLUMNS", "100")
	var buf bytes.Buffer
	err := WriteList(&buf, List{
		Heading: "Search \"planning\": showing 1 of 1, newest first.",
		Items: []ListItem{{
			Time:     time.Date(2026, 3, 4, 9, 0, 0, 0, time.Local),
			Who:      "Alice",
			Where:    "Room 1",
			Calendar: "Work",
			Ref:      "calendar:event/1",
			Text:     "Planning meeting",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"Search \"planning\": showing 1 of 1, newest first.",
		"",
		"date              who    where   calendar  ref               text",
		"2026-03-04 09:00  Alice  Room 1  Work      calendar:event/1  Planning meeting",
		"",
	}, "\n")
	assertGolden(t, buf.String(), want)
}

// TestWriteListShedsRefsBeforeSqueezingDate is the TRAWL-102 degrade
// tripwire: when the terminal cannot fit every column, refs move to a
// per-row open: line — a truncated ref or timestamp is garbage, so the
// date and ref cells are never clipped.
func TestWriteListShedsRefsBeforeSqueezingDate(t *testing.T) {
	t.Setenv("COLUMNS", "48")
	var buf bytes.Buffer
	err := WriteList(&buf, List{
		Heading: "Search \"boat\": showing 1 of 1, newest first.",
		Items: []ListItem{{
			Time: time.Date(2026, 5, 14, 9, 12, 0, 0, time.Local),
			Who:  "Alexandra Livingston",
			Ref:  "imessage:msg/8842",
			Text: "the boat trip is on Saturday",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"Search \"boat\": showing 1 of 1, newest first.",
		"",
		"date              who  text",
		"2026-05-14 09:12  Al…  the boat trip is on",
		"                       Saturday",
		"  open: imessage:msg/8842",
		"",
	}, "\n")
	assertGolden(t, buf.String(), want)
}

func TestWriteListEmpty(t *testing.T) {
	var buf bytes.Buffer
	err := WriteList(&buf, List{
		Heading: "Bookmarks: showing 0 of 398.",
		Hints:   []string{"Sync: trawl twitter sync"},
		Empty:   "No bookmarks archived yet. Run 'trawl twitter sync'.",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"No bookmarks archived yet. Run 'trawl twitter sync'.",
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
