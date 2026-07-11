package calcrawl

import (
	"github.com/opentrawl/opentrawl/calcrawl/internal/archive"
	calendaropenv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/source/calendar/open/v1"
)

func projectOpenRecord(value archive.EventDetail) *calendaropenv1.CalendarRecord {
	record := &calendaropenv1.CalendarRecord{
		Ref:            value.Ref,
		Uuid:           value.UUID,
		Title:          value.Title,
		Start:          value.Start,
		End:            value.End,
		AllDay:         value.AllDay,
		Calendar:       value.Calendar,
		Account:        value.Account,
		Attendees:      make([]*calendaropenv1.Attendee, 0, len(value.Attendees)),
		HasRecurrences: value.HasRecurrences,
	}
	setOptionalString(&record.UniqueIdentifier, value.UniqueIdentifier)
	setOptionalString(&record.Description, value.Description)
	if value.DescriptionTruncated {
		record.DescriptionTruncated = recordBool(true)
	}
	if value.Availability != nil {
		availability := *value.Availability
		record.Availability = &availability
	}
	if value.Location != nil {
		record.Location = &calendaropenv1.Location{}
		setOptionalString(&record.Location.Title, value.Location.Title)
		setOptionalString(&record.Location.Address, value.Location.Address)
	}
	if value.Organizer != (archive.Person{}) {
		record.Organizer = projectPerson(value.Organizer)
	}
	for _, attendee := range value.Attendees {
		record.Attendees = append(record.Attendees, projectAttendee(attendee))
	}
	setOptionalString(&record.Url, value.URL)
	setOptionalString(&record.Status, value.Status)
	return record
}

func projectPerson(value archive.Person) *calendaropenv1.Person {
	record := &calendaropenv1.Person{}
	setOptionalString(&record.DisplayName, value.DisplayName)
	setOptionalString(&record.Email, value.Email)
	setOptionalString(&record.PhoneNumber, value.PhoneNumber)
	return record
}

func projectAttendee(value archive.Attendee) *calendaropenv1.Attendee {
	record := &calendaropenv1.Attendee{}
	setOptionalString(&record.DisplayName, value.DisplayName)
	setOptionalString(&record.Email, value.Email)
	setOptionalString(&record.PhoneNumber, value.PhoneNumber)
	setOptionalString(&record.Address, value.Address)
	setOptionalString(&record.RsvpStatus, value.RSVPStatus)
	setOptionalString(&record.Role, value.Role)
	if value.Self {
		record.Self = recordBool(true)
	}
	setOptionalString(&record.Comment, value.Comment)
	return record
}

func setOptionalString(target **string, value string) {
	if value != "" {
		*target = &value
	}
}

func recordBool(value bool) *bool { return &value }
