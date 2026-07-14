package calcrawl

import (
	"context"
	"strings"

	"github.com/opentrawl/opentrawl/calcrawl/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	"github.com/opentrawl/opentrawl/trawlkit/presentation"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	calendaropenv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/source/calendar/open/v1"
	"google.golang.org/protobuf/types/known/anypb"
)

var _ trawlkit.RecordOpener = (*Crawler)(nil)

func (c *Crawler) OpenRecord(ctx context.Context, req *trawlkit.Request, ref string) (*openv1.OpenRecord, error) {
	value, err := c.loadOpenEvent(ctx, req, ref)
	if err != nil {
		return nil, err
	}
	if err := validateOpenTimestamps(value); err != nil {
		return nil, err
	}
	machine := projectOpenRecord(value)
	data, err := anypb.New(machine)
	if err != nil {
		return nil, err
	}
	record := &openv1.OpenRecord{SourceId: c.Info().ID, OpenRef: machine.GetRef(), Data: data, Presentation: projectOpenPresentation(value)}
	if err := openrecord.Validate(record); err != nil {
		return nil, err
	}
	return record, nil
}

func validateOpenTimestamps(value archive.EventDetail) error {
	return presentation.ValidateTimestamps(value.Start, value.End)
}

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

func projectOpenPresentation(value archive.EventDetail) *presentationv1.PresentationDocument {
	record := projectOpenRecord(value)
	title := strings.TrimSpace(record.Title)
	if title == "" {
		title = "Calendar event"
	}
	fields := make([]*presentationv1.Field, 0, 12)
	appendPresentationField(&fields, "Start", presentation.MustTimestamp(record.Start))
	appendPresentationField(&fields, "End", presentation.MustTimestamp(record.End))
	fields = append(fields, &presentationv1.Field{Label: "All day", Display: formatPresentationBool(record.AllDay)})
	appendPresentationField(&fields, "Calendar", record.Calendar)
	appendPresentationField(&fields, "Account", record.Account)
	if record.Availability != nil {
		fields = append(fields, &presentationv1.Field{Label: "Availability", Display: formatPresentationAvailability(*record.Availability)})
	}
	if location := formatPresentationLocation(record.Location); location != "" {
		fields = append(fields, &presentationv1.Field{Label: "Location", Display: location, AnchorId: "location"})
	}
	if organizer := formatPresentationPerson(record.Organizer); organizer != "" {
		fields = append(fields, &presentationv1.Field{Label: "Organizer", Display: organizer})
	}
	if attendees := formatPresentationAttendees(record.Attendees); attendees != "" {
		fields = append(fields, &presentationv1.Field{Label: "Attendees", Display: attendees, AnchorId: "participant"})
	}
	url := strings.TrimSpace(record.GetUrl())
	if url != "" {
		fields = append(fields, &presentationv1.Field{Label: "URL", Display: url})
	}
	appendPresentationField(&fields, "Status", record.GetStatus())
	fields = append(fields, &presentationv1.Field{Label: "Recurring", Display: formatPresentationBool(record.HasRecurrences)})
	blocks := make([]*presentationv1.Block, 0, 3)
	blocks = append(blocks, &presentationv1.Block{AnchorId: "summary", Content: &presentationv1.Block_Heading{Heading: &presentationv1.Heading{Text: title}}})
	if len(fields) > 0 {
		blocks = append(blocks, &presentationv1.Block{Content: &presentationv1.Block_Fields{Fields: &presentationv1.FieldGroup{Fields: fields}}})
	}
	if description := strings.TrimSpace(record.GetDescription()); description != "" {
		blocks = append(blocks, &presentationv1.Block{AnchorId: "description", Content: &presentationv1.Block_Prose{Prose: &presentationv1.Prose{Text: description}}})
	}
	document := &presentationv1.PresentationDocument{Title: title, Blocks: blocks, PrimaryAnchorId: "summary"}
	if openrecord.ValidHTTPSURL(url) {
		document.Actions = append(document.Actions, &presentationv1.Action{Label: "Open event link", Target: &presentationv1.Action_Url{Url: url}})
	}
	if record.GetDescriptionTruncated() {
		document.Facts = append(document.Facts, &presentationv1.Fact{Kind: presentationv1.Fact_KIND_TRUNCATION, Message: "Event description is truncated."})
	}
	return document
}

func appendPresentationField(fields *[]*presentationv1.Field, label, value string) {
	if value = strings.TrimSpace(value); value != "" {
		*fields = append(*fields, &presentationv1.Field{Label: label, Display: value})
	}
}

func formatPresentationBool(value bool) string {
	if value {
		return "Yes"
	}
	return "No"
}

func formatPresentationAvailability(value int64) string {
	switch value {
	case -1:
		return "Not supported"
	case 0:
		return "Busy"
	case 1:
		return "Free"
	case 2:
		return "Tentative"
	case 3:
		return "Unavailable"
	default:
		return "Unknown"
	}
}

func formatPresentationLocation(value *calendaropenv1.Location) string {
	if value == nil {
		return ""
	}
	title := strings.TrimSpace(value.GetTitle())
	address := strings.TrimSpace(value.GetAddress())
	if title != "" && address != "" && title != address {
		return title + ", " + address
	}
	if title != "" {
		return title
	}
	return address
}

func formatPresentationPerson(value *calendaropenv1.Person) string {
	if value == nil {
		return ""
	}
	for _, candidate := range []string{value.GetDisplayName(), value.GetEmail(), value.GetPhoneNumber()} {
		if candidate = strings.TrimSpace(candidate); candidate != "" {
			return candidate
		}
	}
	return ""
}

func formatPresentationAttendees(values []*calendaropenv1.Attendee) string {
	items := make([]string, 0, len(values))
	for _, value := range values {
		if value == nil {
			continue
		}
		name := ""
		for _, candidate := range []string{value.GetDisplayName(), value.GetEmail(), value.GetPhoneNumber(), value.GetAddress()} {
			if candidate = strings.TrimSpace(candidate); candidate != "" {
				name = candidate
				break
			}
		}
		if name == "" {
			continue
		}
		if status := strings.TrimSpace(value.GetRsvpStatus()); status != "" {
			name += " (" + status + ")"
		}
		items = append(items, name)
	}
	return strings.Join(items, ", ")
}
