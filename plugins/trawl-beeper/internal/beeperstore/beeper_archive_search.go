package beeperstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
)

const beeperMessageSelectFields = `
	m.rowid,
	m.message_id,
	m.chat_id,
	m.account_id,
	coalesce(nullif(m.network, ''), c.network, ''),
	coalesce(c.title, ''),
	coalesce(nullif(c.chat_type, ''), 'single'),
	m.sort_key,
	m.sender_id,
	m.sender_name,
	m.is_sender,
	m.timestamp_unix,
	m.edited_timestamp_unix,
	m.message_type,
	m.text,
	m.linked_message_id,
	m.is_deleted,
	m.is_hidden,
	m.media_kind,
	m.media_title,
	m.media_mime_type,
	m.media_url,
	m.media_size_bytes,
	m.media_transcript,
	m.links_summary,
	m.reactions_summary
`

// SearchMessages executes an FTS5/filtered query against beeper_messages.
func (s *BeeperArchiveStore) SearchMessages(ctx context.Context, filter BeeperMessageQueryFilter) ([]BeeperArchivedMessage, error) {
	whereClause, args, useFTS, err := buildBeeperMessageWhereClause(filter)
	if err != nil {
		return nil, err
	}

	var query strings.Builder
	if useFTS {
		query.WriteString("select ")
		query.WriteString(beeperMessageSelectFields)
		query.WriteString(", ")
		query.WriteString(ckstore.FTS5MarkedSearchResultSnippetSQLExpression("beeper_messages_fts", 0))
		query.WriteString(", ")
		query.WriteString(ckstore.FTS5MarkedSearchResultSnippetSQLExpression("beeper_messages_fts", 4))
		query.WriteString(" from beeper_messages_fts f join beeper_messages m on m.rowid = f.rowid left join beeper_chats c on c.chat_id = m.chat_id")
	} else {
		query.WriteString("select ")
		query.WriteString(beeperMessageSelectFields)
		query.WriteString(", '', '' from beeper_messages m left join beeper_chats c on c.chat_id = m.chat_id")
	}
	if whereClause != "" {
		query.WriteString(" where ")
		query.WriteString(whereClause)
	}
	query.WriteString(" order by m.timestamp_unix desc, m.rowid desc")
	if filter.Limit > 0 {
		query.WriteString(" limit ?")
		args = append(args, filter.Limit)
	}

	rows, err := s.db.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []BeeperArchivedMessage
	for rows.Next() {
		msg, textSnippet, mediaSnippet, scanErr := scanBeeperMessageWithSnippets(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		if runs := ckstore.ParseFTS5MarkedText(textSnippet); len(runs) > 0 {
			msg.SearchMatches = append(msg.SearchMatches, BeeperMessageSearchMatch{Field: "Message", Runs: runs})
		}
		if runs := ckstore.ParseFTS5MarkedText(mediaSnippet); len(runs) > 0 {
			msg.SearchMatches = append(msg.SearchMatches, BeeperMessageSearchMatch{Field: "Media", Runs: runs})
		}
		out = append(out, msg)
	}
	return out, rows.Err()
}

// CountSearchMessages counts total rows matching filter without a limit.
func (s *BeeperArchiveStore) CountSearchMessages(ctx context.Context, filter BeeperMessageQueryFilter) (int, error) {
	whereClause, args, useFTS, err := buildBeeperMessageWhereClause(filter)
	if err != nil {
		return 0, err
	}

	var query strings.Builder
	if useFTS {
		query.WriteString("select count(*) from beeper_messages_fts f join beeper_messages m on m.rowid = f.rowid left join beeper_chats c on c.chat_id = m.chat_id")
	} else {
		query.WriteString("select count(*) from beeper_messages m left join beeper_chats c on c.chat_id = m.chat_id")
	}
	if whereClause != "" {
		query.WriteString(" where ")
		query.WriteString(whereClause)
	}
	var total int
	if err := s.db.QueryRowContext(ctx, query.String(), args...).Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}

// ListMessagesAndTotalCount returns newest-first messages and total match count.
func (s *BeeperArchiveStore) ListMessagesAndTotalCount(ctx context.Context, filter BeeperMessageQueryFilter) ([]BeeperArchivedMessage, int, error) {
	total, err := s.CountSearchMessages(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	messages, err := s.SearchMessages(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	return messages, total, nil
}

// ListChats lists archived Beeper conversations ordered by last activity descending.
func (s *BeeperArchiveStore) ListChats(ctx context.Context, limit int, unreadOnly bool, networkFilter string) ([]BeeperArchivedChat, error) {
	var whereParts []string
	var args []any
	if unreadOnly {
		whereParts = append(whereParts, "unread_count > 0")
	}
	if trimmedNet := strings.TrimSpace(networkFilter); trimmedNet != "" {
		whereParts = append(whereParts, "lower(network) = lower(?)")
		args = append(args, trimmedNet)
	}

	var query strings.Builder
	query.WriteString(`
select chat_id, local_chat_id, account_id, network, title, chat_type, description,
       is_archived, is_muted, is_pinned, is_read_only, is_low_priority,
       unread_count, last_activity_unix, updated_at_unix
from beeper_chats
`)
	if len(whereParts) > 0 {
		query.WriteString(" where ")
		query.WriteString(strings.Join(whereParts, " and "))
	}
	query.WriteString(" order by last_activity_unix desc, chat_id asc")
	if limit > 0 {
		query.WriteString(" limit ?")
		args = append(args, limit)
	}

	rows, err := s.db.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var chats []BeeperArchivedChat
	for rows.Next() {
		chat, scanErr := scanBeeperChat(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		chats = append(chats, chat)
	}
	return chats, rows.Err()
}

// ChatByID fetches one archived conversation by chat ID.
func (s *BeeperArchiveStore) ChatByID(ctx context.Context, chatID string) (BeeperArchivedChat, error) {
	row := s.db.QueryRowContext(ctx, `
select chat_id, local_chat_id, account_id, network, title, chat_type, description,
       is_archived, is_muted, is_pinned, is_read_only, is_low_priority,
       unread_count, last_activity_unix, updated_at_unix
from beeper_chats
where chat_id = ?
`, chatID)
	return scanBeeperChat(row)
}

// MessageByID fetches one archived message by its Beeper message ID.
func (s *BeeperArchiveStore) MessageByID(ctx context.Context, messageID string) (BeeperArchivedMessage, error) {
	row := s.db.QueryRowContext(ctx, `
select `+beeperMessageSelectFields+`, '', ''
from beeper_messages m
left join beeper_chats c on c.chat_id = m.chat_id
where m.message_id = ?
`, messageID)
	msg, _, _, err := scanBeeperMessageWithSnippets(row)
	return msg, err
}

// MessageWindow returns up to eachSide earlier and later messages around target
// in the same conversation, returned newest-first as required by OpenRecord.
func (s *BeeperArchiveStore) MessageWindow(ctx context.Context, target BeeperArchivedMessage, eachSide int) (BeeperMessageContextWindow, error) {
	if eachSide <= 0 {
		eachSide = 10
	}
	targetTS := unixOrZero(target.Timestamp)

	beforeRows, err := s.db.QueryContext(ctx, `
select `+beeperMessageSelectFields+`, '', ''
from beeper_messages m
left join beeper_chats c on c.chat_id = m.chat_id
where m.chat_id = ?
  and (m.timestamp_unix < ? or (m.timestamp_unix = ? and m.rowid < ?))
order by m.timestamp_unix desc, m.rowid desc
limit ?
`, target.ChatID, targetTS, targetTS, target.RowID, eachSide+1)
	if err != nil {
		return BeeperMessageContextWindow{}, err
	}
	defer func() { _ = beforeRows.Close() }()

	var before []BeeperArchivedMessage
	for beforeRows.Next() {
		msg, _, _, scanErr := scanBeeperMessageWithSnippets(beforeRows)
		if scanErr != nil {
			return BeeperMessageContextWindow{}, scanErr
		}
		before = append(before, msg)
	}
	if err := beforeRows.Err(); err != nil {
		return BeeperMessageContextWindow{}, err
	}
	beforeTruncated := len(before) > eachSide
	if beforeTruncated {
		before = before[:eachSide]
	}

	afterRows, err := s.db.QueryContext(ctx, `
select `+beeperMessageSelectFields+`, '', ''
from beeper_messages m
left join beeper_chats c on c.chat_id = m.chat_id
where m.chat_id = ?
  and (m.timestamp_unix > ? or (m.timestamp_unix = ? and m.rowid > ?))
order by m.timestamp_unix asc, m.rowid asc
limit ?
`, target.ChatID, targetTS, targetTS, target.RowID, eachSide+1)
	if err != nil {
		return BeeperMessageContextWindow{}, err
	}
	defer func() { _ = afterRows.Close() }()

	var afterAsc []BeeperArchivedMessage
	for afterRows.Next() {
		msg, _, _, scanErr := scanBeeperMessageWithSnippets(afterRows)
		if scanErr != nil {
			return BeeperMessageContextWindow{}, scanErr
		}
		afterAsc = append(afterAsc, msg)
	}
	if err := afterRows.Err(); err != nil {
		return BeeperMessageContextWindow{}, err
	}
	afterTruncated := len(afterAsc) > eachSide
	if afterTruncated {
		afterAsc = afterAsc[:eachSide]
	}

	// Newest first: reversed afterAsc -> target -> before (already desc).
	combined := make([]BeeperArchivedMessage, 0, len(afterAsc)+1+len(before))
	for i := len(afterAsc) - 1; i >= 0; i-- {
		combined = append(combined, afterAsc[i])
	}
	combined = append(combined, target)
	combined = append(combined, before...)

	return BeeperMessageContextWindow{
		Messages:        combined,
		BeforeTruncated: beforeTruncated,
		AfterTruncated:  afterTruncated,
	}, nil
}

// ConversationParticipantIdentities returns observed non-self participants for one chat.
func (s *BeeperArchiveStore) ConversationParticipantIdentities(ctx context.Context, chatID string) ([]BeeperConversationParticipantIdentity, error) {
	rows, err := s.db.QueryContext(ctx, `
select user_id, full_name, username, phone_number, email
from beeper_chat_participants
where chat_id = ? and is_self = 0
order by lower(coalesce(nullif(full_name, ''), nullif(username, ''), user_id))
`, chatID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var identities []BeeperConversationParticipantIdentity
	for rows.Next() {
		var userID, fullName, username, phone, email string
		if err := rows.Scan(&userID, &fullName, &username, &phone, &email); err != nil {
			return nil, err
		}
		display := firstNonEmptyTrimmed(fullName, username, phone, email, userID)
		if display == "" {
			continue
		}
		var idents []string
		for _, candidate := range []string{userID, username, phone, email} {
			if trimmed := strings.TrimSpace(candidate); trimmed != "" {
				idents = append(idents, trimmed)
			}
		}
		identities = append(identities, BeeperConversationParticipantIdentity{
			PersonDisplayName: display,
			ExactPersonFilterIdentifiersObservedByTrawlerArchive: trawlkit.NewExactPersonFilterIdentifiers(idents),
		})
	}
	return identities, rows.Err()
}

func buildBeeperMessageWhereClause(filter BeeperMessageQueryFilter) (string, []any, bool, error) {
	var parts []string
	var args []any
	useFTS := false

	if trimmedQuery := strings.TrimSpace(filter.Query); trimmedQuery != "" {
		ftsExpr := ckstore.FTS5TokenQuery(trimmedQuery)
		if ftsExpr == "" {
			return "1 = 0", nil, false, nil
		}
		useFTS = true
		parts = append(parts, "beeper_messages_fts match ?")
		args = append(args, ftsExpr)
	}
	if trimmedChatID := strings.TrimSpace(filter.ChatID); trimmedChatID != "" {
		parts = append(parts, "m.chat_id = ?")
		args = append(args, trimmedChatID)
	}
	if trimmedNet := strings.TrimSpace(filter.Network); trimmedNet != "" {
		parts = append(parts, "lower(coalesce(nullif(m.network, ''), c.network, '')) = lower(?)")
		args = append(args, trimmedNet)
	}
	if filter.After != nil && !filter.After.IsZero() {
		parts = append(parts, "m.timestamp_unix >= ?")
		args = append(args, filter.After.UTC().Unix())
	}
	if filter.Before != nil && !filter.Before.IsZero() {
		parts = append(parts, "m.timestamp_unix <= ?")
		args = append(args, filter.Before.UTC().Unix())
	}
	if filter.IsSender != nil {
		parts = append(parts, "m.is_sender = ?")
		args = append(args, boolToSQLiteInt(*filter.IsSender))
	}
	if filter.HasMedia {
		parts = append(parts, "(m.media_kind <> '' or m.media_title <> '' or m.media_url <> '')")
	}
	if trimmedSender := strings.TrimSpace(filter.Sender); trimmedSender != "" {
		like := "%" + strings.ToLower(trimmedSender) + "%"
		parts = append(parts, "(lower(m.sender_name) like ? or lower(m.sender_id) like ?)")
		args = append(args, like, like)
	}

	if personClause, personArgs := buildBeeperPersonFilterClause(filter); personClause != "" {
		parts = append(parts, personClause)
		args = append(args, personArgs...)
	}

	return strings.Join(parts, " and "), args, useFTS, nil
}

func buildBeeperPersonFilterClause(filter BeeperMessageQueryFilter) (string, []any) {
	var exactIDs []string
	if resolved := filter.PersonFilter.ResolvedPersonFilter(); resolved != nil {
		for _, ident := range resolved.ExactPersonFilterIdentifiers {
			if value := strings.TrimSpace(ident.GetExactPersonFilterIdentifier()); value != "" {
				exactIDs = append(exactIDs, value)
			}
		}
	}
	exactIDs = append(exactIDs, filter.ResolvedParticipantIDs...)

	var clauses []string
	var args []any
	for _, id := range uniqueLowerStrings(exactIDs) {
		clauses = append(clauses, `(
			lower(m.sender_id) = lower(?) or
			exists (
				select 1 from beeper_chat_participants p
				where p.chat_id = m.chat_id
				  and (lower(p.user_id) = lower(?) or lower(p.username) = lower(?) or lower(p.phone_number) = lower(?) or lower(p.email) = lower(?))
			)
		)`)
		args = append(args, id, id, id, id, id)
	}
	for _, name := range uniqueLowerStrings(filter.ResolvedParticipantNames) {
		clauses = append(clauses, `(
			lower(m.sender_name) = lower(?) or
			lower(coalesce(c.title, '')) = lower(?) or
			exists (
				select 1 from beeper_chat_participants p
				where p.chat_id = m.chat_id and lower(p.full_name) = lower(?)
			)
		)`)
		args = append(args, name, name, name)
	}
	if len(clauses) == 0 {
		if filter.PersonFilter.ResolvedPersonFilter() != nil || filter.PersonFilter.UnresolvedPersonFilterText() != "" {
			return "1 = 0", nil
		}
		return "", nil
	}
	return "(" + strings.Join(clauses, " or ") + ")", args
}

type sqliteRowScanner interface {
	Scan(dest ...any) error
}

func scanBeeperChat(scanner sqliteRowScanner) (BeeperArchivedChat, error) {
	var chat BeeperArchivedChat
	var isArchived, isMuted, isPinned, isReadOnly, isLowPriority int
	var lastActivityUnix, updatedAtUnix int64
	err := scanner.Scan(
		&chat.ChatID,
		&chat.LocalChatID,
		&chat.AccountID,
		&chat.Network,
		&chat.Title,
		&chat.ChatType,
		&chat.Description,
		&isArchived,
		&isMuted,
		&isPinned,
		&isReadOnly,
		&isLowPriority,
		&chat.UnreadCount,
		&lastActivityUnix,
		&updatedAtUnix,
	)
	if err != nil {
		return BeeperArchivedChat{}, err
	}
	chat.IsArchived = isArchived != 0
	chat.IsMuted = isMuted != 0
	chat.IsPinned = isPinned != 0
	chat.IsReadOnly = isReadOnly != 0
	chat.IsLowPriority = isLowPriority != 0
	chat.LastActivity = timeFromUnix(lastActivityUnix)
	chat.UpdatedAt = timeFromUnix(updatedAtUnix)
	return chat, nil
}

func scanBeeperMessageWithSnippets(scanner sqliteRowScanner) (BeeperArchivedMessage, string, string, error) {
	var msg BeeperArchivedMessage
	var isSender, isDeleted, isHidden int
	var tsUnix, editedUnix int64
	var textSnippet, mediaSnippet string
	err := scanner.Scan(
		&msg.RowID,
		&msg.MessageID,
		&msg.ChatID,
		&msg.AccountID,
		&msg.Network,
		&msg.ChatTitle,
		&msg.ChatType,
		&msg.SortKey,
		&msg.SenderID,
		&msg.SenderName,
		&isSender,
		&tsUnix,
		&editedUnix,
		&msg.MessageType,
		&msg.Text,
		&msg.LinkedMessageID,
		&isDeleted,
		&isHidden,
		&msg.MediaKind,
		&msg.MediaTitle,
		&msg.MediaMimeType,
		&msg.MediaURL,
		&msg.MediaSizeBytes,
		&msg.MediaTranscript,
		&msg.LinksSummary,
		&msg.ReactionsSummary,
		&textSnippet,
		&mediaSnippet,
	)
	if err != nil {
		return BeeperArchivedMessage{}, "", "", err
	}
	msg.IsSender = isSender != 0
	msg.IsDeleted = isDeleted != 0
	msg.IsHidden = isHidden != 0
	msg.Timestamp = timeFromUnix(tsUnix)
	msg.EditedTimestamp = timeFromUnix(editedUnix)
	return msg, textSnippet, mediaSnippet, nil
}

func firstNonEmptyTrimmed(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func uniqueLowerStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	var out []string
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

// ErrorsIsNoRows reports whether err represents sql.ErrNoRows.
func ErrorsIsNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}

// ParseBeeperCanonicalMessageID extracts the Beeper message ID from a canonical ref.
func ParseBeeperCanonicalMessageID(ref string) (string, bool) {
	trimmed := strings.TrimSpace(ref)
	if !strings.HasPrefix(trimmed, BeeperMessageRefPrefix) {
		return "", false
	}
	id := strings.TrimSpace(strings.TrimPrefix(trimmed, BeeperMessageRefPrefix))
	if id == "" {
		return "", false
	}
	return id, true
}

// ParseBeeperCanonicalChatID extracts the Beeper chat ID from a canonical ref.
func ParseBeeperCanonicalChatID(ref string) (string, bool) {
	trimmed := strings.TrimSpace(ref)
	if !strings.HasPrefix(trimmed, BeeperChatRefPrefix) {
		return "", false
	}
	id := strings.TrimSpace(strings.TrimPrefix(trimmed, BeeperChatRefPrefix))
	if id == "" {
		return "", false
	}
	return id, true
}

// FormatCountDebug formats basic store counts for run logs.
func FormatCountDebug(chats int, messages int) string {
	return fmt.Sprintf("chats=%d messages=%d", chats, messages)
}
