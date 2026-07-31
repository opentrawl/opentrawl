package trawlkit

import (
	"errors"

	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
)

// ConversationQuery carries the runner-owned flags for the conversations command.
type ConversationQuery struct {
	// Limit is zero when all matching conversations are requested.
	Limit int
	All   bool
	// Unread keeps only conversation items with a positive unread count.
	Unread                               bool
	ResolvedPersonMatchFactsFromTrawlers []*person.PersonMatchFactsFromTrawler
}

// ErrConversationsNoReadState reports that the archive does not contain read state.
var ErrConversationsNoReadState = errors.New("conversations: archive holds no read state")
