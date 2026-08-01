package notes

import (
	"context"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/notes/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	"github.com/opentrawl/opentrawl/trawlkit/presentation"
	notes "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/note"
	open "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open"
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
)

var _ trawlkit.RecordOpener = (*Crawler)(nil)

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
		TypedOpenedRecord: &open.OpenRecord_OpenedNoteRecord{
			OpenedNoteRecord: openedNoteRecord,
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
		NoteDisplayName:                           noteName,
		NoteFolderDisplayName:                     strings.TrimSpace(openedNoteValues.archivedNote.Folder),
		RecoveredNoteVersionCount:                 uint64(max(openedNoteValues.archivedNote.VersionCount, 0)),
		OpenedNoteBody:                            openedNoteBody,
		SpecificRecoveredNoteVersionWasOpened:     openedRecoveredVersion,
		NoteDisplayNameAnchor:                     trawlkit.NewRecordAnchorIdentifier("title"),
		OpenedNoteBodyAnchor:                      trawlkit.NewRecordAnchorIdentifier("body"),
	}
	record.NoteCreatedTime = parsedNotesTimestamp(openedNoteValues.archivedNote.CreatedAt)
	record.NoteModifiedTime = parsedNotesTimestamp(openedNoteValues.archivedNote.ModifiedAt)
	record.OpenedNoteVersionTime = parsedNotesTimestamp(
		openedNoteVersionTimeForPresentation(openedNoteValues.openedNoteVersionBody),
	)
	return record, canonicalOpenedRecordReference
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
