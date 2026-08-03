package photos

import (
	"bytes"
	"embed"
	"strings"
	"text/template"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/media/mediawire"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/updatephotos"
	"google.golang.org/protobuf/types/known/timestamppb"
)

//go:embed photos_debug_text.tmpl
var photosDebugTextTemplateFile embed.FS

var photosDebugTextTemplate = template.Must(template.New("photos-debug-text").Funcs(template.FuncMap{
	"join": strings.Join,
	"time": photosDebugTimestamp,
}).ParseFS(photosDebugTextTemplateFile, "photos_debug_text.tmpl"))

type photosMediaFailureText struct {
	Failure *mediawire.PhotosMediaOperationFailure
}

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

func photosDebugTimestamp(value *timestamppb.Timestamp) string {
	if value == nil || !value.IsValid() {
		return "not recorded"
	}
	return value.AsTime().Format(time.RFC3339Nano)
}
