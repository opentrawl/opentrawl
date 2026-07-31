package telegram

import (
	"context"
	"strings"
	"unicode"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	messagev1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/message/v1"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
	personv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

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

func projectOpenedMessageRecordWithConversationContext(value store.MessageWindow) *messagev1.OpenedMessageRecordWithConversationContext {
	title := strings.TrimSpace(telegramMessageCommandConversationDisplayContext(value.Target))
	if title == "" || title == "Telegram conversation" {
		title = "Telegram conversation"
	}
	contextMessageRecords := make([]*messagev1.MessageRecord, 0, len(value.Messages))
	for _, message := range value.Messages {
		contextMessageRecords = append(contextMessageRecords, telegramOpenedMessageRecord(message))
	}
	openedMessageRecord := &messagev1.OpenedMessageRecordWithConversationContext{
		ConversationDisplayName:                         title,
		ConversationParticipantDisplayNames:             presentationParticipants(value.Participants),
		ConversationContextMessageRecordsInDisplayOrder: contextMessageRecords,
		OpenedMessageRecordReference: trawlkit.NewCanonicalArchiveRecordReference(
			store.MessageRef(value.Target.SourcePK),
		),
		OpenedMessageRecordAnchor:                 trawlkit.NewRecordAnchorIdentifier(trawlkit.MatchAnchorID),
		EarlierConversationContextMessagesOmitted: value.BeforeTruncated,
		LaterConversationContextMessagesOmitted:   value.AfterTruncated,
		ConversationRecordReference: trawlkit.NewCanonicalArchiveRecordReference(
			store.ChatRef(value.Target.ChatJID),
		),
	}
	if openedMessageMedia := telegramOpenedMessageMedia(value.Target); openedMessageMedia != nil {
		openedMessageRecord.OpenedMessageMedia = openedMessageMedia
	}
	return openedMessageRecord
}

func telegramOpenedMessageRecord(message store.Message) *messagev1.MessageRecord {
	messageRecord := &messagev1.MessageRecord{
		CanonicalRecordReference: trawlkit.NewCanonicalArchiveRecordReference(
			store.MessageRef(message.SourcePK),
		),
		DisplayedMessageOrMediaText: messageText(message),
		ConversationDisplayContext:  telegramMessageCommandConversationDisplayContext(message),
	}
	if !message.Timestamp.IsZero() {
		messageRecord.MessageTime = &presentationv1.ArchiveRecordAssociatedTimeForDisplay{
			ArchiveRecordAssociatedTime: &presentationv1.ArchiveRecordAssociatedTimeForDisplay_ExactTime{ExactTime: timestamppb.New(message.Timestamp)},
		}
	}
	if senderDisplayName := strings.TrimSpace(messageWho(message)); senderDisplayName != "" {
		messageRecord.PeopleRelatedToMessage = []*personv1.PersonRelatedToArchiveRecord{{
			PersonDisplayName:         senderDisplayName,
			PersonRoleInArchiveRecord: personv1.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_SENDER,
		}}
	}
	return messageRecord
}

func telegramOpenedMessageMedia(message store.Message) *messagev1.MessageMedia {
	messageMediaKind := outputField(message.MediaType)
	if messageMediaKind == "" {
		messageMediaKind = outputField(message.MetadataType)
	}
	messageMedia := &messagev1.MessageMedia{
		MessageMediaKind:  messageMediaKind,
		MessageMediaTitle: telegramMessageHumanMediaTitle(message),
	}
	if message.MediaSize > 0 {
		messageMediaByteCount := uint64(message.MediaSize)
		messageMedia.MessageMediaByteCount = &messageMediaByteCount
	}
	if messageMediaHTTPSURL := strings.TrimSpace(message.MediaURL); openrecord.ValidHTTPSURL(messageMediaHTTPSURL) {
		messageMedia.MessageMediaHttpsUrl = messageMediaHTTPSURL
	}
	if messageMediaMetadataHTTPSURL := strings.TrimSpace(message.MetadataURL); openrecord.ValidHTTPSURL(messageMediaMetadataHTTPSURL) {
		messageMedia.MessageMediaMetadataHttpsUrl = messageMediaMetadataHTTPSURL
	}
	if messageMedia.MessageMediaKind == "" &&
		messageMedia.MessageMediaTitle == "" &&
		messageMedia.MessageMediaByteCount == nil &&
		messageMedia.MessageMediaHttpsUrl == "" &&
		messageMedia.MessageMediaMetadataHttpsUrl == "" {
		return nil
	}
	return messageMedia
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
