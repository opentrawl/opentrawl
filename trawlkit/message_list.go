package trawlkit

import (
	"time"

	message "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/message"
)

const defaultTrawlerMessageListMaximumReturnedMessageCount = 20

type TrawlerMessageListQuery struct {
	OptionalLocalConversationShortReferenceForRestrictingMessagesToOneConversation string
	After                                                                          time.Time
	Before                                                                         time.Time
	MaximumReturnedMessageCount                                                    int
}

// MessageDirectionFromSentByArchiveOwner maps a source's own "sent by me"
// flag to the shared message direction.
func MessageDirectionFromSentByArchiveOwner(sentByArchiveOwner bool) message.MessageDirectionRelativeToArchiveOwner {
	if sentByArchiveOwner {
		return message.MessageDirectionRelativeToArchiveOwner_MESSAGE_DIRECTION_RELATIVE_TO_ARCHIVE_OWNER_SENT_BY_ARCHIVE_OWNER
	}
	return message.MessageDirectionRelativeToArchiveOwner_MESSAGE_DIRECTION_RELATIVE_TO_ARCHIVE_OWNER_RECEIVED_FROM_OTHER_PARTICIPANT
}
