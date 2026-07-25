package trawlkit

// Message is the stable, source-neutral scripting record returned by a
// source's messages command. Crawlers map their private archive rows into this
// contract so storage keys and provider-specific fields do not become public
// CLI API by accident.
type Message struct {
	Ref      string `json:"ref"`
	ShortRef string `json:"short_ref,omitempty"`
	Time     string `json:"time,omitempty"`
	Who      string `json:"who,omitempty"`
	Where    string `json:"where,omitempty"`
	Text     string `json:"text,omitempty"`
}

// MessageList is the common JSON envelope for source-specific message lists.
// Total is the number of matching archived messages, not only the returned
// page. Truncated is therefore truthful even when the page is empty.
type MessageList struct {
	Messages  []Message `json:"messages"`
	Total     int64     `json:"total"`
	Truncated bool      `json:"truncated"`
}

// NewMessageList keeps the JSON array stable for empty results and derives
// truncation from the matching total rather than a full-page guess.
func NewMessageList(messages []Message, total int64) MessageList {
	if messages == nil {
		messages = []Message{}
	}
	return MessageList{
		Messages:  messages,
		Total:     total,
		Truncated: total > int64(len(messages)),
	}
}
