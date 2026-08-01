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
	conversation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/conversation"
	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	message "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/message"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (c *Crawler) TrawlerCommands() []trawlkit.TrawlerCommand {
	return []trawlkit.TrawlerCommand{
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_STATUS, TrawlerCommandHelpListing: trawlkit.TrawlerCommandHiddenFromHumanHelp},
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UPDATE, TrawlerCommandHelpListing: trawlkit.TrawlerCommandHiddenFromHumanHelp},
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH, TrawlerCommandHelpListing: trawlkit.TrawlerCommandHiddenFromHumanHelp},
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN, TrawlerCommandHelpListing: trawlkit.TrawlerCommandHiddenFromHumanHelp},
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_WHO, TrawlerCommandHelpListing: trawlkit.TrawlerCommandHiddenFromHumanHelp},
		{
			SharedTrawlerOperation:                 federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_MESSAGES,
			TrawlerCommandShownInBareTrawlOverview: true,
		},
		{
			SharedTrawlerOperation:                 federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_CONVERSATIONS,
			TrawlerCommandShownInBareTrawlOverview: true,
		},
	}
}

// Unread counts only received messages that the owner has not read.
func (c *Crawler) Conversations(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, q trawlkit.ConversationQuery) (*conversation.ConversationListResponse, error) {
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
	conversationRecords := make([]*conversation.ConversationRecord, 0, len(summaries))
	for _, summary := range summaries {
		conversationRecord := &conversation.ConversationRecord{
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
	return &conversation.ConversationListResponse{
		ConversationRecordsNewestFirst: conversationRecords,
		MoreConversationRecordsExist:   moreConversationRecordsExist,
	}, nil
}

func conversationParticipantIdentitiesObservedByTrawlerArchive(
	conversationSummary archive.ChatSummary,
) []*conversation.ConversationParticipantIdentityObservedByTrawlerArchive {
	participantIdentities := make(
		[]*conversation.ConversationParticipantIdentityObservedByTrawlerArchive,
		0,
		len(conversationSummary.ConversationParticipantIdentities),
	)
	for _, participantIdentity := range conversationSummary.ConversationParticipantIdentities {
		exactPersonFilterIdentifier := strings.TrimSpace(
			participantIdentity.ExactPersonFilterIdentifier.GetExactPersonFilterIdentifier(),
		)
		if exactPersonFilterIdentifier == "" {
			continue
		}
		participantIdentities = append(
			participantIdentities,
			&conversation.ConversationParticipantIdentityObservedByTrawlerArchive{
				PersonDisplayName: humanParticipantDisplayIdentity(
					participantIdentity.PersonDisplayName,
				),
				ExactPersonFilterIdentifiersObservedByTrawlerArchive: []*person.ExactPersonFilterIdentifier{{
					ExactPersonFilterIdentifier: exactPersonFilterIdentifier,
				}},
			},
		)
	}
	return participantIdentities
}

func (c *Crawler) ListMessages(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	query trawlkit.TrawlerMessageListQuery,
) (*message.MessageListResponse, error) {
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
	messageRecords := make([]*message.MessageRecord, 0, len(messages))
	messagesAreRestrictedToOneConversation := providerNativeConversationIdentifier != ""
	for _, message := range messages {
		chat, found := messageChatSummariesByChatID[message.ChatID]
		if !found {
			chat, err = st.Chat(ctx, message.ChatID)
			if err != nil {
				return nil, err
			}
			messageChatSummariesByChatID[message.ChatID] = chat
		}
		messageRecord := projectMessageRecord(message, chat)
		if messagesAreRestrictedToOneConversation {
			messageRecord.PeopleRelatedToMessage = imessageMessageListPeopleWhenRestrictedToOneConversation(message, chat)
		}
		messageRecords = append(messageRecords, messageRecord)
	}
	return &message.MessageListResponse{
		MessageRecordsInDisplayOrder: messageRecords,
		TotalMatchingMessageCount:    uint64(total),
		MoreMatchingMessagesExist:    total > int64(len(messages)),
		ConversationDisplayContextWhenMessagesAreRestrictedToOneConversation: scopedConversationDisplayContext,
	}, nil
}

func imessageMessageListPeopleWhenRestrictedToOneConversation(
	message archive.MessageRow,
	chat archive.ChatSummary,
) []*person.PersonRelatedToArchiveRecord {
	if !message.FromMe || chat.ParticipantCount <= 1 {
		return imessageCommandPeople(message, chat)
	}
	return []*person.PersonRelatedToArchiveRecord{{
		PersonDisplayName:         "me",
		PersonRoleInArchiveRecord: person.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_SENDER,
	}}
}

func imessageCommandPeople(message archive.MessageRow, chat archive.ChatSummary) []*person.PersonRelatedToArchiveRecord {
	if message.FromMe {
		people := []*person.PersonRelatedToArchiveRecord{{
			PersonDisplayName:         "me",
			PersonRoleInArchiveRecord: person.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_SENDER,
		}}
		for _, recipientDisplayName := range conversationParticipantDisplayIdentities(chat) {
			people = append(people, &person.PersonRelatedToArchiveRecord{
				PersonDisplayName:         recipientDisplayName,
				PersonRoleInArchiveRecord: person.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_RECIPIENT,
			})
		}
		return people
	}
	people := []*person.PersonRelatedToArchiveRecord{{
		PersonDisplayName:         "me",
		PersonRoleInArchiveRecord: person.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_RECIPIENT,
	}}
	if senderDisplayName := humanParticipantDisplayIdentity(message.SenderLabel); senderDisplayName != "" {
		people = append(people, &person.PersonRelatedToArchiveRecord{
			PersonDisplayName:         senderDisplayName,
			PersonRoleInArchiveRecord: person.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_SENDER,
		})
	}
	return people
}
