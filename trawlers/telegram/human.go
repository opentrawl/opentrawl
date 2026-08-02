package telegram

import (
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
	message "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/message"
)

type telegramMessageMediaHumanProjection struct {
	messageMediaContentKind message.MessageMediaContentKind
	messageMediaTitle       string
}

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
	if telegramMessageMedia(message) != nil {
		return ""
	}
	return "[empty message]"
}

func projectTelegramMessageMediaForHumanPresentation(telegramMessage store.Message) telegramMessageMediaHumanProjection {
	messageMediaHumanProjection := telegramMessageMediaHumanProjection{
		messageMediaContentKind: message.MessageMediaContentKind_MESSAGE_MEDIA_CONTENT_KIND_ATTACHMENT,
		messageMediaTitle:       telegramMessageHumanMediaTitle(telegramMessage),
	}
	providerMediaType := strings.ToLower(strings.TrimSpace(telegramMessage.MediaType))
	if providerMediaType == "" {
		providerMediaType = strings.ToLower(strings.TrimSpace(telegramMessage.MetadataType))
	}
	switch providerMediaType {
	case "photo", "image":
		messageMediaHumanProjection.messageMediaContentKind = message.MessageMediaContentKind_MESSAGE_MEDIA_CONTENT_KIND_IMAGE
	case "video":
		messageMediaHumanProjection.messageMediaContentKind = message.MessageMediaContentKind_MESSAGE_MEDIA_CONTENT_KIND_VIDEO
	case "music", "audio":
		messageMediaHumanProjection.messageMediaContentKind = message.MessageMediaContentKind_MESSAGE_MEDIA_CONTENT_KIND_AUDIO
	case "file", "document":
		messageMediaHumanProjection.messageMediaContentKind = message.MessageMediaContentKind_MESSAGE_MEDIA_CONTENT_KIND_FILE
	case "photo_or_video":
		messageMediaHumanProjection.messageMediaContentKind = message.MessageMediaContentKind_MESSAGE_MEDIA_CONTENT_KIND_PHOTO_OR_VIDEO
	case "voice_or_instant_video":
		messageMediaHumanProjection.messageMediaContentKind = message.MessageMediaContentKind_MESSAGE_MEDIA_CONTENT_KIND_VOICE_OR_INSTANT_VIDEO
	case "gif":
		messageMediaHumanProjection.messageMediaContentKind = message.MessageMediaContentKind_MESSAGE_MEDIA_CONTENT_KIND_GIF
	case "web_page":
		messageMediaHumanProjection.messageMediaContentKind = message.MessageMediaContentKind_MESSAGE_MEDIA_CONTENT_KIND_LINK
	default:
		if providerMediaType == "" &&
			messageMediaHumanProjection.messageMediaTitle == "" &&
			strings.TrimSpace(telegramMessage.MediaPath) == "" &&
			strings.TrimSpace(telegramMessage.MediaURL) == "" &&
			telegramMessage.MediaSize <= 0 {
			messageMediaHumanProjection.messageMediaContentKind = message.MessageMediaContentKind_MESSAGE_MEDIA_CONTENT_KIND_UNSPECIFIED
		}
	}
	return messageMediaHumanProjection
}
