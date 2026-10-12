package render

import (
	"io"
	"strings"

	calendarevent "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/calendar_event"
)

func writeCalendarEventListResponse(
	writer io.Writer,
	response *calendarevent.CalendarEventListResponse,
	globallyRoutableTrawlLinksByCanonicalRecordReference GloballyRoutableTrawlLinksByCanonicalArchiveRecordReference,
) error {
	if response == nil {
		return nil
	}
	if len(response.GetCalendarEventRecordsInDisplayOrder()) == 0 {
		return writeLine(writer, "No events match.")
	}
	showCalendarOwnerOrPurposeDescription := false
	for _, calendarEventRecord := range response.GetCalendarEventRecordsInDisplayOrder() {
		if calendarOwnerOrPurposeDescription(calendarEventRecord.GetCalendarOwnerOrPurposeAnnotation()) != "" {
			showCalendarOwnerOrPurposeDescription = true
			break
		}
	}
	rows := make([][]string, 0, len(response.GetCalendarEventRecordsInDisplayOrder()))
	for _, calendarEventRecord := range response.GetCalendarEventRecordsInDisplayOrder() {
		if calendarEventRecord == nil {
			continue
		}
		row := []string{
			formatArchiveRecordAssociatedTimeRange(
				calendarEventRecord.GetCalendarEventStartTime(),
				calendarEventRecord.GetCalendarEventEndTime(),
				localTimeWithUTCOffsetForListRow,
			),
			strings.TrimSpace(calendarEventRecord.GetCalendarEventDisplayName()),
			strings.TrimSpace(calendarEventRecord.GetCalendarDisplayName()),
		}
		if showCalendarOwnerOrPurposeDescription {
			row = append(row, calendarOwnerOrPurposeDescription(calendarEventRecord.GetCalendarOwnerOrPurposeAnnotation()))
		}
		row = append(row, globallyRoutableTrawlLinkText(
			globallyRoutableTrawlLinksByCanonicalRecordReference.
				globallyRoutableTrawlLinkForCanonicalArchiveRecordReference(
					calendarEventRecord.GetCanonicalRecordReference(),
				),
		))
		rows = append(rows, row)
	}
	columns := []TableColumn{
		archiveEventTimeColumn(columnHeaderWhen),
		{Header: "event", MinimumWidth: 16, Wrap: true, MaximumWrappedLines: 2},
		{Header: "calendar", MinimumWidth: 8, Wrap: true, MaximumWrappedLines: 2},
	}
	if showCalendarOwnerOrPurposeDescription {
		columns = append(columns, TableColumn{Header: "owner or purpose", MinimumWidth: 8, Wrap: true, MaximumWrappedLines: 2})
	}
	columns = append(columns, TableColumn{Header: columnHeaderLink, NeverTruncateCellValues: true})
	return writeHumanRecordRowsWithPrimaryContentColumn(writer, columns, rows, 1)
}

func calendarEventPlace(location *calendarevent.CalendarEventLocation) string {
	if location == nil {
		return ""
	}
	displayName := strings.TrimSpace(location.GetCalendarEventLocationDisplayName())
	address := strings.TrimSpace(location.GetCalendarEventLocationAddress())
	if displayName == "" || calendarEventLocationDisplayNameIsAddressPrefix(displayName, address) {
		return address
	}
	if address == "" {
		return displayName
	}
	return displayName + " · " + address
}

func calendarEventLocationDisplayNameIsAddressPrefix(displayName string, address string) bool {
	if len(address) < len(displayName) || !strings.EqualFold(address[:len(displayName)], displayName) {
		return false
	}
	if len(address) == len(displayName) {
		return true
	}
	switch address[len(displayName)] {
	case ' ', '\t', '\n', '\r', ',':
		return true
	default:
		return false
	}
}

func calendarEventPeople(calendarEventRecord *calendarevent.CalendarEventRecord) string {
	if calendarEventRecord == nil {
		return ""
	}
	people := make([]string, 0, 1+len(calendarEventRecord.GetCalendarEventAttendees()))
	seen := make(map[string]struct{})
	add := func(personDisplayName string) {
		personDisplayName = strings.TrimSpace(personDisplayName)
		normalizedPersonDisplayName := strings.ToLower(personDisplayName)
		if personDisplayName == "" {
			return
		}
		if _, exists := seen[normalizedPersonDisplayName]; exists {
			return
		}
		seen[normalizedPersonDisplayName] = struct{}{}
		people = append(people, personDisplayName)
	}
	if organizer := calendarEventRecord.GetCalendarEventOrganizer(); organizer != nil {
		add(organizer.GetPersonDisplayName())
	}
	for _, attendee := range calendarEventRecord.GetCalendarEventAttendees() {
		if attendee != nil && attendee.GetPersonRelatedToCalendarEvent() != nil {
			add(attendee.GetPersonRelatedToCalendarEvent().GetPersonDisplayName())
		}
	}
	return strings.Join(people, ", ")
}
