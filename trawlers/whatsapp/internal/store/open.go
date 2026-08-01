package store

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"

	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
)

func (s *Store) MessageByID(ctx context.Context, messageID string) (Message, error) {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return Message{}, errors.New("message id required")
	}
	messages, err := scanMessages(ctx, s.db, "select "+messageSelectColumns+" from messages where msg_id = ? order by ts desc, source_pk desc limit 1", messageID)
	if err != nil {
		return Message{}, err
	}
	if len(messages) == 0 {
		return Message{}, sql.ErrNoRows
	}
	messages, err = s.withCanonicalWhatsAppMessageDisplayNames(ctx, messages)
	if err != nil {
		return Message{}, err
	}
	return messages[0], nil
}

type MessageWindow struct {
	Messages        []Message
	BeforeTruncated bool
	AfterTruncated  bool
}

func (s *Store) MessageWindow(ctx context.Context, target Message, eachSide int) (MessageWindow, error) {
	if eachSide < 0 {
		eachSide = 0
	}
	before, err := s.messagesBefore(ctx, target, eachSide+1)
	if err != nil {
		return MessageWindow{}, err
	}
	beforeTruncated := len(before) > eachSide
	if beforeTruncated {
		before = before[len(before)-eachSide:]
	}
	after, err := s.messagesAfter(ctx, target, eachSide+1)
	if err != nil {
		return MessageWindow{}, err
	}
	afterTruncated := len(after) > eachSide
	if afterTruncated {
		after = after[:eachSide]
	}
	out := make([]Message, 0, len(before)+1+len(after))
	out = append(out, before...)
	out = append(out, target)
	out = append(out, after...)
	messages, err := s.withCanonicalWhatsAppMessageDisplayNames(ctx, out)
	if err != nil {
		return MessageWindow{}, err
	}
	return MessageWindow{
		Messages:        messages,
		BeforeTruncated: beforeTruncated,
		AfterTruncated:  afterTruncated,
	}, nil
}

