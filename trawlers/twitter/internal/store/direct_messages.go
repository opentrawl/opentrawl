package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
)

var (
	ErrDirectMessageNotFound             = errors.New("direct message not found")
	ErrDirectMessageConversationNotFound = errors.New("direct message conversation not found")
)

type DirectMessageConversation struct {
	ID                   string
	InboxKind            string // "accepted" or "request"
	ConversationKind     string // "direct" or "group"
	Title                string
	ParticipantAuthorID  string
	ParticipantAuthorIDs []string
	ParticipantHandle    string
	ParticipantName      string
	UnreadCount          int
	NeedsReply           bool
	LastMessageAt        time.Time
}

type DirectMessage struct {
	ID                 string
	ConversationID     string
	ConversationTitle  string
	ConversationKind   string
	InboxKind          string
	CreatedAt          time.Time
	SenderAuthorID     string
	SenderHandle       string
	SenderName         string
	CounterpartyID     string
	CounterpartyHandle string
	CounterpartyName   string
	Direction          string // "inbound" or "outbound"
	Text               string
	MediaCount         int
	RawJSON            string
}

type DirectMessageBatch struct {
	Profiles      []Profile
	Conversations []DirectMessageConversation
	Messages      []DirectMessage
	States        []UpdateStateUpdate
	UpdatedAt     time.Time
}

type DirectMessageListFilter struct {
	ConversationID string
	PersonFilter   trawlkit.SearchPersonFilter
	After          *time.Time
	Before         *time.Time
	FromMe         *bool
	Limit          int
}

type DirectMessageSearchHit struct {
	DirectMessage
	Snippet string
}

type DirectMessageOpenWindow struct {
	Target          DirectMessage
	Conversation    DirectMessageConversation
	Participants    []Profile
	Messages        []DirectMessage // newest first
	BeforeTruncated bool
	AfterTruncated  bool
}

func (s *Store) CommitDirectMessageBatch(ctx context.Context, batch DirectMessageBatch) (int, error) {
	now := batch.UpdatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	addedMessages := 0
	err := s.base.WithTx(ctx, func(tx *sql.Tx) error {
		if err := upsertProfiles(ctx, tx, batch.Profiles, now); err != nil {
			return err
		}
		if err := upsertDirectMessageConversations(ctx, tx, batch.Conversations); err != nil {
			return err
		}
		n, err := upsertDirectMessages(ctx, tx, batch.Messages)
		if err != nil {
			return err
		}
		addedMessages = n
		for _, state := range batch.States {
			if state.LastUpdateAt.IsZero() {
				state.LastUpdateAt = now
			}
			if err := upsertUpdateState(ctx, tx, state); err != nil {
				return err
			}
		}
		return nil
	})
	return addedMessages, err
}

