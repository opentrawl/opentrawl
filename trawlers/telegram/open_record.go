package telecrawl

import (
	"context"
	"strings"
	"unicode"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	"github.com/opentrawl/opentrawl/trawlkit/presentation"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	telegramopenv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/source/telegram/open/v1"
	"google.golang.org/protobuf/types/known/anypb"
)

var _ trawlkit.RecordOpener = (*Crawler)(nil)

func (c *Crawler) OpenRecord(ctx context.Context, req *trawlkit.Request, ref string) (*openv1.OpenRecord, error) {
	value, err := c.loadOpenMessage(ctx, req, ref)
	if err != nil {
		return nil, err
	}
	machine := projectOpenRecord(value)
	data, err := anypb.New(machine)
	if err != nil {
		return nil, err
	}
	record := &openv1.OpenRecord{SourceId: c.Info().ID, OpenRef: machine.GetRef(), Data: data, Presentation: projectOpenPresentation(value)}
	if err := openrecord.Validate(record); err != nil {
		return nil, err
	}
	return record, nil
}

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

func projectOpenPresentation(value store.MessageWindow) *presentationv1.PresentationDocument {
	record := projectOpenRecord(value)
	title := strings.TrimSpace(record.Chat.Name)
	if title == "" || title == "Telegram chat" {
		title = "Telegram conversation"
	}
	fields := make([]*presentationv1.Field, 0, 1)
	if participants := joinPresentationStrings(presentationParticipants(record.Participants)); participants != "" {
		fields = append(fields, &presentationv1.Field{Label: "Participants", Display: participants})
	}
	blocks := make([]*presentationv1.Block, 0, 3)
	if len(fields) > 0 {
		blocks = append(blocks, &presentationv1.Block{Content: &presentationv1.Block_Fields{Fields: &presentationv1.FieldGroup{Fields: fields}}})
	}
	if text := strings.TrimSpace(record.Message.GetText()); text != "" {
		blocks = append(blocks, &presentationv1.Block{Content: &presentationv1.Block_Prose{Prose: &presentationv1.Prose{Text: text}}})
	}
	rows := make([]*presentationv1.Row, 0, len(record.Context))
	for _, message := range record.Context {
		role := presentationv1.Row_ROLE_NORMAL
		if message.GetIsTarget() {
			role = presentationv1.Row_ROLE_TARGET
		}
		who := "Unavailable"
		if message.Sender != nil && message.Sender.State == telegramopenv1.SenderState_SENDER_STATE_AVAILABLE {
			who = message.Sender.GetDisplayName()
		}
		row := &presentationv1.Row{Role: role, Cells: []*presentationv1.Cell{{Display: presentation.MustTimestamp(message.Time)}, {Display: who}, {Display: message.GetText()}}}
		if message.GetIsTarget() {
			row.AnchorId = trawlkit.MatchAnchorID
		}
		rows = append(rows, row)
	}
	blocks = append(blocks, &presentationv1.Block{Content: &presentationv1.Block_Table{Table: &presentationv1.Table{Columns: []string{"Time", "From", "Text"}, Rows: rows}}})
	document := &presentationv1.PresentationDocument{Title: title, Blocks: blocks, PrimaryAnchorId: trawlkit.MatchAnchorID}
	if media := record.Message.Media; media != nil && openrecord.ValidHTTPSURL(media.GetUrl()) {
		document.Actions = append(document.Actions, &presentationv1.Action{Label: "Open media link", Target: &presentationv1.Action_Url{Url: media.GetUrl()}})
	}
	if metadata := record.Message.Metadata; metadata != nil && openrecord.ValidHTTPSURL(metadata.GetUrl()) {
		document.Actions = append(document.Actions, &presentationv1.Action{Label: "Open metadata link", Target: &presentationv1.Action_Url{Url: metadata.GetUrl()}})
	}
	if record.ContextWindow.BeforeTruncated {
		document.Facts = append(document.Facts, &presentationv1.Fact{Kind: presentationv1.Fact_KIND_TRUNCATION, Message: "Earlier context is truncated."})
	}
	if record.ContextWindow.AfterTruncated {
		document.Facts = append(document.Facts, &presentationv1.Fact{Kind: presentationv1.Fact_KIND_TRUNCATION, Message: "Later context is truncated."})
	}
	return document
}

func presentationParticipants(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || opaqueNumericParticipant(value) {
			continue
		}
		result = append(result, value)
	}
	return result
}

func opaqueNumericParticipant(value string) bool {
	for _, r := range value {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
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
