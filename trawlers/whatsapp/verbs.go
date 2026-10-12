package whatsapp

import (
	"context"
	"errors"
	"flag"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/whatsapp/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit"
	ckflags "github.com/opentrawl/opentrawl/trawlkit/flags"
	conversation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/conversation"
	message "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/message"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	"github.com/opentrawl/opentrawl/trawlkit/render"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Conversations implements trawlkit.ConversationLister. WhatsApp Desktop
// stores a real unread count per conversation, so the plain list and the
// --unread filter are answered from the store.
func (c *Crawler) Conversations(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, q trawlkit.ConversationQuery) (*conversation.ConversationListResponse, error) {
	limit := q.Limit
	if q.All {
		limit = 0
	} else if limit > 0 {
		limit++
	}
	st, err := store.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveErr(err)
	}
	var rows []store.Chat
	if q.Unread {
		rows, err = st.ListUnreadChats(ctx, limit)
	} else {
		rows, err = st.ListChats(ctx, limit)
	}
	if err != nil {
		return nil, err
	}
	moreConversationRecordsExist := !q.All && q.Limit > 0 && len(rows) > q.Limit
	if moreConversationRecordsExist {
		rows = rows[:q.Limit]
	}
	conversationRecords := make([]*conversation.ConversationRecord, 0, len(rows))
	for _, row := range rows {
		unreadMessageCount := uint64(row.UnreadCount)
		conversationRecord := &conversation.ConversationRecord{
			CanonicalRecordReference: trawlkit.NewCanonicalArchiveRecordReference(
				store.ChatRef(row.JID),
			),
			UnreadMessageCount: &unreadMessageCount,
		}
		if !row.LastMessageAt.IsZero() {
			conversationRecord.MostRecentConversationActivityTime = timestamppb.New(row.LastMessageAt)
		}
		participantIdentities, err := st.ConversationParticipantIdentitiesObservedByTrawlerArchive(
			ctx,
			row.JID,
		)
		if err != nil {
			return nil, err
		}
		if len(participantIdentities) > 0 {
			conversationRecord.ConversationParticipantIdentitiesObservedByTrawlerArchive =
				conversationParticipantIdentitiesObservedByTrawlerArchive(participantIdentities)
		}
		if row.Kind != "dm" {
			conversationRecord.ConversationDisplayName = whatsappConversationTitle(row)
		}
		conversationRecords = append(conversationRecords, conversationRecord)
	}
	return &conversation.ConversationListResponse{
		ConversationRecordsNewestFirst: conversationRecords,
		MoreConversationRecordsExist:   moreConversationRecordsExist,
	}, nil
}

func whatsappConversationTitle(conversation store.Chat) string {
	return humanDisplayName(conversation.Name)
}

type messageFlagValues struct {
	sender   string
	after    string
	before   string
	fromMe   bool
	fromThem bool
	hasMedia bool
}

func (c *Crawler) bindMessageFlags(fs *flag.FlagSet) {
	c.messageFlags = messageFlagValues{}
	fs.StringVar(&c.messageFlags.sender, "sender", "", "Show only messages from `PERSON`")
	fs.StringVar(&c.messageFlags.after, "after", "", render.OnOrAfterDateOptionDescription)
	fs.StringVar(&c.messageFlags.before, "before", "", render.OnOrBeforeDateOptionDescription)
	fs.BoolVar(&c.messageFlags.fromMe, "from-me", false, "Show only messages sent by you")
	fs.BoolVar(&c.messageFlags.fromThem, "from-them", false, "Show only messages sent by other people")
	fs.BoolVar(&c.messageFlags.hasMedia, "has-media", false, "Show only messages with media")
}

