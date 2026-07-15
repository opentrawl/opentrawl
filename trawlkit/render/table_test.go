package render

import (
	"bytes"
	"strings"
	"testing"
)

func TestWriteTableCountsAndWrapping(t *testing.T) {
	t.Setenv("COLUMNS", "54")
	var buf bytes.Buffer
	err := WriteTable(&buf, []TableColumn{
		{Header: "Name"},
		{Header: "Count", AlignRight: true},
		{Header: "Summary", Wrap: true},
	}, [][]string{
		{"Inbox", "12", "New receipts and booking notes need review before archive."},
		{"Trips", "3", ""},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"name   count  summary",
		"Inbox     12  New receipts and booking notes need",
		"              review before archive.",
		"Trips      3  (empty)",
		"",
	}, "\n")
	assertGolden(t, buf.String(), want)
	assertNoTrailingSpaces(t, buf.String())
}

func TestWriteTableFitsNarrowTerminal(t *testing.T) {
	t.Setenv("COLUMNS", "32")
	var buf bytes.Buffer
	err := WriteTable(&buf, []TableColumn{
		{Header: "Source"},
		{Header: "Count", AlignRight: true},
		{Header: "Summary", Wrap: true},
	}, [][]string{
		{"long-source-name", "1200", "Alpha beta gamma delta epsilon"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"source       count  summary",
		"long-sourc…   1200  Alpha beta",
		"                    gamma delta",
		"                    epsilon",
		"",
	}, "\n")
	assertGolden(t, buf.String(), want)
	for lineNo, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if width := DisplayWidth(line); width > 32 {
			t.Fatalf("line %d width = %d, want <= 32:\n%s", lineNo+1, width, buf.String())
		}
	}
}

// TestWriteTableElidesLongTokens protects long-token rendering: a wrapped
// column too narrow for a long unbreakable token (an email, a URL) elides
// the token on one line instead of hard-splitting it mid-word across
// several. A reader sees one cut identifier, not scattered fragments.
func TestWriteTableElidesLongTokens(t *testing.T) {
	t.Setenv("COLUMNS", "20")
	var buf bytes.Buffer
	err := WriteTable(&buf, []TableColumn{
		{Header: "who"},
		{Header: "identifiers", Wrap: true},
	}, [][]string{
		{"Ada", "verylongusername@example-domain.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"who  identifiers",
		"Ada  verylonguserna…",
		"",
	}, "\n")
	assertGolden(t, buf.String(), want)
	assertNoTrailingSpaces(t, buf.String())
	for lineNo, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if width := DisplayWidth(line); width > 20 {
			t.Fatalf("line %d width = %d, want <= 20:\n%s", lineNo+1, width, buf.String())
		}
	}
}

func TestWriteTableZeroRowsWritesNothing(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteTable(&buf, []TableColumn{{Header: "Name"}}, nil); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "" {
		t.Fatalf("zero-row table wrote %q, want empty", got)
	}
}
