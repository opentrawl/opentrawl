package wacrawl

import (
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/whatsapp/internal/store"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
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

func projectOpenPresentation(target store.Message, context []store.Message, participants []string) *presentationv1.PresentationDocument {
	record := projectOpenRecord(target, context, participants)
	title := strings.TrimSpace(record.Chat)
	if title == "" || title == "Unknown chat" {
		title = "WhatsApp conversation"
	}
	fields := []*presentationv1.Field{{Label: "Ref", Display: record.Ref}}
	if participants := joinPresentationStrings(record.Participants); participants != "" {
		fields = append(fields, &presentationv1.Field{Label: "Participants", Display: participants})
	}
	blocks := []*presentationv1.Block{{Content: &presentationv1.Block_Fields{Fields: &presentationv1.FieldGroup{Fields: fields}}}}
	if text := strings.TrimSpace(record.Message.Text); text != "" {
		blocks = append(blocks, &presentationv1.Block{Content: &presentationv1.Block_Prose{Prose: &presentationv1.Prose{Text: text}}})
	}
	rows := make([]*presentationv1.Row, 0, len(record.Context))
	for _, message := range record.Context {
		role := presentationv1.Row_ROLE_NORMAL
		if message.GetCurrent() {
			role = presentationv1.Row_ROLE_TARGET
		}
		rows = append(rows, &presentationv1.Row{Role: role, Cells: []*presentationv1.Cell{{Display: message.Time}, {Display: message.Who}, {Display: message.Text}}})
	}
	blocks = append(blocks, &presentationv1.Block{Content: &presentationv1.Block_Table{Table: &presentationv1.Table{Columns: []string{"Time", "From", "Text"}, Rows: rows}}})
	if media := record.Message.Media; media != nil {
		mediaFields := make([]*presentationv1.Field, 0, 3)
		if value := strings.TrimSpace(media.GetType()); value != "" {
			mediaFields = append(mediaFields, &presentationv1.Field{Label: "Media type", Display: value})
		}
		if value := strings.TrimSpace(media.GetTitle()); value != "" {
			mediaFields = append(mediaFields, &presentationv1.Field{Label: "Media title", Display: value})
		}
		if media.SizeBytes != nil {
			mediaFields = append(mediaFields, &presentationv1.Field{Label: "Media size", Display: formatPresentationBytes(*media.SizeBytes)})
		}
		if len(mediaFields) != 0 {
			blocks = append(blocks, &presentationv1.Block{Content: &presentationv1.Block_Fields{Fields: &presentationv1.FieldGroup{Fields: mediaFields}}})
		}
	}
	return &presentationv1.PresentationDocument{Title: title, Blocks: blocks}
}

func joinPresentationStrings(values []string) string {
	items := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			items = append(items, value)
		}
	}
	return strings.Join(items, ", ")
}

func formatPresentationBytes(value int64) string { return fmt.Sprintf("%d bytes", value) }
