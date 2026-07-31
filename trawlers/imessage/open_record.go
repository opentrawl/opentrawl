package imessage

import (
	"context"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/imessage/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	"github.com/opentrawl/opentrawl/trawlkit/presentation"
	message "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/message"
	open "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open"
	presentationcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ trawlkit.RecordOpener = (*Crawler)(nil)

func (c *Crawler) OpenRecord(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	localShortReference *trawlkit.LocalTrawlerShortReference,
) (*open.OpenRecord, error) {
	value, err := c.loadOpenMessage(ctx, req, localShortReference)
	if err != nil {
		return nil, err
	}
	if err := validateOpenTimestamps(value); err != nil {
		return nil, err
	}
	openedMessageRecord := projectOpenedMessageRecordWithConversationContext(value)
	record := &open.OpenRecord{
		RecordTrawler:            c.RegisteredTrawlerDeclaration().RegisteredTrawler,
		CanonicalRecordReference: openedMessageRecord.OpenedMessageRecordReference,
		TypedOpenedRecord: &open.OpenRecord_OpenedMessageRecordWithConversationContext{
			OpenedMessageRecordWithConversationContext: openedMessageRecord,
		},
	}
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

func projectOpenedMessageRecordWithConversationContext(value archive.MessageContext) *message.OpenedMessageRecordWithConversationContext {
	title := strings.TrimSpace(conversationDisplayName(value.Chat))
	if title == "" || title == "unknown conversation" {
		title = "Conversation"
	}
	contextMessageRecords := make([]*message.MessageRecord, 0, len(value.Before)+1+len(value.After))
	for _, message := range value.Before {
		contextMessageRecords = append(contextMessageRecords, projectMessageRecord(message, value.Chat))
	}
	contextMessageRecords = append(contextMessageRecords, projectMessageRecord(value.Message, value.Chat))
	for _, message := range value.After {
		contextMessageRecords = append(contextMessageRecords, projectMessageRecord(message, value.Chat))
	}
	openedMessageRecord := &message.OpenedMessageRecordWithConversationContext{
		ConversationDisplayName:                         title,
		ConversationParticipantDisplayNames:             conversationParticipantDisplayIdentities(value.Chat),
		ConversationContextMessageRecordsInDisplayOrder: contextMessageRecords,
		OpenedMessageRecordReference: trawlkit.NewCanonicalArchiveRecordReference(
			archive.MessageRef(value.Message.MessageID),
		),
		OpenedMessageRecordAnchor:                 trawlkit.NewRecordAnchorIdentifier(trawlkit.MatchAnchorID),
		EarlierConversationContextMessagesOmitted: value.BeforeTruncated,
		LaterConversationContextMessagesOmitted:   value.AfterTruncated,
		ConversationRecordReference: trawlkit.NewCanonicalArchiveRecordReference(
			archive.ChatRef(value.Message.ChatID),
		),
	}
	return openedMessageRecord
}

func projectMessageRecord(archiveMessage archive.MessageRow, chat archive.ChatSummary) *message.MessageRecord {
	messageRecord := &message.MessageRecord{
		CanonicalRecordReference: trawlkit.NewCanonicalArchiveRecordReference(
			archive.MessageRef(archiveMessage.MessageID),
		),
		PeopleRelatedToMessage:      imessageCommandPeople(archiveMessage, chat),
		DisplayedMessageOrMediaText: displayMessageText(archiveMessage.Text, archiveMessage.HasAttachments),
		ConversationDisplayContext:  conversationDisplayName(chat),
	}
	if messageTime := parseArchiveTime(archiveMessage.Time); !messageTime.IsZero() {
		messageRecord.MessageTime = &presentationcontract.ArchiveRecordAssociatedTimeForDisplay{
			ArchiveRecordAssociatedTime: &presentationcontract.ArchiveRecordAssociatedTimeForDisplay_ExactTime{ExactTime: timestamppb.New(messageTime)},
		}
	}
	return messageRecord
}
