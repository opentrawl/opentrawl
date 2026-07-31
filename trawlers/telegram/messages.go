package telegram

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/flags"
	messagev1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/message/v1"
	personv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (c *Crawler) ListMessages(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	query trawlkit.TrawlerMessageListQuery,
) (*messagev1.MessageListResponse, error) {
	r := c.handler(ctx, req)
	filter, err := c.messageFilter(query.MaximumReturnedMessageCount)
	if err != nil {
		return nil, err
	}
	filter.ChatJID, err = req.ResolveLocalConversationShortReferenceToProviderNativeConversationIdentifier(
		ctx,
		trawlkit.NewLocalTrawlerShortReference(
			query.OptionalLocalConversationShortReferenceForRestrictingMessagesToOneConversation,
		),
		store.ChatRefPrefix,
	)
	if errors.Is(err, trawlkit.ErrLocalConversationShortReferenceDoesNotIdentifyConversation) {
		return nil, usageErr(errors.New("The link is for a message, not a conversation."))
	}
	if errors.Is(err, trawlkit.ErrUnknownShortRef) {
		return nil, commandErr(1, "not_found", errors.New("No conversation has that link."))
	}
	if errors.Is(err, trawlkit.ErrAmbiguousShortRef) {
		return nil, usageErr(errors.New("More than one conversation has that link."))
	}
	if err != nil {
		return nil, err
	}
	var response *messagev1.MessageListResponse
	err = r.withReadOnlyStore(func(st *store.Store) error {
		messages, err := st.Messages(r.ctx, filter)
		if err != nil {
			return err
		}
		total, err := st.CountMessages(r.ctx, filter)
		if err != nil {
			return err
		}
		messageRecords := make([]*messagev1.MessageRecord, 0, len(messages))
		outgoingGroupRecipientDisplayNamesByConversation := map[string][]string{}
		for _, message := range messages {
			peopleRelatedToMessage, err := telegramMessagePeople(
				r.ctx,
				st,
				message,
				outgoingGroupRecipientDisplayNamesByConversation,
			)
			if err != nil {
				return err
			}
			messageRecord := &messagev1.MessageRecord{
				CanonicalRecordReference: trawlkit.NewCanonicalArchiveRecordReference(
					store.MessageRef(message.SourcePK),
				),
				PeopleRelatedToMessage:      peopleRelatedToMessage,
				DisplayedMessageOrMediaText: messageText(message),
				ConversationDisplayContext:  telegramMessageCommandConversationDisplayContext(message),
			}
			if !message.Timestamp.IsZero() {
				messageRecord.MessageTime = &presentationv1.ArchiveRecordAssociatedTimeForDisplay{
					ArchiveRecordAssociatedTime: &presentationv1.ArchiveRecordAssociatedTimeForDisplay_ExactTime{ExactTime: timestamppb.New(message.Timestamp)},
				}
			}
			messageRecords = append(messageRecords, messageRecord)
		}
		scopedConversationDisplayContext := ""
		if filter.ChatJID != "" && len(messages) > 0 {
			scopedConversationDisplayContext = telegramMessageCommandConversationDisplayContext(messages[0])
		}
		response = &messagev1.MessageListResponse{
			MessageRecordsInDisplayOrder: messageRecords,
			TotalMatchingMessageCount:    uint64(total),
			MoreMatchingMessagesExist:    total > len(messages),
			ConversationDisplayContextWhenMessagesAreRestrictedToOneConversation: scopedConversationDisplayContext,
		}
		return nil
	})
	return response, err
}

func telegramMessagePeople(
	ctx context.Context,
	st *store.Store,
	message store.Message,
	outgoingGroupRecipientDisplayNamesByConversation map[string][]string,
) ([]*personv1.PersonRelatedToArchiveRecord, error) {
	if message.FromMe {
		people := []*personv1.PersonRelatedToArchiveRecord{{
			PersonDisplayName:         "me",
			PersonRoleInArchiveRecord: personv1.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_SENDER,
		}}
		switch message.ChatKind {
		case "user":
			if recipientDisplayName := humanTelegramName(message.ChatName); recipientDisplayName != "" {
				people = append(people, &personv1.PersonRelatedToArchiveRecord{
					PersonDisplayName:         recipientDisplayName,
					PersonRoleInArchiveRecord: personv1.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_RECIPIENT,
				})
			}
		case "group":
			recipientDisplayNames, found := outgoingGroupRecipientDisplayNamesByConversation[message.ChatJID]
			if !found {
				var err error
				recipientDisplayNames, err = st.GroupRecipientDisplayNames(ctx, message.ChatJID)
				if err != nil {
					return nil, err
				}
				outgoingGroupRecipientDisplayNamesByConversation[message.ChatJID] = recipientDisplayNames
			}
			for _, recipientDisplayName := range recipientDisplayNames {
				recipientDisplayName = humanTelegramName(recipientDisplayName)
				if recipientDisplayName == "" {
					continue
				}
				people = append(people, &personv1.PersonRelatedToArchiveRecord{
					PersonDisplayName:         recipientDisplayName,
					PersonRoleInArchiveRecord: personv1.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_RECIPIENT,
				})
			}
		}
		return people, nil
	}
	people := []*personv1.PersonRelatedToArchiveRecord{{
		PersonDisplayName:         "me",
		PersonRoleInArchiveRecord: personv1.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_RECIPIENT,
	}}
	if senderDisplayName := messageWho(message); senderDisplayName != "" {
		people = append(people, &personv1.PersonRelatedToArchiveRecord{
			PersonDisplayName:         senderDisplayName,
			PersonRoleInArchiveRecord: personv1.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_SENDER,
		})
	}
	return people, nil
}

func telegramMessageCommandConversationDisplayContext(message store.Message) string {
	conversationDisplayName := messageWhere(message)
	topicDisplayName := humanTelegramName(message.TopicTitle)
	if topicDisplayName == "" {
		return conversationDisplayName
	}
	return topicDisplayName + " in " + conversationDisplayName
}

func (c *Crawler) messageFilter(maximumReturnedMessageCount int) (store.MessageFilter, error) {
	filter := store.MessageFilter{
		Who:      normalizeWords(c.messages.Who),
		Limit:    maximumReturnedMessageCount,
		HasMedia: c.messages.HasMedia,
		Pinned:   c.messages.Pinned,
		Asc:      false,
	}
	if filter.Who == "" && strings.TrimSpace(c.messages.Who) != "" {
		return filter, usageErr(errors.New("--who needs a person."))
	}
	if c.messages.After != "" {
		t, err := parseDateFlag("--after", c.messages.After)
		if err != nil {
			return filter, usageErr(err)
		}
		filter.After = &t
	}
	if c.messages.Before != "" {
		t, err := parseDateFlag("--before", c.messages.Before)
		if err != nil {
			return filter, usageErr(err)
		}
		filter.Before = &t
	}
	if filter.After != nil && filter.Before != nil && filter.After.After(*filter.Before) {
		return filter, usageErr(errors.New("--after must not be later than --before."))
	}
	if c.messages.FromMe && c.messages.FromThem {
		return filter, usageErr(errors.New("--from-me and --from-them cannot be used together."))
	}
	if c.messages.FromMe || c.messages.FromThem {
		v := c.messages.FromMe
		filter.FromMe = &v
	}
	return filter, nil
}

func parseDateFlag(name, value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	t, err := flags.Date(value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s %w", name, err)
	}
	if name == "--before" {
		if day, err := time.ParseInLocation("2006-01-02", value, time.Local); err == nil {
			return day.Add(24*time.Hour - time.Second).UTC(), nil
		}
	}
	return t, nil
}