func (f messageFlagValues) resolve(maximumReturnedMessageCount int) (store.MessageFilter, error) {
	if f.fromMe && f.fromThem {
		return store.MessageFilter{}, render.OptionsCannotBeUsedTogether("from-me", "from-them")
	}
	out := store.MessageFilter{
		Sender:   f.sender,
		Limit:    maximumReturnedMessageCount,
		HasMedia: f.hasMedia,
		Asc:      false,
	}
	if f.fromMe {
		v := true
		out.FromMe = &v
	}
	if f.fromThem {
		v := false
		out.FromMe = &v
	}
	if strings.TrimSpace(f.after) != "" {
		t, err := ckflags.Date(f.after)
		if err != nil {
			return store.MessageFilter{}, render.DateOptionNeedsTime("after")
		}
		out.After = &t
	}
	if strings.TrimSpace(f.before) != "" {
		t, err := ckflags.ParseDateOrTimeThroughEndOfEnteredPrecision(f.before)
		if err != nil {
			return store.MessageFilter{}, render.DateOptionNeedsTime("before")
		}
		out.Before = &t
	}
	if out.After != nil && out.Before != nil && out.After.After(*out.Before) {
		return store.MessageFilter{}, render.AfterMustNotBeLaterThanBefore()
	}
	return out, nil
}

func (c *Crawler) ListMessages(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	query trawlkit.TrawlerMessageListQuery,
) (*message.MessageListResponse, error) {
	filter, err := c.messageFlags.resolve(query.MaximumReturnedMessageCount)
	if err != nil {
		return nil, usageErr(err)
	}
	filter.ChatJID, err = req.ResolveLocalConversationShortReferenceToProviderNativeConversationIdentifier(
		ctx,
		trawlkit.NewLocalTrawlerShortReference(
			query.OptionalLocalConversationShortReferenceForRestrictingMessagesToOneConversation,
		),
		store.ChatRefPrefix,
	)
	if errors.Is(err, trawlkit.ErrLocalConversationShortReferenceDoesNotIdentifyConversation) {
		return nil, usageErr(render.LinkIsNotForArchiveRecordKind(render.ArchiveRecordKindNounConversation))
	}
	if errors.Is(err, trawlkit.ErrUnknownShortRef) {
		return nil, commandErr(1, "not_found", render.NoArchiveRecordHasLink(render.ArchiveRecordKindNounConversation))
	}
	if errors.Is(err, trawlkit.ErrAmbiguousShortRef) {
		return nil, usageErr(render.SeveralArchiveRecordsHaveLink(render.ArchiveRecordKindNounConversation))
	}
	if err != nil {
		return nil, err
	}
	st, err := store.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveErr(err)
	}
	messages, total, err := st.MessagesAndTotalCount(ctx, filter)
	if err != nil {
		return nil, err
	}
	messageRecords := make([]*message.MessageRecord, 0, len(messages))
	for _, message := range messages {
		messageRecords = append(messageRecords, projectMessageRecord(message))
	}
	conversationDisplayNameForRestrictedMessageRecords := ""
	if filter.ChatJID != "" && len(messages) > 0 {
		conversationDisplayNameForRestrictedMessageRecords = messageWhere(messages[0])
	}
	return &message.MessageListResponse{
		MessageRecordsNewestFirst: messageRecords,
		TotalMatchingMessageCount: uint64(total),
		MoreMatchingMessagesExist: total > len(messages),
		ConversationDisplayNameForMessageRecordsRestrictedToOneConversation: conversationDisplayNameForRestrictedMessageRecords,
	}, nil
}

func whatsappMessageCommandPeople(message store.Message) []*person.PersonRelatedToArchiveRecord {
	people := []*person.PersonRelatedToArchiveRecord{{
		PersonDisplayName:         "me",
		PersonRoleInArchiveRecord: person.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_RECIPIENT,
	}}
	if message.FromMe {
		people[0].PersonRoleInArchiveRecord = person.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_SENDER
		if message.ChatKind == "dm" {
			if recipientDisplayName := humanDisplayName(message.ChatName); recipientDisplayName != "" {
				people = append(people, &person.PersonRelatedToArchiveRecord{
					PersonDisplayName:         recipientDisplayName,
					PersonRoleInArchiveRecord: person.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_RECIPIENT,
				})
			}
		}
		return people
	}
	if senderDisplayName := humanDisplayName(message.SenderName); senderDisplayName != "" {
		people = append(people, &person.PersonRelatedToArchiveRecord{
			PersonDisplayName:         senderDisplayName,
			PersonRoleInArchiveRecord: person.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_SENDER,
		})
	}
	return people
}
