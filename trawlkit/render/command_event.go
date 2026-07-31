package render

import (
	"io"
	"strings"

	calendarevent "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/calendar_event"
)

func WriteCalendarEventListResponse(
	writer io.Writer,
	response *calendarevent.CalendarEventListResponse,
	globallyRoutableTrawlLinksByCanonicalRecordReference GloballyRoutableTrawlLinksByCanonicalArchiveRecordReference,
) error {
	if response == nil {
		return nil
	}
	if len(response.GetCalendarEventRecordsInDisplayOrder()) == 0 {
		_, err := io.WriteString(writer, "No events match.\n")
		return err
	}
	rows := make([][]string, 0, len(response.GetCalendarEventRecordsInDisplayOrder()))
	for _, calendarEventRecord := range response.GetCalendarEventRecordsInDisplayOrder() {
		if calendarEventRecord == nil {
			continue
		}
		rows = append(rows, []string{
			trawlerSpecificCommandAssociatedTime(calendarEventRecord.GetCalendarEventStartTime()),
			globallyRoutableTrawlLinkText(
				globallyRoutableTrawlLinksByCanonicalRecordReference.
					globallyRoutableTrawlLinkForCanonicalArchiveRecordReference(
						calendarEventRecord.GetCanonicalRecordReference(),
					),
			),
			strings.TrimSpace(calendarEventRecord.GetCalendarEventDisplayName()),
		})
	}
	columns := []TableColumn{
		{Header: "when", MinimumWidth: 16},
		{Header: "link", NeverTruncateCellValues: true},
		{Header: "event"},
	}
	return WriteTable(writer, columns, rows)
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
