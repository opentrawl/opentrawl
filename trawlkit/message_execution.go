package trawlkit

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit/output"
	message "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/message"
)

type executeTrawlerMessageListOperation struct {
	query    TrawlerMessageListQuery
	response *message.MessageListResponse
}

func (operation *executeTrawlerMessageListOperation) execute(
	ctx context.Context,
	trawler Trawler,
	request *TrawlerCommandExecutionRequest,
) error {
	response, err := executeTrawlerMessageList(
		ctx,
		trawler.(TrawlerMessageLister),
		request,
		operation.query,
	)
	if err != nil {
		return err
	}
	operation.response = response
	return nil
}

func executeTrawlerMessageList(
	ctx context.Context,
	messageLister TrawlerMessageLister,
	request *TrawlerCommandExecutionRequest,
	query TrawlerMessageListQuery,
) (*message.MessageListResponse, error) {
	if query.MaximumReturnedMessageCount < 1 {
		return nil, output.HumanFacingErrorMessage("--limit must be at least 1.")
	}
	response, err := messageLister.ListMessages(ctx, request, query)
	if err != nil {
		return nil, err
	}
	if response == nil {
		return nil, errors.New("trawler returned no message list response")
	}
	for messageRecordIndex, messageRecord := range response.GetMessageRecordsInDisplayOrder() {
		if messageRecord == nil {
			return nil, fmt.Errorf("message record %d is missing", messageRecordIndex)
		}
		if strings.TrimSpace(
			messageRecord.GetCanonicalRecordReference().GetCanonicalArchiveRecordReference(),
		) == "" {
			return nil, fmt.Errorf(
				"message record %d canonical message record reference is empty",
				messageRecordIndex,
			)
		}
	}
	return response, nil
}
