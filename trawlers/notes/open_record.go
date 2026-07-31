package notes

import (
	"context"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/notes/internal/archive"
	notes "github.com/opentrawl/opentrawl/trawlers/notes/proto/trawl/notes"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	"github.com/opentrawl/opentrawl/trawlkit/presentation"
	open "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open"
	presentationcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	"github.com/opentrawl/opentrawl/trawlkit/render"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type openedNoteValuesLoadedFromNotesArchive struct {
	canonicalOpenedNoteRecordReference string
	archivedNote                       archive.Note
	openedNoteVersionBody              archive.VersionBody
}

const (
	maximumDisplayedOpenedNoteBodyUnicodeCodePointCount = 1200
	maximumDisplayedOpenedNoteBodyLineCount             = 40
	recoveredNoteVersionCountAnchorIdentifier           = "recovered-note-version-count"
)

var _ trawlkit.RecordOpener = (*Crawler)(nil)
var _ trawlkit.TrawlerSpecificOpenedRecordPresentationActionBuilder = (*Crawler)(nil)

func (c *Crawler) OpenRecord(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	localShortReference *trawlkit.LocalTrawlerShortReference,
) (*open.OpenRecord, error) {
	openedNoteValues, err := c.loadOpenNote(ctx, req, localShortReference)
	if err != nil {
		return nil, err
	}
	if err := validateOpenTimestamps(openedNoteValues); err != nil {
		return nil, err
	}
	openedNoteRecord, canonicalOpenedRecordReference := projectOpenedNoteRecord(openedNoteValues)
	record := &open.OpenRecord{
		RecordTrawler:            c.RegisteredTrawlerDeclaration().RegisteredTrawler,
		CanonicalRecordReference: trawlkit.NewCanonicalArchiveRecordReference(canonicalOpenedRecordReference),
		TypedOpenedRecord: &open.OpenRecord_TrawlerSpecificOpenedRecordPresentation{
			TrawlerSpecificOpenedRecordPresentation: &open.TrawlerSpecificOpenedRecordPresentation{
				DetailPresentation: projectOpenedNoteDetailPresentation(openedNoteRecord),
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
		openedNoteVersionTimeForPresentation(openedNoteValues.openedNoteVersionBody),
	)
}

func projectOpenedNoteRecord(
	openedNoteValues openedNoteValuesLoadedFromNotesArchive,
) (*notes.OpenedNoteRecord, string) {
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
	openedNoteBody := &notes.OpenedNoteBody{}
	if openedNoteValues.openedNoteVersionBody.TextStatus == "decoded" {
		displayedNoteBodyText, moreNoteBodyTextIsOmitted := openedNoteBodyTextForHumanPresentation(
			noteBodyWithoutSeparatelyDisplayedTitle(
				noteName,
				openedNoteValues.openedNoteVersionBody.Text,
			),
		)
		openedNoteBody.BodyAvailability = &notes.OpenedNoteBody_AvailableOpenedNoteBodyText{
			AvailableOpenedNoteBodyText: &notes.AvailableOpenedNoteBodyText{
				DisplayedNoteBodyText:     displayedNoteBodyText,
				MoreNoteBodyTextIsOmitted: moreNoteBodyTextIsOmitted,
			},
		}
	} else {
		unavailableNoteBodyExplanation := strings.TrimSpace(
			openedNoteValues.openedNoteVersionBody.Unsupported,
		)
		if unavailableNoteBodyExplanation == "" {
			unavailableNoteBodyExplanation = "Note text is unavailable."
		}
		openedNoteBody.BodyAvailability = &notes.OpenedNoteBody_UnavailableNoteBodyExplanation{
			UnavailableNoteBodyExplanation: unavailableNoteBodyExplanation,
		}
	}
	record := &notes.OpenedNoteRecord{
		CanonicalNoteRecordReference:              trawlkit.NewCanonicalArchiveRecordReference(canonicalNoteRecordReference),
		CanonicalOpenedNoteVersionRecordReference: trawlkit.NewCanonicalArchiveRecordReference(openedNoteValues.openedNoteVersionBody.Ref),
		NoteName:                              noteName,
		NoteFolderName:                        strings.TrimSpace(openedNoteValues.archivedNote.Folder),
		RecoveredNoteVersionCount:             uint64(max(openedNoteValues.archivedNote.VersionCount, 0)),
		OpenedNoteBody:                        openedNoteBody,
		SpecificRecoveredNoteVersionWasOpened: openedRecoveredVersion,
	}
	record.NoteCreatedTime = parsedNotesTimestamp(openedNoteValues.archivedNote.CreatedAt)
	record.NoteModifiedTime = parsedNotesTimestamp(openedNoteValues.archivedNote.ModifiedAt)
	record.OpenedNoteVersionTime = parsedNotesTimestamp(
		openedNoteVersionTimeForPresentation(openedNoteValues.openedNoteVersionBody),
	)
	return record, canonicalOpenedRecordReference
}

func projectOpenedNoteDetailPresentation(
	record *notes.OpenedNoteRecord,
) *presentationcontract.TrawlerSpecificCommandDetailPresentation {
	detailDisplayName := strings.TrimSpace(record.GetNoteName())
	if detailDisplayName == "" {
		detailDisplayName = "Note"
	}
	fields := make([]*presentationcontract.TrawlerSpecificCommandDetailPresentationField, 0, 4)
	if folderName := strings.TrimSpace(record.GetNoteFolderName()); folderName != "" {
		fields = append(fields, notesDetailTextField("Folder", folderName))
	}
	if record.GetNoteCreatedTime() != nil {
		fields = append(fields, notesDetailTimestampField("Created", record.GetNoteCreatedTime()))
	}
	if record.GetSpecificRecoveredNoteVersionWasOpened() && record.GetOpenedNoteVersionTime() != nil {
		fields = append(fields, notesDetailTimestampField("Recovered version", record.GetOpenedNoteVersionTime()))
	} else if record.GetNoteModifiedTime() != nil {
		fields = append(fields, notesDetailTimestampField("Modified", record.GetNoteModifiedTime()))
	}
	recoveredNoteVersionCountField := notesDetailUnsignedCountField(
		"Versions",
		int64(record.GetRecoveredNoteVersionCount()),
	)
	recoveredNoteVersionCountField.FieldAnchor = trawlkit.NewRecordAnchorIdentifier(
		recoveredNoteVersionCountAnchorIdentifier,
	)
	fields = append(fields, recoveredNoteVersionCountField)
	detail := &presentationcontract.TrawlerSpecificCommandDetailPresentation{
		DetailDisplayName:       detailDisplayName,
		DetailDisplayNameAnchor: trawlkit.NewRecordAnchorIdentifier("title"),
		FieldsInDisplayOrder:    fields,
		BodyAnchor:              trawlkit.NewRecordAnchorIdentifier("body"),
	}
	switch body := record.GetOpenedNoteBody().GetBodyAvailability().(type) {
	case *notes.OpenedNoteBody_AvailableOpenedNoteBodyText:
		displayedNoteBodyText := body.AvailableOpenedNoteBodyText.GetDisplayedNoteBodyText()
		if body.AvailableOpenedNoteBodyText.GetMoreNoteBodyTextIsOmitted() {
			displayedNoteBodyText = strings.TrimSpace(displayedNoteBodyText) + "\n\nMore note text is omitted."
		}
		detail.Body = &presentationcontract.TrawlerSpecificCommandDetailPresentation_BodyText{
			BodyText: displayedNoteBodyText,
		}
	case *notes.OpenedNoteBody_UnavailableNoteBodyExplanation:
		detail.Body = &presentationcontract.TrawlerSpecificCommandDetailPresentation_BodyUnavailableExplanation{
			BodyUnavailableExplanation: body.UnavailableNoteBodyExplanation,
		}
	}
	return detail
}

func openedNoteBodyTextForHumanPresentation(completeNoteBodyText string) (string, bool) {
	completeNoteBodyUnicodeCodePoints := []rune(completeNoteBodyText)
	displayedNoteBodyUnicodeCodePoints := make(
		[]rune,
		0,
		min(len(completeNoteBodyUnicodeCodePoints), maximumDisplayedOpenedNoteBodyUnicodeCodePointCount),
	)
	displayedLineCount := 1
	for _, unicodeCodePoint := range completeNoteBodyUnicodeCodePoints {
		if len(displayedNoteBodyUnicodeCodePoints) >= maximumDisplayedOpenedNoteBodyUnicodeCodePointCount {
			return string(displayedNoteBodyUnicodeCodePoints), true
		}
		if unicodeCodePoint == '\n' && displayedLineCount >= maximumDisplayedOpenedNoteBodyLineCount {
			return string(displayedNoteBodyUnicodeCodePoints), true
		}
		displayedNoteBodyUnicodeCodePoints = append(displayedNoteBodyUnicodeCodePoints, unicodeCodePoint)
		if unicodeCodePoint == '\n' {
			displayedLineCount++
		}
	}
	return completeNoteBodyText, false
}

func openedNoteVersionTimeForPresentation(openedNoteVersionBody archive.VersionBody) string {
	if sourceModifiedTime := strings.TrimSpace(openedNoteVersionBody.SourceModifiedAt); sourceModifiedTime != "" {
		return sourceModifiedTime
	}
	return strings.TrimSpace(openedNoteVersionBody.FirstObservedAt)
}

func (c *Crawler) BuildTrawlerSpecificOpenedRecordPresentationActions(
	openedRecord *open.OpenRecord,
) (render.TrawlerSpecificCommandActions, error) {
	trawlerSpecificOpenedRecordPresentation := openedRecord.GetTrawlerSpecificOpenedRecordPresentation()
	if trawlerSpecificOpenedRecordPresentation == nil {
		return render.TrawlerSpecificCommandActions{}, nil
	}
	detailPresentation := trawlerSpecificOpenedRecordPresentation.GetDetailPresentation()
	hasRecoveredNoteVersions := false
	for _, field := range detailPresentation.GetFieldsInDisplayOrder() {
		if trawlkit.RecordAnchorIdentifierText(field.GetFieldAnchor()) != recoveredNoteVersionCountAnchorIdentifier {
			continue
		}
		hasRecoveredNoteVersions = field.GetFieldValue().GetUnsignedCount() > 0
		break
	}
	if !hasRecoveredNoteVersions {
		return render.TrawlerSpecificCommandActions{}, nil
	}
	action := &render.TrawlCommandAction{
		TrawlCommandActionDisplayName: "List versions",
		CommandArgumentsAfterTrawlInvocationInOrder: []render.TrawlCommandArgument{
			render.TrawlCommandTextArgument{Text: "notes"},
			render.TrawlCommandTextArgument{Text: "versions"},
			render.TrawlCommandCanonicalArchiveRecordReferenceArgument{
				CanonicalArchiveRecordReference: openedRecord.GetCanonicalRecordReference(),
			},
		},
	}
	return render.TrawlerSpecificCommandActions{
		DetailActionsInDisplayOrder: []*render.TrawlCommandAction{action},
	}, nil
}

func notesDetailTimestampField(
	fieldDisplayName string,
	exactTime *timestamppb.Timestamp,
) *presentationcontract.TrawlerSpecificCommandDetailPresentationField {
	return &presentationcontract.TrawlerSpecificCommandDetailPresentationField{
		FieldDisplayName: fieldDisplayName,
		FieldValue: &presentationcontract.TrawlerSpecificCommandPresentationValue{
			TypedValue: &presentationcontract.TrawlerSpecificCommandPresentationValue_ArchiveRecordAssociatedTimeForDisplay{
				ArchiveRecordAssociatedTimeForDisplay: &presentationcontract.ArchiveRecordAssociatedTimeForDisplay{
					ArchiveRecordAssociatedTime: &presentationcontract.ArchiveRecordAssociatedTimeForDisplay_ExactTime{
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
