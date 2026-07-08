package crawlkit

import (
	"io"

	"github.com/openclaw/crawlkit/control"
	"github.com/openclaw/crawlkit/render"
)

func writeManifestText(w io.Writer, manifest control.Manifest) error {
	fields := []render.CardField{
		{Label: "ID", Value: manifest.ID},
		{Label: "Name", Value: manifest.DisplayName},
		{Label: "Version", Value: manifest.Version},
		{Label: "Commands", Value: render.FormatInteger(int64(len(manifest.Commands)))},
		{Label: "Database", Value: manifest.Paths.DefaultDatabase},
		{Label: "Logs", Value: manifest.Paths.DefaultLogs},
	}
	return render.WriteCard(w, render.Card{Title: "Metadata", Fields: fields, Body: manifest.Description})
}
