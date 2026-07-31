package imessage

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/imessage/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/output"
	conversationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/conversation/v1"
	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
	messagev1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/message/v1"
	personv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (c *Crawler) TrawlerCommands() []trawlkit.TrawlerCommand {
	return []trawlkit.TrawlerCommand{{
		SharedTrawlerOperation: federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_MESSAGES,
	}}
}

// Unread counts only received messages that the owner has not read.
func (c *Crawler) Conversations(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, q trawlkit.ConversationQuery) (*conversationv1.ConversationListResponse, error) {
	limit := q.Limit
	if q.All {
		limit = 0
	} else if limit > 0 {
		limit++
	}
	st, err := archive.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	summaries, err := st.Chats(ctx, archive.ChatListOptions{Limit: limit, UnreadOnly: q.Unread})
	if err != nil {
		return nil, err
	}
	moreConversationRecordsExist := !q.All && q.Limit > 0 && len(summaries) > q.Limit
	if moreConversationRecordsExist {
		summaries = summaries[:q.Limit]
	}
	conversationRecords := make([]*conversationv1.ConversationRecord, 0, len(summaries))
	for _, summary := range summaries {
		conversationRecord := &conversationv1.ConversationRecord{
			CanonicalRecordReference: trawlkit.NewCanonicalArchiveRecordReference(
				archive.ChatRef(summary.ChatID),
			),
			ConversationDisplayName: conversationListTitle(summary),
			ConversationParticipantIdentitiesObservedByTrawlerArchive: conversationParticipantIdentitiesObservedByTrawlerArchive(
				summary,
			),
		}
		if summary.ParticipantCount > 0 {
			numberOfDistinctConversationParticipantRecordsObservedByTrawlerArchive :=
				uint64(summary.ParticipantCount)
			conversationRecord.NumberOfDistinctConversationParticipantRecordsObservedByTrawlerArchive =
				&numberOfDistinctConversationParticipantRecordsObservedByTrawlerArchive
		}
		if summary.LatestMessageDate > 0 {
			conversationRecord.MostRecentConversationActivityTime = timestamppb.New(archive.AppleDateTime(summary.LatestMessageDate))
		}
		if summary.Unread != nil && *summary.Unread >= 0 {
			unreadMessageCount := uint64(*summary.Unread)
			conversationRecord.UnreadMessageCount = &unreadMessageCount
		}
		conversationRecords = append(conversationRecords, conversationRecord)
	}
	return &conversationv1.ConversationListResponse{
		ConversationRecordsNewestFirst: conversationRecords,
		MoreConversationRecordsExist:   moreConversationRecordsExist,
	}, nil
}

func conversationParticipantIdentitiesObservedByTrawlerArchive(
	conversation archive.ChatSummary,
) []*conversationv1.ConversationParticipantIdentityObservedByTrawlerArchive {
	participantIdentities := make(
		[]*conversationv1.ConversationParticipantIdentityObservedByTrawlerArchive,
		0,
		len(conversation.ConversationParticipantIdentities),
	)
	for _, participantIdentity := range conversation.ConversationParticipantIdentities {
		exactPersonFilterIdentifier := strings.TrimSpace(
			participantIdentity.ExactPersonFilterIdentifier,
		)
		if exactPersonFilterIdentifier == "" {
			continue
		}
		participantIdentities = append(
			participantIdentities,
			&conversationv1.ConversationParticipantIdentityObservedByTrawlerArchive{
				PersonDisplayName: humanParticipantDisplayIdentity(
					participantIdentity.PersonDisplayName,
				),
				ExactPersonFilterIdentifiersObservedByTrawlerArchive: []string{
					exactPersonFilterIdentifier,
				},
			},
		)
	}
	return participantIdentities
}

