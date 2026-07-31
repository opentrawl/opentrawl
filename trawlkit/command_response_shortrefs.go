package trawlkit

import (
	"context"

	commandv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
)

func trawlerCommandResponseLocalShortReferencesByCanonicalRecordReference(
	ctx context.Context,
	request *TrawlerCommandExecutionRequest,
	response *commandv1.TrawlerCommandResponse,
) ([]CanonicalArchiveRecordReferenceWithLocalTrawlerShortReference, error) {
	return request.LocalShortReferencesForCanonicalArchiveRecordReferences(
		ctx,
		trawlerCommandResponseCanonicalRecordReferences(response),
	)
}

func trawlerCommandResponseCanonicalRecordReferences(
	response *commandv1.TrawlerCommandResponse,
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
	case *commandv1.TrawlerCommandResponse_MessageListResponse:
		for _, messageRecord := range typedResponse.MessageListResponse.GetMessageRecordsInDisplayOrder() {
			if messageRecord != nil {
				add(messageRecord.GetCanonicalRecordReference())
			}
		}
	case *commandv1.TrawlerCommandResponse_ConversationListResponse:
		for _, conversationRecord := range typedResponse.ConversationListResponse.GetConversationRecordsNewestFirst() {
			if conversationRecord != nil {
				add(conversationRecord.GetCanonicalRecordReference())
			}
		}
	case *commandv1.TrawlerCommandResponse_PersonListResponse:
		for _, personRecord := range typedResponse.PersonListResponse.GetPersonRecordsInDisplayOrder() {
			if personRecord != nil {
				add(personRecord.GetCanonicalRecordReference())
			}
		}
	case *commandv1.TrawlerCommandResponse_PersonRecord:
		add(typedResponse.PersonRecord.GetCanonicalRecordReference())
	case *commandv1.TrawlerCommandResponse_CalendarEventListResponse:
		for _, calendarEventRecord := range typedResponse.CalendarEventListResponse.GetCalendarEventRecordsInDisplayOrder() {
			if calendarEventRecord != nil {
				add(calendarEventRecord.GetCanonicalRecordReference())
			}
		}
	case *commandv1.TrawlerCommandResponse_TrawlerSpecificCommandResponse:
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
	response *commandv1.TrawlerSpecificCommandResponse,
	add func(*CanonicalArchiveRecordReference),
) {
	if response == nil {
		return
	}
	switch presentation := response.GetTrawlerSpecificCommandPresentation().(type) {
	case *commandv1.TrawlerSpecificCommandResponse_TrawlerSpecificCommandListPresentation:
		for _, row := range presentation.TrawlerSpecificCommandListPresentation.GetRowsInDisplayOrder() {
			if row == nil {
				continue
			}
			for _, value := range row.GetColumnValuesInDisplayOrder() {
				add(trawlerSpecificCommandPresentationCanonicalRecordReference(value))
			}
		}
	case *commandv1.TrawlerSpecificCommandResponse_TrawlerSpecificCommandDetailPresentation:
		for _, field := range presentation.TrawlerSpecificCommandDetailPresentation.GetFieldsInDisplayOrder() {
			if field != nil {
				add(trawlerSpecificCommandPresentationCanonicalRecordReference(field.GetFieldValue()))
			}
		}
	}
}

func trawlerSpecificCommandPresentationCanonicalRecordReference(
	value *presentationv1.TrawlerSpecificCommandPresentationValue,
) *CanonicalArchiveRecordReference {
	if value == nil {
		return nil
	}
	canonicalRecordReference, ok := value.GetTypedValue().(*presentationv1.TrawlerSpecificCommandPresentationValue_CanonicalRecordReference)
	if !ok {
		return nil
	}
	return canonicalRecordReference.CanonicalRecordReference
}
