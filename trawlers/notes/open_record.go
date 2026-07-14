package notes

import (
	"context"
	"strconv"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/notes/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	"github.com/opentrawl/opentrawl/trawlkit/presentation"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	notesopenv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/source/notes/open/v1"
	"google.golang.org/protobuf/types/known/anypb"
)

type openValue struct {
	resolvedRef string
	note        archive.Note
	body        archive.VersionBody
}

var _ trawlkit.RecordOpener = (*Crawler)(nil)

func (c *Crawler) OpenRecord(ctx context.Context, req *trawlkit.Request, ref string) (*openv1.OpenRecord, error) {
	value, err := c.loadOpenNote(ctx, req, ref)
	if err != nil {
		return nil, err
	}
	if err := validateOpenTimestamps(value); err != nil {
		return nil, err
	}
	machine := projectOpenRecord(value)
	data, err := anypb.New(machine)
	if err != nil {
		return nil, err
	}
	record := &openv1.OpenRecord{SourceId: c.Info().ID, OpenRef: machine.GetRef(), Data: data, Presentation: projectOpenPresentation(value)}
	if err := openrecord.Validate(record); err != nil {
		return nil, err
	}
	return record, nil
}

func validateOpenTimestamps(value openValue) error {
	return presentation.ValidateTimestamps(value.note.CreatedAt, value.note.ModifiedAt)
}

func projectOpenRecord(value openValue) *notesopenv1.NotesRecord {
	requestedRef, note, body := value.resolvedRef, value.note, value.body
	recordRef := archive.RefForNote(note.ID)
	if _, _, ok := archive.VersionFromRef(requestedRef); ok {
		recordRef = body.Ref
	}
	title := strings.TrimSpace(body.Title)
	if title == "" {
		title = note.Title
	}
	record := &notesopenv1.NotesRecord{
		Ref:          recordRef,
		VersionRef:   body.Ref,
		Title:        title,
		VersionCount: note.VersionCount,
		TextState:    notesopenv1.TextState_TEXT_STATE_UNAVAILABLE,
	}
	setOptionalString(&record.Folder, note.Folder)
	setOptionalString(&record.CreatedAt, note.CreatedAt)
	setOptionalString(&record.ModifiedAt, note.ModifiedAt)
	setOptionalString(&record.Unsupported, body.Unsupported)
	if body.TextStatus == "decoded" {
		record.TextState = notesopenv1.TextState_TEXT_STATE_DECODED
		record.Text = recordString(body.Text)
	}
	return record
}

func setOptionalString(target **string, value string) {
	if value = strings.TrimSpace(value); value != "" {
		*target = &value
	}
}

func recordString(value string) *string { return &value }

func projectOpenPresentation(value openValue) *presentationv1.PresentationDocument {
	record := projectOpenRecord(value)
	title := strings.TrimSpace(record.Title)
	if title == "" {
		title = "Note"
	}
	fields := make([]*presentationv1.Field, 0, 4)
	appendPresentationField(&fields, "Folder", record.GetFolder())
	appendPresentationField(&fields, "Created", presentation.MustTimestamp(record.GetCreatedAt()))
	appendPresentationField(&fields, "Modified", presentation.MustTimestamp(record.GetModifiedAt()))
	fields = append(fields, &presentationv1.Field{Label: "Versions", Display: strconv.FormatInt(record.VersionCount, 10)})
	blocks := make([]*presentationv1.Block, 0, 3)
	blocks = append(blocks, &presentationv1.Block{AnchorId: "title", Content: &presentationv1.Block_Heading{Heading: &presentationv1.Heading{Text: title}}})
	if len(fields) > 0 {
		blocks = append(blocks, &presentationv1.Block{Content: &presentationv1.Block_Fields{Fields: &presentationv1.FieldGroup{Fields: fields}}})
	}
	if text := strings.TrimSpace(record.GetText()); text != "" {
		blocks = append(blocks, &presentationv1.Block{AnchorId: "body", Content: &presentationv1.Block_Prose{Prose: &presentationv1.Prose{Text: text}}})
	}
	primaryAnchorID := "title"
	if strings.TrimSpace(record.GetText()) != "" {
		primaryAnchorID = "body"
	}
	document := &presentationv1.PresentationDocument{Title: title, Blocks: blocks, PrimaryAnchorId: primaryAnchorID}
	if record.TextState != notesopenv1.TextState_TEXT_STATE_DECODED {
		message := strings.TrimSpace(record.GetUnsupported())
		if message == "" {
			message = "Note text is unavailable."
		}
		document.Facts = append(document.Facts, &presentationv1.Fact{Kind: presentationv1.Fact_KIND_ERROR, Message: message})
	}
	return document
}

func appendPresentationField(fields *[]*presentationv1.Field, label, value string) {
	if value = strings.TrimSpace(value); value != "" {
		*fields = append(*fields, &presentationv1.Field{Label: label, Display: value})
	}
}
