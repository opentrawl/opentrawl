package store

import (
	"context"
	"errors"
	"strings"

	ckstore "github.com/openclaw/crawlkit/store"
)

func (s *Store) CountSearch(ctx context.Context, filter MessageFilter) (int, error) {
	if strings.TrimSpace(filter.Query) == "" {
		if !filter.AllowsFilterOnlySearch() {
			return 0, errors.New("search query required")
		}
		return s.CountMessages(ctx, filter)
	}
	var err error
	filter, err = s.resolveWhoFilter(ctx, filter)
	if err != nil {
		return 0, err
	}
	ftsQuery, err := ckstore.FTS5Terms(filter.Query, "")
	if err != nil {
		return 0, err
	}
	query := `select count(*) from messages_fts f join messages m on m.rowid=f.rowid where messages_fts match ?`
	args := []any{ftsQuery}
	if filter.ChatJID != "" {
		query += " and m.chat_jid = ?"
		args = append(args, filter.ChatJID)
	}
	if filter.Sender != "" {
		query += " and m.sender_jid = ?"
		args = append(args, filter.Sender)
	}
	if filter.TopicID != "" {
		query += " and m.topic_id = ?"
		args = append(args, filter.TopicID)
	}
	if filter.After != nil {
		query += " and m.ts >= ?"
		args = append(args, unix(*filter.After))
	}
	if filter.Before != nil {
		query += " and m.ts <= ?"
		args = append(args, unix(*filter.Before))
	}
	if filter.FromMe != nil {
		query += " and m.from_me = ?"
		args = append(args, boolInt(*filter.FromMe))
	}
	if filter.HasMedia {
		query += " and m.media_type <> ''"
	}
	if filter.Pinned {
		query += " and m.pinned <> 0"
	}
	query, args = appendWhoParticipantFilter(query, args, "m.", filter)
	var total int
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}

func (s *Store) CountMessages(ctx context.Context, filter MessageFilter) (int, error) {
	var err error
	filter, err = s.resolveWhoFilter(ctx, filter)
	if err != nil {
		return 0, err
	}
	query := `select count(*) from messages m where 1=1`
	args := []any{}
	if filter.ChatJID != "" {
		query += " and m.chat_jid = ?"
		args = append(args, filter.ChatJID)
	}
	if filter.Sender != "" {
		query += " and m.sender_jid = ?"
		args = append(args, filter.Sender)
	}
	if filter.TopicID != "" {
		query += " and m.topic_id = ?"
		args = append(args, filter.TopicID)
	}
	if filter.After != nil {
		query += " and m.ts >= ?"
		args = append(args, unix(*filter.After))
	}
	if filter.Before != nil {
		query += " and m.ts <= ?"
		args = append(args, unix(*filter.Before))
	}
	if filter.FromMe != nil {
		query += " and m.from_me = ?"
		args = append(args, boolInt(*filter.FromMe))
	}
	if filter.HasMedia {
		query += " and m.media_type <> ''"
	}
	if filter.Pinned {
		query += " and m.pinned <> 0"
	}
	query, args = appendWhoParticipantFilter(query, args, "m.", filter)
	var total int
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}
