package render

import (
	"fmt"
	"io"
	"strings"

	note "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/note"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func WriteNoteListResponse(
	writer io.Writer,
	response *note.NoteListResponse,
	globallyRoutableTrawlLinksByCanonicalRecordReference GloballyRoutableTrawlLinksByCanonicalArchiveRecordReference,
) error {
	if response == nil {
		return fmt.Errorf("note list response is missing")
	}
	if len(response.GetNoteRecordsNewestFirst()) == 0 {
		_, err := fmt.Fprintln(writer, "No notes match.")
		return err
	}
	rows := make([][]string, 0, len(response.GetNoteRecordsNewestFirst()))
	for _, noteRecord := range response.GetNoteRecordsNewestFirst() {
		if noteRecord == nil {
			continue
		}
		rows = append(rows, []string{
			exactTimestampForHumanOutput(noteRecord.GetNoteModifiedTime()),
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
	return WriteTable(writer, []TableColumn{
		{Header: "modified", MinimumWidth: 16},
		{Header: "note", Wrap: true, MaximumWrappedLines: 2},
		{Header: "folder", Wrap: true, MaximumWrappedLines: 2},
		{Header: "link", NeverTruncateCellValues: true},
	}, rows)
}

func WriteNoteFolderListResponse(
	writer io.Writer,
	response *note.NoteFolderListResponse,
) error {
	if response == nil {
		return fmt.Errorf("note folder list response is missing")
	}
	if len(response.GetNoteFolderRecordsInDisplayOrder()) == 0 {
		_, err := fmt.Fprintln(writer, "No note folders found.")
		return err
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
			exactTimestampForHumanOutput(folderRecord.GetMostRecentNoteModifiedTime()),
			trawlCommandLineForDisplay(writer, []string{"notes", "notes", folderDisplayName}),
		})
	}
	return WriteTable(writer, []TableColumn{
		{Header: "folder", Wrap: true, MaximumWrappedLines: 2},
		{Header: "notes", AlignRight: true},
		{Header: "last modified", MinimumWidth: 16},
		{Header: "list notes", NeverTruncateCellValues: true, CellValueIsTrawlCommandAction: true},
	}, rows)
}

func WriteRecoveredNoteVersionListResponse(
	writer io.Writer,
	response *note.RecoveredNoteVersionListResponse,
	globallyRoutableTrawlLinksByCanonicalRecordReference GloballyRoutableTrawlLinksByCanonicalArchiveRecordReference,
) error {
	if response == nil {
		return fmt.Errorf("recovered note version list response is missing")
	}
	if len(response.GetRecoveredNoteVersionRecordsNewestFirst()) == 0 {
		if response.GetRequestedNoteVersionAtOrBeforeTime() != nil {
			_, err := fmt.Fprintf(
				writer,
				"No recovered version existed at %s.\n",
				exactTimestampForHumanOutput(response.GetRequestedNoteVersionAtOrBeforeTime()),
			)
			return err
		}
		_, err := fmt.Fprintln(writer, "No recovered versions found.")
		return err
	}
	rows := make([][]string, 0, len(response.GetRecoveredNoteVersionRecordsNewestFirst()))
	for _, versionRecord := range response.GetRecoveredNoteVersionRecordsNewestFirst() {
		if versionRecord == nil {
			continue
		}
		rows = append(rows, []string{
			exactTimestampForHumanOutput(versionRecord.GetRecoveredNoteVersionTime()),
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
	return WriteTable(writer, []TableColumn{
		{Header: "when", MinimumWidth: 16},
		{Header: "version"},
		{Header: "link", NeverTruncateCellValues: true},
	}, rows)
}

func recoveredNoteVersionPositionForHumanOutput(numberOfMoreRecentRecoveredNoteVersions uint64) string {
	if numberOfMoreRecentRecoveredNoteVersions == 0 {
		return "latest"
	}
	return fmt.Sprintf("previous %d", numberOfMoreRecentRecoveredNoteVersions)
}

func exactTimestampForHumanOutput(exactTime *timestamppb.Timestamp) string {
	if exactTime == nil || !exactTime.IsValid() {
		return ""
	}
	return ShortLocalTime(exactTime.AsTime())
}
