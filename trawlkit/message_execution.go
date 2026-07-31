package trawlkit

import (
	"context"
	"errors"
	"fmt"
	"strings"

	messagev1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/message/v1"
)

type executeTrawlerMessageListOperation struct {
	query    TrawlerMessageListQuery
	response *messagev1.MessageListResponse
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
) (*messagev1.MessageListResponse, error) {
	if query.MaximumReturnedMessageCount < 1 {
		return nil, errors.New("--limit must be at least 1.")
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

func canonicalMessageRecordReferences(response *messagev1.MessageListResponse) []string {
	if response == nil {
		return nil
	}
	canonicalMessageRecordReferences := make(
		[]string,
		0,
		len(response.GetMessageRecordsInDisplayOrder()),
	)
	for _, messageRecord := range response.GetMessageRecordsInDisplayOrder() {
		if messageRecord == nil {
			continue
		}
		canonicalMessageRecordReference := strings.TrimSpace(
			messageRecord.GetCanonicalRecordReference().GetCanonicalArchiveRecordReference(),
		)
		if canonicalMessageRecordReference != "" {
			canonicalMessageRecordReferences = append(
				canonicalMessageRecordReferences,
				canonicalMessageRecordReference,
			)
		}
	}
	return uniqueStrings(canonicalMessageRecordReferences)
}
