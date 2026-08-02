package telegram

import (
	"context"
	"strings"
	"unicode"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	message "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/message"
	open "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
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

func projectOpenedMessageRecordWithConversationContext(value store.MessageWindow) *message.OpenedMessageRecordWithConversationContext {
	title := strings.TrimSpace(telegramMessageConversationDisplayName(value.Target))
	if title == "" || title == "Telegram conversation" {
		title = "Telegram conversation"
	}
	contextMessageRecords := make([]*message.MessageRecord, 0, len(value.Messages))
	for _, message := range value.Messages {
		contextMessageRecords = append(contextMessageRecords, telegramOpenedMessageRecord(message))
	}
	openedMessageRecord := &message.OpenedMessageRecordWithConversationContext{
		ConversationDisplayName:                      title,
		ConversationParticipantDisplayNames:          presentationParticipants(value.Participants),
		ConversationContextMessageRecordsNewestFirst: contextMessageRecords,
		OpenedMessageRecordReference: trawlkit.NewCanonicalArchiveRecordReference(
			store.MessageRef(value.Target.SourcePK),
		),
		OpenedMessageRecordAnchor:                 trawlkit.NewRecordAnchorIdentifier(trawlkit.MatchAnchorID),
		EarlierConversationContextMessagesOmitted: value.BeforeTruncated,
		LaterConversationContextMessagesOmitted:   value.AfterTruncated,
		ConversationRecordReference: trawlkit.NewCanonicalArchiveRecordReference(
			store.ChatRef(value.AccountScopedConversationIdentifierForConversationAcrossTelegramMigrations),
		),
	}
	return openedMessageRecord
}

func telegramOpenedMessageRecord(telegramMessage store.Message) *message.MessageRecord {
	messageRecord := &message.MessageRecord{
		CanonicalRecordReference: trawlkit.NewCanonicalArchiveRecordReference(
			store.MessageRef(telegramMessage.SourcePK),
		),
		MessageText:             messageText(telegramMessage),
		ConversationDisplayName: telegramMessageConversationDisplayName(telegramMessage),
		MessageMedia:            telegramMessageMedia(telegramMessage),
	}
	if !telegramMessage.Timestamp.IsZero() {
		messageRecord.MessageTime = &presentation.ArchiveRecordAssociatedTimeForDisplay{
			ArchiveRecordAssociatedTime: &presentation.ArchiveRecordAssociatedTimeForDisplay_ExactTime{ExactTime: timestamppb.New(telegramMessage.Timestamp)},
		}
	}
	if senderDisplayName := strings.TrimSpace(messageWho(telegramMessage)); senderDisplayName != "" {
		messageRecord.PeopleRelatedToMessage = []*person.PersonRelatedToArchiveRecord{{
			PersonDisplayName:         senderDisplayName,
			PersonRoleInArchiveRecord: person.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_SENDER,
		}}
	}
	return messageRecord
}

func telegramMessageMedia(telegramMessage store.Message) *message.MessageMedia {
	messageMediaHumanProjection := projectTelegramMessageMediaForHumanPresentation(telegramMessage)
	messageMedia := &message.MessageMedia{
		MessageMediaContentKind: messageMediaHumanProjection.messageMediaContentKind,
		MessageMediaTitle:       messageMediaHumanProjection.messageMediaTitle,
	}
	if telegramMessage.MediaSize > 0 {
		messageMediaByteCount := uint64(telegramMessage.MediaSize)
		messageMedia.MessageMediaByteCount = &messageMediaByteCount
	}
	if messageMediaHTTPSURL := strings.TrimSpace(telegramMessage.MediaURL); openrecord.ValidHTTPSURL(messageMediaHTTPSURL) {
		messageMedia.MessageMediaHttpsUrl = messageMediaHTTPSURL
	}
	if messageMediaMetadataHTTPSURL := strings.TrimSpace(telegramMessage.MetadataURL); openrecord.ValidHTTPSURL(messageMediaMetadataHTTPSURL) {
		messageMedia.MessageMediaMetadataHttpsUrl = messageMediaMetadataHTTPSURL
	}
	if messageMedia.MessageMediaContentKind == message.MessageMediaContentKind_MESSAGE_MEDIA_CONTENT_KIND_UNSPECIFIED &&
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
