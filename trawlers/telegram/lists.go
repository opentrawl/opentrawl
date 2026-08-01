package telegram

import (
	"context"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit"
	conversation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/conversation"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Conversations implements trawlkit.ConversationLister. Telegram stores a
// real unread count per conversation, so the plain list and the --unread
// filter both come from the store.
func (c *Crawler) Conversations(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, q trawlkit.ConversationQuery) (*conversation.ConversationListResponse, error) {
	limit := q.Limit
	if q.All {
		limit = 0
	} else if limit > 0 {
		limit++
	}
	r := c.handler(ctx, req)
	response := &conversation.ConversationListResponse{}
	err := r.withReadOnlyStore(func(st *store.Store) error {
		rows, err := st.ListChats(r.ctx, limit, q.Unread)
		if err != nil {
			return err
		}
		// Telegram's group_participants table records observed group message
		// authors. It does not establish current or historical membership.
		observedGroupMessageAuthorsByChat, err := st.ObservedGroupMessageAuthorsByChat(r.ctx)
		if err != nil {
			return err
		}
		response.MoreConversationRecordsExist = !q.All && q.Limit > 0 && len(rows) > q.Limit
		if response.MoreConversationRecordsExist {
			rows = rows[:q.Limit]
		}
		response.ConversationRecordsNewestFirst = make([]*conversation.ConversationRecord, 0, len(rows))
		for _, chat := range rows {
			unreadMessageCount := uint64(chat.UnreadCount)
			conversationRecord := &conversation.ConversationRecord{
				CanonicalRecordReference: trawlkit.NewCanonicalArchiveRecordReference(
					store.ChatRef(chat.JID),
				),
				UnreadMessageCount: &unreadMessageCount,
			}
			if !chat.LastMessageAt.IsZero() {
				conversationRecord.MostRecentConversationActivityTime = timestamppb.New(chat.LastMessageAt)
			}
			switch chat.Kind {
			case "user":
				otherPersonDisplayName := humanTelegramName(chat.Name)
				exactPersonFilterIdentifier := strings.TrimSpace(chat.JID)
				if exactPersonFilterIdentifier != "" &&
					!strings.EqualFold(otherPersonDisplayName, "me") {
					conversationRecord.ConversationParticipantIdentitiesObservedByTrawlerArchive =
						[]*conversation.ConversationParticipantIdentityObservedByTrawlerArchive{{
							PersonDisplayName: otherPersonDisplayName,
							ExactPersonFilterIdentifiersObservedByTrawlerArchive: []string{
								exactPersonFilterIdentifier,
							},
						}}
				}
			case "group", "channel":
				conversationRecord.ConversationDisplayName = telegramConversationTitle(chat)
				if observedGroupMessageAuthors, found := observedGroupMessageAuthorsByChat[chat.JID]; found {
					conversationRecord.ConversationParticipantIdentitiesObservedByTrawlerArchive =
						telegramConversationParticipantIdentitiesObservedByTrawlerArchive(
							observedGroupMessageAuthors.ConversationParticipantIdentitiesObservedByTrawlerArchive,
						)
					if observedGroupMessageAuthors.NumberOfDistinctConversationParticipantRecordsObservedByTrawlerArchive > 0 {
						numberOfDistinctConversationParticipantRecordsObservedByTrawlerArchive := uint64(
							observedGroupMessageAuthors.NumberOfDistinctConversationParticipantRecordsObservedByTrawlerArchive,
						)
						conversationRecord.NumberOfDistinctConversationParticipantRecordsObservedByTrawlerArchive =
							&numberOfDistinctConversationParticipantRecordsObservedByTrawlerArchive
					}
				}
			}
			response.ConversationRecordsNewestFirst = append(response.ConversationRecordsNewestFirst, conversationRecord)
		}
		return nil
	})
	return response, err
}

func telegramConversationParticipantIdentitiesObservedByTrawlerArchive(
	participantIdentities []store.ConversationParticipantIdentityObservedByTrawlerArchive,
) []*conversation.ConversationParticipantIdentityObservedByTrawlerArchive {
	projectedParticipantIdentities := make(
		[]*conversation.ConversationParticipantIdentityObservedByTrawlerArchive,
		0,
		len(participantIdentities),
	)
	for _, participantIdentity := range participantIdentities {
		projectedParticipantIdentities = append(
			projectedParticipantIdentities,
			&conversation.ConversationParticipantIdentityObservedByTrawlerArchive{
				PersonDisplayName: participantIdentity.PersonDisplayName,
				ExactPersonFilterIdentifiersObservedByTrawlerArchive: append(
					[]string(nil),
					participantIdentity.ExactPersonFilterIdentifiersObservedByTrawlerArchive...,
				),
			},
		)
	}
	return projectedParticipantIdentities
}
