package trawlkit

import (
	"context"

	command "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
)

func trawlerCommandResponseLocalShortReferencesByCanonicalRecordReference(
	ctx context.Context,
	request *TrawlerCommandExecutionRequest,
	response *command.TrawlerCommandResponse,
) ([]CanonicalArchiveRecordReferenceWithLocalTrawlerShortReference, error) {
	return request.LocalShortReferencesForCanonicalArchiveRecordReferences(
		ctx,
		trawlerCommandResponseCanonicalRecordReferences(response),
	)
}

func trawlerCommandResponseCanonicalRecordReferences(
	response *command.TrawlerCommandResponse,
) []*CanonicalArchiveRecordReference {
	if response == nil {
		return nil
	}
	var recordReferences []*CanonicalArchiveRecordReference
	add := func(recordReference *CanonicalArchiveRecordReference) {
		if CanonicalArchiveRecordReferenceText(recordReference) != "" {
			recordReferences = append(recordReferences, recordReference)
		}
	}
	switch typedResponse := response.GetTypedTrawlerCommandResponse().(type) {
	case *command.TrawlerCommandResponse_PhotoListResponse:
		for _, photoRecord := range typedResponse.PhotoListResponse.GetPhotoRecordsNewestFirst() {
			add(photoRecord.GetCanonicalRecordReference())
		}
	case *command.TrawlerCommandResponse_PhotoInspectionResponse:
		add(typedResponse.PhotoInspectionResponse.GetPhotoRecord().GetCanonicalRecordReference())
	case *command.TrawlerCommandResponse_MessageListResponse:
		for _, messageRecord := range typedResponse.MessageListResponse.GetMessageRecordsNewestFirst() {
			if messageRecord != nil {
				add(messageRecord.GetCanonicalRecordReference())
			}
		}
	case *command.TrawlerCommandResponse_ConversationListResponse:
		for _, conversationRecord := range typedResponse.ConversationListResponse.GetConversationRecordsNewestFirst() {
			if conversationRecord != nil {
				add(conversationRecord.GetCanonicalRecordReference())
			}
		}
	case *command.TrawlerCommandResponse_PersonListResponse:
		for _, personRecord := range typedResponse.PersonListResponse.GetPersonRecordsInDisplayOrder() {
			if personRecord != nil {
				add(personRecord.GetCanonicalRecordReference())
			}
		}
	case *command.TrawlerCommandResponse_PersonRecord:
		add(typedResponse.PersonRecord.GetCanonicalRecordReference())
	case *command.TrawlerCommandResponse_CalendarEventListResponse:
		for _, calendarEventRecord := range typedResponse.CalendarEventListResponse.GetCalendarEventRecordsInDisplayOrder() {
			if calendarEventRecord != nil {
				add(calendarEventRecord.GetCanonicalRecordReference())
			}
		}
	case *command.TrawlerCommandResponse_CalendarListResponse:
		for _, calendarRecord := range typedResponse.CalendarListResponse.GetCalendarRecordsInDisplayOrder() {
			if calendarRecord != nil {
				add(calendarRecord.GetCanonicalRecordReference())
			}
		}
	case *command.TrawlerCommandResponse_NoteListResponse:
		for _, noteRecord := range typedResponse.NoteListResponse.GetNoteRecordsNewestFirst() {
			if noteRecord != nil {
				add(noteRecord.GetCanonicalRecordReference())
			}
		}
	case *command.TrawlerCommandResponse_RecoveredNoteVersionListResponse:
		for _, versionRecord := range typedResponse.RecoveredNoteVersionListResponse.GetRecoveredNoteVersionRecordsNewestFirst() {
			if versionRecord != nil {
				add(versionRecord.GetCanonicalRecordReference())
			}
		}
	case *command.TrawlerCommandResponse_TrawlerSpecificCommandResponse:
		addTrawlerSpecificCommandResponseCanonicalRecordReferences(typedResponse.TrawlerSpecificCommandResponse, add)
	}
	return deduplicateCanonicalRecordReferences(recordReferences)
}