func (s *Store) HasDirectMessage(ctx context.Context, messageID string) (bool, error) {
	var exists int
	err := s.db.QueryRowContext(ctx, `select 1 from dm_messages where id = ? limit 1`, strings.TrimSpace(messageID)).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) ListDirectMessageConversations(ctx context.Context, limit int, unreadOnly bool) ([]DirectMessageConversation, error) {
	query := `select c.id, c.inbox_kind, c.conversation_kind, c.title, c.participant_author_id,
coalesce(p.handle, ''), coalesce(p.display_name, ''),
c.unread_count, c.needs_reply, c.last_message_at
from dm_conversations c
left join profiles p on p.author_id = c.participant_author_id`
	var args []any
	if unreadOnly {
		query += ` where c.unread_count > 0`
	}
	query += ` order by c.last_message_at desc, c.id asc limit ?`
	args = append(args, limitArg(limit))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []DirectMessageConversation
	for rows.Next() {
		var c DirectMessageConversation
		var needsReply int
		var lastAt string
		if err := rows.Scan(
			&c.ID, &c.InboxKind, &c.ConversationKind, &c.Title, &c.ParticipantAuthorID,
			&c.ParticipantHandle, &c.ParticipantName,
			&c.UnreadCount, &needsReply, &lastAt,
		); err != nil {
			return nil, err
		}
		c.NeedsReply = needsReply != 0
		c.LastMessageAt = parseStoredTime(lastAt)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) ConversationParticipantProfiles(ctx context.Context, conversationID string) ([]Profile, error) {
	rows, err := s.db.QueryContext(ctx, `select cp.author_id, coalesce(p.handle, ''), coalesce(p.display_name, ''), coalesce(p.last_seen_at, '')
from dm_conversation_participants cp
left join profiles p on p.author_id = cp.author_id
where cp.conversation_id = ?
order by coalesce(nullif(p.display_name, ''), nullif(p.handle, ''), cp.author_id) asc`, conversationID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Profile
	for rows.Next() {
		var p Profile
		var lastSeen string
		if err := rows.Scan(&p.AuthorID, &p.Handle, &p.DisplayName, &lastSeen); err != nil {
			return nil, err
		}
		p.LastSeenAt = parseStoredTime(lastSeen)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) ListDirectMessages(ctx context.Context, filter DirectMessageListFilter) ([]DirectMessage, error) {
	where, args := dmFilterWhere(filter)
	query := `select ` + dmSelectColumns() + dmFromClause() + where + ` order by m.created_at desc, m.id desc limit ?`
	args = append(args, limitArg(filter.Limit))
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []DirectMessage
	for rows.Next() {
		msg, err := scanDirectMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, msg)
	}
	return out, rows.Err()
}

func (s *Store) CountDirectMessages(ctx context.Context, filter DirectMessageListFilter) (int, error) {
	where, args := dmFilterWhere(filter)
	var total int
	err := s.db.QueryRowContext(ctx, `select count(*)`+dmFromClause()+where, args...).Scan(&total)
	return total, err
}

func (s *Store) SearchDirectMessages(ctx context.Context, filter SearchFilter) ([]DirectMessageSearchHit, int, error) {
	if strings.TrimSpace(filter.Query) == "" {
		return nil, 0, errors.New("search query required")
	}
	queryText, err := ckstore.FTS5Terms(filter.Query, "")
	if err != nil {
		return nil, 0, err
	}
	where, args := searchWhere(filter, "m.")
	countArgs := append([]any{queryText}, args...)
	var total int
	countQuery := `select count(*) from dm_messages_fts f join dm_messages m on m.rowid = f.rowid where dm_messages_fts match ?` + where
	if err := s.db.QueryRowContext(ctx, countQuery, countArgs...).Scan(&total); err != nil {
		return nil, 0, err
	}
	selectQuery := `select ` + dmSelectColumns() + `
from dm_messages_fts f
join dm_messages m on m.rowid = f.rowid
join dm_conversations c on c.id = m.conversation_id
left join profiles sp on sp.author_id = m.sender_author_id
left join profiles cp on cp.author_id = c.participant_author_id
where dm_messages_fts match ?` + where + `
order by m.created_at desc, m.id desc limit ?`
	selectArgs := append(countArgs, limitArg(filter.Limit))
	rows, err := s.db.QueryContext(ctx, selectQuery, selectArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	var out []DirectMessageSearchHit
	for rows.Next() {
		msg, err := scanDirectMessage(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, DirectMessageSearchHit{
			DirectMessage: msg,
			Snippet:       ckstore.FTS5Snippet(msg.Text, filter.Query),
		})
	}
	return out, total, rows.Err()
}

func (s *Store) OpenDirectMessageWindow(ctx context.Context, messageID string, radius int) (DirectMessageOpenWindow, error) {
	if radius <= 0 {
		radius = 10
	}
	row := s.db.QueryRowContext(ctx, `select `+dmSelectColumns()+dmFromClause()+` where m.id = ?`, strings.TrimSpace(messageID))
	target, err := scanDirectMessage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return DirectMessageOpenWindow{}, ErrDirectMessageNotFound
	}
	if err != nil {
		return DirectMessageOpenWindow{}, err
	}
	conv, err := s.directMessageConversationByID(ctx, target.ConversationID)
	if err != nil {
		return DirectMessageOpenWindow{}, err
	}
	participants, err := s.ConversationParticipantProfiles(ctx, target.ConversationID)
	if err != nil {
		return DirectMessageOpenWindow{}, err
	}
	targetCreatedAt := formatUTC(target.CreatedAt)

	afterMsgs, afterTrunc, err := s.loadAdjacentDirectMessages(ctx, target.ConversationID, targetCreatedAt, target.ID, true, radius)
	if err != nil {
		return DirectMessageOpenWindow{}, err
	}
	beforeMsgs, beforeTrunc, err := s.loadAdjacentDirectMessages(ctx, target.ConversationID, targetCreatedAt, target.ID, false, radius)
	if err != nil {
		return DirectMessageOpenWindow{}, err
	}

	// Combine newest-first: afterMsgs (newest->oldest), target, beforeMsgs (newest->oldest).
	combined := make([]DirectMessage, 0, len(afterMsgs)+1+len(beforeMsgs))
	combined = append(combined, afterMsgs...)
	combined = append(combined, target)
	combined = append(combined, beforeMsgs...)
	return DirectMessageOpenWindow{
		Target:          target,
		Conversation:    conv,
		Participants:    participants,
		Messages:        combined,
		BeforeTruncated: beforeTrunc,
		AfterTruncated:  afterTrunc,
	}, nil
}

func (s *Store) OpenDirectMessageConversationByID(ctx context.Context, conversationID string) (DirectMessageConversation, []Profile, error) {
	conv, err := s.directMessageConversationByID(ctx, conversationID)
	if err != nil {
		return DirectMessageConversation{}, nil, err
	}
	participants, err := s.ConversationParticipantProfiles(ctx, conversationID)
	if err != nil {
		return DirectMessageConversation{}, nil, err
	}
	return conv, participants, nil
}

func (s *Store) directMessageConversationByID(ctx context.Context, conversationID string) (DirectMessageConversation, error) {
	var c DirectMessageConversation
	var needsReply int
	var lastAt string
	err := s.db.QueryRowContext(ctx, `select c.id, c.inbox_kind, c.conversation_kind, c.title, c.participant_author_id,
coalesce(p.handle, ''), coalesce(p.display_name, ''),
c.unread_count, c.needs_reply, c.last_message_at
from dm_conversations c
left join profiles p on p.author_id = c.participant_author_id
where c.id = ?`, strings.TrimSpace(conversationID)).Scan(
		&c.ID, &c.InboxKind, &c.ConversationKind, &c.Title, &c.ParticipantAuthorID,
		&c.ParticipantHandle, &c.ParticipantName,
		&c.UnreadCount, &needsReply, &lastAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return DirectMessageConversation{}, ErrDirectMessageConversationNotFound
	}
	if err != nil {
		return DirectMessageConversation{}, err
	}
	c.NeedsReply = needsReply != 0
	c.LastMessageAt = parseStoredTime(lastAt)
	return c, nil
}

func (s *Store) loadAdjacentDirectMessages(
	ctx context.Context,
	conversationID string,
	targetCreatedAt string,
	targetID string,
	newer bool,
	limit int,
) ([]DirectMessage, bool, error) {
	var query string
	if newer {
		query = `select ` + dmSelectColumns() + dmFromClause() + `
where m.conversation_id = ? and (m.created_at > ? or (m.created_at = ? and m.id > ?))
order by m.created_at asc, m.id asc limit ?`
	} else {
		query = `select ` + dmSelectColumns() + dmFromClause() + `
where m.conversation_id = ? and (m.created_at < ? or (m.created_at = ? and m.id < ?))
order by m.created_at desc, m.id desc limit ?`
	}
	rows, err := s.db.QueryContext(ctx, query, conversationID, targetCreatedAt, targetCreatedAt, targetID, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	var out []DirectMessage
	for rows.Next() {
		msg, err := scanDirectMessage(rows)
		if err != nil {
			return nil, false, err
		}
		out = append(out, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	truncated := len(out) > limit
	if truncated {
		out = out[:limit]
	}
	if newer {
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	return out, truncated, nil
}

func upsertDirectMessageConversations(ctx context.Context, tx *sql.Tx, convs []DirectMessageConversation) error {
	candidates := make([]trawlkit.ShortReferenceAssignmentCandidate, 0, len(convs))
	for _, c := range convs {
		id := strings.TrimSpace(c.ID)
		if id == "" {
			continue
		}
		inboxKind := c.InboxKind
		if inboxKind != "request" {
			inboxKind = "accepted"
		}
		convKind := c.ConversationKind
		if convKind != "group" {
			convKind = "direct"
		}
		lastAt := ""
		if !c.LastMessageAt.IsZero() {
			lastAt = formatUTC(c.LastMessageAt)
		}
		_, err := tx.ExecContext(ctx, `insert into dm_conversations(
id, inbox_kind, conversation_kind, title, participant_author_id, unread_count, needs_reply, last_message_at)
values(?,?,?,?,?,?,?,?)
on conflict(id) do update set
inbox_kind = excluded.inbox_kind,
conversation_kind = excluded.conversation_kind,
title = coalesce(nullif(excluded.title, ''), dm_conversations.title),
participant_author_id = coalesce(nullif(excluded.participant_author_id, ''), dm_conversations.participant_author_id),
unread_count = excluded.unread_count,
needs_reply = excluded.needs_reply,
last_message_at = case when excluded.last_message_at > dm_conversations.last_message_at then excluded.last_message_at else dm_conversations.last_message_at end`,
			id, inboxKind, convKind, strings.TrimSpace(c.Title), strings.TrimSpace(c.ParticipantAuthorID),
			max(c.UnreadCount, 0), boolInt(c.NeedsReply), lastAt)
		if err != nil {
			return err
		}
		for _, pid := range c.ParticipantAuthorIDs {
			pid = strings.TrimSpace(pid)
			if pid == "" {
				continue
			}
			if _, err := tx.ExecContext(ctx, `insert or ignore into dm_conversation_participants(conversation_id, author_id) values(?,?)`, id, pid); err != nil {
				return err
			}
		}
		if pid := strings.TrimSpace(c.ParticipantAuthorID); pid != "" {
			if _, err := tx.ExecContext(ctx, `insert or ignore into dm_conversation_participants(conversation_id, author_id) values(?,?)`, id, pid); err != nil {
				return err
			}
		}
		candidates = append(candidates, trawlkit.ShortReferenceAssignmentCandidate{
			StableRecordReferenceUsedForShortReferenceAssignment: trawlkit.NewCanonicalArchiveRecordReference(DMConversationRef(id)),
		})
	}
	return trawlkit.AssignShortReferencesForObservedArchiveRecordsUsingCallerOwnedSQLTransaction(ctx, tx, candidates)
}

func upsertDirectMessages(ctx context.Context, tx *sql.Tx, msgs []DirectMessage) (int, error) {
	candidates := make([]trawlkit.ShortReferenceAssignmentCandidate, 0, len(msgs))
	added := 0
	for _, m := range msgs {
		id := strings.TrimSpace(m.ID)
		convID := strings.TrimSpace(m.ConversationID)
		if id == "" || convID == "" {
			continue
		}
		createdAt := m.CreatedAt
		if createdAt.IsZero() {
			createdAt = parseStoredTime(UnknownTimeRFC3339)
		}
		direction := m.Direction
		if direction != "outbound" {
			direction = "inbound"
		}
		var existed int
		err := tx.QueryRowContext(ctx, `select 1 from dm_messages where id = ? limit 1`, id).Scan(&existed)
		isNew := errors.Is(err, sql.ErrNoRows)
		if err != nil && !isNew {
			return added, err
		}
		// Ensure conversation shell exists if a message arrives before its conversation row.
		if _, err := tx.ExecContext(ctx, `insert or ignore into dm_conversations(id, last_message_at) values(?, ?)`, convID, formatUTC(createdAt)); err != nil {
			return added, err
		}
		_, err = tx.ExecContext(ctx, `insert into dm_messages(
id, conversation_id, created_at, sender_author_id, direction, text, media_count, raw_json)
values(?,?,?,?,?,?,?,?)
on conflict(id) do update set
created_at = case when excluded.created_at <> ? then excluded.created_at else dm_messages.created_at end,
sender_author_id = coalesce(nullif(excluded.sender_author_id, ''), dm_messages.sender_author_id),
direction = excluded.direction,
text = coalesce(nullif(excluded.text, ''), dm_messages.text),
media_count = max(dm_messages.media_count, excluded.media_count),
raw_json = coalesce(excluded.raw_json, dm_messages.raw_json)`,
			id, convID, formatUTC(createdAt), strings.TrimSpace(m.SenderAuthorID), direction,
			m.Text, max(m.MediaCount, 0), nullableString(m.RawJSON), UnknownTimeRFC3339)
		if err != nil {
			return added, err
		}
		if _, err := tx.ExecContext(ctx, `update dm_conversations set last_message_at = ? where id = ? and last_message_at < ?`,
			formatUTC(createdAt), convID, formatUTC(createdAt)); err != nil {
			return added, err
		}
		if isNew {
			added++
		}
		candidates = append(candidates, trawlkit.ShortReferenceAssignmentCandidate{
			StableRecordReferenceUsedForShortReferenceAssignment: trawlkit.NewCanonicalArchiveRecordReference(DMMessageRef(id)),
		})
	}
	if err := trawlkit.AssignShortReferencesForObservedArchiveRecordsUsingCallerOwnedSQLTransaction(ctx, tx, candidates); err != nil {
		return added, err
	}
	return added, nil
}

func dmSelectColumns() string {
	return `m.id, m.conversation_id, coalesce(c.title, ''), c.conversation_kind, c.inbox_kind,
m.created_at, coalesce(m.sender_author_id, ''), coalesce(sp.handle, ''), coalesce(sp.display_name, ''),
coalesce(c.participant_author_id, ''), coalesce(cp.handle, ''), coalesce(cp.display_name, ''),
m.direction, coalesce(m.text, ''), m.media_count, coalesce(m.raw_json, '')`
}

func dmFromClause() string {
	return ` from dm_messages m
join dm_conversations c on c.id = m.conversation_id
left join profiles sp on sp.author_id = m.sender_author_id
left join profiles cp on cp.author_id = c.participant_author_id`
}

func scanDirectMessage(scanner tweetScanner) (DirectMessage, error) {
	var m DirectMessage
	var createdAt string
	err := scanner.Scan(
		&m.ID, &m.ConversationID, &m.ConversationTitle, &m.ConversationKind, &m.InboxKind,
		&createdAt, &m.SenderAuthorID, &m.SenderHandle, &m.SenderName,
		&m.CounterpartyID, &m.CounterpartyHandle, &m.CounterpartyName,
		&m.Direction, &m.Text, &m.MediaCount, &m.RawJSON,
	)
	if err != nil {
		return DirectMessage{}, err
	}
	m.CreatedAt = parseStoredTime(createdAt)
	return m, nil
}

func dmFilterWhere(filter DirectMessageListFilter) (string, []any) {
	var clauses []string
	var args []any
	if strings.TrimSpace(filter.ConversationID) != "" {
		clauses = append(clauses, "m.conversation_id = ?")
		args = append(args, strings.TrimSpace(filter.ConversationID))
	}
	if filter.After != nil {
		clauses = append(clauses, "m.created_at >= ?")
		args = append(args, formatUTC(*filter.After))
	}
	if filter.Before != nil {
		clauses = append(clauses, "m.created_at <= ?")
		args = append(args, formatUTC(*filter.Before))
	}
	if filter.FromMe != nil {
		if *filter.FromMe {
			clauses = append(clauses, "m.direction = 'outbound'")
		} else {
			clauses = append(clauses, "m.direction = 'inbound'")
		}
	}
	if resolved := filter.PersonFilter.ResolvedPersonFilter(); resolved != nil && len(resolved.ExactPersonFilterIdentifiers) > 0 {
		ids := make([]string, 0, len(resolved.ExactPersonFilterIdentifiers))
		for _, id := range resolved.ExactPersonFilterIdentifiers {
			if text := strings.TrimSpace(id.GetExactPersonFilterIdentifier()); text != "" {
				ids = append(ids, text)
			}
		}
		if len(ids) > 0 {
			placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
			clauses = append(clauses, `(m.sender_author_id in (`+placeholders+`) or lower(coalesce(sp.handle,'')) in (`+placeholders+`) or c.participant_author_id in (`+placeholders+`) or lower(coalesce(cp.handle,'')) in (`+placeholders+`))`)
			for range 4 {
				for _, id := range ids {
					args = append(args, strings.TrimPrefix(strings.ToLower(id), "@"))
				}
			}
		}
	} else if unresolved := strings.TrimSpace(filter.PersonFilter.UnresolvedPersonFilterText()); unresolved != "" {
		like := "%" + strings.ToLower(strings.TrimPrefix(unresolved, "@")) + "%"
		clauses = append(clauses, `(lower(coalesce(sp.handle,'')) like ? or lower(coalesce(sp.display_name,'')) like ? or lower(coalesce(cp.handle,'')) like ? or lower(coalesce(cp.display_name,'')) like ? or lower(coalesce(c.title,'')) like ?)`)
		args = append(args, like, like, like, like, like)
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return " where " + strings.Join(clauses, " and "), args
}
