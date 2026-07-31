package telegram

import (
	"context"
	"errors"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit"
	command "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command"
	conversation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/conversation"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
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

func (c *Crawler) runFolders(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
	r := c.handler(ctx, req)
	if len(req.TrawlerCommandPositionalArguments) != 0 {
		return nil, usageErr(errors.New("folders takes flags only"))
	}
	var response *command.TrawlerCommandResponse
	err := r.withReadOnlyStore(func(st *store.Store) error {
		folders, err := st.ListFolders(r.ctx)
		if err != nil {
			return err
		}
		if len(folders) == 0 {
			response = folderListCommandResponse(nil, 0)
			return nil
		}
		rows := make([]*presentation.TrawlerSpecificCommandListPresentationRow, 0, len(folders))
		for _, folder := range folders {
			rows = append(rows, trawlerSpecificCommandListPresentationRow(
				trawlerSpecificCommandTextPresentationValue(folderHumanName(folder)),
				trawlerSpecificCommandCountPresentationValue(uint64(folder.ChatCount)),
				trawlerSpecificCommandCountPresentationValue(uint64(folder.UnreadCount)),
			))
		}
		response = folderListCommandResponse(rows, uint64(len(folders)))
		return nil
	})
	return response, err
}

func trawlerSpecificCommandListPresentationRow(
	columnValuesInDisplayOrder ...*presentation.TrawlerSpecificCommandPresentationValue,
) *presentation.TrawlerSpecificCommandListPresentationRow {
	return &presentation.TrawlerSpecificCommandListPresentationRow{
		ColumnValuesInDisplayOrder: columnValuesInDisplayOrder,
	}
}

func trawlerSpecificCommandTextPresentationValue(
	value string,
) *presentation.TrawlerSpecificCommandPresentationValue {
	return &presentation.TrawlerSpecificCommandPresentationValue{
		TypedValue: &presentation.TrawlerSpecificCommandPresentationValue_Text{Text: value},
	}
}

func trawlerSpecificCommandCountPresentationValue(
	value uint64,
) *presentation.TrawlerSpecificCommandPresentationValue {
	return &presentation.TrawlerSpecificCommandPresentationValue{
		TypedValue: &presentation.TrawlerSpecificCommandPresentationValue_UnsignedCount{
			UnsignedCount: value,
		},
	}
}

func folderListCommandResponse(
	rows []*presentation.TrawlerSpecificCommandListPresentationRow,
	totalFolderCount uint64,
) *command.TrawlerCommandResponse {
	return &command.TrawlerCommandResponse{
		TypedTrawlerCommandResponse: &command.TrawlerCommandResponse_TrawlerSpecificCommandResponse{
			TrawlerSpecificCommandResponse: &command.TrawlerSpecificCommandResponse{
				TrawlerSpecificCommandPresentation: &command.TrawlerSpecificCommandResponse_TrawlerSpecificCommandListPresentation{
					TrawlerSpecificCommandListPresentation: &presentation.TrawlerSpecificCommandListPresentation{
						ColumnDisplayNamesInOrder: []string{"folder", "conversations", "unread"},
						RowsInDisplayOrder:        rows,
						TotalRowCount: &presentation.TrawlerSpecificCommandListPresentation_ExactTotalRowCount{
							ExactTotalRowCount: totalFolderCount,
						},
						ConciseTextShownWhenListIsEmpty: "No folders.",
					},
				},
			},
		},
	}
}
