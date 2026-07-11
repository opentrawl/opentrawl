package notes

import (
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/notes/internal/archive"
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
