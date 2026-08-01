package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
)

func (s *Store) Messages(ctx context.Context, filter MessageFilter) ([]Message, error) {
	var err error
	filter, err = s.resolveMessageFilterSender(ctx, filter)
	if err != nil {
		return nil, err
	}
	// limit <= 0 means everything; SQLite reads LIMIT -1 as no limit.
	if filter.Limit <= 0 {
		filter.Limit = -1
	}
	query, args := messageListQuery(filter)
	messages, err := scanMessages(ctx, s.db, query, args...)
	if err != nil {
		return nil, err
	}
	return s.withCanonicalWhatsAppMessageDisplayNames(ctx, messages)
}

func (s *Store) MessagesAndTotalCount(ctx context.Context, filter MessageFilter) ([]Message, int, error) {
	whoCandidateRecords, err := s.whoCandidateRecords(ctx)
	if err != nil {
		return nil, 0, err
	}
	filter = resolveMessageFilterSenderFromWhoCandidateRecords(filter, whoCandidateRecords)
	if filter.Limit <= 0 {
		filter.Limit = -1
	}
	query, args := messageListQuery(filter)
	messages, err := scanMessages(ctx, s.db, query, args...)
	if err != nil {
		return nil, 0, err
	}
	total, err := s.countMessagesWithResolvedFilter(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	canonicalWhatsAppMessageDisplayNamesFromWhoCandidateRecords(whoCandidateRecords).applyToMessages(messages)
	return messages, total, nil
}

func (s *Store) CountMessages(ctx context.Context, filter MessageFilter) (int, error) {
	var err error
	filter, err = s.resolveMessageFilterSender(ctx, filter)
	if err != nil {
		return 0, err
	}
	return s.countMessagesWithResolvedFilter(ctx, filter)
}

func (s *Store) countMessagesWithResolvedFilter(ctx context.Context, filter MessageFilter) (int, error) {
	query := "select count(*) from messages where 1=1"
	query, args := applyMessageFilters(query, nil, filter, false)
	var total int
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
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
	filter, err = s.resolveMessageFilterSender(ctx, filter)
	if err != nil {
		return nil, err
	}
	filter, err = s.resolveMessageFilterWho(ctx, filter)
	if err != nil {
		return nil, err
	}
	if filter.Limit <= 0 {
		filter.Limit = -1
	}
	if !hasQuery {
		query := `select m.source_pk, m.chat_jid, m.chat_name, m.msg_id, m.sender_jid, m.sender_name, m.ts, m.from_me, m.text, m.raw_type, m.message_type, m.media_type, m.media_title, m.media_path, m.media_url, m.media_size, m.starred, coalesce(ch.kind, ''), '', ''
from messages m left join chats ch on ch.jid = m.chat_jid where 1=1`
		var args []any
		query += " and not (" + providerNativeSystemMetadataMessageSQLPredicate + ")"
		query, args = applyMessageFilters(query, args, filter, true)
		if filter.Asc {
			query += " order by m.ts asc, m.source_pk asc"
		} else {
			query += " order by m.ts desc, m.source_pk desc"
		}
		query += " limit ?"
		args = append(args, filter.Limit)
		messages, err := scanSearchMessages(ctx, s.db, query, args...)
		if err != nil {
			return nil, err
		}
		return s.withCanonicalWhatsAppMessageDisplayNames(ctx, messages)
	}
	ftsQuery, err := ckstore.FTS5TermsInTextAndMediaColumns(filter.Query)
	if err != nil {
		return nil, err
	}
	query := `select m.source_pk, m.chat_jid, m.chat_name, m.msg_id, m.sender_jid, m.sender_name, m.ts, m.from_me, m.text, m.raw_type, m.message_type, m.media_type, m.media_title, m.media_path, m.media_url, m.media_size, m.starred, coalesce(ch.kind, ''),
` + ckstore.FTS5MarkedSearchResultSnippetSQLExpression("messages_fts", 0) + `,
` + ckstore.FTS5MarkedSearchResultSnippetSQLExpression("messages_fts", 3) + `
from messages_fts f join messages m on m.rowid=f.rowid left join chats ch on ch.jid = m.chat_jid where messages_fts match ?`
	args := []any{ftsQuery}
	query += " and not (" + providerNativeSystemMetadataMessageSQLPredicate + ")"
	query, args = applyMessageFilters(query, args, filter, true)
	query += " order by m.ts desc, m.source_pk desc limit ?"
	args = append(args, filter.Limit)
	messages, err := scanSearchMessages(ctx, s.db, query, args...)
	if err != nil {
		return nil, err
	}
	return s.withCanonicalWhatsAppMessageDisplayNames(ctx, messages)
}

func scanSearchMessages(ctx context.Context, db *sql.DB, query string, args ...any) ([]Message, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var messages []Message
	for rows.Next() {
		var message Message
		var timestamp int64
		var fromMe, starred int
		var messageTextMatch, mediaMatch string
		if err := rows.Scan(
			&message.SourcePK,
			&message.ChatJID,
			&message.ChatName,
			&message.MessageID,
			&message.SenderJID,
			&message.SenderName,
			&timestamp,
			&fromMe,
			&message.Text,
			&message.RawType,
			&message.MessageType,
			&message.MediaType,
			&message.MediaTitle,
			&message.MediaPath,
			&message.MediaURL,
			&message.MediaSize,
			&starred,
			&message.ChatKind,
			&messageTextMatch,
			&mediaMatch,
		); err != nil {
			return nil, err
		}
		message.Timestamp = fromUnix(timestamp)
		message.FromMe = fromMe != 0
		message.Starred = starred != 0
		message.SearchMatches = messageSearchMatchesFromMarkedText(
			messageTextMatch,
			mediaMatch,
		)
		message.Snippet = ckstore.FTS5Snippet(messageSnippetText(message), "")
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

func messageSearchMatchesFromMarkedText(
	messageTextMatch string,
	mediaMatch string,
) []MessageSearchMatch {
	markedTextByField := []struct {
		field      string
		markedText string
	}{
		{field: "Message", markedText: messageTextMatch},
		{field: "Media", markedText: mediaMatch},
	}
	searchMatches := make([]MessageSearchMatch, 0, len(markedTextByField))
	earlierNormalizedMarkedText := make(map[string]struct{}, len(markedTextByField))
	for _, markedText := range markedTextByField {
		normalizedMarkedText := strings.Join(strings.Fields(markedText.markedText), " ")
		if _, duplicatesEarlierField := earlierNormalizedMarkedText[normalizedMarkedText]; duplicatesEarlierField {
			continue
		}
		earlierNormalizedMarkedText[normalizedMarkedText] = struct{}{}
		if runs := ckstore.ParseFTS5MarkedText(markedText.markedText); len(runs) > 0 {
			searchMatches = append(searchMatches, MessageSearchMatch{Field: markedText.field, Runs: runs})
		}
	}
	return searchMatches
}

func (s *Store) SearchCount(ctx context.Context, filter MessageFilter) (int, error) {
	hasQuery := strings.TrimSpace(filter.Query) != ""
	if !hasQuery && !filterAllowsEmptyQuery(filter) {
		return 0, errors.New("search query required")
	}
	var err error
	filter, err = s.resolveMessageFilterSender(ctx, filter)
	if err != nil {
		return 0, err
	}
	filter, err = s.resolveMessageFilterWho(ctx, filter)
	if err != nil {
		return 0, err
	}
	if !hasQuery {
		query := "select count(*) from messages m where 1=1"
		var args []any
		query += " and not (" + providerNativeSystemMetadataMessageSQLPredicate + ")"
		query, args = applyMessageFilters(query, args, filter, true)
		var total int
		if err := s.db.QueryRowContext(ctx, query, args...).Scan(&total); err != nil {
			return 0, err
		}
		return total, nil
	}
	ftsQuery, err := ckstore.FTS5TermsInTextAndMediaColumns(filter.Query)
	if err != nil {
		return 0, err
	}
	query := `select count(*) from messages_fts f join messages m on m.rowid=f.rowid where messages_fts match ?`
	args := []any{ftsQuery}
	query += " and not (" + providerNativeSystemMetadataMessageSQLPredicate + ")"
	query, args = applyMessageFilters(query, args, filter, true)
	var total int
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}

func filterAllowsEmptyQuery(filter MessageFilter) bool {
	return filter.SenderParticipantKeys != nil || strings.TrimSpace(filter.Sender) != "" || filter.WhoKeys != nil || len(filter.ExactPersonFilterIdentifiers) > 0 || strings.TrimSpace(filter.Who) != "" || filter.After != nil || filter.Before != nil
}

func (s *Store) resolveMessageFilterSender(ctx context.Context, filter MessageFilter) (MessageFilter, error) {
	if normalizeWhoIdentity(filter.Sender) == "" || filter.SenderParticipantKeys != nil {
		return filter, nil
	}
	senderIdentity := normalizeWhoIdentity(filter.Sender)
	resolution, err := s.ResolveWho(ctx, senderIdentity)
	if err != nil {
		return MessageFilter{}, err
	}
	filter.Sender = senderIdentity
	filter.SenderParticipantKeys = participantKeysFromMessageFilterWhoResolution(senderIdentity, resolution)
	return filter, nil
}

func resolveMessageFilterSenderFromWhoCandidateRecords(filter MessageFilter, records []whoCandidateRecord) MessageFilter {
	if normalizeWhoIdentity(filter.Sender) == "" || filter.SenderParticipantKeys != nil {
		return filter
	}
	filter.Sender = normalizeWhoIdentity(filter.Sender)
	resolution := resolveWhoFromCandidateRecords(records, filter.Sender)
	filter.SenderParticipantKeys = participantKeysFromMessageFilterWhoResolution(filter.Sender, resolution)
	return filter
}

func (s *Store) resolveMessageFilterWho(ctx context.Context, filter MessageFilter) (MessageFilter, error) {
	if len(filter.ExactPersonFilterIdentifiers) > 0 && filter.WhoKeys == nil {
		filter.WhoKeys = []string{}
		for _, exactPersonFilterIdentifier := range filter.ExactPersonFilterIdentifiers {
			exactPersonFilterIdentifierText := normalizeWhoIdentity(
				exactPersonFilterIdentifier.GetExactPersonFilterIdentifier(),
			)
			if exactPersonFilterIdentifierText == "" {
				continue
			}
			resolution, err := s.ResolveWhoIdentifier(ctx, exactPersonFilterIdentifierText)
			if err != nil {
				return MessageFilter{}, err
			}
			filter.WhoKeys = append(filter.WhoKeys, resolution.ParticipantKeys...)
		}
		filter.WhoKeys = uniqueStrings(filter.WhoKeys)
		return filter, nil
	}
	if normalizeWhoIdentity(filter.Who) == "" || filter.WhoKeys != nil {
		return filter, nil
	}
	whoIdentity, whoParticipantKeys, err := s.resolveWhatsAppIdentityToParticipantKeysForMessageFilter(ctx, filter.Who)
	if err != nil {
		return MessageFilter{}, err
	}
	filter.Who = whoIdentity
	filter.WhoKeys = whoParticipantKeys
	return filter, nil
}

func (s *Store) resolveWhatsAppIdentityToParticipantKeysForMessageFilter(ctx context.Context, identity string) (string, []string, error) {
	identity = normalizeWhoIdentity(identity)
	resolution, err := s.ResolveWhoIdentifier(ctx, identity)
	if err != nil {
		return "", nil, err
	}
	if len(resolution.Candidates) == 0 {
		resolution, err = s.ResolveWho(ctx, identity)
		if err != nil {
			return "", nil, err
		}
	}
	return identity, participantKeysFromMessageFilterWhoResolution(identity, resolution), nil
}

func participantKeysFromMessageFilterWhoResolution(identity string, resolution WhoResolution) []string {
	var exactDisplayNameCandidate *WhoCandidate
	for candidateIndex := range resolution.Candidates {
		candidate := &resolution.Candidates[candidateIndex]
		if strings.EqualFold(normalizeWhoIdentity(candidate.Who), identity) {
			if exactDisplayNameCandidate != nil {
				exactDisplayNameCandidate = nil
				break
			}
			exactDisplayNameCandidate = candidate
		}
	}
	if exactDisplayNameCandidate != nil {
		return exactDisplayNameCandidate.ParticipantKeys
	}
	if len(resolution.Candidates) != 1 || resolution.OnlyCloseSpellingMatch() {
		return []string{}
	}
	return resolution.ParticipantKeys
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
	if filter.SenderParticipantKeys != nil {
		if len(filter.SenderParticipantKeys) == 0 {
			query += " and 0=1"
		} else {
			query += " and exists (select 1 from (" + whoMessageSenderParticipantKeysQuery(prefix) + ") where participant_key in (" + queryPlaceholders(len(filter.SenderParticipantKeys)) + "))"
			for _, key := range filter.SenderParticipantKeys {
				args = append(args, key)
			}
		}
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
		if err := rows.Scan(&m.SourcePK, &m.ChatJID, &m.ChatName, &m.MessageID, &m.SenderJID, &m.SenderName, &ts, &fromMe, &m.Text, &m.RawType, &m.MessageType, &m.MediaType, &m.MediaTitle, &m.MediaPath, &m.MediaURL, &m.MediaSize, &starred, &m.ChatKind, &m.Snippet); err != nil {
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
