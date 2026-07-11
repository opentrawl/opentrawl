package imsgcrawl

import (
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/imessage/internal/archive"
	imessageopenv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/source/imessage/open/v1"
)

func projectOpenRecord(value archive.MessageContext) *imessageopenv1.IMessageRecord {
	where := strings.TrimSpace(chatDisplayName(value.Chat))
	if where == "" {
		where = "unknown chat"
	}
	record := &imessageopenv1.IMessageRecord{
		Ref: archive.MessageRef(value.Message.MessageID),
		Chat: &imessageopenv1.Chat{
			Name:         where,
			Participants: append([]string(nil), value.Chat.ParticipantHandles...),
		},
		Message: projectMessage(value.Message, where, false),
		Context: make([]*imessageopenv1.Message, 0, len(value.Before)+1+len(value.After)),
	}
	for _, message := range value.Before {
		record.Context = append(record.Context, projectMessage(message, where, false))
	}
	record.Context = append(record.Context, projectMessage(value.Message, where, true))
	for _, message := range value.After {
		record.Context = append(record.Context, projectMessage(message, where, false))
	}
	return record
}

func projectMessage(value archive.MessageRow, where string, target bool) *imessageopenv1.Message {
	message := &imessageopenv1.Message{
		Ref:    archive.MessageRef(value.MessageID),
		Time:   value.Time,
		Who:    strings.TrimSpace(senderName(value.FromMe, value.SenderLabel)),
		Where:  where,
		Text:   value.Text,
		FromMe: value.FromMe,
	}
	if value.HasAttachments {
		message.HasAttachments = recordBool(true)
	}
	if target {
		message.Target = recordBool(true)
	}
	return message
}

func recordBool(value bool) *bool { return &value }
