package render

import (
	"bytes"
	"strings"
	"testing"
)

func TestWriteCardWithBodyAndHints(t *testing.T) {
	t.Setenv("COLUMNS", "44")
	var buf bytes.Buffer
	err := WriteCard(&buf, Card{
		Title: "Planning note",
		Fields: []CardField{
			{Label: "Who", Value: "Ada Lovelace"},
			{Label: "Notes", Value: "Alpha beta gamma delta epsilon zeta eta"},
			{Label: "Empty", Value: ""},
		},
		Body: "Body text wraps cleanly across the card body for detail views.",
		Hints: []string{
			"Search: trawl examplecrawl search QUERY",
			"Open: trawl examplecrawl open c4",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"Planning note",
		"Who: Ada Lovelace",
		"Notes: Alpha beta gamma delta epsilon zeta",
		"       eta",
		"",
		"Body text wraps cleanly across the card body",
		"for detail views.",
		"",
		"Search: trawl examplecrawl search QUERY",
		"Open: trawl examplecrawl open c4",
		"",
	}, "\n")
	assertGolden(t, buf.String(), want)
	assertNoTrailingSpaces(t, buf.String())
}

func TestWriteCardWithoutBodyAndHints(t *testing.T) {
	var buf bytes.Buffer
	err := WriteCard(&buf, Card{
		Title: "Simple card",
		Fields: []CardField{
			{Label: "Ref", Value: "c4"},
			{Label: "Empty", Value: ""},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"Simple card",
		"Ref: c4",
		"",
	}, "\n")
	assertGolden(t, buf.String(), want)
}
