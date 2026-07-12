package notes

import (
	"strconv"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/notes/internal/archive"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	notesopenv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/source/notes/open/v1"
)

func projectOpenRecord(requestedRef string, note archive.Note, body archive.VersionBody) *notesopenv1.NotesRecord {
	recordRef := archive.RefForNote(note.ID)
	if _, _, ok := archive.VersionFromRef(requestedRef); ok {
		recordRef = requestedRef
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

func projectOpenPresentation(requestedRef string, note archive.Note, body archive.VersionBody) *presentationv1.PresentationDocument {
	record := projectOpenRecord(requestedRef, note, body)
	title := strings.TrimSpace(record.Title)
	if title == "" {
		title = "Note"
	}
	fields := []*presentationv1.Field{{Label: "Ref", Display: record.Ref}}
	appendPresentationField(&fields, "Version ref", record.VersionRef)
	appendPresentationField(&fields, "Folder", record.GetFolder())
	appendPresentationField(&fields, "Created", record.GetCreatedAt())
	appendPresentationField(&fields, "Modified", record.GetModifiedAt())
	fields = append(fields, &presentationv1.Field{Label: "Versions", Display: strconv.FormatInt(record.VersionCount, 10)})
	blocks := []*presentationv1.Block{{Content: &presentationv1.Block_Fields{Fields: &presentationv1.FieldGroup{Fields: fields}}}}
	if text := strings.TrimSpace(record.GetText()); text != "" {
		blocks = append(blocks, &presentationv1.Block{Content: &presentationv1.Block_Prose{Prose: &presentationv1.Prose{Text: text}}})
	}
	document := &presentationv1.PresentationDocument{Title: title, Blocks: blocks}
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
