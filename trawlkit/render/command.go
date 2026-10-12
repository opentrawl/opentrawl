package render

import (
	"fmt"
	"io"
	"strconv"

	command "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command"
	identity "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/identity"
)

type TrawlerCommandRenderContext struct {
	MoreTrawlerCommandArgumentsAfterTrawlInvocation []string
	MoreTrawlerCommandMaximumReturnedRowCount       uint64
	TrawlerSpecificCommandActions                   TrawlerSpecificCommandActions
}

func (context TrawlerCommandRenderContext) WithMoreTrawlerCommandArgumentsBeforeMaximumReturnedRowCount(
	moreTrawlerCommandArgumentsBeforeMaximumReturnedRowCount []string,
) TrawlerCommandRenderContext {
	if len(context.MoreTrawlerCommandArgumentsAfterTrawlInvocation) == 0 {
		return context
	}
	context.MoreTrawlerCommandArgumentsAfterTrawlInvocation = append(
		append(
			[]string(nil),
			moreTrawlerCommandArgumentsBeforeMaximumReturnedRowCount...,
		),
		"--limit",
		strconv.FormatUint(context.MoreTrawlerCommandMaximumReturnedRowCount, 10),
	)
	return context
}

// WriteTrawlerCommandResponse writes what a trawler command returned, then
// the command that shows more rows when there are more.
func (output TrawlCommandOutput) WriteTrawlerCommandResponse(
	response *command.TrawlerCommandResponse,
	globallyRoutableTrawlLinksByCanonicalRecordReference GloballyRoutableTrawlLinksByCanonicalArchiveRecordReference,
	context TrawlerCommandRenderContext,
) error {
	writer := output.standardOutput
	if response == nil {
		return fmt.Errorf("trawler command response is missing")
	}
	var err error
	switch typedResponse := response.GetTypedTrawlerCommandResponse().(type) {
	case *command.TrawlerCommandResponse_PhotosStepRunResponse:
		err = writePhotosStepRunResponse(writer, typedResponse.PhotosStepRunResponse)
	case *command.TrawlerCommandResponse_PhotoListResponse:
		err = writePhotoListResponse(writer, typedResponse.PhotoListResponse, globallyRoutableTrawlLinksByCanonicalRecordReference)
	case *command.TrawlerCommandResponse_PhotoLocalDayListResponse:
		err = writePhotoLocalDayListResponse(writer, typedResponse.PhotoLocalDayListResponse)
	case *command.TrawlerCommandResponse_PhotoInspectionResponse:
		err = writePhotoInspectionResponse(writer, typedResponse.PhotoInspectionResponse, globallyRoutableTrawlLinksByCanonicalRecordReference)
	case *command.TrawlerCommandResponse_MessageListResponse:
		err = writeTrawlerMessageListResponse(writer, typedResponse.MessageListResponse, globallyRoutableTrawlLinksByCanonicalRecordReference)
	case *command.TrawlerCommandResponse_ConversationListResponse:
		err = writeConversationListResponse(writer, typedResponse.ConversationListResponse, globallyRoutableTrawlLinksByCanonicalRecordReference)
	case *command.TrawlerCommandResponse_PersonListResponse:
		err = writePersonListResponse(writer, typedResponse.PersonListResponse, globallyRoutableTrawlLinksByCanonicalRecordReference)
	case *command.TrawlerCommandResponse_PersonRecord:
		personRecord := typedResponse.PersonRecord
		err = writePersonRecord(
			writer,
			personRecord,
			globallyRoutableTrawlLinksByCanonicalRecordReference.
				globallyRoutableTrawlLinkForCanonicalArchiveRecordReference(
					personRecord.GetCanonicalRecordReference(),
				),
		)
	case *command.TrawlerCommandResponse_CalendarEventListResponse:
		err = writeCalendarEventListResponse(writer, typedResponse.CalendarEventListResponse, globallyRoutableTrawlLinksByCanonicalRecordReference)
	case *command.TrawlerCommandResponse_CalendarListResponse:
		err = writeCalendarListResponse(writer, typedResponse.CalendarListResponse, globallyRoutableTrawlLinksByCanonicalRecordReference)
	case *command.TrawlerCommandResponse_NoteListResponse:
		err = writeNoteListResponse(writer, typedResponse.NoteListResponse, globallyRoutableTrawlLinksByCanonicalRecordReference)
	case *command.TrawlerCommandResponse_NoteFolderListResponse:
		err = writeNoteFolderListResponse(writer, typedResponse.NoteFolderListResponse)
	case *command.TrawlerCommandResponse_RecoveredNoteVersionListResponse:
		err = writeRecoveredNoteVersionListResponse(writer, typedResponse.RecoveredNoteVersionListResponse, globallyRoutableTrawlLinksByCanonicalRecordReference)
	case *command.TrawlerCommandResponse_TrawlerSpecificCommandResponse:
		err = writeTrawlerSpecificCommandResponse(
			writer,
			typedResponse.TrawlerSpecificCommandResponse,
			globallyRoutableTrawlLinksByCanonicalRecordReference,
			context.TrawlerSpecificCommandActions,
		)
	default:
		return fmt.Errorf("trawler command response has no typed response")
	}
	if err != nil {
		return err
	}
	nextCommands := []NextCommand{
		nextCommandForFirstRowOfTrawlerCommandResponse(response, globallyRoutableTrawlLinksByCanonicalRecordReference),
	}
	if trawlerCommandResponseHasMore(response) && len(context.MoreTrawlerCommandArgumentsAfterTrawlInvocation) > 0 {
		nextCommands = append(nextCommands, NextCommandShowingMoreRows(context.MoreTrawlerCommandArgumentsAfterTrawlInvocation))
	}
	return writeNextCommands(writer, nextCommands)
}

