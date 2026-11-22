package twitter

import (
	"context"
	"errors"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
	ckflags "github.com/opentrawl/opentrawl/trawlkit/flags"
	message "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/message"
	"github.com/opentrawl/opentrawl/trawlkit/render"
	"github.com/opentrawl/opentrawl/twitter/internal/store"
)

type dmMessageOptions struct {
	who      string
	after    string
	before   string
	fromMe   bool
	fromThem bool
}

func (c *Crawler) ListMessages(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	query trawlkit.TrawlerMessageListQuery,
) (*message.MessageListResponse, error) {
	r := c.handler(ctx, req)
	filter, err := c.dmMessageFilter(query.MaximumReturnedMessageCount)
	if err != nil {
		return nil, err
	}
	filter.ConversationID, err = req.ResolveLocalConversationShortReferenceToProviderNativeConversationIdentifier(
		ctx,
		trawlkit.NewLocalTrawlerShortReference(
			query.OptionalLocalConversationShortReferenceForRestrictingMessagesToOneConversation,
		),
		store.DMConversationRefPrefix,
	)
	if errors.Is(err, trawlkit.ErrLocalConversationShortReferenceDoesNotIdentifyConversation) {
		return nil, usageErr(render.LinkIsNotForArchiveRecordKind(render.ArchiveRecordKindNounConversation))
	}
	if errors.Is(err, trawlkit.ErrUnknownShortRef) {
		return nil, r.contractError("not_found", render.NoArchiveRecordHasLink(render.ArchiveRecordKindNounConversation).Error())
	}
	if errors.Is(err, trawlkit.ErrAmbiguousShortRef) {
		return nil, usageErr(render.SeveralArchiveRecordsHaveLink(render.ArchiveRecordKindNounConversation))
	}
	if err != nil {
		return nil, err
	}
	var response *message.MessageListResponse
	err = r.withReadOnlyStore(func(st *store.Store) error {
		messages, err := st.ListDirectMessages(r.ctx, filter)
		if err != nil {
			return err
		}
		total, err := st.CountDirectMessages(r.ctx, filter)
		if err != nil {
			return err
		}
		records := make([]*message.MessageRecord, 0, len(messages))
		for _, dm := range messages {
			records = append(records, projectDirectMessageRecord(dm))
		}
		convTitle := ""
		if filter.ConversationID != "" && len(messages) > 0 {
			convTitle = directMessageWhere(messages[0])
		}
		response = &message.MessageListResponse{
			MessageRecordsNewestFirst: records,
			TotalMatchingMessageCount: uint64(max(total, 0)),
			MoreMatchingMessagesExist: total > len(messages),
			ConversationDisplayNameForMessageRecordsRestrictedToOneConversation: convTitle,
		}
		return nil
	})
	return response, err
}

func (c *Crawler) dmMessageFilter(maxCount int) (store.DirectMessageListFilter, error) {
	filter := store.DirectMessageListFilter{
		PersonFilter: trawlkit.NewUnresolvedSearchPersonFilter(c.dmOpts.who),
		Limit:        maxCount,
	}
	if strings.TrimSpace(c.dmOpts.after) != "" {
		after, err := ckflags.Date(c.dmOpts.after)
		if err != nil {
			return filter, usageErr(render.DateOptionNeedsTime("after"))
		}
		filter.After = &after
	}
	if strings.TrimSpace(c.dmOpts.before) != "" {
		before, err := ckflags.ParseDateOrTimeThroughEndOfEnteredPrecision(c.dmOpts.before)
		if err != nil {
			return filter, usageErr(render.DateOptionNeedsTime("before"))
		}
		filter.Before = &before
	}
	if filter.After != nil && filter.Before != nil && filter.After.After(*filter.Before) {
		return filter, usageErr(render.AfterMustNotBeLaterThanBefore())
	}
	if c.dmOpts.fromMe && c.dmOpts.fromThem {
		return filter, usageErr(render.OptionsCannotBeUsedTogether("from-me", "from-them"))
	}
	if c.dmOpts.fromMe || c.dmOpts.fromThem {
		v := c.dmOpts.fromMe
		filter.FromMe = &v
	}
	return filter, nil
}

func projectDirectMessageRecord(dm store.DirectMessage) *message.MessageRecord {
	text := strings.TrimSpace(dm.Text)
	if text == "" && dm.MediaCount > 0 {
		text = "[attachment]"
	}
	return &message.MessageRecord{
		CanonicalRecordReference: trawlkit.NewCanonicalArchiveRecordReference(store.DMMessageRef(dm.ID)),
		MessageTime:              twitterArchiveRecordAssociatedTime(dm.CreatedAt),
		PeopleRelatedToMessage:   directMessagePeople(dm),
		MessageText:              text,
		ConversationDisplayName:  directMessageWhere(dm),
	}
}

func directMessageWhere(dm store.DirectMessage) string {
	name := strings.TrimSpace(dm.ConversationTitle)
	if name == "" {
		name = twitterProfileDisplayName(dm.CounterpartyName, dm.CounterpartyHandle, "")
	}
	if name == "" {
		name = "X direct message"
	}
	if dm.InboxKind == "request" {
		return name + " (request)"
	}
	return name
}
