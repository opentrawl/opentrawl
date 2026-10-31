package render

import (
	"bytes"
	"io"
	"strings"

	open "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
)

// MoneyOpenedRecordPresentation uses the shared Money detail renderer for app
// clients, including the source's balance dates and read times.
func MoneyOpenedRecordPresentation(response *open.OpenResponse) (*open.TrawlerSpecificOpenedRecordPresentation, error) {
	var rendered bytes.Buffer
	output := NewTrawlCommandOutput(&rendered, io.Discard, "trawl")
	if err := output.WriteOpenedRecord(response, OpenResponseRenderContext{}); err != nil {
		return nil, err
	}
	title, body, _ := strings.Cut(strings.TrimSpace(rendered.String()), "\n")
	return &open.TrawlerSpecificOpenedRecordPresentation{DetailPresentation: &presentation.TrawlerSpecificCommandDetailPresentation{
		DetailDisplayName: title,
		Body:              &presentation.TrawlerSpecificCommandDetailPresentation_BodyText{BodyText: strings.TrimSpace(body)},
	}}, nil
}
