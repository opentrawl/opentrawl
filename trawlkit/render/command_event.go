package render

import (
	"io"
	"strings"

	calendareventv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/calendar_event/v1"
)

func WriteCalendarEventListResponse(
	writer io.Writer,
	response *calendareventv1.CalendarEventListResponse,
	globallyRoutableTrawlLinksByCanonicalRecordReference GloballyRoutableTrawlLinksByCanonicalArchiveRecordReference,
) error {
	if response == nil {
		return nil
	}
	if len(response.GetCalendarEventRecordsInDisplayOrder()) == 0 {
		_, err := io.WriteString(writer, "No events match.\n")
		return err
	}
	allRows := make([][]string, 0, len(response.GetCalendarEventRecordsInDisplayOrder()))
	showCalendar := false
	showPlace := false
	showPeople := false
	for _, calendarEventRecord := range response.GetCalendarEventRecordsInDisplayOrder() {
		if calendarEventRecord == nil {
			continue
		}
		calendarDisplayName := strings.TrimSpace(calendarEventRecord.GetCalendarDisplayName())
		place := calendarEventPlace(calendarEventRecord.GetCalendarEventLocation())
		people := calendarEventPeople(calendarEventRecord)
		showCalendar = showCalendar || calendarDisplayName != ""
		showPlace = showPlace || place != ""
		showPeople = showPeople || people != ""
		allRows = append(allRows, []string{
			trawlerSpecificCommandAssociatedTime(calendarEventRecord.GetCalendarEventStartTime()),
			globallyRoutableTrawlLinkText(
				globallyRoutableTrawlLinksByCanonicalRecordReference.
					globallyRoutableTrawlLinkForCanonicalArchiveRecordReference(
						calendarEventRecord.GetCanonicalRecordReference(),
					),
			),
			strings.TrimSpace(calendarEventRecord.GetCalendarEventDisplayName()),
			calendarDisplayName,
			place,
			people,
		})
	}
	columns := []TableColumn{
		{Header: "when", MinimumWidth: 16},
		{Header: "link", NeverTruncateCellValues: true},
		{Header: "event", Wrap: true, MaximumWrappedLines: 2},
	}
	if showCalendar {
		columns = append(columns, TableColumn{Header: "calendar", Wrap: true})
	}
	if showPlace {
		columns = append(columns, TableColumn{Header: "where", Wrap: true})
	}
	if showPeople {
		columns = append(columns, TableColumn{Header: "people", Wrap: true, MaximumWrappedLines: 2})
	}
	rows := make([][]string, 0, len(allRows))
	for _, allRow := range allRows {
		row := append([]string(nil), allRow[:3]...)
		if showCalendar {
			row = append(row, allRow[3])
		}
		if showPlace {
			row = append(row, allRow[4])
		}
		if showPeople {
			row = append(row, allRow[5])
		}
		rows = append(rows, row)
	}
	return WriteTable(writer, columns, rows)
}

func calendarEventPlace(location *calendareventv1.CalendarEventLocation) string {
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

func calendarEventPeople(calendarEventRecord *calendareventv1.CalendarEventRecord) string {
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
