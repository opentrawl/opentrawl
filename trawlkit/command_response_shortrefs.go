package trawlkit

import (
	"context"
	"strings"

	commandv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
)

func trawlerCommandResponseLocalShortReferenceAliasesByCanonicalRecordReference(
	ctx context.Context,
	request *TrawlerCommandExecutionRequest,
	response *commandv1.TrawlerCommandResponse,
) (map[string]string, error) {
	return readAssignedLocalShortReferenceAliasesByCanonicalRecordReference(
		ctx,
		request,
		trawlerCommandResponseCanonicalRecordReferences(response),
	)
}

func trawlerCommandResponseCanonicalRecordReferences(
	response *commandv1.TrawlerCommandResponse,
) []string {
	if response == nil {
		return nil
	}
	var recordReferences []string
	add := func(recordReference string) {
		if recordReference = strings.TrimSpace(recordReference); recordReference != "" {
			recordReferences = append(recordReferences, recordReference)
		}
	}
	switch typedResponse := response.GetTypedTrawlerCommandResponse().(type) {
	case *commandv1.TrawlerCommandResponse_MessageListResponse:
		for _, messageRecord := range typedResponse.MessageListResponse.GetMessageRecordsInDisplayOrder() {
			if messageRecord != nil {
				add(messageRecord.GetCanonicalMessageRecordReferenceForGloballyRoutableTrawlLinkAssignment())
			}
		}
	case *commandv1.TrawlerCommandResponse_ConversationListResponse:
		for _, conversationRecord := range typedResponse.ConversationListResponse.GetConversationRecordsNewestFirst() {
			if conversationRecord != nil {
				add(conversationRecord.GetCanonicalConversationRecordReferenceForGloballyRoutableTrawlLinkAssignment())
			}
		}
	case *commandv1.TrawlerCommandResponse_PersonListResponse:
		for _, personRecord := range typedResponse.PersonListResponse.GetPersonRecordsInDisplayOrder() {
			if personRecord != nil {
				add(personRecord.GetCanonicalPersonRecordReferenceForGloballyRoutableTrawlLinkAssignment())
			}
		}
	case *commandv1.TrawlerCommandResponse_PersonRecord:
		add(typedResponse.PersonRecord.GetCanonicalPersonRecordReferenceForGloballyRoutableTrawlLinkAssignment())
	case *commandv1.TrawlerCommandResponse_CalendarEventListResponse:
		for _, calendarEventRecord := range typedResponse.CalendarEventListResponse.GetCalendarEventRecordsInDisplayOrder() {
			if calendarEventRecord != nil {
				add(calendarEventRecord.GetCanonicalCalendarEventRecordReferenceForGloballyRoutableTrawlLinkAssignment())
			}
		}
	case *commandv1.TrawlerCommandResponse_TrawlerSpecificCommandResponse:
		addTrawlerSpecificCommandResponseCanonicalRecordReferences(typedResponse.TrawlerSpecificCommandResponse, add)
	}
	return uniqueStrings(recordReferences)
}

func addTrawlerSpecificCommandResponseCanonicalRecordReferences(
	response *commandv1.TrawlerSpecificCommandResponse,
	add func(string),
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
) string {
	if value == nil {
		return ""
	}
	canonicalRecordReference, ok := value.GetTypedValue().(*presentationv1.TrawlerSpecificCommandPresentationValue_CanonicalRecordReferenceForGloballyRoutableTrawlLinkAssignment)
	if !ok {
		return ""
	}
	return canonicalRecordReference.CanonicalRecordReferenceForGloballyRoutableTrawlLinkAssignment
}
