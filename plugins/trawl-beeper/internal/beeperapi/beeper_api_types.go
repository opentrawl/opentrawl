package beeperapi

import "time"

// BeeperAccount represents one connected chat account returned by GET /v1/accounts.
type BeeperAccount struct {
	AccountID  string              `json:"accountID"`
	Network    string              `json:"network"`
	Status     string              `json:"status"`
	StatusText string              `json:"statusText"`
	LoginID    string              `json:"loginID"`
	Bridge     BeeperAccountBridge `json:"bridge"`
	User       BeeperUser          `json:"user"`
}

// BeeperAccountBridge holds bridge metadata for a connected Beeper account.
type BeeperAccountBridge struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Network string `json:"network"`
}

// BeeperUser represents a Beeper user identity on an account or chat participant.
type BeeperUser struct {
	ID            string `json:"id"`
	FullName      string `json:"fullName"`
	Username      string `json:"username"`
	PhoneNumber   string `json:"phoneNumber"`
	Email         string `json:"email"`
	IsSelf        bool   `json:"isSelf"`
	CannotMessage bool   `json:"cannotMessage"`
}

// BeeperChatParticipant represents one participant entry on a Beeper chat.
type BeeperChatParticipant struct {
	BeeperUser
	IsAdmin      bool `json:"isAdmin"`
	IsNetworkBot bool `json:"isNetworkBot"`
	IsPending    bool `json:"isPending"`
}

// BeeperChatParticipants wraps the participant list returned with a Beeper chat.
type BeeperChatParticipants struct {
	Items   []BeeperChatParticipant `json:"items"`
	HasMore bool                    `json:"hasMore"`
	Total   int64                   `json:"total"`
}

// BeeperChatMerge is populated when a chat is a merged container across member chats.
type BeeperChatMerge struct {
	MemberChatIDs []string `json:"memberChatIDs"`
	MergedChatID  string   `json:"mergedChatID"`
}

// BeeperChat represents one conversation returned by GET /v1/chats.
type BeeperChat struct {
	ID               string                 `json:"id"`
	LocalChatID      string                 `json:"localChatID"`
	AccountID        string                 `json:"accountID"`
	Network          string                 `json:"network"`
	Title            string                 `json:"title"`
	Type             string                 `json:"type"`
	Description      string                 `json:"description"`
	IsArchived       bool                   `json:"isArchived"`
	IsLowPriority    bool                   `json:"isLowPriority"`
	IsMarkedUnread   bool                   `json:"isMarkedUnread"`
	IsMuted          bool                   `json:"isMuted"`
	IsPinned         bool                   `json:"isPinned"`
	IsReadOnly       bool                   `json:"isReadOnly"`
	LastActivity     string                 `json:"lastActivity"`
	UnreadCount      int64                  `json:"unreadCount"`
	Participants     BeeperChatParticipants `json:"participants"`
	Preview          *BeeperMessage         `json:"preview"`
	Merge            *BeeperChatMerge       `json:"merge"`
	MergedIntoChatID string                 `json:"mergedIntoChatID"`
}

// IsMergedContainer reports whether the chat is a synthetic merged container
// that holds no messages of its own.
func (c BeeperChat) IsMergedContainer() bool {
	return c.Merge != nil && len(c.Merge.MemberChatIDs) > 0
}

// BeeperAttachment represents one media or file attachment on a Beeper message.
type BeeperAttachment struct {
	Type          string                        `json:"type"`
	ID            string                        `json:"id"`
	Duration      float64                       `json:"duration"`
	FileName      string                        `json:"fileName"`
	FileSize      float64                       `json:"fileSize"`
	IsGif         bool                          `json:"isGif"`
	IsSticker     bool                          `json:"isSticker"`
	IsVoiceNote   bool                          `json:"isVoiceNote"`
	MimeType      string                        `json:"mimeType"`
	SrcURL        string                        `json:"srcURL"`
	Transcription BeeperAttachmentTranscription `json:"transcription"`
}

// BeeperAttachmentTranscription holds an audio or voice-note transcript when present.
type BeeperAttachmentTranscription struct {
	Engine        string `json:"engine"`
	Transcription string `json:"transcription"`
	Language      string `json:"language"`
}

// BeeperLinkPreview represents a link preview attached to a Beeper message.
type BeeperLinkPreview struct {
	URL         string `json:"url"`
	Title       string `json:"title"`
	Description string `json:"description"`
	SiteName    string `json:"siteName"`
}

// BeeperReaction represents a reaction attached to a Beeper message.
type BeeperReaction struct {
	ID            string `json:"id"`
	ParticipantID string `json:"participantID"`
	ReactionKey   string `json:"reactionKey"`
	Emoji         bool   `json:"emoji"`
}

// BeeperMessage represents one message returned by GET /v1/chats/{chatID}/messages.
type BeeperMessage struct {
	ID              string              `json:"id"`
	AccountID       string              `json:"accountID"`
	ChatID          string              `json:"chatID"`
	SenderID        string              `json:"senderID"`
	SenderName      string              `json:"senderName"`
	SortKey         string              `json:"sortKey"`
	Timestamp       string              `json:"timestamp"`
	EditedTimestamp string              `json:"editedTimestamp"`
	Text            string              `json:"text"`
	Type            string              `json:"type"`
	IsSender        bool                `json:"isSender"`
	IsDeleted       bool                `json:"isDeleted"`
	IsHidden        bool                `json:"isHidden"`
	IsUnread        bool                `json:"isUnread"`
	LinkedMessageID string              `json:"linkedMessageID"`
	Attachments     []BeeperAttachment  `json:"attachments"`
	Links           []BeeperLinkPreview `json:"links"`
	Reactions       []BeeperReaction    `json:"reactions"`
}

// BeeperChatsPage is one paginated response from GET /v1/chats.
type BeeperChatsPage struct {
	Items        []BeeperChat `json:"items"`
	HasMore      bool         `json:"hasMore"`
	OldestCursor string       `json:"oldestCursor"`
	NewestCursor string       `json:"newestCursor"`
}

// BeeperMessagesPage is one paginated response from GET /v1/chats/{chatID}/messages.
type BeeperMessagesPage struct {
	Items        []BeeperMessage `json:"items"`
	HasMore      bool            `json:"hasMore"`
	OldestCursor string          `json:"oldestCursor"`
	NewestCursor string          `json:"newestCursor"`
}

// ParseBeeperTimestamp parses an RFC3339 or RFC3339Nano timestamp string from
// the Beeper Desktop API.
func ParseBeeperTimestamp(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}
	if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return parsed.UTC()
	}
	if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
		return parsed.UTC()
	}
	return time.Time{}
}
