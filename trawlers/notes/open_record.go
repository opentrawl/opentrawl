package notes

import (
	"context"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/notes/internal/archive"
	notesv1 "github.com/opentrawl/opentrawl/trawlers/notes/proto/trawl/notes/v1"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	"github.com/opentrawl/opentrawl/trawlkit/presentation"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type openedNoteValuesLoadedFromNotesArchive struct {
	canonicalOpenedNoteRecordReference string
	archivedNote                       archive.Note
	openedNoteVersionBody              archive.VersionBody
}

var _ trawlkit.RecordOpener = (*Crawler)(nil)

func (c *Crawler) OpenRecord(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	ref string,
) (*openv1.OpenRecord, error) {
	openedNoteValues, err := c.loadOpenNote(ctx, req, ref)
	if err != nil {
		return nil, err
	}
	if err := validateOpenTimestamps(openedNoteValues); err != nil {
		return nil, err
	}
	openedNoteRecord, canonicalOpenedRecordReference := projectOpenedNoteRecord(openedNoteValues)
	typedOpenedNoteRecord, err := anypb.New(openedNoteRecord)
	if err != nil {
		return nil, err
	}
	record := &openv1.OpenRecord{
		RegisteredTrawlerManifestIdentity: c.RegisteredTrawlerDeclaration().RegisteredTrawlerManifestIdentity,
		CanonicalOpenedRecordReference:    canonicalOpenedRecordReference,
		TypedOpenedRecord: &openv1.OpenRecord_TrawlerSpecificOpenedRecord{
			TrawlerSpecificOpenedRecord: &openv1.TrawlerSpecificOpenedRecord{
				TypedTrawlerSpecificOpenedRecord: typedOpenedNoteRecord,
				TrawlerSpecificOpenedRecordDetailPresentation: projectOpenedNoteDetailPresentation(
					openedNoteRecord,
				),
			},
		},
	}
	if err := openrecord.Validate(record); err != nil {
		return nil, err
	}
	return record, nil
}

func validateOpenTimestamps(
	openedNoteValues openedNoteValuesLoadedFromNotesArchive,
) error {
	return presentation.ValidateTimestamps(
		openedNoteValues.archivedNote.CreatedAt,
		openedNoteValues.archivedNote.ModifiedAt,
		openedNoteValues.openedNoteVersionBody.SourceModifiedAt,
	)
}

func projectOpenedNoteRecord(
	openedNoteValues openedNoteValuesLoadedFromNotesArchive,
) (*notesv1.OpenedNoteRecord, string) {
	canonicalNoteRecordReference := archive.RefForNote(openedNoteValues.archivedNote.ID)
	canonicalOpenedRecordReference := canonicalNoteRecordReference
	openedRecoveredVersion := false
	if _, _, openedRecoveredVersion = archive.VersionFromRef(
		openedNoteValues.canonicalOpenedNoteRecordReference,
	); openedRecoveredVersion {
		canonicalOpenedRecordReference = openedNoteValues.openedNoteVersionBody.Ref
	}
	noteName := strings.TrimSpace(openedNoteValues.openedNoteVersionBody.Title)
	if noteName == "" {
		noteName = strings.TrimSpace(openedNoteValues.archivedNote.Title)
	}
	noteModifiedTime := openedNoteValues.archivedNote.ModifiedAt
	if openedRecoveredVersion {
		noteModifiedTime = openedNoteValues.openedNoteVersionBody.SourceModifiedAt
	}
	openedNoteBody := &notesv1.OpenedNoteBody{}
	if openedNoteValues.openedNoteVersionBody.TextStatus == "decoded" {
		openedNoteBody.BodyAvailability = &notesv1.OpenedNoteBody_AvailableNoteBodyText{
			AvailableNoteBodyText: noteBodyWithoutSeparatelyDisplayedTitle(
				noteName,
				openedNoteValues.openedNoteVersionBody.Text,
			),
		}
	} else {
		unavailableNoteBodyExplanation := strings.TrimSpace(
			openedNoteValues.openedNoteVersionBody.Unsupported,
		)
		if unavailableNoteBodyExplanation == "" {
			unavailableNoteBodyExplanation = "Note text is unavailable."
		}
		openedNoteBody.BodyAvailability = &notesv1.OpenedNoteBody_UnavailableNoteBodyExplanation{
			UnavailableNoteBodyExplanation: unavailableNoteBodyExplanation,
		}
	}
	record := &notesv1.OpenedNoteRecord{
		CanonicalNoteRecordReference:              canonicalNoteRecordReference,
		CanonicalOpenedNoteVersionRecordReference: openedNoteValues.openedNoteVersionBody.Ref,
		NoteName:                  noteName,
		NoteFolderName:            strings.TrimSpace(openedNoteValues.archivedNote.Folder),
		RecoveredNoteVersionCount: uint64(max(openedNoteValues.archivedNote.VersionCount, 0)),
		OpenedNoteBody:            openedNoteBody,
	}
	record.NoteCreatedTime = parsedNotesTimestamp(openedNoteValues.archivedNote.CreatedAt)
	record.NoteModifiedTime = parsedNotesTimestamp(noteModifiedTime)
	record.OpenedNoteVersionTime = parsedNotesTimestamp(
		openedNoteValues.openedNoteVersionBody.SourceModifiedAt,
	)
	return record, canonicalOpenedRecordReference
}

func projectOpenedNoteDetailPresentation(
	record *notesv1.OpenedNoteRecord,
) *presentationv1.TrawlerSpecificCommandDetailPresentation {
	detailDisplayName := strings.TrimSpace(record.GetNoteName())
	if detailDisplayName == "" {
		detailDisplayName = "Note"
	}
	fields := make([]*presentationv1.TrawlerSpecificCommandDetailPresentationField, 0, 4)
	if folderName := strings.TrimSpace(record.GetNoteFolderName()); folderName != "" {
		fields = append(fields, notesDetailTextField("Folder", folderName))
	}
	if record.GetNoteCreatedTime() != nil {
		fields = append(fields, notesDetailTimestampField("Created", record.GetNoteCreatedTime()))
	}
	if record.GetNoteModifiedTime() != nil {
		fields = append(fields, notesDetailTimestampField("Modified", record.GetNoteModifiedTime()))
	}
	fields = append(fields, notesDetailUnsignedCountField(
		"Versions",
		int64(record.GetRecoveredNoteVersionCount()),
	))
	detail := &presentationv1.TrawlerSpecificCommandDetailPresentation{
		DetailDisplayName:                      detailDisplayName,
		DetailDisplayNameFixedAnchorIdentifier: fixedAnchorIdentifierPointer("title"),
		FieldsInDisplayOrder:                   fields,
		BodyFixedAnchorIdentifier:              fixedAnchorIdentifierPointer("body"),
	}
	switch body := record.GetOpenedNoteBody().GetBodyAvailability().(type) {
	case *notesv1.OpenedNoteBody_AvailableNoteBodyText:
		detail.Body = &presentationv1.TrawlerSpecificCommandDetailPresentation_BodyText{
			BodyText: body.AvailableNoteBodyText,
		}
	case *notesv1.OpenedNoteBody_UnavailableNoteBodyExplanation:
		detail.Body = &presentationv1.TrawlerSpecificCommandDetailPresentation_BodyUnavailableExplanation{
			BodyUnavailableExplanation: body.UnavailableNoteBodyExplanation,
		}
	}
	return detail
}

func notesDetailTimestampField(
	fieldDisplayName string,
	exactTime *timestamppb.Timestamp,
) *presentationv1.TrawlerSpecificCommandDetailPresentationField {
	return &presentationv1.TrawlerSpecificCommandDetailPresentationField{
		FieldDisplayName: fieldDisplayName,
		FieldValue: &presentationv1.TrawlerSpecificCommandPresentationValue{
			TypedValue: &presentationv1.TrawlerSpecificCommandPresentationValue_ArchiveRecordAssociatedTimeForDisplay{
				ArchiveRecordAssociatedTimeForDisplay: &presentationv1.ArchiveRecordAssociatedTimeForDisplay{
					ArchiveRecordAssociatedTime: &presentationv1.ArchiveRecordAssociatedTimeForDisplay_ExactTime{
						ExactTime: exactTime,
					},
				},
			},
		},
	}
}

func parsedNotesTimestamp(value string) *timestamppb.Timestamp {
	parsedTime := parseNotesArchiveTimeForPresentation(value)
	if parsedTime.IsZero() {
		return nil
	}
	return timestamppb.New(parsedTime)
}

func fixedAnchorIdentifierPointer(fixedAnchorIdentifier string) *string {
	return &fixedAnchorIdentifier
}

// noteBodyWithoutSeparatelyDisplayedTitle removes only Apple's repeated title
// line. A different first line is note content and must remain unchanged.
func noteBodyWithoutSeparatelyDisplayedTitle(title, body string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return body
	}
	firstLine, remainingBody, hasMoreLines := strings.Cut(body, "\n")
	if strings.TrimSpace(firstLine) != title {
		return body
	}
	if !hasMoreLines {
		return ""
	}
	blankSeparator, bodyAfterBlankSeparator, hasBodyAfterBlankSeparator := strings.Cut(remainingBody, "\n")
	if strings.TrimSpace(blankSeparator) != "" {
		return remainingBody
	}
	if !hasBodyAfterBlankSeparator {
		return ""
	}
	return bodyAfterBlankSeparator
}
