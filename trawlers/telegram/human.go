package telegram

import (
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
)

func humanTelegramName(value string) string {
	value = outputField(value)
	if value == "" || strings.Contains(value, "@") || opaqueNumericParticipant(value) {
		return ""
	}
	return value
}

func telegramConversationTitle(conversation store.Chat) string {
	return telegramConversationDisplayName(conversation.Name, conversation.Kind)
}

func telegramConversationDisplayName(conversationName string, conversationKind string) string {
	conversationDisplayName := humanTelegramName(conversationName)
	if conversationDisplayName != "" && strings.EqualFold(strings.TrimSpace(conversationKind), "channel") {
		return conversationDisplayName + " (channel)"
	}
	return conversationDisplayName
}

func messageText(message store.Message) string {
	if text := strings.TrimSpace(message.Text); text != "" {
		return text
	}
	switch {
	case strings.TrimSpace(message.MediaTitle) != "":
		return "[" + strings.TrimSpace(message.MediaTitle) + "]"
	case strings.TrimSpace(message.MediaType) != "":
		return "[" + strings.TrimSpace(message.MediaType) + "]"
	default:
		return "[empty message]"
	}
}
