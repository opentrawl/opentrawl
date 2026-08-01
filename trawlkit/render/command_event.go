package render

import (
	"io"
	"strings"

	calendarevent "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/calendar_event"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	"google.golang.org/protobuf/types/known/timestamppb"
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
			calendarEventWhen(
				calendarEventRecord.GetCalendarEventStartTime(),
				calendarEventRecord.GetCalendarEventEndTime(),
			),
			strings.TrimSpace(calendarEventRecord.GetCalendarEventDisplayName()),
			strings.TrimSpace(calendarEventRecord.GetCalendarDisplayName()),
			globallyRoutableTrawlLinkText(
				globallyRoutableTrawlLinksByCanonicalRecordReference.
					globallyRoutableTrawlLinkForCanonicalArchiveRecordReference(
						calendarEventRecord.GetCanonicalRecordReference(),
					),
			),
		})
	}
	columns := []TableColumn{
		{Header: "when", MinimumWidth: 16},
		{Header: "event", MinimumWidth: 16, Wrap: true, MaximumWrappedLines: 2},
		{Header: "calendar", MinimumWidth: 8},
		{Header: "link", NeverTruncateCellValues: true},
	}
	return WriteTable(writer, columns, rows)
}

func calendarEventWhen(
	calendarEventStartTime *presentation.ArchiveRecordAssociatedTimeForDisplay,
	calendarEventEndTime *presentation.ArchiveRecordAssociatedTimeForDisplay,
) string {
	if calendarEventStartTime == nil {
		return ""
	}
	switch typedCalendarEventStartTime := calendarEventStartTime.GetArchiveRecordAssociatedTime().(type) {
	case *presentation.ArchiveRecordAssociatedTimeForDisplay_ExactTime:
		return timedCalendarEventWhen(typedCalendarEventStartTime.ExactTime, calendarEventEndTime)
	case *presentation.ArchiveRecordAssociatedTimeForDisplay_CalendarDate:
		return allDayCalendarEventWhen(typedCalendarEventStartTime.CalendarDate, calendarEventEndTime)
	default:
		return ""
	}
}

func timedCalendarEventWhen(
	calendarEventStartTime *timestamppb.Timestamp,
	calendarEventEndTime *presentation.ArchiveRecordAssociatedTimeForDisplay,
) string {
	if calendarEventStartTime == nil || !calendarEventStartTime.IsValid() {
		return ""
	}
	localCalendarEventStartTime := calendarEventStartTime.AsTime().Local()
	calendarEventStartText := localCalendarEventStartTime.Format("2006-01-02 15:04")
	if calendarEventEndTime == nil {
		return calendarEventStartText
	}
	typedCalendarEventEndTime, isExactTime := calendarEventEndTime.GetArchiveRecordAssociatedTime().(*presentation.ArchiveRecordAssociatedTimeForDisplay_ExactTime)
	if !isExactTime || typedCalendarEventEndTime.ExactTime == nil || !typedCalendarEventEndTime.ExactTime.IsValid() {
		return calendarEventStartText
	}
	localCalendarEventEndTime := typedCalendarEventEndTime.ExactTime.AsTime().Local()
	if localCalendarEventStartTime.Year() == localCalendarEventEndTime.Year() &&
		localCalendarEventStartTime.YearDay() == localCalendarEventEndTime.YearDay() {
		return calendarEventStartText + "–" + localCalendarEventEndTime.Format("15:04")
	}
	return calendarEventStartText + " – " + localCalendarEventEndTime.Format("2006-01-02 15:04")
}

func allDayCalendarEventWhen(
	calendarEventStartDate *presentation.CalendarDate,
	calendarEventEndTime *presentation.ArchiveRecordAssociatedTimeForDisplay,
) string {
	calendarEventStartDateText := trawlerSpecificCommandCalendarDate(calendarEventStartDate)
	if calendarEventStartDateText == "" {
		return ""
	}
	calendarEventEndDateText := ""
	if calendarEventEndTime != nil {
		if typedCalendarEventEndTime, isCalendarDate := calendarEventEndTime.GetArchiveRecordAssociatedTime().(*presentation.ArchiveRecordAssociatedTimeForDisplay_CalendarDate); isCalendarDate {
			calendarEventEndDateText = trawlerSpecificCommandCalendarDate(typedCalendarEventEndTime.CalendarDate)
		}
	}
	if calendarEventEndDateText == "" || calendarEventEndDateText == calendarEventStartDateText {
		return calendarEventStartDateText + " (all day)"
	}
	return calendarEventStartDateText + " – " + calendarEventEndDateText + " (all day)"
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
