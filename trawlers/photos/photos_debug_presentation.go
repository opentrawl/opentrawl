package photos

import (
	"bytes"
	"embed"
	"strings"
	"text/template"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/updatephotos"
)

//go:embed photos_debug_text.tmpl
var photosDebugTextTemplateFile embed.FS

var photosDebugTextTemplate = template.Must(template.New("photos-debug-text").Funcs(template.FuncMap{
	"join": strings.Join,
}).ParseFS(photosDebugTextTemplateFile, "photos_debug_text.tmpl"))

type photosDebugNodeCommandText struct {
	Command string
	Node    updatephotos.ProductionNodeName
}

type photosDebugNodeDetailsText struct {
	Node          updatephotos.ProductionNodeName
	Dependencies  []string
	RequiresPhoto bool
}

func renderPhotosDebugText(templateName string, data any) (string, error) {
	var rendered bytes.Buffer
	if err := photosDebugTextTemplate.ExecuteTemplate(&rendered, templateName, data); err != nil {
		return "", err
	}
	return strings.TrimSpace(rendered.String()), nil
}
