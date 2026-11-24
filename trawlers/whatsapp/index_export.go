package whatsapp

import (
	"context"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/whatsapp/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit"
	indexcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/index"
)

func (c *Crawler) ExportArchiveIndexChanges(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	sinceOpaqueCursor string,
	emit func(*indexcontract.ArchiveIndexChangeBatch) error,
) error {
	st, err := store.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return archiveErr(fmt.Errorf("open archive: %w", err))
	}
	participantDisplayNamesByChatJID, err := participantDisplayNamesOfUnnamedGroupConversations(ctx, st)
	if err != nil {
		return archiveErr(fmt.Errorf("read conversation participants for index: %w", err))
	}
	return st.ExportArchiveIndexChanges(ctx, sinceOpaqueCursor, func(archivedMessage store.Message) *indexcontract.IndexedArchiveRecordUpsert {
		return projectIndexedArchiveRecord(archivedMessage, participantDisplayNamesByChatJID[archivedMessage.ChatJID])
	}, emit)
}

func participantDisplayNamesOfUnnamedGroupConversations(ctx context.Context, st *store.Store) (map[string][]string, error) {
	conversations, err := st.ListChats(ctx, 0)
	if err != nil {
		return nil, err
	}
	participantDisplayNamesByChatJID := map[string][]string{}
	for _, conversation := range conversations {
		if conversation.Kind != "group" || whatsappConversationTitle(conversation) != "" {
			continue
		}
		participantIdentities, err := st.ConversationParticipantIdentitiesObservedByTrawlerArchive(ctx, conversation.JID)
		if err != nil {
			return nil, err
		}
		participantDisplayNamesByChatJID[conversation.JID] = resolvedParticipantNames(
			conversationParticipantDisplayNamesFromIdentitiesObservedByTrawlerArchive(participantIdentities),
		)
	}
	return participantDisplayNamesByChatJID, nil
}

func projectIndexedArchiveRecord(archivedMessage store.Message, groupParticipantDisplayNames []string) *indexcontract.IndexedArchiveRecordUpsert {
	bodyText := outputField(archivedMessage.Text)
	if bodyText == "" || messageTextIsKnownProviderMetadata(archivedMessage, bodyText) {
		return nil
	}
	role := indexcontract.ObservedArchiveRecordRole_OBSERVED_ARCHIVE_RECORD_ROLE_RECEIVED
	if archivedMessage.FromMe {
		role = indexcontract.ObservedArchiveRecordRole_OBSERVED_ARCHIVE_RECORD_ROLE_AUTHORED
	}
	sender := &indexcontract.ArchiveRecordParticipant{
		DisplayName:     whatsappMessageSenderDisplayName(archivedMessage),
		ParticipantRole: "sender",
	}
	senderJID := strings.TrimSpace(archivedMessage.SenderJID)
	metadataText := ""
	if sender.DisplayName != "" {
		sender.AddressOrHandle = senderJID
	} else {
		metadataText = senderJID
	}
	var participants []*indexcontract.ArchiveRecordParticipant
	if sender.DisplayName != "" {
		participants = []*indexcontract.ArchiveRecordParticipant{sender}
	}
	canonical := trawlkit.NewCanonicalArchiveRecordReference(messageRef(archivedMessage))
	return &indexcontract.IndexedArchiveRecordUpsert{
		StableRecordReference:          canonical,
		ResolvedRecordReference:        canonical,
		RecordKind:                     indexcontract.ArchiveRecordKind_ARCHIVE_RECORD_KIND_CHAT_MESSAGE,
		ObservedSourceRole:             role,
		ConversationOrThreadIdentifier: store.ChatRef(archivedMessage.ChatJID),
		DisplayContextTitle:            whatsappConversationDisplayName(archivedMessage, groupParticipantDisplayNames),
		MetadataText:                   metadataText,
		OccurredUnixSeconds:            archivedMessage.Timestamp.Unix(),
		HasOccurredTime:                !archivedMessage.Timestamp.IsZero(),
		HasTimeOfDay:                   true,
		BodyText:                       bodyText,
		Participants:                   participants,
	}
}