// nextCommandForFirstRowOfTrawlerCommandResponse is what a reader does with
// a list's first row: open a record, list a conversation's messages, list
// conversations with a person or list a calendar's events.
func nextCommandForFirstRowOfTrawlerCommandResponse(
	response *command.TrawlerCommandResponse,
	globallyRoutableTrawlLinks GloballyRoutableTrawlLinksByCanonicalArchiveRecordReference,
) NextCommand {
	linkForRecord := func(canonicalRecordReference *identity.CanonicalArchiveRecordReference) string {
		return globallyRoutableTrawlLinkText(
			globallyRoutableTrawlLinks.globallyRoutableTrawlLinkForCanonicalArchiveRecordReference(canonicalRecordReference),
		)
	}
	switch typedResponse := response.GetTypedTrawlerCommandResponse().(type) {
	case *command.TrawlerCommandResponse_PhotoListResponse:
		if records := typedResponse.PhotoListResponse.GetPhotoRecordsNewestFirst(); len(records) > 0 {
			return nextCommandOpeningRecord(linkForRecord(records[0].GetCanonicalRecordReference()))
		}
		return nextCommandWithLabel(nextCommandLabelHelp, "photos", "--help")
	case *command.TrawlerCommandResponse_PhotoLocalDayListResponse:
		if records := typedResponse.PhotoLocalDayListResponse.GetPhotoLocalDayRecordsNewestFirst(); len(records) > 0 {
			return nextCommandWithLabel(nextCommandLabelPhotos, "photos", "--on", formatCalendarDate(records[0].GetPhotoLocalDate()))
		}
		return nextCommandWithLabel(nextCommandLabelHelp, "photos", "days", "--help")
	case *command.TrawlerCommandResponse_MessageListResponse:
		if messageRecords := typedResponse.MessageListResponse.GetMessageRecordsNewestFirst(); len(messageRecords) > 0 {
			messageRecord := messageRecords[0]
			return nextCommandOpeningRecord(linkForRecord(messageRecord.GetCanonicalRecordReference()))
		}
	case *command.TrawlerCommandResponse_ConversationListResponse:
		if conversationRecords := typedResponse.ConversationListResponse.GetConversationRecordsNewestFirst(); len(conversationRecords) > 0 {
			conversationRecord := conversationRecords[0]
			return nextCommandListingConversationMessages(linkForRecord(conversationRecord.GetCanonicalRecordReference()))
		}
	case *command.TrawlerCommandResponse_PersonListResponse:
		if personRecords := typedResponse.PersonListResponse.GetPersonRecordsInDisplayOrder(); len(personRecords) > 0 {
			personRecord := personRecords[0]
			return nextCommandListingConversationsWithPerson(linkForRecord(personRecord.GetCanonicalRecordReference()))
		}
	case *command.TrawlerCommandResponse_CalendarEventListResponse:
		if calendarEventRecords := typedResponse.CalendarEventListResponse.GetCalendarEventRecordsInDisplayOrder(); len(calendarEventRecords) > 0 {
			calendarEventRecord := calendarEventRecords[0]
			return nextCommandOpeningRecord(linkForRecord(calendarEventRecord.GetCanonicalRecordReference()))
		}
	case *command.TrawlerCommandResponse_CalendarListResponse:
		for _, calendarRecord := range typedResponse.CalendarListResponse.GetCalendarRecordsInDisplayOrder() {
			if calendarRecord.GetActiveOrFutureCalendarEventCount() > 0 {
				return nextCommandListingCalendarEvents(linkForRecord(calendarRecord.GetCanonicalRecordReference()))
			}
		}
	case *command.TrawlerCommandResponse_NoteListResponse:
		if noteRecords := typedResponse.NoteListResponse.GetNoteRecordsNewestFirst(); len(noteRecords) > 0 {
			noteRecord := noteRecords[0]
			return nextCommandOpeningRecord(linkForRecord(noteRecord.GetCanonicalRecordReference()))
		}
	case *command.TrawlerCommandResponse_RecoveredNoteVersionListResponse:
		if versionRecords := typedResponse.RecoveredNoteVersionListResponse.GetRecoveredNoteVersionRecordsNewestFirst(); len(versionRecords) > 0 {
			versionRecord := versionRecords[0]
			return nextCommandOpeningRecord(linkForRecord(versionRecord.GetCanonicalRecordReference()))
		}
	}
	return NextCommand{}
}

