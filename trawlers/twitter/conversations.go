package twitter

import (
	"context"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
	conversation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/conversation"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	"github.com/opentrawl/opentrawl/twitter/internal/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (c *Crawler) Conversations(
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
	r := c.handler(ctx, req)
	response := &conversation.ConversationListResponse{}
	err := r.withReadOnlyStore(func(st *store.Store) error {
		convs, err := st.ListDirectMessageConversations(r.ctx, limit, q.Unread)
		if err != nil {
			return err
		}
		response.MoreConversationRecordsExist = !q.All && q.Limit > 0 && len(convs) > q.Limit
		if response.MoreConversationRecordsExist {
			convs = convs[:q.Limit]
		}
		response.ConversationRecordsNewestFirst = make([]*conversation.ConversationRecord, 0, len(convs))
		for _, conv := range convs {
			response.ConversationRecordsNewestFirst = append(
				response.ConversationRecordsNewestFirst,
				projectDirectMessageConversationRecord(conv, nil),
			)
		}
		return nil
	})
	return response, err
}

func projectDirectMessageConversationRecord(
	conv store.DirectMessageConversation,
	participants []store.Profile,
) *conversation.ConversationRecord {
	unreadCount := uint64(max(conv.UnreadCount, 0))
	record := &conversation.ConversationRecord{
		CanonicalRecordReference: trawlkit.NewCanonicalArchiveRecordReference(store.DMConversationRef(conv.ID)),
		UnreadMessageCount:       &unreadCount,
	}
	if !conv.LastMessageAt.IsZero() {
		record.MostRecentConversationActivityTime = timestamppb.New(conv.LastMessageAt)
	}
	displayName := directMessageConversationDisplayName(conv)
	if conv.ConversationKind == "group" {
		record.ConversationDisplayName = displayName
		if len(participants) > 0 {
			identities := make([]*conversation.ConversationParticipantIdentityObservedByTrawlerArchive, 0, len(participants))
			seen := make(map[string]struct{}, len(participants))
			for _, p := range participants {
				identity := twitterConversationParticipantIdentity(p.AuthorID, p.DisplayName, p.Handle)
				key := strings.ToLower(strings.TrimSpace(identity.GetPersonDisplayName()))
				if key != "" {
					if _, dup := seen[key]; dup {
						continue
					}
					seen[key] = struct{}{}
				}
				identities = append(identities, identity)
			}
			record.ConversationParticipantIdentitiesObservedByTrawlerArchive = identities
			count := uint64(len(identities))
			record.NumberOfDistinctConversationParticipantRecordsObservedByTrawlerArchive = &count
		}
		return record
	}
	if conv.InboxKind == "request" {
		record.ConversationDisplayName = "Message request"
	}
	if strings.TrimSpace(conv.ParticipantAuthorID) != "" || strings.TrimSpace(conv.ParticipantHandle) != "" || strings.TrimSpace(conv.ParticipantName) != "" {
		record.ConversationParticipantIdentitiesObservedByTrawlerArchive = []*conversation.ConversationParticipantIdentityObservedByTrawlerArchive{
			twitterConversationParticipantIdentity(conv.ParticipantAuthorID, conv.ParticipantName, conv.ParticipantHandle),
		}
	} else if displayName != "" {
		record.ConversationDisplayName = displayName
	}
	return record
}

func twitterConversationParticipantIdentity(authorID, name, handle string) *conversation.ConversationParticipantIdentityObservedByTrawlerArchive {
	display := twitterProfileDisplayName(name, handle, authorID)
	var ids []string
	if authorID = strings.TrimSpace(authorID); authorID != "" {
		ids = append(ids, authorID)
	}
	if handle = strings.TrimPrefix(strings.TrimSpace(handle), "@"); handle != "" {
		ids = append(ids, "@"+strings.ToLower(handle))
	}
	return &conversation.ConversationParticipantIdentityObservedByTrawlerArchive{
		PersonDisplayName: display,
		ExactPersonFilterIdentifiersObservedByTrawlerArchive: trawlkit.NewExactPersonFilterIdentifiers(ids),
	}
}

func directMessageConversationDisplayName(conv store.DirectMessageConversation) string {
	if conv.ConversationKind == "group" {
		if title := strings.TrimSpace(conv.Title); title != "" {
			return title
		}
		return "X group conversation"
	}
	if name := twitterProfileDisplayName(conv.ParticipantName, conv.ParticipantHandle, ""); name != "" {
		return name
	}
	if title := strings.TrimSpace(conv.Title); title != "" {
		return title
	}
	return "X direct message"
}

func twitterProfileDisplayName(name, handle, fallbackID string) string {
	name = strings.TrimSpace(name)
	handle = strings.TrimPrefix(strings.TrimSpace(handle), "@")
	switch {
	case name != "" && handle != "":
		return name + " (@" + handle + ")"
	case name != "":
		return name
	case handle != "":
		return "@" + handle
	default:
		return strings.TrimSpace(fallbackID)
	}
}

func directMessagePeople(msg store.DirectMessage) []*person.PersonRelatedToArchiveRecord {
	counterparty := twitterProfileDisplayName(msg.CounterpartyName, msg.CounterpartyHandle, "")
	if counterparty == "" {
		counterparty = strings.TrimSpace(msg.ConversationTitle)
	}
	if msg.Direction == "outbound" {
		people := []*person.PersonRelatedToArchiveRecord{{
			PersonDisplayName:         "me",
			PersonRoleInArchiveRecord: person.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_SENDER,
		}}
		if counterparty != "" {
			people = append(people, &person.PersonRelatedToArchiveRecord{
				PersonDisplayName:         counterparty,
				PersonRoleInArchiveRecord: person.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_RECIPIENT,
			})
		}
		return people
	}
	sender := twitterProfileDisplayName(msg.SenderName, msg.SenderHandle, "")
	if sender == "" {
		sender = counterparty
	}
	people := []*person.PersonRelatedToArchiveRecord{{
		PersonDisplayName:         "me",
		PersonRoleInArchiveRecord: person.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_RECIPIENT,
	}}
	if sender != "" {
		people = append(people, &person.PersonRelatedToArchiveRecord{
			PersonDisplayName:         sender,
			PersonRoleInArchiveRecord: person.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_SENDER,
		})
	}
	return people
}
