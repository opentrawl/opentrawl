package telegram

import (
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
)

func messageRef(sourcePK int64) string {
	return store.MessageRef(sourcePK)
}

func messageWho(message store.Message) string {
	if message.FromMe {
		return "me"
	}
	if value := strings.TrimSpace(message.SenderName); value != "" {
		return outputField(value)
	}
	if strings.TrimSpace(message.SenderJID) == "" || strings.TrimSpace(message.SenderJID) == strings.TrimSpace(message.ChatJID) {
		return outputField(messageWhere(message))
	}
	return ""
}

func messageWhere(message store.Message) string {
	if conversationDisplayName := telegramConversationDisplayName(message.ChatName, message.ChatKind); conversationDisplayName != "" {
		return conversationDisplayName
	}
	return "Telegram conversation"
}

func messageSnippet(message store.Message) string {
	if value := strings.TrimSpace(message.Snippet); value != "" {
		return outputField(value)
	}
	return outputField(message.Text)
}

func outputField(value string) string {
	return strings.Join(strings.Fields(value), " ")
}
