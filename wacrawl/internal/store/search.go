package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	ckstore "github.com/openclaw/crawlkit/store"
)

func (s *Store) Messages(ctx context.Context, filter MessageFilter) ([]Message, error) {
	if filter.Limit <= 0 {
		filter.Limit = 50
	}
	query, args := messageListQuery(filter)
	return scanMessages(ctx, s.db, query, args...)
}

func messageListQuery(filter MessageFilter) (string, []any) {
	validQuery, validArgs := filteredMessagesQuery(filter, "")
	validQuery += " and " + validUnixPredicate("ts")
	if filter.After != nil || filter.Before != nil {
		if filter.Asc {
			validQuery += " order by ts asc, source_pk asc limit ?"
		} else {
			validQuery += " order by ts desc, source_pk desc limit ?"
		}
		return validQuery, append(validArgs, filter.Limit)
	}

	if filter.Asc {
		validQuery, validArgs = filteredMessagesQuery(filter, ", 1 as sort_bucket, ts as sort_ts")
		validQuery += " and " + validUnixPredicate("ts")
		invalidQuery, invalidArgs := filteredMessagesQuery(filter, ", 0 as sort_bucket, 0 as sort_ts")
		invalidQuery += " and " + invalidUnixPredicate("ts")
		query := "select " + messageScanColumns + " from (select * from (" + invalidQuery + " order by source_pk asc limit ?) union all select * from (" + validQuery + " order by ts asc, source_pk asc limit ?)) order by sort_bucket asc, sort_ts asc, source_pk asc limit ?"
		args := append([]any{}, invalidArgs...)
		args = append(args, filter.Limit)
		args = append(args, validArgs...)
		args = append(args, filter.Limit, filter.Limit)
		return query, args
	}

	validQuery, validArgs = filteredMessagesQuery(filter, ", 0 as sort_bucket, ts as sort_ts")
	validQuery += " and " + validUnixPredicate("ts")
	invalidQuery, invalidArgs := filteredMessagesQuery(filter, ", 1 as sort_bucket, 0 as sort_ts")
	invalidQuery += " and " + invalidUnixPredicate("ts")
	query := "select " + messageScanColumns + " from (select * from (" + validQuery + " order by ts desc, source_pk desc limit ?) union all select * from (" + invalidQuery + " order by source_pk desc limit ?)) order by sort_bucket asc, sort_ts desc, source_pk desc limit ?"
	args := append([]any{}, validArgs...)
	args = append(args, filter.Limit)
	args = append(args, invalidArgs...)
	args = append(args, filter.Limit, filter.Limit)
	return query, args
}

func filteredMessagesQuery(filter MessageFilter, extraColumns string) (string, []any) {
	query := "select " + messageSelectColumns + extraColumns + " from messages where 1=1"
	return applyMessageFilters(query, nil, filter, false)
}

func (s *Store) Search(ctx context.Context, filter MessageFilter) ([]Message, error) {
	hasQuery := strings.TrimSpace(filter.Query) != ""
	if !hasQuery && !filterAllowsEmptyQuery(filter) {
		return nil, errors.New("search query required")
	}
	var err error
	filter, err = s.resolveMessageFilterWho(ctx, filter)
	if err != nil {
		return nil, err
	}
	if filter.Limit <= 0 {
		filter.Limit = 50
	}
	if !hasQuery {
		query := "select " + messageSelectColumns + " from messages where 1=1"
		var args []any
		query, args = applyMessageFilters(query, args, filter, false)
		if filter.Asc {
			query += " order by ts asc, source_pk asc"
		} else {
			query += " order by ts desc, source_pk desc"
		}
		query += " limit ?"
		args = append(args, filter.Limit)
		messages, err := scanMessages(ctx, s.db, query, args...)
		if err != nil {
			return nil, err
		}
		return s.withCanonicalSenderNames(ctx, messages)
	}
	ftsQuery, err := ckstore.FTS5Terms(filter.Query, "")
	if err != nil {
		return nil, err
	}
	query := `select m.source_pk, m.chat_jid, m.chat_name, m.msg_id, m.sender_jid, m.sender_name, m.ts, m.from_me, m.text, m.raw_type, m.message_type, m.media_type, m.media_title, m.media_path, m.media_url, m.media_size, m.starred, '' from messages_fts f join messages m on m.rowid=f.rowid where messages_fts match ?`
	args := []any{ftsQuery}
	query, args = applyMessageFilters(query, args, filter, true)
	query += " order by bm25(messages_fts) limit ?"
	args = append(args, filter.Limit)
	messages, err := scanMessages(ctx, s.db, query, args...)
	if err != nil {
		return nil, err
	}
	for i := range messages {
		messages[i].Snippet = contractSnippet(messageSnippetText(messages[i]), filter.Query)
	}
	return s.withCanonicalSenderNames(ctx, messages)
}

