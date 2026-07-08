package crawlkit

import (
	"io"

	"github.com/openclaw/crawlkit/render"
)

func writeSyncReportText(w io.Writer, report *SyncReport) error {
	if report == nil {
		return render.WriteCard(w, render.Card{Title: "Sync complete"})
	}
	fields := []render.CardField{
		{Label: "Added", Value: render.FormatInteger(report.Added)},
		{Label: "Updated", Value: render.FormatInteger(report.Updated)},
		{Label: "Removed", Value: render.FormatInteger(report.Removed)},
	}
	return render.WriteCard(w, render.Card{Title: "Sync complete", Fields: fields, Hints: report.Warnings})
}