func trawlerCommandResponseHasMore(response *command.TrawlerCommandResponse) bool {
	switch typedResponse := response.GetTypedTrawlerCommandResponse().(type) {
	case *command.TrawlerCommandResponse_PhotoListResponse:
		return typedResponse.PhotoListResponse.GetMoreMatchingPhotosExist()
	case *command.TrawlerCommandResponse_PhotoLocalDayListResponse:
		return typedResponse.PhotoLocalDayListResponse.GetMoreMatchingLocalDaysExist()
	case *command.TrawlerCommandResponse_MessageListResponse:
		return typedResponse.MessageListResponse.GetMoreMatchingMessagesExist()
	case *command.TrawlerCommandResponse_ConversationListResponse:
		return typedResponse.ConversationListResponse.GetMoreConversationRecordsExist()
	case *command.TrawlerCommandResponse_PersonListResponse:
		return typedResponse.PersonListResponse.GetMoreMatchingPeopleExist()
	case *command.TrawlerCommandResponse_CalendarEventListResponse:
		return typedResponse.CalendarEventListResponse.GetMoreMatchingCalendarEventsExist()
	case *command.TrawlerCommandResponse_NoteListResponse:
		return typedResponse.NoteListResponse.GetMoreMatchingNotesExist()
	case *command.TrawlerCommandResponse_RecoveredNoteVersionListResponse:
		return typedResponse.RecoveredNoteVersionListResponse.GetMoreRecoveredNoteVersionsExist()
	case *command.TrawlerCommandResponse_TrawlerSpecificCommandResponse:
		return typedResponse.TrawlerSpecificCommandResponse.GetTrawlerSpecificCommandListPresentation().GetMoreRowsExist()
	default:
		return false
	}
}

func writeTrawlerSpecificCommandResponse(
	writer io.Writer,
	response *command.TrawlerSpecificCommandResponse,
	globallyRoutableTrawlLinksByCanonicalRecordReference GloballyRoutableTrawlLinksByCanonicalArchiveRecordReference,
	actions TrawlerSpecificCommandActions,
) error {
	if response == nil {
		return fmt.Errorf("trawler-specific command response is missing")
	}
	switch presentation := response.GetTrawlerSpecificCommandPresentation().(type) {
	case *command.TrawlerSpecificCommandResponse_TrawlerSpecificCommandListPresentation:
		return writeTrawlerSpecificCommandListPresentation(
			writer,
			presentation.TrawlerSpecificCommandListPresentation,
			globallyRoutableTrawlLinksByCanonicalRecordReference,
			actions.ListRowActionsInDisplayOrder,
		)
	case *command.TrawlerSpecificCommandResponse_TrawlerSpecificCommandDetailPresentation:
		return writeTrawlerSpecificCommandDetailPresentation(
			writer,
			presentation.TrawlerSpecificCommandDetailPresentation,
			globallyRoutableTrawlLinksByCanonicalRecordReference,
			actions.DetailActionsInDisplayOrder,
			false,
		)
	default:
		return fmt.Errorf("trawler-specific command response has no presentation")
	}
}
