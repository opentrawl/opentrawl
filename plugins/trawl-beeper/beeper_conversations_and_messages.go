package beeper

import (
	"context"
	"errors"
	"flag"
	"strings"

	"github.com/opentrawl/opentrawl/plugins/trawl-beeper/internal/beeperstore"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	conversation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/conversation"
	message "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/message"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	"github.com/opentrawl/opentrawl/trawlkit/render"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type beeperMessageFlagValues struct {
	network  string
	sender   string
	fromMe   bool
	fromThem bool
	hasMedia bool
}

func (t *BeeperTrawler) bindMessageFlags(fs *flag.FlagSet) {
	t.messageFlags = beeperMessageFlagValues{}
	fs.StringVar(&t.messageFlags.network, "network", "", "Show only messages from `NETWORK` (for example Instagram or LinkedIn)")
	fs.StringVar(&t.messageFlags.sender, "sender", "", "Show only messages from `PERSON`")
	fs.BoolVar(&t.messageFlags.fromMe, "from-me", false, "Show only messages sent by you")
	fs.BoolVar(&t.messageFlags.fromThem, "from-them", false, "Show only messages sent by other people")
	fs.BoolVar(&t.messageFlags.hasMedia, "has-media", false, "Show only messages with media or attachments")
}

// Conversations lists archived Beeper conversations newest-first.
func (t *BeeperTrawler) Conversations(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	q trawlkit.ConversationQuery,
) (*conversation.ConversationListResponse, error) {
	limit := q.Limit
	if q.All {
		limit = 0
	} else if limit > 0 {
		limit++
	}
	st, err := beeperstore.UseExisting(req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveFailure(err)
	}
	chats, err := st.ListChats(ctx, limit, q.Unread, "")
	if err != nil {
		return nil, err
	}
	moreExist := !q.All && q.Limit > 0 && len(chats) > q.Limit
	if moreExist {
		chats = chats[:q.Limit]
	}

	records := make([]*conversation.ConversationRecord, 0, len(chats))
	for _, chat := range chats {
		unreadCount := uint64(max(chat.UnreadCount, 0))
		record := &conversation.ConversationRecord{
			CanonicalRecordReference: trawlkit.NewCanonicalArchiveRecordReference(beeperstore.BeeperChatRef(chat.ChatID)),
			ConversationDisplayName:  beeperstore.FormatNetworkChatDisplayTitle(chat.Network, chat.Title),
			UnreadMessageCount:       &unreadCount,
		}
		if !chat.LastActivity.IsZero() {
			record.MostRecentConversationActivityTime = timestamppb.New(chat.LastActivity)
		}
		participants, partErr := st.ConversationParticipantIdentities(ctx, chat.ChatID)
		if partErr != nil {
			return nil, partErr
		}
		if len(participants) > 0 {
			record.ConversationParticipantIdentitiesObservedByTrawlerArchive = projectConversationParticipants(participants)
		}
		records = append(records, record)
	}

	return &conversation.ConversationListResponse{
		ConversationRecordsNewestFirst: records,
		MoreConversationRecordsExist:   moreExist,
	}, nil
}

// ListMessages lists archived Beeper messages with optional conversation,
// network, sender, direction, media, and time filters.
func (t *BeeperTrawler) ListMessages(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	query trawlkit.TrawlerMessageListQuery,
) (*message.MessageListResponse, error) {
	if t.messageFlags.fromMe && t.messageFlags.fromThem {
		return nil, usageFailure(render.OptionsCannotBeUsedTogether("from-me", "from-them"))
	}

	chatID, err := req.ResolveLocalConversationShortReferenceToProviderNativeConversationIdentifier(
		ctx,
		trawlkit.NewLocalTrawlerShortReference(
			query.OptionalLocalConversationShortReferenceForRestrictingMessagesToOneConversation,
		),
		beeperstore.BeeperChatRefPrefix,
	)
	if errors.Is(err, trawlkit.ErrLocalConversationShortReferenceDoesNotIdentifyConversation) {
		return nil, usageFailure(render.LinkIsNotForArchiveRecordKind(render.ArchiveRecordKindNounConversation))
	}
	if errors.Is(err, trawlkit.ErrUnknownShortRef) {
		return nil, commandFailure(1, "not_found", render.NoArchiveRecordHasLink(render.ArchiveRecordKindNounConversation))
	}
	if errors.Is(err, trawlkit.ErrAmbiguousShortRef) {
		return nil, usageFailure(render.SeveralArchiveRecordsHaveLink(render.ArchiveRecordKindNounConversation))
	}
	if err != nil {
		return nil, err
	}

	filter := beeperstore.BeeperMessageQueryFilter{
		ChatID:   chatID,
		Network:  strings.TrimSpace(t.messageFlags.network),
		Sender:   strings.TrimSpace(t.messageFlags.sender),
		Limit:    query.MaximumReturnedMessageCount,
		HasMedia: t.messageFlags.hasMedia,
	}
	if t.messageFlags.fromMe {
		v := true
		filter.IsSender = &v
	}
	if t.messageFlags.fromThem {
		v := false
		filter.IsSender = &v
	}
	if !query.After.IsZero() {
		after := query.After
		filter.After = &after
	}
	if !query.Before.IsZero() {
		before := query.Before
		filter.Before = &before
	}

	st, err := beeperstore.UseExisting(req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveFailure(err)
	}
	messages, total, err := st.ListMessagesAndTotalCount(ctx, filter)
	if err != nil {
		return nil, err
	}

	records := make([]*message.MessageRecord, 0, len(messages))
	for _, msg := range messages {
		records = append(records, projectBeeperMessageRecord(msg))
	}

	conversationTitle := ""
	if chatID != "" && len(messages) > 0 {
		conversationTitle = beeperstore.FormatNetworkChatDisplayTitle(messages[0].Network, messages[0].ChatTitle)
	}

	return &message.MessageListResponse{
		MessageRecordsNewestFirst: records,
		TotalMatchingMessageCount: uint64(max(total, 0)),
		MoreMatchingMessagesExist: total > len(messages),
		ConversationDisplayNameForMessageRecordsRestrictedToOneConversation: conversationTitle,
	}, nil
}

func projectConversationParticipants(items []beeperstore.BeeperConversationParticipantIdentity) []*conversation.ConversationParticipantIdentityObservedByTrawlerArchive {
	out := make([]*conversation.ConversationParticipantIdentityObservedByTrawlerArchive, 0, len(items))
	for _, item := range items {
		out = append(out, &conversation.ConversationParticipantIdentityObservedByTrawlerArchive{
			PersonDisplayName: item.PersonDisplayName,
			ExactPersonFilterIdentifiersObservedByTrawlerArchive: item.ExactPersonFilterIdentifiersObservedByTrawlerArchive,
		})
	}
	return out
}

func projectBeeperMessageRecord(msg beeperstore.BeeperArchivedMessage) *message.MessageRecord {
	record := &message.MessageRecord{
		CanonicalRecordReference: trawlkit.NewCanonicalArchiveRecordReference(beeperstore.BeeperMessageRef(msg.MessageID)),
		PeopleRelatedToMessage:   beeperMessageRelatedPeople(msg),
		MessageText:              formatBeeperMessageBodyText(msg),
		ConversationDisplayName:  beeperstore.FormatNetworkChatDisplayTitle(msg.Network, msg.ChatTitle),
		MessageMedia:             projectBeeperMessageMedia(msg),
	}
	if !msg.Timestamp.IsZero() {
		record.MessageTime = &presentation.ArchiveRecordAssociatedTimeForDisplay{
			ArchiveRecordAssociatedTime: &presentation.ArchiveRecordAssociatedTimeForDisplay_ExactTime{
				ExactTime: timestamppb.New(msg.Timestamp),
			},
		}
	}
	return record
}

func beeperMessageRelatedPeople(msg beeperstore.BeeperArchivedMessage) []*person.PersonRelatedToArchiveRecord {
	people := []*person.PersonRelatedToArchiveRecord{{
		PersonDisplayName:         "me",
		PersonRoleInArchiveRecord: person.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_RECIPIENT,
	}}
	if msg.IsSender {
		people[0].PersonRoleInArchiveRecord = person.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_SENDER
		if strings.EqualFold(msg.ChatType, "single") && strings.TrimSpace(msg.ChatTitle) != "" {
			people = append(people, &person.PersonRelatedToArchiveRecord{
				PersonDisplayName:         strings.TrimSpace(msg.ChatTitle),
				PersonRoleInArchiveRecord: person.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_RECIPIENT,
			})
		}
		return people
	}
	if sender := firstNonEmpty(msg.SenderName, msg.SenderID); sender != "" {
		people = append(people, &person.PersonRelatedToArchiveRecord{
			PersonDisplayName:         sender,
			PersonRoleInArchiveRecord: person.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_SENDER,
		})
	}
	return people
}

func formatBeeperMessageBodyText(msg beeperstore.BeeperArchivedMessage) string {
	var parts []string
	if trimmed := strings.TrimSpace(msg.Text); trimmed != "" {
		parts = append(parts, trimmed)
	}
	if trimmed := strings.TrimSpace(msg.MediaTranscript); trimmed != "" && !strings.Contains(msg.Text, trimmed) {
		parts = append(parts, trimmed)
	}
	if trimmed := strings.TrimSpace(msg.LinksSummary); trimmed != "" && !strings.Contains(msg.Text, trimmed) {
		parts = append(parts, trimmed)
	}
	if len(parts) == 0 && msg.IsDeleted {
		return "[deleted message]"
	}
	return strings.Join(parts, "\n")
}

func projectBeeperMessageMedia(msg beeperstore.BeeperArchivedMessage) *message.MessageMedia {
	if msg.MediaKind == "" && msg.MediaTitle == "" && msg.MediaURL == "" {
		return nil
	}
	media := &message.MessageMedia{
		MessageMediaContentKind: mapBeeperMediaKind(msg.MediaKind),
		MessageMediaTitle:       strings.TrimSpace(msg.MediaTitle),
	}
	if msg.MediaSizeBytes > 0 {
		size := uint64(msg.MediaSizeBytes)
		media.MessageMediaByteCount = &size
	}
	if openrecord.ValidHTTPSURL(msg.MediaURL) {
		media.MessageMediaHttpsUrl = strings.TrimSpace(msg.MediaURL)
	}
	return media
}

func mapBeeperMediaKind(kind string) message.MessageMediaContentKind {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "image":
		return message.MessageMediaContentKind_MESSAGE_MEDIA_CONTENT_KIND_IMAGE
	case "video":
		return message.MessageMediaContentKind_MESSAGE_MEDIA_CONTENT_KIND_VIDEO
	case "audio":
		return message.MessageMediaContentKind_MESSAGE_MEDIA_CONTENT_KIND_AUDIO
	case "voice":
		return message.MessageMediaContentKind_MESSAGE_MEDIA_CONTENT_KIND_VOICE_OR_INSTANT_VIDEO
	case "gif":
		return message.MessageMediaContentKind_MESSAGE_MEDIA_CONTENT_KIND_GIF
	case "sticker":
		return message.MessageMediaContentKind_MESSAGE_MEDIA_CONTENT_KIND_STICKER
	case "file":
		return message.MessageMediaContentKind_MESSAGE_MEDIA_CONTENT_KIND_FILE
	default:
		return message.MessageMediaContentKind_MESSAGE_MEDIA_CONTENT_KIND_ATTACHMENT
	}
}
