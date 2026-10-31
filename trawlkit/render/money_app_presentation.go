package render

import (
	"bytes"
	"io"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	identity "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/identity"
	open "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	search "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search"
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
		DetailDisplayName:       title,
		DetailDisplayNameAnchor: &identity.RecordAnchorIdentifier{RecordAnchorIdentifier: openrecord.MoneyDetailAnchorID},
		Body:                    &presentation.TrawlerSpecificCommandDetailPresentation_BodyText{BodyText: strings.TrimSpace(body)},
	}}, nil
}

func MoneySearchMatchPresentationForApp(match *search.SearchMatchPresentation) {
	facts := match.GetMoneyMovementSearchMatch()
	if facts == nil {
		return
	}
	amount := FormatMoneyAmount(facts.GetSignedAmountIntoMoneyAccount(), true)
	if facts.GetMoneyMovementIsPending() {
		amount += " pending"
	}
	match.SearchMatchTextFieldsInDisplayOrder = append([]*search.SearchMatchTextField{{SearchMatchTextFieldName: "Amount", SearchMatchTextFragmentsInDisplayOrder: []*search.SearchMatchTextFragment{{SearchMatchTextFragmentContent: amount}}}}, match.GetSearchMatchTextFieldsInDisplayOrder()...)
}
