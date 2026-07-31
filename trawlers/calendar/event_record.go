package calendar

import (
	"strings"

	"github.com/opentrawl/opentrawl/calendar/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	calendareventv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/calendar_event/v1"
	personv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person/v1"
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
) *calendareventv1.CalendarEventRecord {
	record := &calendareventv1.CalendarEventRecord{
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
) calendareventv1.CalendarEventAvailability {
	if availability == nil {
		return calendareventv1.CalendarEventAvailability_CALENDAR_EVENT_AVAILABILITY_UNSPECIFIED
	}
	switch *availability {
	case -1:
		return calendareventv1.CalendarEventAvailability_CALENDAR_EVENT_AVAILABILITY_NOT_SUPPORTED
	case 0:
		return calendareventv1.CalendarEventAvailability_CALENDAR_EVENT_AVAILABILITY_BUSY
	case 1:
		return calendareventv1.CalendarEventAvailability_CALENDAR_EVENT_AVAILABILITY_FREE
	case 2:
		return calendareventv1.CalendarEventAvailability_CALENDAR_EVENT_AVAILABILITY_TENTATIVE
	case 3:
		return calendareventv1.CalendarEventAvailability_CALENDAR_EVENT_AVAILABILITY_UNAVAILABLE
	default:
		return calendareventv1.CalendarEventAvailability_CALENDAR_EVENT_AVAILABILITY_UNKNOWN
	}
}

func calendarEventStatus(status string) calendareventv1.CalendarEventStatus {
	switch archive.NormalizeEventStatus(status) {
	case "":
		return calendareventv1.CalendarEventStatus_CALENDAR_EVENT_STATUS_UNSPECIFIED
	case "confirmed":
		return calendareventv1.CalendarEventStatus_CALENDAR_EVENT_STATUS_CONFIRMED
	case "tentative":
		return calendareventv1.CalendarEventStatus_CALENDAR_EVENT_STATUS_TENTATIVE
	case "cancelled":
		return calendareventv1.CalendarEventStatus_CALENDAR_EVENT_STATUS_CANCELLED
	default:
		return calendareventv1.CalendarEventStatus_CALENDAR_EVENT_STATUS_UNKNOWN
	}
}

func calendarEventLocation(
	location *archive.Location,
) *calendareventv1.CalendarEventLocation {
	if location == nil {
		return nil
	}
	displayName := strings.Join(strings.Fields(location.Title), " ")
	address := strings.Join(strings.Fields(location.Address), " ")
	if displayName == "" && address == "" {
		return nil
	}
	return &calendareventv1.CalendarEventLocation{
		CalendarEventLocationDisplayName: displayName,
		CalendarEventLocationAddress:     address,
	}
}

func calendarEventOrganizer(
	organizer archive.Person,
) *personv1.PersonRelatedToArchiveRecord {
	personDisplayName := calendarSearchResultSafeHumanPersonDisplayName(
		organizer.DisplayName,
		organizer.Email,
		organizer.PhoneNumber,
	)
	if personDisplayName == "" {
		return nil
	}
	return &personv1.PersonRelatedToArchiveRecord{
		PersonDisplayName:         personDisplayName,
		PersonRoleInArchiveRecord: personv1.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_ORGANIZER,
	}
}

func calendarEventAttendees(
	attendees []archive.Attendee,
) []*calendareventv1.CalendarEventAttendee {
	calendarEventAttendees := make([]*calendareventv1.CalendarEventAttendee, 0, len(attendees))
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
			&calendareventv1.CalendarEventAttendee{
				PersonRelatedToCalendarEvent: &personv1.PersonRelatedToArchiveRecord{
					PersonDisplayName:         personDisplayName,
					PersonRoleInArchiveRecord: personv1.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_ATTENDEE,
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
) calendareventv1.CalendarEventAttendeeAttendanceStatus {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "":
		return calendareventv1.CalendarEventAttendeeAttendanceStatus_CALENDAR_EVENT_ATTENDEE_ATTENDANCE_STATUS_UNSPECIFIED
	case "pending":
		return calendareventv1.CalendarEventAttendeeAttendanceStatus_CALENDAR_EVENT_ATTENDEE_ATTENDANCE_STATUS_PENDING
	case "accepted":
		return calendareventv1.CalendarEventAttendeeAttendanceStatus_CALENDAR_EVENT_ATTENDEE_ATTENDANCE_STATUS_ACCEPTED
	case "declined":
		return calendareventv1.CalendarEventAttendeeAttendanceStatus_CALENDAR_EVENT_ATTENDEE_ATTENDANCE_STATUS_DECLINED
	case "tentative":
		return calendareventv1.CalendarEventAttendeeAttendanceStatus_CALENDAR_EVENT_ATTENDEE_ATTENDANCE_STATUS_TENTATIVE
	case "delegated":
		return calendareventv1.CalendarEventAttendeeAttendanceStatus_CALENDAR_EVENT_ATTENDEE_ATTENDANCE_STATUS_DELEGATED
	case "completed":
		return calendareventv1.CalendarEventAttendeeAttendanceStatus_CALENDAR_EVENT_ATTENDEE_ATTENDANCE_STATUS_COMPLETED
	case "in_process":
		return calendareventv1.CalendarEventAttendeeAttendanceStatus_CALENDAR_EVENT_ATTENDEE_ATTENDANCE_STATUS_IN_PROCESS
	default:
		return calendareventv1.CalendarEventAttendeeAttendanceStatus_CALENDAR_EVENT_ATTENDEE_ATTENDANCE_STATUS_UNKNOWN
	}
}
