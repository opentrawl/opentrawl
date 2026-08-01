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
	"github.com/opentrawl/opentrawl/trawlkit/output"
	message "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/message"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (c *Crawler) ListMessages(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	query trawlkit.TrawlerMessageListQuery,
) (*message.MessageListResponse, error) {
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
	var response *message.MessageListResponse
	err = r.withReadOnlyStore(func(st *store.Store) error {
		messages, err := st.Messages(r.ctx, filter)
		if err != nil {
			return err
		}
		total, err := st.CountMessages(r.ctx, filter)
		if err != nil {
			return err
		}
		messageRecords := make([]*message.MessageRecord, 0, len(messages))
		outgoingGroupRecipientDisplayNamesByConversation := map[string][]string{}
		for _, telegramMessage := range messages {
			peopleRelatedToMessage, err := telegramMessagePeople(
				r.ctx,
				st,
				telegramMessage,
				outgoingGroupRecipientDisplayNamesByConversation,
			)
			if err != nil {
				return err
			}
			messageRecord := &message.MessageRecord{
				CanonicalRecordReference: trawlkit.NewCanonicalArchiveRecordReference(
					store.MessageRef(telegramMessage.SourcePK),
				),
				PeopleRelatedToMessage:      peopleRelatedToMessage,
				DisplayedMessageOrMediaText: messageText(telegramMessage),
				ConversationDisplayContext:  telegramMessageCommandConversationDisplayContext(telegramMessage),
			}
			if !telegramMessage.Timestamp.IsZero() {
				messageRecord.MessageTime = &presentation.ArchiveRecordAssociatedTimeForDisplay{
					ArchiveRecordAssociatedTime: &presentation.ArchiveRecordAssociatedTimeForDisplay_ExactTime{ExactTime: timestamppb.New(telegramMessage.Timestamp)},
				}
			}
			messageRecords = append(messageRecords, messageRecord)
		}
		scopedConversationDisplayContext := ""
		if filter.ChatJID != "" && len(messages) > 0 {
			scopedConversationDisplayContext = telegramMessageCommandConversationDisplayContext(messages[0])
		}
		response = &message.MessageListResponse{
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
) ([]*person.PersonRelatedToArchiveRecord, error) {
	if message.FromMe {
		people := []*person.PersonRelatedToArchiveRecord{{
			PersonDisplayName:         "me",
			PersonRoleInArchiveRecord: person.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_SENDER,
		}}
		switch message.ChatKind {
		case "user":
			if recipientDisplayName := humanTelegramName(message.ChatName); recipientDisplayName != "" {
				people = append(people, &person.PersonRelatedToArchiveRecord{
					PersonDisplayName:         recipientDisplayName,
					PersonRoleInArchiveRecord: person.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_RECIPIENT,
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
				people = append(people, &person.PersonRelatedToArchiveRecord{
					PersonDisplayName:         recipientDisplayName,
					PersonRoleInArchiveRecord: person.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_RECIPIENT,
				})
			}
		}
		return people, nil
	}
	people := []*person.PersonRelatedToArchiveRecord{{
		PersonDisplayName:         "me",
		PersonRoleInArchiveRecord: person.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_RECIPIENT,
	}}
	if senderDisplayName := messageWho(message); senderDisplayName != "" {
		people = append(people, &person.PersonRelatedToArchiveRecord{
			PersonDisplayName:         senderDisplayName,
			PersonRoleInArchiveRecord: person.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_SENDER,
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
		PersonFilter: trawlkit.NewUnresolvedSearchPersonFilter(c.messages.Who),
		Limit:        maximumReturnedMessageCount,
		HasMedia:     c.messages.HasMedia,
		Pinned:       c.messages.Pinned,
		Asc:          false,
	}
	if filter.PersonFilter.UnresolvedPersonFilterText() == "" && strings.TrimSpace(c.messages.Who) != "" {
		return filter, usageErr(output.HumanFacingErrorMessage("--who needs a person."))
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
		return filter, usageErr(output.HumanFacingErrorMessage("--after must not be later than --before."))
	}
	if c.messages.FromMe && c.messages.FromThem {
		return filter, usageErr(output.HumanFacingErrorMessage("--from-me and --from-them cannot be used together."))
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
	parseDateOrTime := flags.Date
	if name == "--before" {
		parseDateOrTime = flags.ParseDateOrTimeThroughEndOfEnteredPrecision
	}
	t, err := parseDateOrTime(value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s %w", name, err)
	}
	return t, nil
}
