package densesearch

import (
	"errors"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
	calendarevent "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/calendar_event"
	identity "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/identity"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	search "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search"
	searchablerecord "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/searchable_record"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type searchableTextSection struct {
	displayName  string
	content      string
	recordAnchor *identity.RecordAnchorIdentifier
}

type searchableRecordProjection struct {
	associatedTime     *presentation.ArchiveRecordAssociatedTimeForDisplay
	textSections       []searchableTextSection
	searchPresentation *search.SearchMatchPresentation
}

func projectTypedSourceRecord(
	registeredTrawlerDisplayName string,
	record *searchablerecord.SearchableArchiveRecord,
) (searchableRecordProjection, error) {
	basePresentation := &search.SearchMatchPresentation{
		RegisteredTrawlerDisplayName: registeredTrawlerDisplayName,
	}
	switch typedRecord := record.GetTypedSourceRecord().(type) {
	case *searchablerecord.SearchableArchiveRecord_MessageRecord:
		messageRecord := typedRecord.MessageRecord
		if messageRecord == nil {
			return searchableRecordProjection{}, errors.New("searchable message record is missing")
		}
		basePresentation.MatchingRecordAssociatedTime = messageRecord.GetMessageTime()
		basePresentation.MatchingRecordDisplayName = messageRecord.GetConversationDisplayName()
		basePresentation.PeopleRelatedToMatchingRecord = messageRecord.GetPeopleRelatedToMessage()
		basePresentation.MatchingRecordKindDisplayName = "message"
		sections := []searchableTextSection{{
			displayName:  "Message",
			content:      messageRecord.GetMessageText(),
			recordAnchor: trawlkit.NewRecordAnchorIdentifier(trawlkit.MatchAnchorID),
		}}
		if messageMedia := messageRecord.GetMessageMedia(); messageMedia != nil {
			sections = append(sections, searchableTextSection{
				displayName: "Media",
				content:     messageMedia.GetMessageMediaTitle(),
			})
		}
		return completeSearchableRecordProjection(basePresentation, sections), nil

	case *searchablerecord.SearchableArchiveRecord_CalendarEventRecord:
		calendarEventRecord := typedRecord.CalendarEventRecord
		if calendarEventRecord == nil {
			return searchableRecordProjection{}, errors.New("searchable calendar event record is missing")
		}
		basePresentation.MatchingRecordAssociatedTime = calendarEventRecord.GetCalendarEventStartTime()
		basePresentation.MatchingRecordDisplayName = calendarEventRecord.GetCalendarEventDisplayName()
		basePresentation.MatchingRecordKindDisplayName = "event"
		basePresentation.DigitalContainerNamesNearestToBroadest = nonEmptyValues(
			calendarEventRecord.GetCalendarDisplayName(),
			calendarEventRecord.GetCalendarAccountDisplayName(),
		)
		location := calendarEventRecord.GetCalendarEventLocation()
		if location != nil {
			basePresentation.PhysicalPlaceNamesSpecificToBroadest = nonEmptyValues(
				location.GetCalendarEventLocationDisplayName(),
				location.GetCalendarEventLocationAddress(),
			)
		}
		participants := make([]string, 0, len(calendarEventRecord.GetCalendarEventAttendees())+1)
		if organizer := calendarEventRecord.GetCalendarEventOrganizer(); organizer != nil {
			basePresentation.PeopleRelatedToMatchingRecord = append(
				basePresentation.PeopleRelatedToMatchingRecord,
				organizer,
			)
			participants = appendCalendarPersonSearchValues(participants, organizer)
		}
		for _, attendee := range calendarEventRecord.GetCalendarEventAttendees() {
			if attendee == nil || attendee.GetPersonRelatedToCalendarEvent() == nil {
				continue
			}
			attendeePerson := attendee.GetPersonRelatedToCalendarEvent()
			basePresentation.PeopleRelatedToMatchingRecord = append(
				basePresentation.PeopleRelatedToMatchingRecord,
				attendeePerson,
			)
			participants = appendCalendarPersonSearchValues(participants, attendeePerson)
			participants = appendNonDuplicateValue(
				participants,
				attendee.GetCalendarEventAttendeeSourceAttendanceStatus(),
			)
		}
		sections := []searchableTextSection{
			{displayName: "Summary", content: calendarEventRecord.GetCalendarEventDisplayName()},
			{displayName: "Description", content: calendarEventRecord.GetCalendarEventDescription()},
			{displayName: "Location", content: strings.Join(nonEmptyValues(
				locationDisplayName(location), locationAddress(location),
			), "\n")},
			{displayName: "Participants", content: strings.Join(nonEmptyValues(participants...), "\n")},
		}
		return completeSearchableRecordProjection(basePresentation, sections), nil

	case *searchablerecord.SearchableArchiveRecord_OpenedNoteRecord:
		noteRecord := typedRecord.OpenedNoteRecord
		if noteRecord == nil {
			return searchableRecordProjection{}, errors.New("searchable opened note record is missing")
		}
		basePresentation.MatchingRecordAssociatedTime = exactTimeForSearchPresentation(noteRecord.GetOpenedNoteVersionTime())
		basePresentation.MatchingRecordDisplayName = noteRecord.GetNoteDisplayName()
		basePresentation.MatchingRecordKindDisplayName = "note version"
		basePresentation.DigitalContainerNamesNearestToBroadest = nonEmptyValues(noteRecord.GetNoteFolderDisplayName())
		return completeSearchableRecordProjection(basePresentation, []searchableTextSection{
			{
				displayName:  "Title",
				content:      noteRecord.GetNoteDisplayName(),
				recordAnchor: noteRecord.GetNoteDisplayNameAnchor(),
			},
			{
				displayName:  "Note",
				content:      noteRecord.GetOpenedNoteBody().GetAvailableNoteBody().GetNoteBodyText(),
				recordAnchor: noteRecord.GetOpenedNoteBodyAnchor(),
			},
		}), nil

	case *searchablerecord.SearchableArchiveRecord_TrawlerSpecificRecord:
		trawlerSpecificRecord := typedRecord.TrawlerSpecificRecord
		if trawlerSpecificRecord == nil || trawlerSpecificRecord.GetDetailPresentation() == nil {
			return searchableRecordProjection{}, errors.New("trawler-specific searchable record is missing")
		}
		detail := trawlerSpecificRecord.GetDetailPresentation()
		basePresentation.MatchingRecordAssociatedTime = exactTimeForSearchPresentation(
			trawlerSpecificRecord.GetArchiveRecordAssociatedTime(),
		)
		basePresentation.MatchingRecordDisplayName = detail.GetDetailDisplayName()
		basePresentation.MatchingRecordKindDisplayName = "message"
		sections := make([]searchableTextSection, 0, 2)
		if detail.GetDetailDisplayNameAnchor() != nil {
			sections = append(sections, searchableTextSection{
				displayName:  "Subject",
				content:      detail.GetDetailDisplayName(),
				recordAnchor: detail.GetDetailDisplayNameAnchor(),
			})
		}
		sections = append(sections, searchableTextSection{
			displayName:  "Message",
			content:      detail.GetBodyText(),
			recordAnchor: detail.GetBodyAnchor(),
		})
		return completeSearchableRecordProjection(basePresentation, sections), nil

	default:
		return searchableRecordProjection{}, errors.New("searchable archive record has no typed source record")
	}
}

func appendCalendarPersonSearchValues(values []string, relatedPerson *person.PersonRelatedToArchiveRecord) []string {
	if relatedPerson == nil {
		return values
	}
	values = appendNonDuplicateValue(values, relatedPerson.GetPersonDisplayName())
	for _, contactMethod := range relatedPerson.GetPersonContactMethodsInDisplayOrder() {
		if contactMethod != nil {
			values = appendNonDuplicateValue(values, contactMethod.GetPersonContactMethodDisplayValue())
		}
	}
	return values
}

func appendNonDuplicateValue(values []string, candidate string) []string {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return values
	}
	for _, existingValue := range values {
		if strings.EqualFold(strings.TrimSpace(existingValue), candidate) {
			return values
		}
	}
	return append(values, candidate)
}

