package beeper

import (
	"context"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/plugins/trawl-beeper/internal/beeperstore"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	conversation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/conversation"
	message "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/message"
	open "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open"
	"github.com/opentrawl/opentrawl/trawlkit/render"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const beeperOpenMessageWindowEachSide = 12

// OpenCanonicalRecord opens either an archived Beeper message (with surrounding
// conversation context) or an archived Beeper conversation.
func (t *BeeperTrawler) OpenCanonicalRecord(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	canonicalRecordReference *trawlkit.CanonicalArchiveRecordReference,
) (*open.OpenRecord, error) {
	st, err := beeperstore.UseExisting(req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveFailure(fmt.Errorf("open beeper archive: %w", err))
	}

	refText := strings.TrimSpace(trawlkit.CanonicalArchiveRecordReferenceText(canonicalRecordReference))
	if !strings.HasPrefix(refText, beeperTrawlerIdentifier+":") {
		return nil, commandFailure(1, "foreign_ref", render.LinkIsForAnotherTrawler())
	}

	if messageID, ok := beeperstore.ParseBeeperCanonicalMessageID(refText); ok {
		return t.openBeeperMessage(ctx, st, messageID)
	}
	if chatID, ok := beeperstore.ParseBeeperCanonicalChatID(refText); ok {
		return t.openBeeperConversation(ctx, st, chatID)
	}
	return nil, commandFailure(1, "invalid_ref", render.LinkIsNotValid())
}

func (t *BeeperTrawler) openBeeperMessage(
	ctx context.Context,
	st *beeperstore.BeeperArchiveStore,
	messageID string,
) (*open.OpenRecord, error) {
	target, err := st.MessageByID(ctx, messageID)
	if err != nil {
		if beeperstore.ErrorsIsNoRows(err) {
			return nil, commandFailure(1, "not_found", render.NoArchiveRecordHasLink(render.ArchiveRecordKindNounMessage))
		}
		return nil, err
	}
	window, err := st.MessageWindow(ctx, target, beeperOpenMessageWindowEachSide)
	if err != nil {
		return nil, err
	}
	participants, err := st.ConversationParticipantIdentities(ctx, target.ChatID)
	if err != nil {
		return nil, err
	}
	participantNames := make([]string, 0, len(participants))
	for _, p := range participants {
		if name := strings.TrimSpace(p.PersonDisplayName); name != "" {
			participantNames = append(participantNames, name)
		}
	}

	contextRecords := make([]*message.MessageRecord, 0, len(window.Messages))
	for _, msg := range window.Messages {
		contextRecords = append(contextRecords, projectBeeperMessageRecord(msg))
	}

	openedRef := trawlkit.NewCanonicalArchiveRecordReference(beeperstore.BeeperMessageRef(target.MessageID))
	opened := &message.OpenedMessageRecordWithConversationContext{
		ConversationDisplayName:                      beeperstore.FormatNetworkChatDisplayTitle(target.Network, target.ChatTitle),
		ConversationParticipantDisplayNames:          participantNames,
		ConversationContextMessageRecordsNewestFirst: contextRecords,
		OpenedMessageRecordReference:                 openedRef,
		OpenedMessageRecordAnchor:                    trawlkit.NewRecordAnchorIdentifier(trawlkit.MatchAnchorID),
		EarlierConversationContextMessagesOmitted:    window.BeforeTruncated,
		LaterConversationContextMessagesOmitted:      window.AfterTruncated,
		ConversationRecordReference:                  trawlkit.NewCanonicalArchiveRecordReference(beeperstore.BeeperChatRef(target.ChatID)),
	}
	record := &open.OpenRecord{
		RecordTrawler:            t.RegisteredTrawlerDeclaration().RegisteredTrawler,
		CanonicalRecordReference: openedRef,
		TypedOpenedRecord: &open.OpenRecord_OpenedMessageRecordWithConversationContext{
			OpenedMessageRecordWithConversationContext: opened,
		},
	}
	if err := openrecord.Validate(record); err != nil {
		return nil, err
	}
	return record, nil
}

func (t *BeeperTrawler) openBeeperConversation(
	ctx context.Context,
	st *beeperstore.BeeperArchiveStore,
	chatID string,
) (*open.OpenRecord, error) {
	chat, err := st.ChatByID(ctx, chatID)
	if err != nil {
		if beeperstore.ErrorsIsNoRows(err) {
			return nil, commandFailure(1, "not_found", render.NoArchiveRecordHasLink(render.ArchiveRecordKindNounConversation))
		}
		return nil, err
	}
	participants, err := st.ConversationParticipantIdentities(ctx, chat.ChatID)
	if err != nil {
		return nil, err
	}
	unreadCount := uint64(max(chat.UnreadCount, 0))
	chatRef := trawlkit.NewCanonicalArchiveRecordReference(beeperstore.BeeperChatRef(chat.ChatID))
	convRecord := &conversation.ConversationRecord{
		CanonicalRecordReference: chatRef,
		ConversationDisplayName:  beeperstore.FormatNetworkChatDisplayTitle(chat.Network, chat.Title),
		UnreadMessageCount:       &unreadCount,
	}
	if !chat.LastActivity.IsZero() {
		convRecord.MostRecentConversationActivityTime = timestamppb.New(chat.LastActivity)
	}
	if len(participants) > 0 {
		convRecord.ConversationParticipantIdentitiesObservedByTrawlerArchive = projectConversationParticipants(participants)
	}
	record := &open.OpenRecord{
		RecordTrawler:            t.RegisteredTrawlerDeclaration().RegisteredTrawler,
		CanonicalRecordReference: chatRef,
		TypedOpenedRecord: &open.OpenRecord_ConversationRecord{
			ConversationRecord: convRecord,
		},
	}
	if err := openrecord.Validate(record); err != nil {
		return nil, err
	}
	return record, nil
}
