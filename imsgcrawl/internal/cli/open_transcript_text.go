package cli

import (
	"fmt"
	"io"
	"time"

	"github.com/openclaw/crawlkit/render"
)

const (
	openTranscriptMinWhoWidth = 8
	openTranscriptMaxWhoWidth = 32
)

func printOpenTranscript(w io.Writer, context []openMessageOutput) error {
	width := render.OutputWidth(w)
	rows := make([]render.TranscriptRow, 0, len(context))
	for _, item := range context {
		rows = append(rows, render.TranscriptRow{
			Time:   parseArchiveTime(item.Time),
			Prefix: openTranscriptPrefix(width, item),
			Text:   displayMessageText(item.Text, item.HasAttachments),
		})
	}
	return render.WriteTranscript(w, rows)
}

func openTranscriptPrefix(width int, item openMessageOutput) string {
	marker := " "
	if item.Target {
		marker = ">"
	}
	when := "--:--"
	if t := parseArchiveTime(item.Time); !t.IsZero() {
		when = t.Local().Format("15:04")
	}
	fixed := fmt.Sprintf("%s %s  ", marker, when)
	whoWidth := width - render.DisplayWidth(fixed) - render.DisplayWidth(": ") - 1
	if whoWidth < openTranscriptMinWhoWidth {
		whoWidth = openTranscriptMinWhoWidth
	}
	if whoWidth > openTranscriptMaxWhoWidth {
		whoWidth = openTranscriptMaxWhoWidth
	}
	return fixed + render.Truncate(item.Who, whoWidth) + ": "
}

func openDateSpan(context []openMessageOutput) string {
	var first time.Time
	var last time.Time
	for _, item := range context {
		t := parseArchiveTime(item.Time)
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

func sameTranscriptDate(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}
