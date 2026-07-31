package render

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"

	commandv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command/v1"
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
	response *commandv1.TrawlerCommandResponse,
	globallyRoutableTrawlLinksByCanonicalRecordReference GloballyRoutableTrawlLinksByCanonicalArchiveRecordReference,
	context TrawlerCommandRenderContext,
) error {
	if response == nil {
		return fmt.Errorf("trawler command response is missing")
	}
	var err error
	switch typedResponse := response.GetTypedTrawlerCommandResponse().(type) {
	case *commandv1.TrawlerCommandResponse_MessageListResponse:
		err = WriteTrawlerMessageListResponse(writer, typedResponse.MessageListResponse, globallyRoutableTrawlLinksByCanonicalRecordReference)
	case *commandv1.TrawlerCommandResponse_ConversationListResponse:
		err = WriteConversationListResponse(writer, typedResponse.ConversationListResponse, globallyRoutableTrawlLinksByCanonicalRecordReference)
	case *commandv1.TrawlerCommandResponse_PersonListResponse:
		err = WritePersonListResponse(writer, typedResponse.PersonListResponse, globallyRoutableTrawlLinksByCanonicalRecordReference)
	case *commandv1.TrawlerCommandResponse_PersonRecord:
		personRecord := typedResponse.PersonRecord
		err = WritePersonRecord(
			writer,
			personRecord,
			globallyRoutableTrawlLinksByCanonicalRecordReference.
				globallyRoutableTrawlLinkForCanonicalArchiveRecordReference(
					personRecord.GetCanonicalRecordReference(),
				),
		)
	case *commandv1.TrawlerCommandResponse_CalendarEventListResponse:
		err = WriteCalendarEventListResponse(writer, typedResponse.CalendarEventListResponse, globallyRoutableTrawlLinksByCanonicalRecordReference)
	case *commandv1.TrawlerCommandResponse_TrawlerSpecificCommandResponse:
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
	if trawlerCommandResponseIsList(response) &&
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

func trawlerCommandResponseIsList(response *commandv1.TrawlerCommandResponse) bool {
	switch typedResponse := response.GetTypedTrawlerCommandResponse().(type) {
	case *commandv1.TrawlerCommandResponse_MessageListResponse,
		*commandv1.TrawlerCommandResponse_ConversationListResponse,
		*commandv1.TrawlerCommandResponse_PersonListResponse,
		*commandv1.TrawlerCommandResponse_CalendarEventListResponse:
		return true
	case *commandv1.TrawlerCommandResponse_TrawlerSpecificCommandResponse:
		return typedResponse.TrawlerSpecificCommandResponse.GetTrawlerSpecificCommandListPresentation() != nil
	default:
		return false
	}
}

func trawlerCommandResponseHasMore(response *commandv1.TrawlerCommandResponse) bool {
	switch typedResponse := response.GetTypedTrawlerCommandResponse().(type) {
	case *commandv1.TrawlerCommandResponse_MessageListResponse:
		return typedResponse.MessageListResponse.GetMoreMatchingMessagesExist()
	case *commandv1.TrawlerCommandResponse_ConversationListResponse:
		return typedResponse.ConversationListResponse.GetMoreConversationRecordsExist()
	case *commandv1.TrawlerCommandResponse_PersonListResponse:
		return typedResponse.PersonListResponse.GetMoreMatchingPeopleExist()
	case *commandv1.TrawlerCommandResponse_CalendarEventListResponse:
		return typedResponse.CalendarEventListResponse.GetMoreMatchingCalendarEventsExist()
	case *commandv1.TrawlerCommandResponse_TrawlerSpecificCommandResponse:
		return typedResponse.TrawlerSpecificCommandResponse.GetTrawlerSpecificCommandListPresentation().GetMoreRowsExist()
	default:
		return false
	}
}

func writeTrawlerSpecificCommandResponse(
	writer io.Writer,
	response *commandv1.TrawlerSpecificCommandResponse,
	globallyRoutableTrawlLinksByCanonicalRecordReference GloballyRoutableTrawlLinksByCanonicalArchiveRecordReference,
	actions TrawlerSpecificCommandActions,
) error {
	if response == nil {
		return fmt.Errorf("trawler-specific command response is missing")
	}
	switch presentation := response.GetTrawlerSpecificCommandPresentation().(type) {
	case *commandv1.TrawlerSpecificCommandResponse_TrawlerSpecificCommandListPresentation:
		return WriteTrawlerSpecificCommandListPresentation(
			writer,
			presentation.TrawlerSpecificCommandListPresentation,
			globallyRoutableTrawlLinksByCanonicalRecordReference,
			actions.ListRowActionsInDisplayOrder,
		)
	case *commandv1.TrawlerSpecificCommandResponse_TrawlerSpecificCommandDetailPresentation:
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
