package trawlkit

const defaultTrawlerMessageListMaximumReturnedMessageCount = 20

type TrawlerMessageListQuery struct {
	OptionalLocalConversationShortReferenceForRestrictingMessagesToOneConversation string
	MaximumReturnedMessageCount                                                    int
}
