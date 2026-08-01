package calendar

import (
	"strings"

	"github.com/opentrawl/opentrawl/calendar/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	calendarevent "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/calendar_event"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
)

type calendarEventRecordValuesFromArchive struct {
	canonicalCalendarEventRecordReference string
	startTime                             string
	endTime                               string
	allDay                                bool
	eventDisplayName                      string
	calendarDisplayName                   string
	calendarAccountDisplayName            string
	availability                          *int64
	location                              *archive.Location
	organizer                             archive.Person
	attendees                             []archive.Attendee
	httpsURL                              string
	status                                string
	recurring                             bool
	description                           string
	descriptionIsTruncated                bool
}

func calendarEventRecordValuesFromListItem(
	archiveEvent archive.EventListItem,
) calendarEventRecordValuesFromArchive {
	attendeesWithoutCurrentUser := make([]archive.Attendee, 0, len(archiveEvent.Attendees))
	for _, attendee := range archiveEvent.Attendees {
		if !attendee.Self {
			attendeesWithoutCurrentUser = append(attendeesWithoutCurrentUser, attendee)
		}
	}
	return calendarEventRecordValuesFromArchive{
		canonicalCalendarEventRecordReference: archiveEvent.Ref,
		startTime:                             archiveEvent.Start,
		endTime:                               archiveEvent.End,
		allDay:                                archiveEvent.AllDay,
		eventDisplayName:                      archiveEvent.Title,
		calendarDisplayName:                   archiveEvent.Calendar,
		location:                              archiveEvent.Location,
		organizer:                             archiveEvent.Organizer,
		attendees:                             attendeesWithoutCurrentUser,
	}
}

func calendarEventRecordValuesFromDetail(
	archiveEvent archive.EventDetail,
) calendarEventRecordValuesFromArchive {
	return calendarEventRecordValuesFromArchive{
		canonicalCalendarEventRecordReference: archiveEvent.Ref,
		startTime:                             archiveEvent.Start,
		endTime:                               archiveEvent.End,
		allDay:                                archiveEvent.AllDay,
		eventDisplayName:                      archiveEvent.Title,
		calendarDisplayName:                   archiveEvent.Calendar,
		calendarAccountDisplayName:            archiveEvent.Account,
		availability:                          archiveEvent.Availability,
		location:                              archiveEvent.Location,
		organizer:                             archiveEvent.Organizer,
		attendees:                             archiveEvent.Attendees,
		httpsURL:                              archiveEvent.URL,
		status:                                archiveEvent.Status,
		recurring:                             archiveEvent.HasRecurrences,
		description:                           archiveEvent.Description,
		descriptionIsTruncated:                archiveEvent.DescriptionTruncated,
	}
}

func projectCalendarEventRecord(
	values calendarEventRecordValuesFromArchive,
) *calendarevent.CalendarEventRecord {
	record := &calendarevent.CalendarEventRecord{
		CanonicalRecordReference:            trawlkit.NewCanonicalArchiveRecordReference(values.canonicalCalendarEventRecordReference),
		CalendarEventStartTime:              calendarEventStartTimeForDisplay(values.startTime, values.allDay),
		CalendarEventEndTime:                calendarEventEndTimeForDisplay(values.startTime, values.endTime, values.allDay),
		CalendarEventDisplayName:            calendarEventDisplayName(values.eventDisplayName),
		CalendarDisplayName:                 strings.TrimSpace(values.calendarDisplayName),
		CalendarAccountDisplayName:          strings.TrimSpace(values.calendarAccountDisplayName),
		CalendarEventAvailability:           calendarEventAvailability(values.availability),
		CalendarEventLocation:               calendarEventLocation(values.location),
		CalendarEventOrganizer:              calendarEventOrganizer(values.organizer),
		CalendarEventAttendees:              calendarEventAttendees(values.attendees),
		CalendarEventStatus:                 calendarEventStatus(values.status),
		CalendarEventIsRecurring:            values.recurring,
		CalendarEventDescription:            strings.TrimSpace(values.description),
		CalendarEventDescriptionIsTruncated: values.descriptionIsTruncated,
	}
	if openrecord.ValidHTTPSURL(values.httpsURL) {
		record.CalendarEventHttpsUrl = strings.TrimSpace(values.httpsURL)
	}
	return record
}

func calendarEventAvailability(
	availability *int64,
) calendarevent.CalendarEventAvailability {
	if availability == nil {
		return calendarevent.CalendarEventAvailability_CALENDAR_EVENT_AVAILABILITY_UNSPECIFIED
	}
	switch *availability {
	case -1:
		return calendarevent.CalendarEventAvailability_CALENDAR_EVENT_AVAILABILITY_NOT_SUPPORTED
	case 0:
		return calendarevent.CalendarEventAvailability_CALENDAR_EVENT_AVAILABILITY_BUSY
	case 1:
		return calendarevent.CalendarEventAvailability_CALENDAR_EVENT_AVAILABILITY_FREE
	case 2:
		return calendarevent.CalendarEventAvailability_CALENDAR_EVENT_AVAILABILITY_TENTATIVE
	case 3:
		return calendarevent.CalendarEventAvailability_CALENDAR_EVENT_AVAILABILITY_UNAVAILABLE
	default:
		return calendarevent.CalendarEventAvailability_CALENDAR_EVENT_AVAILABILITY_UNKNOWN
	}
}

