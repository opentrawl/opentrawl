package render

import (
	"fmt"
	"io"
	"strings"

	calendarrecord "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/calendar"
)

func WriteCalendarListResponse(
	writer io.Writer,
	calendarListResponse *calendarrecord.CalendarListResponse,
	globallyRoutableTrawlLinksByCanonicalRecordReference GloballyRoutableTrawlLinksByCanonicalArchiveRecordReference,
) error {
	if calendarListResponse == nil {
		return nil
	}
	if len(calendarListResponse.GetCalendarRecordsInDisplayOrder()) == 0 {
		_, err := io.WriteString(writer, "No calendars found.\n")
		return err
	}
	showOwnerOrPurposeDescription := false
	allRows := make([][]string, 0, len(calendarListResponse.GetCalendarRecordsInDisplayOrder()))
	for _, calendarRecord := range calendarListResponse.GetCalendarRecordsInDisplayOrder() {
		if calendarRecord == nil {
			continue
		}
		ownerOrPurposeDescription := strings.TrimSpace(
			calendarOwnerOrPurposeDescription(calendarRecord.GetCalendarOwnerOrPurposeAnnotation()),
		)
		showOwnerOrPurposeDescription = showOwnerOrPurposeDescription || ownerOrPurposeDescription != ""
		allRows = append(allRows, []string{
			strings.TrimSpace(calendarRecord.GetCalendarDisplayName()),
			strings.TrimSpace(calendarRecord.GetCalendarAccountDisplayName()),
			ownerOrPurposeDescription,
			FormatInteger(int64(calendarRecord.GetActiveOrFutureCalendarEventCount())),
			globallyRoutableTrawlLinkText(
				globallyRoutableTrawlLinksByCanonicalRecordReference.
					globallyRoutableTrawlLinkForCanonicalArchiveRecordReference(
						calendarRecord.GetCanonicalRecordReference(),
					),
			),
		})
	}
	columns := []TableColumn{
		{Header: "calendar", Wrap: true, MaximumWrappedLines: 2},
		{Header: "account", Wrap: true, MaximumWrappedLines: 2},
	}
	if showOwnerOrPurposeDescription {
		columns = append(columns, TableColumn{
			Header:              "owner or purpose",
			MinimumWidth:        8,
			Wrap:                true,
			MaximumWrappedLines: 2,
		})
	}
	columns = append(columns,
		TableColumn{Header: "events", AlignRight: true},
		TableColumn{Header: "link", NeverTruncateCellValues: true},
	)
	rows := make([][]string, 0, len(allRows))
	for _, allRow := range allRows {
		row := []string{allRow[0], allRow[1]}
		if showOwnerOrPurposeDescription {
			row = append(row, allRow[2])
		}
		row = append(row, allRow[3], allRow[4])
		rows = append(rows, row)
	}
	if err := writeHumanRecordRowsWithPrimaryContentColumn(writer, columns, rows, 0); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(writer); err != nil {
		return err
	}
	return WriteTrawlCommandHint(
		writer,
		"Events: "+trawlCommandLineForDisplay(writer, []string{"calendar", "events", "LINK"}),
	)
}

func calendarOwnerOrPurposeDescription(
	annotation *calendarrecord.CalendarOwnerOrPurposeAnnotation,
) string {
	if annotation == nil {
		return ""
	}
	return strings.TrimSpace(annotation.GetCalendarOwnerOrPurposeDescription())
}
