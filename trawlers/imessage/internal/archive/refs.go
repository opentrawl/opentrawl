package archive

import "strings"

const (
	MessageRefPrefix       = "imessage:msg/"
	LegacyMessageRefPrefix = "imsgcrawl:msg/"
	// ChatRefPrefix names a chat the same way a message ref names a message:
	// the source-scoped handle a reader copies from the chats table into
	// messages --chat. The raw chat id keeps working; the prefix is stripped.
	ChatRefPrefix = "imessage:chat/"
)

func MessageRef(messageID string) string {
	return MessageRefPrefix + strings.TrimSpace(messageID)
}

func ChatRef(chatID string) string {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return ""
	}
	return ChatRefPrefix + chatID
}

func ChatIDFromRef(value string) string {
	return strings.TrimPrefix(strings.TrimSpace(value), ChatRefPrefix)
}
