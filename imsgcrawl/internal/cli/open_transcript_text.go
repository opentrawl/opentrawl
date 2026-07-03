package cli

import (
	"bytes"
	"io"
	"time"

	"github.com/openclaw/crawlkit/render"
)

func printOpenTranscript(w io.Writer, columns []textColumn, context []openMessageOutput) error {
	header := make([]string, 0, len(columns))
	for _, column := range columns {
		header = append(header, column.header)
	}
	if err := renderTextRow(w, columns, header); err != nil {
		return err
	}
	rows := make([]render.TranscriptRow, 0, len(context))
	for _, item := range context {
		rows = append(rows, render.TranscriptRow{
			Time: parseTranscriptTime(item.Time),
			Line: openTranscriptLine(columns, item),
		})
	}
	return render.WriteTranscript(w, rows)
}

func openTranscriptLine(columns []textColumn, item openMessageOutput) string {
	marker := ""
	if item.Target {
		marker = ">"
	}
	var buf bytes.Buffer
	_ = renderTextRow(&buf, columns, []string{
		marker,
		formatArchiveTime(item.Time),
		item.Who,
		displayMessageText(item.Text, item.HasAttachments),
	})
	return buf.String()
}

func openDateSpan(context []openMessageOutput) string {
	var first time.Time
	var last time.Time
	for _, item := range context {
		t := parseTranscriptTime(item.Time)
		if t.IsZero() {
			continue
		}
		if first.IsZero() {
			first = t
		}
		last = t
	}
	if first.IsZero() {
		return ""
	}
	if sameTranscriptDate(first, last) {
		return first.Format("2 Jan 2006")
	}
	return first.Format("2 Jan 2006") + " to " + last.Format("2 Jan 2006")
}

func parseTranscriptTime(value string) time.Time {
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return t
}

func sameTranscriptDate(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}
