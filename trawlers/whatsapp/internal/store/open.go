package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
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
	messages, err = s.withCanonicalSenderNames(ctx, messages)
	if err != nil {
		return Message{}, err
	}
	return messages[0], nil
}

func (s *Store) MessageWindow(ctx context.Context, target Message, eachSide int) ([]Message, error) {
	if eachSide < 0 {
		eachSide = 0
	}
	before, err := s.messagesBefore(ctx, target, eachSide)
	if err != nil {
		return nil, err
	}
	after, err := s.messagesAfter(ctx, target, eachSide)
	if err != nil {
		return nil, err
	}
	out := make([]Message, 0, len(before)+1+len(after))
	out = append(out, before...)
	out = append(out, target)
	out = append(out, after...)
	return s.withCanonicalSenderNames(ctx, out)
}

func (s *Store) GroupParticipants(ctx context.Context, chatJID string) ([]string, error) {
	chatJID = strings.TrimSpace(chatJID)
	if chatJID == "" {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
select coalesce(
	nullif(trim(c.full_name), ''),
	nullif(trim(gp.contact_name), ''),
	nullif(trim(c.business_name), ''),
	nullif(trim(c.first_name || ' ' || c.last_name), ''),
	nullif(trim(gp.user_jid), '')
) as display_name
from group_participants gp
join chats ch on ch.jid = gp.group_jid and ch.kind = 'group'
left join contacts c on `+contactJIDPredicate("c", "gp.user_jid")+`
where gp.group_jid = ?
  and gp.is_active != 0
order by lower(display_name), display_name`, chatJID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	seen := map[string]struct{}{}
	out := []string{}
	for rows.Next() {
		var displayName string
		if err := rows.Scan(&displayName); err != nil {
			return nil, err
		}
		displayName = normalizeWhoIdentity(displayName)
		if displayName == "" {
			continue
		}
		key := strings.ToLower(displayName)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, displayName)
	}
	return out, rows.Err()
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
