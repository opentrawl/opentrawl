package trawlkit

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

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
	for messageRecordIndex, messageRecord := range response.GetMessageRecordsNewestFirst() {
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
	sortMessageRecordsNewestFirst(response.MessageRecordsNewestFirst)
	return response, nil
}

func sortMessageRecordsNewestFirst(messageRecords []*message.MessageRecord) {
	sort.SliceStable(messageRecords, func(leftIndex, rightIndex int) bool {
		leftTime, leftTimeIsAvailable := exactMessageRecordTime(messageRecords[leftIndex])
		rightTime, rightTimeIsAvailable := exactMessageRecordTime(messageRecords[rightIndex])
		if leftTimeIsAvailable != rightTimeIsAvailable {
			return leftTimeIsAvailable
		}
		if !leftTimeIsAvailable || leftTime.Equal(rightTime) {
			return false
		}
		return leftTime.After(rightTime)
	})
}

func exactMessageRecordTime(messageRecord *message.MessageRecord) (time.Time, bool) {
	if messageRecord == nil {
		return time.Time{}, false
	}
	exactTime := messageRecord.GetMessageTime().GetExactTime()
	if exactTime == nil || !exactTime.IsValid() {
		return time.Time{}, false
	}
	return exactTime.AsTime(), true
}
