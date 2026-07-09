package telecrawl

import (
	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
)

type topicJSON struct {
	ChatJID              string `json:"chat_jid"`
	TopicID              string `json:"topic_id"`
	Title                string `json:"title,omitempty"`
	TopMessageID         string `json:"top_message_id,omitempty"`
	IconColor            int    `json:"icon_color,omitempty"`
	IconEmojiID          string `json:"icon_emoji_id,omitempty"`
	UnreadCount          int    `json:"unread_count"`
	UnreadMentionsCount  int    `json:"unread_mentions_count"`
	UnreadReactionsCount int    `json:"unread_reactions_count"`
	Pinned               bool   `json:"pinned,omitempty"`
	Closed               bool   `json:"closed,omitempty"`
	Hidden               bool   `json:"hidden,omitempty"`
	LastMessageAt        string `json:"last_message_at,omitempty"`
}

type messagesJSONEnvelope struct {
	Messages  []messageJSON `json:"messages"`
	Total     int           `json:"total"`
	Truncated bool          `json:"truncated"`
}

type messageJSON struct {
	Ref      string `json:"ref"`
	ShortRef string `json:"short_ref"`
	Time     string `json:"time"`
	Who      string `json:"who"`
	Where    string `json:"where"`
	Text     string `json:"text"`
}

type contactJSON struct {
	JID          string `json:"jid"`
	PeerType     string `json:"peer_type,omitempty"`
	Phone        string `json:"phone,omitempty"`
	FullName     string `json:"full_name,omitempty"`
	FirstName    string `json:"first_name,omitempty"`
	LastName     string `json:"last_name,omitempty"`
	BusinessName string `json:"business_name,omitempty"`
	Username     string `json:"username,omitempty"`
	LID          string `json:"lid,omitempty"`
	AboutText    string `json:"about_text,omitempty"`
	AvatarPath   string `json:"avatar_path,omitempty"`
	UpdatedAt    string `json:"updated_at,omitempty"`
}

type folderJSON struct {
	ID        string `json:"id"`
	Title     string `json:"title,omitempty"`
	Emoticon  string `json:"emoticon,omitempty"`
	Color     int    `json:"color,omitempty"`
	FlagsJSON string `json:"flags_json,omitempty"`
}

func topicJSONRows(topics []store.Topic) []topicJSON {
	out := make([]topicJSON, 0, len(topics))
	for _, topic := range topics {
		out = append(out, topicJSON{
			ChatJID:              topic.ChatJID,
			TopicID:              topic.TopicID,
			Title:                topic.Title,
			TopMessageID:         topic.TopMessageID,
			IconColor:            topic.IconColor,
			IconEmojiID:          topic.IconEmojiID,
			UnreadCount:          topic.UnreadCount,
			UnreadMentionsCount:  topic.UnreadMentionsCount,
			UnreadReactionsCount: topic.UnreadReactionsCount,
			Pinned:               topic.Pinned,
			Closed:               topic.Closed,
			Hidden:               topic.Hidden,
			LastMessageAt:        formatOptionalTime(topic.LastMessageAt),
		})
	}
	return out
}

func messageJSONEnvelope(messages []store.Message, total int, shortRefs map[string]string) messagesJSONEnvelope {
	return messagesJSONEnvelope{
		Messages:  messageJSONRows(messages, shortRefs),
		Total:     total,
		Truncated: total > len(messages),
	}
}

func messageJSONRows(messages []store.Message, shortRefs map[string]string) []messageJSON {
	out := make([]messageJSON, 0, len(messages))
	for _, message := range messages {
		ref := messageRef(message.SourcePK)
		out = append(out, messageJSON{
			Ref:      ref,
			ShortRef: shortRefs[ref],
			Time:     formatOptionalTime(message.Timestamp),
			Who:      outputField(messageWho(message)),
			Where:    outputField(messageWhereForList(message)),
			Text:     outputField(messageText(message)),
		})
	}
	return out
}

func contactJSONRows(contacts []store.Contact) []contactJSON {
	out := make([]contactJSON, 0, len(contacts))
	for _, contact := range contacts {
		out = append(out, contactJSON{
			JID:          contact.JID,
			PeerType:     contact.PeerType,
			Phone:        contact.Phone,
			FullName:     contact.FullName,
			FirstName:    contact.FirstName,
			LastName:     contact.LastName,
			BusinessName: contact.BusinessName,
			Username:     contact.Username,
			LID:          contact.LID,
			AboutText:    contact.AboutText,
			AvatarPath:   contact.AvatarPath,
			UpdatedAt:    formatOptionalTime(contact.UpdatedAt),
		})
	}
	return out
}

func folderJSONRows(folders []store.Folder) []folderJSON {
	out := make([]folderJSON, 0, len(folders))
	for _, folder := range folders {
		out = append(out, folderJSON{
			ID:        folder.ID,
			Title:     folder.Title,
			Emoticon:  folder.Emoticon,
			Color:     folder.Color,
			FlagsJSON: folder.FlagsJSON,
		})
	}
	return out
}
