package imessage

import (
	"context"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/imessage/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	message "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/message"
	open "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open"
	presentationcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (c *Crawler) OpenCanonicalRecord(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	canonicalRecordReference *trawlkit.CanonicalArchiveRecordReference,
) (*open.OpenRecord, error) {
	value, err := c.loadOpenMessage(ctx, req, canonicalRecordReference)
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
	return trawlkit.ValidateOptionalRFC3339Timestamps(values...)
}

func projectOpenedMessageRecordWithConversationContext(value archive.MessageContext) *message.OpenedMessageRecordWithConversationContext {
	title := strings.TrimSpace(conversationDisplayName(value.Chat))
	if strings.TrimSpace(value.Message.ChatID) == "" ||
		title == "" ||
		title == "unknown conversation" ||
		title == "direct conversation" {
		title = "iMessage conversation"
		if sender := humanParticipantDisplayIdentity(value.Message.SenderLabel); sender != "" && sender != "me" {
			title = sender
		}
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
		ConversationDisplayName:                      title,
		ConversationParticipantDisplayNames:          conversationParticipantDisplayIdentities(value.Chat),
		ConversationContextMessageRecordsNewestFirst: contextMessageRecords,
		OpenedMessageRecordReference: trawlkit.NewCanonicalArchiveRecordReference(
			archive.MessageRef(value.Message.MessageID),
		),
		OpenedMessageRecordAnchor:                 trawlkit.NewRecordAnchorIdentifier(trawlkit.MatchAnchorID),
		EarlierConversationContextMessagesOmitted: value.BeforeTruncated,
		LaterConversationContextMessagesOmitted:   value.AfterTruncated,
	}
	if chatRef := archive.ChatRef(value.Message.ChatID); chatRef != "" {
		openedMessageRecord.ConversationRecordReference = trawlkit.NewCanonicalArchiveRecordReference(chatRef)
	}
	return openedMessageRecord
}

func projectMessageRecord(archiveMessage archive.MessageRow, chat archive.ChatSummary) *message.MessageRecord {
	messageRecord := &message.MessageRecord{
		CanonicalRecordReference: trawlkit.NewCanonicalArchiveRecordReference(
			archive.MessageRef(archiveMessage.MessageID),
		),
		PeopleRelatedToMessage:                 imessageCommandPeople(archiveMessage, chat),
		MessageText:                            strings.TrimSpace(strings.ReplaceAll(archiveMessage.Text, objectReplacementCharacter, "")),
		ConversationDisplayName:                messageRecordConversationDisplayName(chat),
		MessageDirectionRelativeToArchiveOwner: trawlkit.MessageDirectionFromSentByArchiveOwner(archiveMessage.FromMe),
	}
	if archiveMessage.HasAttachments {
		messageRecord.MessageMedia = &message.MessageMedia{
			MessageMediaContentKind: message.MessageMediaContentKind_MESSAGE_MEDIA_CONTENT_KIND_ATTACHMENT,
		}
	}
	if messageTime := parseArchiveTime(archiveMessage.Time); !messageTime.IsZero() {
		messageRecord.MessageTime = &presentationcontract.ArchiveRecordAssociatedTimeForDisplay{
			ArchiveRecordAssociatedTime: &presentationcontract.ArchiveRecordAssociatedTimeForDisplay_ExactTime{ExactTime: timestamppb.New(messageTime)},
		}
	}
	return messageRecord
}
