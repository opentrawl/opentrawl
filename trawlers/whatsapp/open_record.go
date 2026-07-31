package whatsapp

import (
	"context"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/whatsapp/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	messagev1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/message/v1"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
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
) (*openv1.OpenRecord, error) {
	value, err := c.loadOpenMessage(ctx, req, localShortReference)
	if err != nil {
		return nil, err
	}
	if err := validateOpenTimestamps(value); err != nil {
		return nil, err
	}
	openedMessageRecord := projectOpenedMessageRecordWithConversationContext(value)
	record := &openv1.OpenRecord{
		RecordTrawler:            c.RegisteredTrawlerDeclaration().RegisteredTrawler,
		CanonicalRecordReference: openedMessageRecord.OpenedMessageRecordReference,
		TypedOpenedRecord: &openv1.OpenRecord_OpenedMessageRecordWithConversationContext{
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

func projectOpenedMessageRecordWithConversationContext(value openValue) *messagev1.OpenedMessageRecordWithConversationContext {
	participantDisplayNames := resolvedParticipantNames(value.participants)
	title := strings.TrimSpace(messageWhere(value.target))
	if (title == "" || title == "Unknown conversation" || title == "WhatsApp conversation") && len(participantDisplayNames) == 1 {
		title = participantDisplayNames[0]
	}
	if title == "" || title == "Unknown conversation" {
		title = "WhatsApp conversation"
	}
	contextMessageRecords := make([]*messagev1.MessageRecord, 0, len(value.context))
	for _, message := range value.context {
		contextMessageRecords = append(contextMessageRecords, projectMessageRecord(message))
	}
	openedMessageRecord := &messagev1.OpenedMessageRecordWithConversationContext{
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
		openedMessageRecord.OpenedMessageMedia = &messagev1.MessageMedia{
			MessageMediaKind:  strings.TrimSpace(media.Type),
			MessageMediaTitle: strings.TrimSpace(media.Title),
		}
		if media.SizeBytes > 0 {
			messageMediaByteCount := uint64(media.SizeBytes)
			openedMessageRecord.OpenedMessageMedia.MessageMediaByteCount = &messageMediaByteCount
		}
	}
	return openedMessageRecord
}

func projectMessageRecord(message store.Message) *messagev1.MessageRecord {
	messageRecord := &messagev1.MessageRecord{
		CanonicalRecordReference:    trawlkit.NewCanonicalArchiveRecordReference(messageRef(message)),
		PeopleRelatedToMessage:      whatsappMessageCommandPeople(message),
		DisplayedMessageOrMediaText: messageText(message),
		ConversationDisplayContext:  messageWhere(message),
	}
	if !message.Timestamp.IsZero() {
		messageRecord.MessageTime = &presentationv1.ArchiveRecordAssociatedTimeForDisplay{
			ArchiveRecordAssociatedTime: &presentationv1.ArchiveRecordAssociatedTimeForDisplay_ExactTime{ExactTime: timestamppb.New(message.Timestamp)},
		}
	}
	return messageRecord
}