func (s *Store) ConversationParticipantIdentitiesObservedByTrawlerArchive(
	ctx context.Context,
	chatJID string,
) ([]ConversationParticipantIdentity, error) {
	chatJID = strings.TrimSpace(chatJID)
	if chatJID == "" {
		return nil, nil
	}
	messageSenderContact := contactJIDPredicate("c", "m.sender_jid")
	groupParticipantContact := contactJIDPredicate("c", "gp.user_jid")
	directConversationContact := contactJIDPredicate("c", "ch.jid")
	rows, err := s.db.QueryContext(ctx, `
select participant_key, display_name, name_kind
from (
	select
		case
			when trim(m.sender_jid) <> '' then 'jid:' || coalesce(c.jid, m.sender_jid)
			else 'sender:' || trim(m.sender_name)
		end as participant_key,
		coalesce(
			nullif(trim(c.full_name), ''),
			nullif(trim(m.sender_name), ''),
			nullif(trim(c.business_name), ''),
			nullif(trim(c.first_name || ' ' || c.last_name), ''),
			''
		) as display_name,
		case
			when trim(c.full_name) <> '' then 'contact_full'
			when trim(m.sender_name) <> '' then 'push'
			else 'other'
		end as name_kind
	from messages m
	left join contacts c on `+messageSenderContact+`
	where m.chat_jid = ? and m.from_me = 0

	union all

	select
		case
			when trim(gp.user_jid) <> '' then 'jid:' || coalesce(c.jid, gp.user_jid)
			else 'group_participant:' || lower(trim(gp.contact_name))
		end,
		coalesce(
			nullif(trim(c.full_name), ''),
			nullif(trim(gp.contact_name), ''),
			nullif(trim(c.business_name), ''),
			nullif(trim(c.first_name || ' ' || c.last_name), ''),
			''
		),
		case when trim(c.full_name) <> '' then 'contact_full' else 'other' end
	from group_participants gp
	left join contacts c on `+groupParticipantContact+`
	where gp.group_jid = ? and gp.is_active != 0

	union all

	select
		'jid:' || coalesce(c.jid, ch.jid),
		coalesce(
			nullif(trim(c.full_name), ''),
			nullif(trim(ch.name), ''),
			nullif(trim(c.business_name), ''),
			nullif(trim(c.first_name || ' ' || c.last_name), ''),
			''
		),
		case when trim(c.full_name) <> '' then 'contact_full' else 'other' end
	from chats ch
	left join contacts c on `+directConversationContact+`
	where ch.jid = ? and ch.kind <> 'group'
)
where trim(participant_key) <> ''`, chatJID, chatJID, chatJID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	participantIdentityBuilders := map[string]*whoCandidateBuilder{}
	for rows.Next() {
		var participantKey, displayName, nameKind string
		if err := rows.Scan(&participantKey, &displayName, &nameKind); err != nil {
			return nil, err
		}
		participantKey = normalizeWhoIdentity(participantKey)
		if participantKey == "" {
			continue
		}
		whoBuilder(participantIdentityBuilders, participantKey).addName(displayName, nameKind)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	conversationParticipantIdentities := make(
		[]ConversationParticipantIdentity,
		0,
		len(participantIdentityBuilders),
	)
	for _, participantIdentityBuilder := range participantIdentityBuilders {
		displayName := chooseWhoName(participantIdentityBuilder.names, nil)
		if !humanWhoName(displayName) {
			displayName = ""
		}
		conversationParticipantIdentities = append(
			conversationParticipantIdentities,
			ConversationParticipantIdentity{
				PersonDisplayName: displayName,
				ExactPersonFilterIdentifiersObservedByTrawlerArchive: []*person.ExactPersonFilterIdentifier{
					{ExactPersonFilterIdentifier: participantIdentityBuilder.key},
				},
			},
		)
	}
	sort.SliceStable(
		conversationParticipantIdentities,
		func(leftParticipantIndex int, rightParticipantIndex int) bool {
			leftParticipant := conversationParticipantIdentities[leftParticipantIndex]
			rightParticipant := conversationParticipantIdentities[rightParticipantIndex]
			leftDisplayName := strings.ToLower(leftParticipant.PersonDisplayName)
			rightDisplayName := strings.ToLower(rightParticipant.PersonDisplayName)
			if leftDisplayName != rightDisplayName {
				return leftDisplayName < rightDisplayName
			}
			return strings.ToLower(
				leftParticipant.ExactPersonFilterIdentifiersObservedByTrawlerArchive[0].GetExactPersonFilterIdentifier(),
			) < strings.ToLower(
				rightParticipant.ExactPersonFilterIdentifiersObservedByTrawlerArchive[0].GetExactPersonFilterIdentifier(),
			)
		},
	)
	return conversationParticipantIdentities, nil
}

func (s *Store) messagesBefore(ctx context.Context, target Message, limit int) ([]Message, error) {
	if limit == 0 {
		return nil, nil
	}
	if target.Timestamp.IsZero() {
		query := "select " + messageScanColumns + " from (select " + messageSelectColumns + " from messages where chat_jid = ? and source_pk < ? order by source_pk desc limit ?) order by source_pk asc"
		return scanMessages(ctx, s.db, query, target.ChatJID, target.SourcePK, limit)
	}
	query := "select " + messageScanColumns + " from (select " + messageSelectColumns + " from messages where chat_jid = ? and (ts < ? or (ts = ? and source_pk < ?)) order by ts desc, source_pk desc limit ?) order by ts asc, source_pk asc"
	ts := unix(target.Timestamp)
	return scanMessages(ctx, s.db, query, target.ChatJID, ts, ts, target.SourcePK, limit)
}

func (s *Store) messagesAfter(ctx context.Context, target Message, limit int) ([]Message, error) {
	if limit == 0 {
		return nil, nil
	}
	if target.Timestamp.IsZero() {
		query := "select " + messageSelectColumns + " from messages where chat_jid = ? and source_pk > ? order by source_pk asc limit ?"
		return scanMessages(ctx, s.db, query, target.ChatJID, target.SourcePK, limit)
	}
	query := "select " + messageSelectColumns + " from messages where chat_jid = ? and (ts > ? or (ts = ? and source_pk > ?)) order by ts asc, source_pk asc limit ?"
	ts := unix(target.Timestamp)
	return scanMessages(ctx, s.db, query, target.ChatJID, ts, ts, target.SourcePK, limit)
}
