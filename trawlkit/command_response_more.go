package trawlkit

import (
	"strconv"
	"strings"

	commandv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

func trawlerCommandRenderContext(
	trawler RegisteredTrawlerDeclaration,
	command targetTrawlerCommand,
	response *commandv1.TrawlerCommandResponse,
) render.TrawlerCommandRenderContext {
	returnedRowCount, totalMatchingRowCount, totalMatchingRowCountIsLowerBound, moreMatchingRowsExist :=
		trawlerCommandResponseRowCounts(response)
	if !moreMatchingRowsExist {
		return render.TrawlerCommandRenderContext{}
	}

	nextLimit := returnedRowCount
	if returnedRowCount < ^uint64(0) {
		nextLimit++
	}
	if returnedRowCount > 0 && returnedRowCount <= ^uint64(0)/2 {
		nextLimit = returnedRowCount * 2
	}
	if !totalMatchingRowCountIsLowerBound &&
		totalMatchingRowCount > returnedRowCount &&
		totalMatchingRowCount < nextLimit {
		nextLimit = totalMatchingRowCount
	}

	invocationArguments := replaceTrawlerCommandLimitArgument(command.invocationArguments, nextLimit)
	argumentsAfterTrawlInvocation := []string{trawler.RegisteredTrawlerCommandName}
	argumentsAfterTrawlInvocation = append(argumentsAfterTrawlInvocation, command.childArgs()...)
	argumentsAfterTrawlInvocation = append(argumentsAfterTrawlInvocation, invocationArguments...)
	return render.TrawlerCommandRenderContext{
		MoreTrawlerCommandArgumentsAfterTrawlInvocation: argumentsAfterTrawlInvocation,
		MoreTrawlerCommandMaximumReturnedRowCount:       nextLimit,
	}
}

func trawlerCommandResponseRowCounts(
	response *commandv1.TrawlerCommandResponse,
) (returnedRowCount uint64, totalMatchingRowCount uint64, totalMatchingRowCountIsLowerBound bool, moreMatchingRowsExist bool) {
	if response == nil {
		return 0, 0, false, false
	}
	switch typedResponse := response.GetTypedTrawlerCommandResponse().(type) {
	case *commandv1.TrawlerCommandResponse_MessageListResponse:
		return uint64(len(typedResponse.MessageListResponse.GetMessageRecordsInDisplayOrder())),
			typedResponse.MessageListResponse.GetTotalMatchingMessageCount(),
			typedResponse.MessageListResponse.GetTotalMatchingMessageCountIsLowerBound(),
			typedResponse.MessageListResponse.GetMoreMatchingMessagesExist()
	case *commandv1.TrawlerCommandResponse_ConversationListResponse:
		returnedConversationCount := uint64(len(typedResponse.ConversationListResponse.GetConversationRecordsNewestFirst()))
		return returnedConversationCount,
			returnedConversationCount,
			typedResponse.ConversationListResponse.GetMoreConversationRecordsExist(),
			typedResponse.ConversationListResponse.GetMoreConversationRecordsExist()
	case *commandv1.TrawlerCommandResponse_PersonListResponse:
		return uint64(len(typedResponse.PersonListResponse.GetPersonRecordsInDisplayOrder())),
			typedResponse.PersonListResponse.GetTotalMatchingPersonCount(),
			typedResponse.PersonListResponse.GetTotalMatchingPersonCountIsLowerBound(),
			typedResponse.PersonListResponse.GetMoreMatchingPeopleExist()
	case *commandv1.TrawlerCommandResponse_CalendarEventListResponse:
		return uint64(len(typedResponse.CalendarEventListResponse.GetCalendarEventRecordsInDisplayOrder())),
			typedResponse.CalendarEventListResponse.GetTotalMatchingCalendarEventCount(),
			typedResponse.CalendarEventListResponse.GetTotalMatchingCalendarEventCountIsLowerBound(),
			typedResponse.CalendarEventListResponse.GetMoreMatchingCalendarEventsExist()
	case *commandv1.TrawlerCommandResponse_TrawlerSpecificCommandResponse:
		listPresentation := typedResponse.TrawlerSpecificCommandResponse.GetTrawlerSpecificCommandListPresentation()
		if listPresentation == nil {
			return 0, 0, false, false
		}
		returnedRowCount := uint64(len(listPresentation.GetRowsInDisplayOrder()))
		switch listPresentation.GetTotalRowCount().(type) {
		case *presentationv1.TrawlerSpecificCommandListPresentation_ExactTotalRowCount:
			return returnedRowCount,
				listPresentation.GetExactTotalRowCount(),
				false,
				listPresentation.GetMoreRowsExist()
		case *presentationv1.TrawlerSpecificCommandListPresentation_LowerBoundTotalRowCount:
			return returnedRowCount,
				listPresentation.GetLowerBoundTotalRowCount(),
				true,
				listPresentation.GetMoreRowsExist()
		default:
			return returnedRowCount,
				returnedRowCount,
				listPresentation.GetMoreRowsExist(),
				listPresentation.GetMoreRowsExist()
		}
	default:
		return 0, 0, false, false
	}
}

func replaceTrawlerCommandLimitArgument(arguments []string, nextLimit uint64) []string {
	nextLimitText := strconv.FormatUint(nextLimit, 10)
	result := make([]string, 0, len(arguments)+2)
	replacedLimit := false
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		switch {
		case argument == "--limit" || argument == "-limit":
			if !replacedLimit {
				result = append(result, "--limit", nextLimitText)
				replacedLimit = true
			}
			if index+1 < len(arguments) {
				index++
			}
		case strings.HasPrefix(argument, "--limit=") || strings.HasPrefix(argument, "-limit="):
			if !replacedLimit {
				result = append(result, "--limit="+nextLimitText)
				replacedLimit = true
			}
		default:
			result = append(result, argument)
		}
	}
	if !replacedLimit {
		result = append(result, "--limit", nextLimitText)
	}
	return result
}
