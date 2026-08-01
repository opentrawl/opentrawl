package render

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"

	command "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command"
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

func WriteTrawlerCommandResponse(
	writer io.Writer,
	response *command.TrawlerCommandResponse,
	globallyRoutableTrawlLinksByCanonicalRecordReference GloballyRoutableTrawlLinksByCanonicalArchiveRecordReference,
	context TrawlerCommandRenderContext,
) error {
	if response == nil {
		return fmt.Errorf("trawler command response is missing")
	}
	var err error
	switch typedResponse := response.GetTypedTrawlerCommandResponse().(type) {
	case *command.TrawlerCommandResponse_MessageListResponse:
		err = WriteTrawlerMessageListResponse(writer, typedResponse.MessageListResponse, globallyRoutableTrawlLinksByCanonicalRecordReference)
	case *command.TrawlerCommandResponse_ConversationListResponse:
		err = WriteConversationListResponse(writer, typedResponse.ConversationListResponse, globallyRoutableTrawlLinksByCanonicalRecordReference)
	case *command.TrawlerCommandResponse_PersonListResponse:
		err = WritePersonListResponse(writer, typedResponse.PersonListResponse, globallyRoutableTrawlLinksByCanonicalRecordReference)
	case *command.TrawlerCommandResponse_PersonRecord:
		personRecord := typedResponse.PersonRecord
		err = WritePersonRecord(
			writer,
			personRecord,
			globallyRoutableTrawlLinksByCanonicalRecordReference.
				globallyRoutableTrawlLinkForCanonicalArchiveRecordReference(
					personRecord.GetCanonicalRecordReference(),
				),
		)
	case *command.TrawlerCommandResponse_CalendarEventListResponse:
		err = WriteCalendarEventListResponse(writer, typedResponse.CalendarEventListResponse, globallyRoutableTrawlLinksByCanonicalRecordReference)
	case *command.TrawlerCommandResponse_CalendarListResponse:
		err = WriteCalendarListResponse(writer, typedResponse.CalendarListResponse, globallyRoutableTrawlLinksByCanonicalRecordReference)
	case *command.TrawlerCommandResponse_NoteListResponse:
		err = WriteNoteListResponse(writer, typedResponse.NoteListResponse, globallyRoutableTrawlLinksByCanonicalRecordReference)
	case *command.TrawlerCommandResponse_NoteFolderListResponse:
		err = WriteNoteFolderListResponse(writer, typedResponse.NoteFolderListResponse)
	case *command.TrawlerCommandResponse_RecoveredNoteVersionListResponse:
		err = WriteRecoveredNoteVersionListResponse(writer, typedResponse.RecoveredNoteVersionListResponse, globallyRoutableTrawlLinksByCanonicalRecordReference)
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
	hints := make([]string, 0, 2)
	if trawlerCommandResponseListsRecordsOpenedByRootOpen(response) &&
		globallyRoutableTrawlLinkExists(globallyRoutableTrawlLinksByCanonicalRecordReference) {
		hints = append(hints, "Open: "+trawlCommandLineForDisplay(writer, []string{"open", "LINK"}))
	}
	if trawlerCommandResponseHasMore(response) {
		if len(context.MoreTrawlerCommandArgumentsAfterTrawlInvocation) > 0 {
			hints = append(hints, "More: "+trawlCommandLineForDisplay(
				writer,
				context.MoreTrawlerCommandArgumentsAfterTrawlInvocation,
			))
		}
	}
	if len(hints) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(writer); err != nil {
		return err
	}
	for _, hint := range hints {
		if err := WriteTrawlCommandHint(writer, hint); err != nil {
			return err
		}
	}
	return nil
}

func trawlerCommandResponseListsRecordsOpenedByRootOpen(
	response *command.TrawlerCommandResponse,
) bool {
	if response.GetCalendarListResponse() != nil {
		return false
	}
	return trawlerCommandResponseIsList(response)
}

func trawlerCommandResponseIsList(response *command.TrawlerCommandResponse) bool {
	switch typedResponse := response.GetTypedTrawlerCommandResponse().(type) {
	case *command.TrawlerCommandResponse_MessageListResponse,
		*command.TrawlerCommandResponse_ConversationListResponse,
		*command.TrawlerCommandResponse_PersonListResponse,
		*command.TrawlerCommandResponse_CalendarEventListResponse,
		*command.TrawlerCommandResponse_CalendarListResponse,
		*command.TrawlerCommandResponse_NoteListResponse,
		*command.TrawlerCommandResponse_RecoveredNoteVersionListResponse:
		return true
	case *command.TrawlerCommandResponse_TrawlerSpecificCommandResponse:
		return typedResponse.TrawlerSpecificCommandResponse.GetTrawlerSpecificCommandListPresentation() != nil
	default:
		return false
	}
}

func trawlerCommandResponseHasMore(response *command.TrawlerCommandResponse) bool {
	switch typedResponse := response.GetTypedTrawlerCommandResponse().(type) {
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
		return WriteTrawlerSpecificCommandListPresentation(
			writer,
			presentation.TrawlerSpecificCommandListPresentation,
			globallyRoutableTrawlLinksByCanonicalRecordReference,
			actions.ListRowActionsInDisplayOrder,
		)
	case *command.TrawlerSpecificCommandResponse_TrawlerSpecificCommandDetailPresentation:
		return WriteTrawlerSpecificCommandDetailPresentation(
			writer,
			presentation.TrawlerSpecificCommandDetailPresentation,
			globallyRoutableTrawlLinksByCanonicalRecordReference,
			actions.DetailActionsInDisplayOrder,
		)
	default:
		return fmt.Errorf("trawler-specific command response has no presentation")
	}
}

func globallyRoutableTrawlLinkExists(
	globallyRoutableTrawlLinksByCanonicalRecordReference GloballyRoutableTrawlLinksByCanonicalArchiveRecordReference,
) bool {
	for _, globallyRoutableTrawlLink := range globallyRoutableTrawlLinksByCanonicalRecordReference {
		if globallyRoutableTrawlLinkText(globallyRoutableTrawlLink.GloballyRoutableTrawlLink) != "" {
			return true
		}
	}
	return false
}

func trawlCommandLineForDisplay(writer io.Writer, argumentsAfterTrawlInvocation []string) string {
	commandParts := make([]string, 0, len(argumentsAfterTrawlInvocation)+1)
	commandParts = append(commandParts, quoteShellArgumentForDisplay(TrawlInvocationDisplay(writer)))
	for _, argument := range argumentsAfterTrawlInvocation {
		commandParts = append(commandParts, quoteShellArgumentForDisplay(argument))
	}
	return strings.Join(commandParts, " ")
}

func quoteShellArgumentForDisplay(argument string) string {
	if argument != "" && strings.IndexFunc(argument, func(character rune) bool {
		return !unicode.IsLetter(character) &&
			!unicode.IsDigit(character) &&
			!strings.ContainsRune("_-./:@%+=,", character)
	}) == -1 {
		return argument
	}
	return "'" + strings.ReplaceAll(argument, "'", "'\"'\"'") + "'"
}
