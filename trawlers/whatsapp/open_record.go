package wacrawl

import (
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/whatsapp/internal/store"
	whatsappopenv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/source/whatsapp/open/v1"
)

func projectOpenRecord(target store.Message, context []store.Message, participants []string) *whatsappopenv1.WhatsAppRecord {
	record := &whatsappopenv1.WhatsAppRecord{
		Ref:          messageRef(target),
		Chat:         messageWhereJSON(target),
		Participants: append([]string(nil), participants...),
		Message:      projectMessage(target, false),
		Context:      make([]*whatsappopenv1.Message, 0, len(context)),
		Window:       &whatsappopenv1.Window{},
	}
	for _, message := range context {
		current := message.SourcePK == target.SourcePK
		if !current && (message.Timestamp.Before(target.Timestamp) || message.Timestamp.Equal(target.Timestamp) && message.SourcePK < target.SourcePK) {
			record.Window.Before++
		} else if !current {
			record.Window.After++
		}
		record.Context = append(record.Context, projectMessage(message, current))
	}
	return record
}

func projectMessage(value store.Message, current bool) *whatsappopenv1.Message {
	message := &whatsappopenv1.Message{
		Ref:   messageRef(value),
		Time:  formatTime(value.Timestamp),
		Who:   outputField(messageWhoJSON(value)),
		Where: outputField(messageWhereJSON(value)),
		Text:  messageText(value),
	}
	if kind := strings.TrimSpace(messageKind(value)); kind != "" {
		message.Type = recordString(kind)
	}
	if media := messageMedia(value); media != nil {
		message.Media = &whatsappopenv1.Media{}
		if media.Type != "" {
			message.Media.Type = recordString(media.Type)
		}
		if media.Title != "" {
			message.Media.Title = recordString(media.Title)
		}
		if media.SizeBytes != 0 {
			message.Media.SizeBytes = recordInt64(media.SizeBytes)
		}
	}
	if value.Starred {
		message.Starred = recordBool(true)
	}
	if current {
		message.Current = recordBool(true)
	}
	return message
}

func recordString(value string) *string { return &value }
func recordInt64(value int64) *int64    { return &value }
func recordBool(value bool) *bool       { return &value }