func completeSearchableRecordProjection(
	basePresentation *search.SearchMatchPresentation,
	sections []searchableTextSection,
) searchableRecordProjection {
	nonEmptySections := make([]searchableTextSection, 0, len(sections))
	for _, section := range sections {
		section.content = strings.TrimSpace(section.content)
		if section.content != "" {
			nonEmptySections = append(nonEmptySections, section)
		}
	}
	return searchableRecordProjection{
		associatedTime:     basePresentation.GetMatchingRecordAssociatedTime(),
		textSections:       nonEmptySections,
		searchPresentation: basePresentation,
	}
}

func exactTimeForSearchPresentation(exactTime *timestamppb.Timestamp) *presentation.ArchiveRecordAssociatedTimeForDisplay {
	if exactTime == nil || !exactTime.IsValid() {
		return nil
	}
	return &presentation.ArchiveRecordAssociatedTimeForDisplay{
		ArchiveRecordAssociatedTime: &presentation.ArchiveRecordAssociatedTimeForDisplay_ExactTime{ExactTime: exactTime},
	}
}

func nonEmptyValues(values ...string) []string {
	nonEmptyValues := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			nonEmptyValues = append(nonEmptyValues, value)
		}
	}
	return nonEmptyValues
}

func locationDisplayName(location *calendarevent.CalendarEventLocation) string {
	if location == nil {
		return ""
	}
	return location.GetCalendarEventLocationDisplayName()
}

func locationAddress(location *calendarevent.CalendarEventLocation) string {
	if location == nil {
		return ""
	}
	return location.GetCalendarEventLocationAddress()
}

func searchMatchTextField(fieldName, content string) []*search.SearchMatchTextField {
	if strings.TrimSpace(fieldName) == "" && strings.TrimSpace(content) == "" {
		return nil
	}
	return []*search.SearchMatchTextField{{
		SearchMatchTextFieldName: fieldName,
		SearchMatchTextFragmentsInDisplayOrder: []*search.SearchMatchTextFragment{{
			SearchMatchTextFragmentContent: content,
		}},
	}}
}