func (c *Crawler) ListMessages(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	query trawlkit.TrawlerMessageListQuery,
) (*messagev1.MessageListResponse, error) {
	providerNativeConversationIdentifier, err := req.ResolveLocalConversationShortReferenceToProviderNativeConversationIdentifier(
		ctx,
		trawlkit.NewLocalTrawlerShortReference(
			query.OptionalLocalConversationShortReferenceForRestrictingMessagesToOneConversation,
		),
		archive.ChatRefPrefix,
	)
	if errors.Is(err, trawlkit.ErrLocalConversationShortReferenceDoesNotIdentifyConversation) {
		return nil, usageErr(output.HumanFacingErrorMessage("The link is for a message, not a conversation."))
	}
	if errors.Is(err, trawlkit.ErrUnknownShortRef) {
		return nil, commandErr(1, "not_found", output.HumanFacingErrorMessage("No conversation has that link."))
	}
	if errors.Is(err, trawlkit.ErrAmbiguousShortRef) {
		return nil, usageErr(output.HumanFacingErrorMessage("More than one conversation has that link."))
	}
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(query.OptionalLocalConversationShortReferenceForRestrictingMessagesToOneConversation) != "" {
		resolvedConversationID, parseErr := strconv.ParseInt(providerNativeConversationIdentifier, 10, 64)
		if parseErr != nil || resolvedConversationID <= 0 {
			return nil, usageErr(output.HumanFacingErrorMessage("The conversation link is not valid."))
		}
	}
	st, err := archive.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	messages, err := st.Messages(ctx, providerNativeConversationIdentifier, query.MaximumReturnedMessageCount, false)
	if err != nil {
		return nil, err
	}
	total, err := st.CountMessages(ctx, providerNativeConversationIdentifier)
	if err != nil {
		return nil, err
	}
	messageChatSummariesByChatID := make(map[string]archive.ChatSummary)
	scopedConversationDisplayContext := ""
	if providerNativeConversationIdentifier != "" {
		chat, err := st.Chat(ctx, providerNativeConversationIdentifier)
		if errors.Is(err, archive.ErrChatNotFound) {
			return nil, commandErr(1, "not_found", output.HumanFacingErrorMessage("No conversation has that link."))
		}
		if err != nil {
			return nil, err
		}
		messageChatSummariesByChatID[providerNativeConversationIdentifier] = chat
		scopedConversationDisplayContext = conversationDisplayName(chat)
	}
	messageRecords := make([]*messagev1.MessageRecord, 0, len(messages))
	for _, message := range messages {
		chat, found := messageChatSummariesByChatID[message.ChatID]
		if !found {
			chat, err = st.Chat(ctx, message.ChatID)
			if err != nil {
				return nil, err
			}
			messageChatSummariesByChatID[message.ChatID] = chat
		}
		messageRecords = append(messageRecords, projectMessageRecord(message, chat))
	}
	return &messagev1.MessageListResponse{
		MessageRecordsInDisplayOrder: messageRecords,
		TotalMatchingMessageCount:    uint64(total),
		MoreMatchingMessagesExist:    total > int64(len(messages)),
		ConversationDisplayContextWhenMessagesAreRestrictedToOneConversation: scopedConversationDisplayContext,
	}, nil
}

func imessageCommandPeople(message archive.MessageRow, chat archive.ChatSummary) []*personv1.PersonRelatedToArchiveRecord {
	if message.FromMe {
		people := []*personv1.PersonRelatedToArchiveRecord{{
			PersonDisplayName:         "me",
			PersonRoleInArchiveRecord: personv1.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_SENDER,
		}}
		for _, recipientDisplayName := range conversationParticipantDisplayIdentities(chat) {
			people = append(people, &personv1.PersonRelatedToArchiveRecord{
				PersonDisplayName:         recipientDisplayName,
				PersonRoleInArchiveRecord: personv1.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_RECIPIENT,
			})
		}
		return people
	}
	people := []*personv1.PersonRelatedToArchiveRecord{{
		PersonDisplayName:         "me",
		PersonRoleInArchiveRecord: personv1.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_RECIPIENT,
	}}
	if senderDisplayName := humanParticipantDisplayIdentity(message.SenderLabel); senderDisplayName != "" {
		people = append(people, &personv1.PersonRelatedToArchiveRecord{
			PersonDisplayName:         senderDisplayName,
			PersonRoleInArchiveRecord: personv1.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_SENDER,
		})
	}
	return people
}
