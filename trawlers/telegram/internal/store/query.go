package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
)

func (s *Store) Status(ctx context.Context) (Status, error) {
	var out Status
	var successfullyCompletedAtUnixMilliseconds int64
	err := s.db.QueryRowContext(ctx, `
select archive_message_count,
	archive_conversation_count,
	archive_folder_count,
	archive_source_path,
	successfully_completed_at_unix_milliseconds
from last_successfully_completed_archive_sync
where last_successfully_completed_archive_sync_id = 1`).Scan(
		&out.ArchiveMessageCountAfterLastSuccessfullyCompletedSync,
		&out.ArchiveConversationCountAfterLastSuccessfullyCompletedSync,
		&out.ArchiveFolderCountAfterLastSuccessfullyCompletedSync,
		&out.ArchiveSourcePathUsedByLastSuccessfullyCompletedSync,
		&successfullyCompletedAtUnixMilliseconds,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.LastSuccessfullyCompletedArchiveSyncTime = time.UnixMilli(successfullyCompletedAtUnixMilliseconds).UTC()
	out.HasSuccessfullyCompletedArchiveSync = true
	return out, nil
}

func (s *Store) ArchiveCanResolveEveryMessageAndConversationToLocalTrawlerShortReference(
	ctx context.Context,
) (bool, error) {
	var archiveCanResolveEveryMessageAndConversationToLocalTrawlerShortReference bool
	err := s.db.QueryRowContext(ctx, `
with canonical_record_references_with_uniquely_resolvable_valid_local_short_references as (
	select distinct candidate_short_reference.canonical_ref
	from short_refs candidate_short_reference
	where length(candidate_short_reference.alias) between 5 and 52
		and candidate_short_reference.alias not glob '*[^23456789abcdefghjkmnpqrstuvwxyz]*'
		and not exists (
			select 1
			from short_refs conflicting_short_reference
			where conflicting_short_reference.alias = candidate_short_reference.alias
				and conflicting_short_reference.canonical_ref <> candidate_short_reference.canonical_ref
		)
)
select not exists (
	select 1
	from messages archived_message
	where not exists (
		select 1
		from canonical_record_references_with_uniquely_resolvable_valid_local_short_references
		where canonical_ref = ? || cast(archived_message.source_pk as text)
	)
)
and not exists (
	select 1
	from chats archived_conversation
	where not exists (
		select 1
		from canonical_record_references_with_uniquely_resolvable_valid_local_short_references
		where canonical_ref = ? || archived_conversation.account_scoped_conversation_identifier_for_conversation_across_telegram_migrations
	)
)`, MessageRefPrefix, ChatRefPrefix).Scan(
		&archiveCanResolveEveryMessageAndConversationToLocalTrawlerShortReference,
	)
	if err != nil {
		return false, err
	}
	return archiveCanResolveEveryMessageAndConversationToLocalTrawlerShortReference, nil
}

func (s *Store) ListChats(ctx context.Context, limit int, unread bool) ([]Chat, error) {
	if limit <= 0 {
		limit = -1 // SQLite LIMIT -1 is unbounded.
	}
	having := ""
	if unread {
		having = "having sum(unread_count) > 0"
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
with conversation_totals_across_provider_migrations as (
	select account_scoped_conversation_identifier_for_conversation_across_telegram_migrations,
		max(last_message_at) as last_message_at,
		sum(unread_count) as unread_count,
		sum(message_count) as message_count
	from chats
	group by account_scoped_conversation_identifier_for_conversation_across_telegram_migrations
	%s
)
select conversation_totals.account_scoped_conversation_identifier_for_conversation_across_telegram_migrations,
	current_conversation.kind,
	current_conversation.name,
	current_conversation.username,
	conversation_totals.last_message_at,
	conversation_totals.unread_count,
	conversation_totals.message_count,
	coalesce(current_conversation.folder_id, ''),
	current_conversation.forum
from conversation_totals_across_provider_migrations conversation_totals
join chats current_conversation
	on cast(current_conversation.id as text) =
		conversation_totals.account_scoped_conversation_identifier_for_conversation_across_telegram_migrations
order by conversation_totals.last_message_at desc
limit ?`, having), limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]Chat, 0)
	for rows.Next() {
		var c Chat
		var ts int64
		var forum int
		if err := rows.Scan(
			&c.JID,
			&c.Kind,
			&c.Name,
			&c.Username,
			&ts,
			&c.UnreadCount,
			&c.MessageCount,
			&c.FolderID,
			&forum,
		); err != nil {
			return nil, err
		}
		c.AccountScopedConversationIdentifierForConversationAcrossTelegramMigrations = c.JID
		c.LastMessageAt = fromUnix(ts)
		c.Forum = forum != 0
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, s.nameSelfChat(ctx, out)
}

func (s *Store) ListFolders(ctx context.Context) ([]Folder, error) {
	rows, err := s.db.QueryContext(ctx, `select f.id,f.title,f.emoticon,f.color,f.flags_json,
       count(fc.chat_jid), coalesce(sum(c.unread_count), 0)
from folders f
left join folder_chats fc on fc.folder_id=f.id
left join chats c on c.id=fc.chat_jid
group by f.id,f.title,f.emoticon,f.color,f.flags_json
order by f.title, cast(f.id as integer)`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]Folder, 0)
	for rows.Next() {
		var f Folder
		if err := rows.Scan(&f.ID, &f.Title, &f.Emoticon, &f.Color, &f.FlagsJSON, &f.ChatCount, &f.UnreadCount); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) Messages(ctx context.Context, filter MessageFilter) ([]Message, error) {
	return s.messages(ctx, filter, false)
}

// MessagesBySourcePKs returns only the archived messages identified by the
// supplied source keys. Sync uses this narrow lookup to preserve cloud-derived
// Telegram fields while merging the much smaller local Postbox cache; loading
// and humanising the entire lifetime archive on every sync would make ordinary
// incremental work scale with all downloaded history.
func (s *Store) MessagesBySourcePKs(ctx context.Context, sourcePKs []int64) ([]Message, error) {
	const batchSize = 500 // Stay comfortably below SQLite's variable limit.
	if len(sourcePKs) == 0 {
		return nil, nil
	}
	out := make([]Message, 0, len(sourcePKs))
	for start := 0; start < len(sourcePKs); start += batchSize {
		end := min(start+batchSize, len(sourcePKs))
		placeholders := strings.TrimSuffix(strings.Repeat("?,", end-start), ",")
		args := make([]any, 0, end-start)
		for _, sourcePK := range sourcePKs[start:end] {
			args = append(args, sourcePK)
		}
		rows, err := s.db.QueryContext(ctx, `select source_pk,coalesce(sender_jid,''),coalesce(sender_name,''),from_me,coalesce(message_type,''),coalesce(media_type,''),coalesce(media_title,''),coalesce(media_path,''),coalesce(topic_id,''),coalesce(reply_to_msg_id,''),coalesce(reply_to_chat_jid,''),coalesce(thread_id,''),coalesce(edit_ts,0),coalesce(forward_json,''),coalesce(reactions_json,''),coalesce(views,0),coalesce(forwards,0),coalesce(replies_count,0),coalesce(pinned,0)
from messages where source_pk in (`+placeholders+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var message Message
			var fromMe, pinned int
			var editTS int64
			if err := rows.Scan(&message.SourcePK, &message.SenderJID, &message.SenderName, &fromMe, &message.MessageType, &message.MediaType, &message.MediaTitle, &message.MediaPath, &message.TopicID, &message.ReplyToID, &message.ReplyToChat, &message.ThreadID, &editTS, &message.ForwardJSON, &message.ReactionsJSON, &message.Views, &message.Forwards, &message.RepliesCount, &pinned); err != nil {
				_ = rows.Close()
				return nil, err
			}
			message.FromMe = fromMe != 0
			message.Pinned = pinned != 0
			message.EditTime = fromUnix(editTS)
			out = append(out, message)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// MessageExists answers the hot incremental-history boundary check without
// materialising the lifetime archive in memory.
func (s *Store) MessageExists(ctx context.Context, sourcePK int64) (bool, error) {
	var exists int
	err := s.db.QueryRowContext(ctx, `select exists(select 1 from messages where source_pk = ?)`, sourcePK).Scan(&exists)
	return exists != 0, err
}

func (s *Store) Search(ctx context.Context, filter MessageFilter) ([]Message, error) {
	if strings.TrimSpace(filter.Query) == "" {
		if !filter.AllowsFilterOnlySearch() {
			return nil, errors.New("search query required")
		}
		return s.messages(ctx, filter, false)
	}
	return s.messages(ctx, filter, true)
}

func (s *Store) messages(ctx context.Context, filter MessageFilter, search bool) ([]Message, error) {
	var err error
	filter, err = s.resolveWhoFilter(ctx, filter)
	if err != nil {
		return nil, err
	}
	if filter.Limit <= 0 {
		filter.Limit = -1 // SQLite LIMIT -1 is unbounded.
	}
	query := `select m.source_pk,m.chat_jid,coalesce(m.chat_name,''),m.msg_id,coalesce(m.sender_jid,''),coalesce(m.sender_name,''),m.ts,coalesce(m.edit_ts,0),m.from_me,coalesce(m.text,''),m.raw_type,coalesce(m.message_type,''),coalesce(m.media_type,''),coalesce(m.media_title,''),coalesce(m.media_path,''),coalesce(m.media_url,''),coalesce(m.media_size,0),coalesce(m.metadata_type,''),coalesce(m.metadata_title,''),coalesce(m.metadata_url,''),coalesce(m.metadata_json,''),m.starred,coalesce(m.topic_id,''),coalesce(m.reply_to_msg_id,''),coalesce(m.reply_to_chat_jid,''),coalesce(m.thread_id,''),coalesce(m.forward_json,''),coalesce(m.reactions_json,''),coalesce(m.views,0),coalesce(m.forwards,0),coalesce(m.replies_count,0),coalesce(m.pinned,0),coalesce(c.kind,''),coalesce(t.title,''),''
from messages m
left join chats c on cast(c.id as text) = m.chat_jid
left join topics t on t.chat_jid=m.chat_jid and t.topic_id=m.topic_id
where 1=1`
	args := []any{}
	prefix := "m."
	if search {
		ftsQuery, err := ckstore.FTS5TermsInTextAndMediaColumns(filter.Query)
		if err != nil {
			return nil, err
		}
		query = `select m.source_pk,m.chat_jid,coalesce(m.chat_name,''),m.msg_id,coalesce(m.sender_jid,''),coalesce(m.sender_name,''),m.ts,coalesce(m.edit_ts,0),m.from_me,coalesce(m.text,''),m.raw_type,coalesce(m.message_type,''),coalesce(m.media_type,''),coalesce(m.media_title,''),coalesce(m.media_path,''),coalesce(m.media_url,''),coalesce(m.media_size,0),coalesce(m.metadata_type,''),coalesce(m.metadata_title,''),coalesce(m.metadata_url,''),coalesce(m.metadata_json,''),m.starred,coalesce(m.topic_id,''),coalesce(m.reply_to_msg_id,''),coalesce(m.reply_to_chat_jid,''),coalesce(m.thread_id,''),coalesce(m.forward_json,''),coalesce(m.reactions_json,''),coalesce(m.views,0),coalesce(m.forwards,0),coalesce(m.replies_count,0),coalesce(m.pinned,0),coalesce(c.kind,''),coalesce(t.title,''),
` + ckstore.FTS5MarkedSearchResultSnippetSQLExpression("messages_fts", 0) + `,
` + ckstore.FTS5MarkedSearchResultSnippetSQLExpression("messages_fts", 3) + `
from messages_fts f
join messages m on m.rowid=f.rowid
left join chats c on cast(c.id as text) = m.chat_jid
left join topics t on t.chat_jid=m.chat_jid and t.topic_id=m.topic_id
where messages_fts match ?`
		args = append(args, ftsQuery)
	}
	if filter.ChatJID != "" {
		query += " and " + prefix + `chat_jid in (
select cast(id as text)
from chats
where account_scoped_conversation_identifier_for_conversation_across_telegram_migrations = ?
)`
		args = append(args, filter.ChatJID)
	}
	if filter.Sender != "" {
		query += " and " + prefix + "sender_jid = ?"
		args = append(args, filter.Sender)
	}
	if filter.After != nil {
		query += " and " + prefix + "ts >= ?"
		args = append(args, unix(*filter.After))
	}
	if filter.Before != nil {
		query += " and " + prefix + "ts <= ?"
		args = append(args, unix(*filter.Before))
	}
	if filter.FromMe != nil {
		query += " and " + prefix + "from_me = ?"
		args = append(args, boolInt(*filter.FromMe))
	}
	if filter.HasMedia {
		query += " and " + prefix + "media_type <> ''"
	}
	if filter.Pinned {
		query += " and " + prefix + "pinned <> 0"
	}
	query, args = appendWhoParticipantFilter(query, args, prefix, filter)
	if search {
		query += " order by ts desc, source_pk desc limit ?"
	} else if filter.Asc {
		query += " order by ts asc, source_pk asc limit ?"
	} else {
		query += " order by ts desc, source_pk desc limit ?"
	}
	args = append(args, filter.Limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]Message, 0)
	for rows.Next() {
		var m Message
		var ts, editTS int64
		var fromMe, starred, pinned int
		var messageTextMatch, mediaMatch string
		scanDestinations := []any{
			&m.SourcePK, &m.ChatJID, &m.ChatName, &m.MessageID, &m.SenderJID, &m.SenderName,
			&ts, &editTS, &fromMe, &m.Text, &m.RawType, &m.MessageType, &m.MediaType,
			&m.MediaTitle, &m.MediaPath, &m.MediaURL, &m.MediaSize, &m.MetadataType,
			&m.MetadataTitle, &m.MetadataURL, &m.MetadataJSON, &starred, &m.TopicID,
			&m.ReplyToID, &m.ReplyToChat, &m.ThreadID, &m.ForwardJSON, &m.ReactionsJSON,
			&m.Views, &m.Forwards, &m.RepliesCount, &pinned, &m.ChatKind, &m.TopicTitle,
		}
		if search {
			scanDestinations = append(
				scanDestinations,
				&messageTextMatch,
				&mediaMatch,
			)
		} else {
			scanDestinations = append(scanDestinations, &m.Snippet)
		}
		if err := rows.Scan(scanDestinations...); err != nil {
			return nil, err
		}
		m.Timestamp = fromUnix(ts)
		m.EditTime = fromUnix(editTS)
		m.FromMe = fromMe != 0
		m.Starred = starred != 0
		m.Pinned = pinned != 0
		if search {
			m.SearchMatches = telegramMessageSearchMatchesFromMarkedText(
				messageTextMatch,
				mediaMatch,
				m.MediaType,
			)
			m.Snippet = ckstore.FTS5Snippet(messageSnippetText(m), "")
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := s.humanizeMessages(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

func telegramMessageSearchMatchesFromMarkedText(
	messageTextMatch string,
	mediaMatch string,
	providerMediaType string,
) []MessageSearchMatch {
	markedTextByField := []struct {
		field      string
		markedText string
	}{
		{field: "Message", markedText: messageTextMatch},
		{
			field: "Media",
			markedText: removeFinalProviderMediaTypeFromMarkedMediaSearchText(
				mediaMatch,
				providerMediaType,
			),
		},
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

func removeFinalProviderMediaTypeFromMarkedMediaSearchText(
	markedMediaSearchText string,
	providerMediaType string,
) string {
	providerMediaType = strings.TrimSpace(providerMediaType)
	markedWords := strings.Fields(markedMediaSearchText)
	if providerMediaType == "" || len(markedWords) == 0 {
		return markedMediaSearchText
	}
	finalMarkedWord := markedWords[len(markedWords)-1]
	finalWordWithoutMatchMarkers := strings.ReplaceAll(
		strings.ReplaceAll(finalMarkedWord, "\ue000", ""),
		"\ue001",
		"",
	)
	if finalWordWithoutMatchMarkers != providerMediaType {
		return markedMediaSearchText
	}
	return strings.Join(markedWords[:len(markedWords)-1], " ")
}

func messageSnippetText(message Message) string {
	return strings.TrimSpace(strings.Join([]string{
		message.Text,
		message.MediaTitle,
		message.MetadataTitle,
		message.MetadataURL,
		message.ChatName,
		message.SenderName,
	}, " "))
}
