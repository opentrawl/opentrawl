package main

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

type searchIndexTextSection struct {
	displayName  string
	content      string
	recordAnchor *identity.RecordAnchorIdentifier
}

type searchIndexRecordProjection struct {
	recordKindDisplayName string
	associatedTime        *presentation.ArchiveRecordAssociatedTimeForDisplay
	textSections          []searchIndexTextSection
	searchPresentation    *search.SearchMatchPresentation
}

func projectTypedSourceRecordForSearchIndex(
	registeredTrawler registeredTrawlerName,
	record *searchablerecord.SearchableArchiveRecord,
) (searchIndexRecordProjection, error) {
	basePresentation := &search.SearchMatchPresentation{
		RegisteredTrawlerDisplayName: string(registeredTrawler),
	}
	switch typedRecord := record.GetTypedSourceRecord().(type) {
	case *searchablerecord.SearchableArchiveRecord_MessageRecord:
		messageRecord := typedRecord.MessageRecord
		if messageRecord == nil {
			return searchIndexRecordProjection{}, errors.New("searchable message record is missing")
		}
		basePresentation.MatchingRecordAssociatedTime = messageRecord.GetMessageTime()
		basePresentation.MatchingRecordDisplayName = messageRecord.GetConversationDisplayName()
		basePresentation.PeopleRelatedToMatchingRecord = messageRecord.GetPeopleRelatedToMessage()
		basePresentation.MatchingRecordKindDisplayName = "message"
		sections := []searchIndexTextSection{{
			displayName:  "Message",
			content:      messageRecord.GetMessageText(),
			recordAnchor: trawlkit.NewRecordAnchorIdentifier(trawlkit.MatchAnchorID),
		}}
		if messageMedia := messageRecord.GetMessageMedia(); messageMedia != nil {
			sections = append(sections, searchIndexTextSection{
				displayName: "Media",
				content:     messageMedia.GetMessageMediaTitle(),
			})
		}
		return completeSearchIndexRecordProjection(basePresentation, sections), nil

	case *searchablerecord.SearchableArchiveRecord_CalendarEventRecord:
		calendarEventRecord := typedRecord.CalendarEventRecord
		if calendarEventRecord == nil {
			return searchIndexRecordProjection{}, errors.New("searchable calendar event record is missing")
		}
		basePresentation.MatchingRecordAssociatedTime = calendarEventRecord.GetCalendarEventStartTime()
		basePresentation.MatchingRecordDisplayName = calendarEventRecord.GetCalendarEventDisplayName()
		basePresentation.MatchingRecordKindDisplayName = "event"
		basePresentation.DigitalContainerNamesNearestToBroadest = nonEmptyProjectionValues(
			calendarEventRecord.GetCalendarDisplayName(),
			calendarEventRecord.GetCalendarAccountDisplayName(),
		)
		location := calendarEventRecord.GetCalendarEventLocation()
		if location != nil {
			basePresentation.PhysicalPlaceNamesSpecificToBroadest = nonEmptyProjectionValues(
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
			participants = appendNonDuplicateProjectionValue(
				participants,
				attendee.GetCalendarEventAttendeeSourceAttendanceStatus(),
			)
		}
		sections := []searchIndexTextSection{
			{displayName: "Summary", content: calendarEventRecord.GetCalendarEventDisplayName()},
			{displayName: "Description", content: calendarEventRecord.GetCalendarEventDescription()},
			{displayName: "Location", content: strings.Join(nonEmptyProjectionValues(
				locationDisplayName(location), locationAddress(location),
			), "\n")},
			{displayName: "Participants", content: strings.Join(nonEmptyProjectionValues(participants...), "\n")},
		}
		return completeSearchIndexRecordProjection(basePresentation, sections), nil

	case *searchablerecord.SearchableArchiveRecord_OpenedNoteRecord:
		noteRecord := typedRecord.OpenedNoteRecord
		if noteRecord == nil {
			return searchIndexRecordProjection{}, errors.New("searchable opened note record is missing")
		}
		basePresentation.MatchingRecordAssociatedTime = exactTimeForSearchPresentation(noteRecord.GetOpenedNoteVersionTime())
		basePresentation.MatchingRecordDisplayName = noteRecord.GetNoteDisplayName()
		basePresentation.MatchingRecordKindDisplayName = "note version"
		basePresentation.DigitalContainerNamesNearestToBroadest = nonEmptyProjectionValues(
			noteRecord.GetNoteFolderDisplayName(),
		)
		sections := []searchIndexTextSection{
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
		}
		return completeSearchIndexRecordProjection(basePresentation, sections), nil

	case *searchablerecord.SearchableArchiveRecord_TrawlerSpecificRecord:
		trawlerSpecificRecord := typedRecord.TrawlerSpecificRecord
		if trawlerSpecificRecord == nil || trawlerSpecificRecord.GetDetailPresentation() == nil {
			return searchIndexRecordProjection{}, errors.New("trawler-specific searchable record is missing")
		}
		detail := trawlerSpecificRecord.GetDetailPresentation()
		basePresentation.MatchingRecordAssociatedTime = exactTimeForSearchPresentation(
			trawlerSpecificRecord.GetArchiveRecordAssociatedTime(),
		)
		basePresentation.MatchingRecordDisplayName = detail.GetDetailDisplayName()
		basePresentation.MatchingRecordKindDisplayName = "message"
		sections := make([]searchIndexTextSection, 0, 2)
		if detail.GetDetailDisplayNameAnchor() != nil {
			sections = append(sections, searchIndexTextSection{
				displayName:  "Subject",
				content:      detail.GetDetailDisplayName(),
				recordAnchor: detail.GetDetailDisplayNameAnchor(),
			})
		}
		sections = append(sections, searchIndexTextSection{
			displayName:  "Message",
			content:      detail.GetBodyText(),
			recordAnchor: detail.GetBodyAnchor(),
		})
		return completeSearchIndexRecordProjection(basePresentation, sections), nil
	default:
		return searchIndexRecordProjection{}, errors.New("searchable archive record has no typed source record")
	}
}

func appendCalendarPersonSearchValues(
	values []string,
	relatedPerson *person.PersonRelatedToArchiveRecord,
) []string {
	if relatedPerson == nil {
		return values
	}
	values = appendNonDuplicateProjectionValue(values, relatedPerson.GetPersonDisplayName())
	for _, contactMethod := range relatedPerson.GetPersonContactMethodsInDisplayOrder() {
		if contactMethod != nil {
			values = appendNonDuplicateProjectionValue(values, contactMethod.GetPersonContactMethodDisplayValue())
		}
	}
	return values
}

func appendNonDuplicateProjectionValue(values []string, candidate string) []string {
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

func completeSearchIndexRecordProjection(
	basePresentation *search.SearchMatchPresentation,
	sections []searchIndexTextSection,
) searchIndexRecordProjection {
	nonEmptySections := make([]searchIndexTextSection, 0, len(sections))
	for _, section := range sections {
		section.content = strings.TrimSpace(section.content)
		if section.content != "" {
			nonEmptySections = append(nonEmptySections, section)
		}
	}
	return searchIndexRecordProjection{
		recordKindDisplayName: basePresentation.GetMatchingRecordKindDisplayName(),
		associatedTime:        basePresentation.GetMatchingRecordAssociatedTime(),
		textSections:          nonEmptySections,
		searchPresentation:    basePresentation,
	}
}

func exactTimeForSearchPresentation(
	exactTime *timestamppb.Timestamp,
) *presentation.ArchiveRecordAssociatedTimeForDisplay {
	if exactTime == nil || !exactTime.IsValid() {
		return nil
	}
	return &presentation.ArchiveRecordAssociatedTimeForDisplay{
		ArchiveRecordAssociatedTime: &presentation.ArchiveRecordAssociatedTimeForDisplay_ExactTime{
			ExactTime: exactTime,
		},
	}
}

func nonEmptyProjectionValues(values ...string) []string {
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

func searchMatchTextFieldFromCandidate(
	fieldName string,
	content string,
) []*search.SearchMatchTextField {
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
