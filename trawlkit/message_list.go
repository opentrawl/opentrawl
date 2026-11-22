package trawlkit

import "time"

const defaultTrawlerMessageListMaximumReturnedMessageCount = 20

type TrawlerMessageListQuery struct {
	OptionalLocalConversationShortReferenceForRestrictingMessagesToOneConversation string
	After                                                                          time.Time
	Before                                                                         time.Time
	MaximumReturnedMessageCount                                                    int
}