func calendarEventStatus(status string) calendarevent.CalendarEventStatus {
	switch archive.NormalizeEventStatus(status) {
	case "":
		return calendarevent.CalendarEventStatus_CALENDAR_EVENT_STATUS_UNSPECIFIED
	case "confirmed":
		return calendarevent.CalendarEventStatus_CALENDAR_EVENT_STATUS_CONFIRMED
	case "tentative":
		return calendarevent.CalendarEventStatus_CALENDAR_EVENT_STATUS_TENTATIVE
	case "cancelled":
		return calendarevent.CalendarEventStatus_CALENDAR_EVENT_STATUS_CANCELLED
	default:
		return calendarevent.CalendarEventStatus_CALENDAR_EVENT_STATUS_UNKNOWN
	}
}

func calendarEventLocation(
	location *archive.Location,
) *calendarevent.CalendarEventLocation {
	if location == nil {
		return nil
	}
	displayName := strings.Join(strings.Fields(location.Title), " ")
	address := strings.Join(strings.Fields(location.Address), " ")
	if displayName == "" && address == "" {
		return nil
	}
	return &calendarevent.CalendarEventLocation{
		CalendarEventLocationDisplayName: displayName,
		CalendarEventLocationAddress:     address,
	}
}

func calendarEventOrganizer(
	organizer archive.Person,
) *person.PersonRelatedToArchiveRecord {
	personDisplayName := calendarSearchResultSafeHumanPersonDisplayName(
		organizer.DisplayName,
		organizer.Email,
		organizer.PhoneNumber,
	)
	if personDisplayName == "" {
		return nil
	}
	return &person.PersonRelatedToArchiveRecord{
		PersonDisplayName:         personDisplayName,
		PersonRoleInArchiveRecord: person.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_ORGANIZER,
	}
}

func calendarEventAttendees(
	attendees []archive.Attendee,
) []*calendarevent.CalendarEventAttendee {
	calendarEventAttendees := make([]*calendarevent.CalendarEventAttendee, 0, len(attendees))
	seenPersonDisplayNames := make(map[string]struct{}, len(attendees))
	for _, attendee := range attendees {
		personDisplayName := calendarSearchResultSafeHumanPersonDisplayName(
			attendee.DisplayName,
			attendee.Email,
			attendee.PhoneNumber,
			attendee.Address,
		)
		normalizedPersonDisplayName := strings.ToLower(personDisplayName)
		if normalizedPersonDisplayName == "" {
			continue
		}
		if _, alreadyAdded := seenPersonDisplayNames[normalizedPersonDisplayName]; alreadyAdded {
			continue
		}
		seenPersonDisplayNames[normalizedPersonDisplayName] = struct{}{}
		calendarEventAttendees = append(
			calendarEventAttendees,
			&calendarevent.CalendarEventAttendee{
				PersonRelatedToCalendarEvent: &person.PersonRelatedToArchiveRecord{
					PersonDisplayName:         personDisplayName,
					PersonRoleInArchiveRecord: person.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_ATTENDEE,
				},
				AttendeeAttendanceStatus: calendarEventAttendeeAttendanceStatus(
					attendee.RSVPStatus,
				),
			},
		)
	}
	return calendarEventAttendees
}

func calendarEventAttendeeAttendanceStatus(
	status string,
) calendarevent.CalendarEventAttendeeAttendanceStatus {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "":
		return calendarevent.CalendarEventAttendeeAttendanceStatus_CALENDAR_EVENT_ATTENDEE_ATTENDANCE_STATUS_UNSPECIFIED
	case "pending":
		return calendarevent.CalendarEventAttendeeAttendanceStatus_CALENDAR_EVENT_ATTENDEE_ATTENDANCE_STATUS_PENDING
	case "accepted":
		return calendarevent.CalendarEventAttendeeAttendanceStatus_CALENDAR_EVENT_ATTENDEE_ATTENDANCE_STATUS_ACCEPTED
	case "declined":
		return calendarevent.CalendarEventAttendeeAttendanceStatus_CALENDAR_EVENT_ATTENDEE_ATTENDANCE_STATUS_DECLINED
	case "tentative":
		return calendarevent.CalendarEventAttendeeAttendanceStatus_CALENDAR_EVENT_ATTENDEE_ATTENDANCE_STATUS_TENTATIVE
	case "delegated":
		return calendarevent.CalendarEventAttendeeAttendanceStatus_CALENDAR_EVENT_ATTENDEE_ATTENDANCE_STATUS_DELEGATED
	case "completed":
		return calendarevent.CalendarEventAttendeeAttendanceStatus_CALENDAR_EVENT_ATTENDEE_ATTENDANCE_STATUS_COMPLETED
	case "in_process":
		return calendarevent.CalendarEventAttendeeAttendanceStatus_CALENDAR_EVENT_ATTENDEE_ATTENDANCE_STATUS_IN_PROCESS
	default:
		return calendarevent.CalendarEventAttendeeAttendanceStatus_CALENDAR_EVENT_ATTENDEE_ATTENDANCE_STATUS_UNKNOWN
	}
}
