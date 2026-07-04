package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/openclaw/crawlkit/render"
)

func TestTextTableWidthUsesSharedOutputWidth(t *testing.T) {
	t.Setenv("COLUMNS", "180")
	var out bytes.Buffer
	if got := normalizeTextTableWidth(render.OutputWidth(&out)); got != 180 {
		t.Fatalf("text table width = %d, want 180", got)
	}
}

func TestRenderTextTableDoesNotPadFinalColumn(t *testing.T) {
	var out bytes.Buffer
	err := renderTextTable(&out, []textColumn{
		{header: "left", width: 8},
		{header: "text", width: 20, wrap: true},
	}, [][]string{{"x", "short"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n") {
		if strings.HasSuffix(line, " ") {
			t.Fatalf("line has trailing padding: %q", line)
		}
	}
}

func TestOpenMarkerColumnKeepsBlankCellsBlank(t *testing.T) {
	var out bytes.Buffer
	err := renderTextTable(&out, openTextColumns(88), [][]string{
		{"", "2025-06-10 23:50", "Alice Example", "earlier note"},
		{">", "2025-06-11 09:00", "me", "target reply"},
	})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) < 3 {
		t.Fatalf("open table lines = %#v", lines)
	}
	if strings.HasPrefix(lines[0], "-") || strings.HasPrefix(lines[1], "-") {
		t.Fatalf("blank marker column rendered as data:\n%s", out.String())
	}
	if !strings.HasPrefix(lines[2], ">") {
		t.Fatalf("target marker missing:\n%s", out.String())
	}
}

func TestSearchColumnsWrapContextWithoutEllipsis(t *testing.T) {
	var out bytes.Buffer
	err := renderTextTable(&out, searchTextColumns(88), [][]string{{
		"2026-06-07 09:10",
		"person2@example.com",
		"q7cux3",
		"group with person2@example.com, person1@example.com",
		"And banana",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "...") {
		t.Fatalf("search table truncated context:\n%s", out.String())
	}
}

func TestMessageColumnsWrapSenderWithoutServiceOrEllipsis(t *testing.T) {
	var out bytes.Buffer
	err := renderTextTable(&out, messageTextColumns(88), [][]string{{
		"2026-06-07 09:10",
		"person1@example.com",
		"Sure. This message should keep sender identity visible.",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "...") {
		t.Fatalf("message table truncated sender:\n%s", out.String())
	}
	if strings.Contains(out.String(), "service") || strings.Contains(out.String(), "iMessage") {
		t.Fatalf("message table kept service column:\n%s", out.String())
	}
}
