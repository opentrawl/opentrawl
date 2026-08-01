package whatsapp

import (
	"context"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/whatsapp/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	message "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/message"
	open "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type openValue struct {
	target          store.Message
	context         []store.Message
	participants    []string
	beforeTruncated bool
	afterTruncated  bool
}

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

func validateOpenTimestamps(value openValue) error {
	if value.target.Timestamp.IsZero() {
		return fmt.Errorf("message timestamp is missing")
	}
	for _, message := range value.context {
		if message.Timestamp.IsZero() {
			return fmt.Errorf("message timestamp is missing")
		}
	}
	return nil
}

func projectOpenedMessageRecordWithConversationContext(value openValue) *message.OpenedMessageRecordWithConversationContext {
	participantDisplayNames := resolvedParticipantNames(value.participants)
	title := strings.TrimSpace(messageWhere(value.target))
	if (title == "" || title == "Unknown conversation" || title == "WhatsApp conversation") && len(participantDisplayNames) == 1 {
		title = participantDisplayNames[0]
	}
	if title == "" || title == "Unknown conversation" {
		title = "WhatsApp conversation"
	}
	contextMessageRecords := make([]*message.MessageRecord, 0, len(value.context))
	for _, message := range value.context {
		contextMessageRecords = append(contextMessageRecords, projectMessageRecord(message))
	}
	openedMessageRecord := &message.OpenedMessageRecordWithConversationContext{
		ConversationDisplayName:                         title,
		ConversationParticipantDisplayNames:             participantDisplayNames,
		ConversationContextMessageRecordsInDisplayOrder: contextMessageRecords,
		OpenedMessageRecordReference: trawlkit.NewCanonicalArchiveRecordReference(
			messageRef(value.target),
		),
		OpenedMessageRecordAnchor:                 trawlkit.NewRecordAnchorIdentifier(trawlkit.MatchAnchorID),
		EarlierConversationContextMessagesOmitted: value.beforeTruncated,
		LaterConversationContextMessagesOmitted:   value.afterTruncated,
		ConversationRecordReference: trawlkit.NewCanonicalArchiveRecordReference(
			store.ChatRef(value.target.ChatJID),
		),
	}
	if media := messageMedia(value.target); media != nil {
		openedMessageRecord.OpenedMessageMedia = &message.MessageMedia{
			MessageMediaContentKind: whatsappMessageMediaContentKind(media.Type),
			MessageMediaTitle:       strings.TrimSpace(media.Title),
		}
		if media.SizeBytes > 0 {
			messageMediaByteCount := uint64(media.SizeBytes)
			openedMessageRecord.OpenedMessageMedia.MessageMediaByteCount = &messageMediaByteCount
		}
	}
	return openedMessageRecord
}

func whatsappMessageMediaContentKind(whatsappMediaType string) message.MessageMediaContentKind {
	switch strings.ToLower(strings.TrimSpace(whatsappMediaType)) {
	case "image":
		return message.MessageMediaContentKind_MESSAGE_MEDIA_CONTENT_KIND_IMAGE
	case "video":
		return message.MessageMediaContentKind_MESSAGE_MEDIA_CONTENT_KIND_VIDEO
	case "audio":
		return message.MessageMediaContentKind_MESSAGE_MEDIA_CONTENT_KIND_AUDIO
	case "document":
		return message.MessageMediaContentKind_MESSAGE_MEDIA_CONTENT_KIND_FILE
	default:
		return message.MessageMediaContentKind_MESSAGE_MEDIA_CONTENT_KIND_UNSPECIFIED
	}
}

func projectMessageRecord(whatsappMessage store.Message) *message.MessageRecord {
	messageRecord := &message.MessageRecord{
		CanonicalRecordReference:    trawlkit.NewCanonicalArchiveRecordReference(messageRef(whatsappMessage)),
		PeopleRelatedToMessage:      whatsappMessageCommandPeople(whatsappMessage),
		DisplayedMessageOrMediaText: messageText(whatsappMessage),
		ConversationDisplayContext:  messageWhere(whatsappMessage),
	}
	if !whatsappMessage.Timestamp.IsZero() {
		messageRecord.MessageTime = &presentation.ArchiveRecordAssociatedTimeForDisplay{
			ArchiveRecordAssociatedTime: &presentation.ArchiveRecordAssociatedTimeForDisplay_ExactTime{ExactTime: timestamppb.New(whatsappMessage.Timestamp)},
		}
	}
	return messageRecord
}
