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
	uniqueRecordReferences := make([]*CanonicalArchiveRecordReference, 0, len(recordReferences))
	seenRecordReferences := make(map[string]struct{}, len(recordReferences))
	for _, recordReference := range recordReferences {
		recordReferenceText := CanonicalArchiveRecordReferenceText(recordReference)
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
	switch presentation := response.GetTrawlerSpecificCommandPresentation().(type) {
	case *command.TrawlerSpecificCommandResponse_TrawlerSpecificCommandListPresentation:
		for _, row := range presentation.TrawlerSpecificCommandListPresentation.GetRowsInDisplayOrder() {
			if row == nil {
				continue
			}
			for _, value := range row.GetColumnValuesInDisplayOrder() {
				add(trawlerSpecificCommandPresentationCanonicalRecordReference(value))
			}
		}
	case *command.TrawlerSpecificCommandResponse_TrawlerSpecificCommandDetailPresentation:
		for _, field := range presentation.TrawlerSpecificCommandDetailPresentation.GetFieldsInDisplayOrder() {
			if field != nil {
				add(trawlerSpecificCommandPresentationCanonicalRecordReference(field.GetFieldValue()))
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
