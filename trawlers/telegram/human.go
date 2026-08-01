package telegram

import (
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
	message "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/message"
)

type telegramMessageMediaHumanProjection struct {
	messageMediaContentKind message.MessageMediaContentKind
	messageMediaTitle       string
	messageMediaTypeName    string
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
	messageMediaHumanProjection := projectTelegramMessageMediaForHumanPresentation(message)
	if messageMediaHumanProjection.messageMediaTitle != "" {
		return "[" + messageMediaHumanProjection.messageMediaTitle + "]"
	}
	if messageMediaHumanProjection.messageMediaTypeName != "" {
		return "[" + messageMediaHumanProjection.messageMediaTypeName + "]"
	}
	return "[empty message]"
}

func projectTelegramMessageMediaForHumanPresentation(telegramMessage store.Message) telegramMessageMediaHumanProjection {
	messageMediaHumanProjection := telegramMessageMediaHumanProjection{
		messageMediaTitle: telegramMessageHumanMediaTitle(telegramMessage),
	}
	providerMediaType := strings.ToLower(strings.TrimSpace(telegramMessage.MediaType))
	if providerMediaType == "" {
		providerMediaType = strings.ToLower(strings.TrimSpace(telegramMessage.MetadataType))
	}
	switch providerMediaType {
	case "photo", "image":
		messageMediaHumanProjection.messageMediaContentKind = message.MessageMediaContentKind_MESSAGE_MEDIA_CONTENT_KIND_IMAGE
		messageMediaHumanProjection.messageMediaTypeName = "Image"
	case "video":
		messageMediaHumanProjection.messageMediaContentKind = message.MessageMediaContentKind_MESSAGE_MEDIA_CONTENT_KIND_VIDEO
		messageMediaHumanProjection.messageMediaTypeName = "Video"
	case "music", "audio":
		messageMediaHumanProjection.messageMediaContentKind = message.MessageMediaContentKind_MESSAGE_MEDIA_CONTENT_KIND_AUDIO
		messageMediaHumanProjection.messageMediaTypeName = "Audio"
	case "file", "document":
		messageMediaHumanProjection.messageMediaContentKind = message.MessageMediaContentKind_MESSAGE_MEDIA_CONTENT_KIND_FILE
		messageMediaHumanProjection.messageMediaTypeName = "File"
	case "photo_or_video":
		messageMediaHumanProjection.messageMediaTypeName = "Photo or video"
	case "voice_or_instant_video":
		messageMediaHumanProjection.messageMediaTypeName = "Voice message or instant video"
	case "gif":
		messageMediaHumanProjection.messageMediaTypeName = "GIF"
	case "web_page":
		messageMediaHumanProjection.messageMediaTypeName = "Web page"
	default:
		if providerMediaType != "" ||
			messageMediaHumanProjection.messageMediaTitle != "" ||
			strings.TrimSpace(telegramMessage.MediaPath) != "" ||
			strings.TrimSpace(telegramMessage.MediaURL) != "" ||
			telegramMessage.MediaSize > 0 {
			messageMediaHumanProjection.messageMediaTypeName = "Media"
		}
	}
	return messageMediaHumanProjection
}
