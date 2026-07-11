package telecrawl

import (
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
	telegramopenv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/source/telegram/open/v1"
)

func projectOpenRecord(value store.MessageWindow) *telegramopenv1.TelegramRecord {
	targetPosition := -1
	context := make([]*telegramopenv1.Message, 0, len(value.Messages))
	messageRefs := make(map[string]string, len(value.Messages))
	for _, message := range value.Messages {
		if message.MessageID != "" {
			messageRefs[message.MessageID] = store.MessageRef(message.SourcePK)
		}
	}
	for index, message := range value.Messages {
		isTarget := message.SourcePK == value.Target.SourcePK
		if isTarget {
			targetPosition = index
		}
		context = append(context, projectMessage(message, isTarget, messageRefs))
	}
	return &telegramopenv1.TelegramRecord{
		Ref:          store.MessageRef(value.Target.SourcePK),
		Chat:         projectChat(value.Target),
		Participants: append([]string(nil), value.Participants...),
		Message:      projectMessage(value.Target, true, messageRefs),
		Context:      context,
		ContextWindow: &telegramopenv1.ContextWindow{
			Before:          int32(targetPosition),
			After:           int32(len(context) - targetPosition - 1),
			BeforeTruncated: value.BeforeTruncated,
			AfterTruncated:  value.AfterTruncated,
		},
		TargetPosition: int32(targetPosition),
	}
}

func projectMessage(value store.Message, target bool, messageRefs map[string]string) *telegramopenv1.Message {
	message := &telegramopenv1.Message{
		Ref:    store.MessageRef(value.SourcePK),
		Time:   formatOptionalTime(value.Timestamp),
		Chat:   projectChat(value),
		Sender: projectSender(value),
		FromMe: value.FromMe,
	}
	if target {
		message.IsTarget = recordBool(true)
	}
	if text := strings.TrimSpace(value.Text); text != "" {
		message.Text = recordString(text)
	}
	if editTime := formatOptionalTime(value.EditTime); editTime != "" {
		message.EditTime = recordString(editTime)
	}
	if value.MessageType != "" {
		message.MessageType = recordString(value.MessageType)
	}
	if value.MediaType != "" || value.MediaTitle != "" || value.MediaURL != "" || value.MediaSize != 0 {
		message.Media = &telegramopenv1.Media{}
		setOptionalString(&message.Media.Type, value.MediaType)
		setOptionalString(&message.Media.Title, value.MediaTitle)
		setOptionalString(&message.Media.Url, value.MediaURL)
		if value.MediaSize != 0 {
			message.Media.SizeBytes = recordInt64(value.MediaSize)
		}
	}
	if value.MetadataType != "" || value.MetadataTitle != "" || value.MetadataURL != "" {
		message.Metadata = &telegramopenv1.Metadata{}
		setOptionalString(&message.Metadata.Type, value.MetadataType)
		setOptionalString(&message.Metadata.Title, value.MetadataTitle)
		setOptionalString(&message.Metadata.Url, value.MetadataURL)
	}
	if value.Starred {
		message.Starred = recordBool(true)
	}
	setOptionalString(&message.ReplyToMessageRef, messageRefs[value.ReplyToID])
	setOptionalString(&message.ReplyToChatRef, store.ChatRef(value.ReplyToChat))
	if value.Views != 0 {
		message.Views = recordInt32(int32(value.Views))
	}
	if value.Forwards != 0 {
		message.Forwards = recordInt32(int32(value.Forwards))
	}
	if value.RepliesCount != 0 {
		message.RepliesCount = recordInt32(int32(value.RepliesCount))
	}
	if value.Pinned {
		message.Pinned = recordBool(true)
	}
	return message
}

func projectChat(value store.Message) *telegramopenv1.Chat {
	return &telegramopenv1.Chat{Ref: store.ChatRef(value.ChatJID), Name: messageWhere(value)}
}

func projectSender(value store.Message) *telegramopenv1.Sender {
	sender := &telegramopenv1.Sender{State: telegramopenv1.SenderState_SENDER_STATE_UNAVAILABLE}
	if value.FromMe {
		sender.DisplayName = recordString("me")
		sender.State = telegramopenv1.SenderState_SENDER_STATE_AVAILABLE
		return sender
	}
	setOptionalString(&sender.Ref, store.ChatRef(value.SenderJID))
	if displayName := outputField(value.SenderName); displayName != "" {
		sender.DisplayName = recordString(displayName)
		sender.State = telegramopenv1.SenderState_SENDER_STATE_AVAILABLE
	}
	return sender
}

func setOptionalString(target **string, value string) {
	if value = strings.TrimSpace(value); value != "" {
		*target = &value
	}
}

func recordString(value string) *string { return &value }
func recordInt64(value int64) *int64    { return &value }
func recordInt32(value int32) *int32    { return &value }
func recordBool(value bool) *bool       { return &value }
