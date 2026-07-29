package archive

import "strings"

const (
	MessageRefPrefix = "imessage:msg/"
	// ChatRefPrefix is the provider-native prefix for a canonical conversation record.
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