func (s *Store) SearchCount(ctx context.Context, filter MessageFilter) (int, error) {
	hasQuery := strings.TrimSpace(filter.Query) != ""
	if !hasQuery && !filterAllowsEmptyQuery(filter) {
		return 0, errors.New("search query required")
	}
	var err error
	filter, err = s.resolveMessageFilterWho(ctx, filter)
	if err != nil {
		return 0, err
	}
	if !hasQuery {
		query := "select count(*) from messages where 1=1"
		var args []any
		query, args = applyMessageFilters(query, args, filter, false)
		var total int
		if err := s.db.QueryRowContext(ctx, query, args...).Scan(&total); err != nil {
			return 0, err
		}
		return total, nil
	}
	ftsQuery, err := ckstore.FTS5Terms(filter.Query, "")
	if err != nil {
		return 0, err
	}
	query := `select count(*) from messages_fts f join messages m on m.rowid=f.rowid where messages_fts match ?`
	args := []any{ftsQuery}
	query, args = applyMessageFilters(query, args, filter, true)
	var total int
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}

func filterAllowsEmptyQuery(filter MessageFilter) bool {
	return filter.WhoKeys != nil || strings.TrimSpace(filter.Who) != "" || filter.After != nil || filter.Before != nil
}

func (s *Store) resolveMessageFilterWho(ctx context.Context, filter MessageFilter) (MessageFilter, error) {
	if normalizeWhoIdentity(filter.Who) == "" || filter.WhoKeys != nil {
		return filter, nil
	}
	resolution, err := s.ResolveWhoIdentifier(ctx, filter.Who)
	if err != nil {
		return MessageFilter{}, err
	}
	if len(resolution.Candidates) == 0 {
		resolution, err = s.ResolveWho(ctx, filter.Who)
		if err != nil {
			return MessageFilter{}, err
		}
	}
	filter.Who = normalizeWhoIdentity(filter.Who)
	if len(resolution.Candidates) != 1 || resolution.OnlyCloseSpellingMatch() {
		filter.WhoKeys = []string{}
		return filter, nil
	}
	filter.WhoKeys = resolution.ParticipantKeys
	return filter, nil
}

func applyMessageFilters(query string, args []any, filter MessageFilter, joined bool) (string, []any) {
	prefix := ""
	if joined {
		prefix = "m."
	}
	if strings.TrimSpace(filter.ChatJID) != "" {
		query += " and " + prefix + "chat_jid = ?"
		args = append(args, filter.ChatJID)
	}
	if strings.TrimSpace(filter.Sender) != "" {
		query += " and " + prefix + "sender_jid = ?"
		args = append(args, filter.Sender)
	}
	if filter.After != nil {
		query += " and " + prefix + "ts >= ?"
		args = append(args, unix(*filter.After))
	}
	if filter.Before != nil {
		if filter.BeforePK > 0 {
			query += " and (" + prefix + "ts < ? or (" + prefix + "ts = ? and " + prefix + "source_pk < ?))"
			args = append(args, unix(*filter.Before), unix(*filter.Before), filter.BeforePK)
		} else {
			query += " and " + prefix + "ts <= ?"
			args = append(args, unix(*filter.Before))
		}
	}
	if filter.FromMe != nil {
		query += " and " + prefix + "from_me = ?"
		args = append(args, boolInt(*filter.FromMe))
	}
	if filter.HasMedia {
		query += " and (" + prefix + "media_type <> '' or " + prefix + "media_path <> '' or " + prefix + "media_url <> '')"
	}
	if filter.WhoKeys != nil {
		if len(filter.WhoKeys) == 0 {
			query += " and 0=1"
		} else {
			query += " and exists (select 1 from (" + whoMessageParticipantKeysQuery(prefix) + ") where participant_key in (" + queryPlaceholders(len(filter.WhoKeys)) + "))"
			for _, key := range filter.WhoKeys {
				args = append(args, key)
			}
		}
	}
	return query, args
}

func queryPlaceholders(count int) string {
	if count <= 0 {
		return ""
	}
	placeholders := make([]string, count)
	for i := range placeholders {
		placeholders[i] = "?"
	}
	return strings.Join(placeholders, ",")
}

func scanMessages(ctx context.Context, db *sql.DB, query string, args ...any) ([]Message, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Message
	for rows.Next() {
		var m Message
		var ts int64
		var fromMe, starred int
		if err := rows.Scan(&m.SourcePK, &m.ChatJID, &m.ChatName, &m.MessageID, &m.SenderJID, &m.SenderName, &ts, &fromMe, &m.Text, &m.RawType, &m.MessageType, &m.MediaType, &m.MediaTitle, &m.MediaPath, &m.MediaURL, &m.MediaSize, &starred, &m.Snippet); err != nil {
			return nil, err
		}
		m.Timestamp = fromUnix(ts)
		m.FromMe = fromMe != 0
		m.Starred = starred != 0
		out = append(out, m)
	}
	return out, rows.Err()
}

func messageSnippetText(message Message) string {
	return strings.TrimSpace(message.Text + " " + message.MediaTitle)
}
