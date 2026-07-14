package imsgcrawl

import (
	"context"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/imessage/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	"github.com/opentrawl/opentrawl/trawlkit/presentation"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	imessageopenv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/source/imessage/open/v1"
	"google.golang.org/protobuf/types/known/anypb"
)

var _ trawlkit.RecordOpener = (*Crawler)(nil)

func (c *Crawler) OpenRecord(ctx context.Context, req *trawlkit.Request, ref string) (*openv1.OpenRecord, error) {
	value, err := c.loadOpenMessage(ctx, req, ref)
	if err != nil {
		return nil, err
	}
	if err := validateOpenTimestamps(value); err != nil {
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

func validateOpenTimestamps(value archive.MessageContext) error {
	values := []string{value.Message.Time}
	for _, message := range value.Before {
		values = append(values, message.Time)
	}
	for _, message := range value.After {
		values = append(values, message.Time)
	}
	return presentation.ValidateTimestamps(values...)
}

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

func projectOpenPresentation(value archive.MessageContext) *presentationv1.PresentationDocument {
	record := projectOpenRecord(value)
	title := strings.TrimSpace(chatDisplayName(value.Chat))
	if title == "" || title == "unknown chat" {
		title = "Conversation"
	}
	fields := make([]*presentationv1.Field, 0, 1)
	if participants := joinPresentationStrings(record.Chat.Participants); participants != "" {
		fields = append(fields, &presentationv1.Field{Label: "Participants", Display: participants})
	}
	blocks := make([]*presentationv1.Block, 0, 3)
	if len(fields) > 0 {
		blocks = append(blocks, &presentationv1.Block{Content: &presentationv1.Block_Fields{Fields: &presentationv1.FieldGroup{Fields: fields}}})
	}
	if text := strings.TrimSpace(record.Message.Text); text != "" {
		blocks = append(blocks, &presentationv1.Block{Content: &presentationv1.Block_Prose{Prose: &presentationv1.Prose{Text: text}}})
	}
	rows := make([]*presentationv1.Row, 0, len(record.Context))
	for _, message := range record.Context {
		role := presentationv1.Row_ROLE_NORMAL
		if message.GetTarget() {
			role = presentationv1.Row_ROLE_TARGET
		}
		row := &presentationv1.Row{Role: role, Cells: []*presentationv1.Cell{{Display: presentation.MustTimestamp(message.Time)}, {Display: message.Who}, {Display: message.Text}}}
		if message.GetTarget() {
			row.AnchorId = trawlkit.MatchAnchorID
		}
		rows = append(rows, row)
	}
	blocks = append(blocks, &presentationv1.Block{Content: &presentationv1.Block_Table{Table: &presentationv1.Table{Columns: []string{"Time", "From", "Text"}, Rows: rows}}})
	document := &presentationv1.PresentationDocument{Title: title, Blocks: blocks, PrimaryAnchorId: trawlkit.MatchAnchorID}
	if record.Message.GetHasAttachments() {
		document.Facts = append(document.Facts, &presentationv1.Fact{Kind: presentationv1.Fact_KIND_WARNING, Message: "Message has attachments."})
	}
	return document
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