func deduplicateCanonicalRecordReferences(recordReferences []*CanonicalArchiveRecordReference) []*CanonicalArchiveRecordReference {
	uniqueRecordReferences := make([]*CanonicalArchiveRecordReference, 0, len(recordReferences))
	seenRecordReferences := make(map[string]struct{}, len(recordReferences))
	for _, recordReference := range recordReferences {
		recordReferenceText := CanonicalArchiveRecordReferenceText(recordReference)
		if recordReferenceText == "" {
			continue
		}
		if _, alreadyAdded := seenRecordReferences[recordReferenceText]; alreadyAdded {
			continue
		}
		seenRecordReferences[recordReferenceText] = struct{}{}
		uniqueRecordReferences = append(uniqueRecordReferences, recordReference)
	}
	return uniqueRecordReferences
}

func addTrawlerSpecificCommandResponseCanonicalRecordReferences(
	response *command.TrawlerSpecificCommandResponse,
	add func(*CanonicalArchiveRecordReference),
) {
	if response == nil {
		return
	}
	switch presentationResponse := response.GetTrawlerSpecificCommandPresentation().(type) {
	case *command.TrawlerSpecificCommandResponse_TrawlerSpecificCommandListPresentation:
		list := presentationResponse.TrawlerSpecificCommandListPresentation
		addTrawlerSpecificRowsCanonicalRecordReferences(list.GetRowsInDisplayOrder(), add)
		for _, section := range list.GetSectionsInDisplayOrder() {
			if section != nil {
				addTrawlerSpecificRowsCanonicalRecordReferences(section.GetRowsInDisplayOrder(), add)
			}
		}
		addDeclarativeNextCommandCanonicalRecordReferences(list.GetNextCommandsInDisplayOrder(), add)
	case *command.TrawlerSpecificCommandResponse_TrawlerSpecificCommandDetailPresentation:
		addTrawlerSpecificDetailCanonicalRecordReferences(presentationResponse.TrawlerSpecificCommandDetailPresentation, add)
	}
}

func addTrawlerSpecificRowsCanonicalRecordReferences(
	rows []*presentation.TrawlerSpecificCommandListPresentationRow,
	add func(*CanonicalArchiveRecordReference),
) {
	for _, row := range rows {
		if row == nil {
			continue
		}
		for _, value := range row.GetColumnValuesInDisplayOrder() {
			add(trawlerSpecificCommandPresentationCanonicalRecordReference(value))
		}
	}
}

func addTrawlerSpecificDetailCanonicalRecordReferences(
	detail *presentation.TrawlerSpecificCommandDetailPresentation,
	add func(*CanonicalArchiveRecordReference),
) {
	if detail == nil {
		return
	}
	for _, field := range detail.GetFieldsInDisplayOrder() {
		if field != nil {
			add(trawlerSpecificCommandPresentationCanonicalRecordReference(field.GetFieldValue()))
		}
	}
	addDeclarativeNextCommandCanonicalRecordReferences(detail.GetNextCommandsInDisplayOrder(), add)
}

func addDeclarativeNextCommandCanonicalRecordReferences(
	commands []*presentation.DeclarativeTrawlCommand,
	add func(*CanonicalArchiveRecordReference),
) {
	for _, cmd := range commands {
		if cmd == nil {
			continue
		}
		for _, arg := range cmd.GetCommandArgumentsAfterTrawlInvocation() {
			if ref := arg.GetCanonicalRecordReferenceArgument(); ref != nil {
				add(ref)
			}
		}
	}
}

func trawlerSpecificCommandPresentationCanonicalRecordReference(
	value *presentation.TrawlerSpecificCommandPresentationValue,
) *CanonicalArchiveRecordReference {
	if value == nil {
		return nil
	}
	canonicalRecordReference, ok := value.GetTypedValue().(*presentation.TrawlerSpecificCommandPresentationValue_CanonicalRecordReference)
	if !ok {
		return nil
	}
	return canonicalRecordReference.CanonicalRecordReference
}
