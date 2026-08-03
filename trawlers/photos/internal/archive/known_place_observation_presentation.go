package archive

import (
	"bytes"
	_ "embed"
	"strings"
	"text/template"
)

const knownPlaceObservationType = "known_place"

//go:embed known_place_card_line.txt.tmpl
var knownPlaceCardLineTemplateText string

var knownPlaceCardLineTemplate = template.Must(template.New("known-place-card-line").Parse(knownPlaceCardLineTemplateText))

type knownPlaceCardLineTemplateData struct {
	Label                               string
	Name                                string
	CaptureTimeWasAfterConfiguredPeriod bool
}

func KnownPlaceCardLine(kind, name string, after bool) string {
	var label string
	switch kind {
	case "home":
		label = "Near saved home"
	case "former_home":
		label = "Near former home"
	case "work":
		label = "Near saved workplace"
	default:
		return ""
	}
	var rendered bytes.Buffer
	if err := knownPlaceCardLineTemplate.Execute(&rendered, knownPlaceCardLineTemplateData{
		Label: label, Name: strings.TrimSpace(name), CaptureTimeWasAfterConfiguredPeriod: after,
	}); err != nil {
		return ""
	}
	return strings.TrimSpace(rendered.String())
}
