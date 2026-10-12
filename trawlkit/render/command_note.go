package render

import (
	"fmt"
	"io"
	"strings"

	note "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/note"
)

func writeNoteListResponse(
	writer io.Writer,
	response *note.NoteListResponse,
	globallyRoutableTrawlLinksByCanonicalRecordReference GloballyRoutableTrawlLinksByCanonicalArchiveRecordReference,
) error {
	if response == nil {
		return fmt.Errorf("note list response is missing")
	}
	if len(response.GetNoteRecordsNewestFirst()) == 0 {
		return writeLine(writer, "No notes match.")
	}
	rows := make([][]string, 0, len(response.GetNoteRecordsNewestFirst()))
	for _, noteRecord := range response.GetNoteRecordsNewestFirst() {
		if noteRecord == nil {
			continue
		}
		rows = append(rows, []string{
			formatExactTimeInThisComputerTimeZone(noteRecord.GetNoteModifiedTime(), localTimeWithUTCOffsetForListRow),
			strings.TrimSpace(noteRecord.GetNoteDisplayName()),
			strings.TrimSpace(noteRecord.GetNoteFolderDisplayName()),
			globallyRoutableTrawlLinkText(
				globallyRoutableTrawlLinksByCanonicalRecordReference.
					globallyRoutableTrawlLinkForCanonicalArchiveRecordReference(
						noteRecord.GetCanonicalRecordReference(),
					),
			),
		})
	}
	return writeHumanRecordRowsWithPrimaryContentColumn(writer, []TableColumn{
		archiveEventTimeColumn(columnHeaderWhen),
		{Header: "note", Wrap: true, MaximumWrappedLines: 2},
		{Header: "folder", Wrap: true, MaximumWrappedLines: 2},
		{Header: columnHeaderLink, NeverTruncateCellValues: true},
	}, rows, 1)
}

func writeNoteFolderListResponse(
	writer io.Writer,
	response *note.NoteFolderListResponse,
) error {
	if response == nil {
		return fmt.Errorf("note folder list response is missing")
	}
	if len(response.GetNoteFolderRecordsInDisplayOrder()) == 0 {
		return writeLine(writer, "No note folders found.")
	}
	rows := make([][]string, 0, len(response.GetNoteFolderRecordsInDisplayOrder()))
	for _, folderRecord := range response.GetNoteFolderRecordsInDisplayOrder() {
		if folderRecord == nil {
			continue
		}
		folderDisplayName := strings.TrimSpace(folderRecord.GetNoteFolderDisplayName())
		rows = append(rows, []string{
			folderDisplayName,
			FormatInteger(int64(folderRecord.GetNoteCount())),
			formatExactTimeInThisComputerTimeZone(folderRecord.GetMostRecentNoteModifiedTime(), localTimeWithUTCOffsetForListRow),
			formatTrawlCommandLine(writer, []string{"notes", "notes", folderDisplayName}),
		})
	}
	return writeHumanRecordRowsWithPrimaryContentColumn(writer, []TableColumn{
		{Header: "folder", Wrap: true, MaximumWrappedLines: 2},
		{Header: "notes", AlignRight: true},
		archiveEventTimeColumn(columnHeaderWhen),
		{Header: "list notes", NeverTruncateCellValues: true},
	}, rows, 0)
}

func writeRecoveredNoteVersionListResponse(
	writer io.Writer,
	response *note.RecoveredNoteVersionListResponse,
	globallyRoutableTrawlLinksByCanonicalRecordReference GloballyRoutableTrawlLinksByCanonicalArchiveRecordReference,
) error {
	if response == nil {
		return fmt.Errorf("recovered note version list response is missing")
	}
	if len(response.GetRecoveredNoteVersionRecordsNewestFirst()) == 0 {
		if response.GetRequestedNoteVersionAtOrBeforeTime() != nil {
			return writeLine(
				writer,
				"No recovered version existed at "+
					formatExactTimeInThisComputerTimeZone(response.GetRequestedNoteVersionAtOrBeforeTime(), localTimeWithUTCOffsetAndUTCForOpenedRecord)+".",
			)
		}
		return writeLine(writer, "No recovered versions found.")
	}
	rows := make([][]string, 0, len(response.GetRecoveredNoteVersionRecordsNewestFirst()))
	for _, versionRecord := range response.GetRecoveredNoteVersionRecordsNewestFirst() {
		if versionRecord == nil {
			continue
		}
		rows = append(rows, []string{
			formatExactTimeInThisComputerTimeZone(versionRecord.GetRecoveredNoteVersionTime(), localTimeWithUTCOffsetForListRow),
			recoveredNoteVersionPositionForHumanOutput(
				versionRecord.GetNumberOfMoreRecentRecoveredNoteVersions(),
			),
			globallyRoutableTrawlLinkText(
				globallyRoutableTrawlLinksByCanonicalRecordReference.
					globallyRoutableTrawlLinkForCanonicalArchiveRecordReference(
						versionRecord.GetCanonicalRecordReference(),
					),
			),
		})
	}
	return writeTable(writer, []TableColumn{
		archiveEventTimeColumn(columnHeaderWhen),
		{Header: "version"},
		{Header: columnHeaderLink, NeverTruncateCellValues: true},
	}, rows, -1)
}

func recoveredNoteVersionPositionForHumanOutput(numberOfMoreRecentRecoveredNoteVersions uint64) string {
	if numberOfMoreRecentRecoveredNoteVersions == 0 {
		return "latest"
	}
	return fmt.Sprintf("previous %d", numberOfMoreRecentRecoveredNoteVersions)
}
