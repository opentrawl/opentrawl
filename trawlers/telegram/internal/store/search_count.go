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

func (s *Store) MessageContents(ctx context.Context, chatJID string) (map[int64]Message, error) {
	query := `select source_pk,chat_jid,coalesce(chat_name,''),msg_id,coalesce(sender_jid,''),coalesce(sender_name,''),ts,coalesce(edit_ts,0),from_me,coalesce(text,''),raw_type,coalesce(message_type,''),coalesce(media_type,''),coalesce(media_title,''),coalesce(media_path,''),coalesce(media_url,''),coalesce(media_size,0),coalesce(metadata_type,''),coalesce(metadata_title,''),coalesce(metadata_url,''),coalesce(metadata_json,''),starred,coalesce(topic_id,''),coalesce(reply_to_msg_id,''),coalesce(reply_to_chat_jid,''),coalesce(thread_id,''),coalesce(forward_json,''),coalesce(reactions_json,''),coalesce(views,0),coalesce(forwards,0),coalesce(replies_count,0),coalesce(pinned,0) from messages`
	args := []any{}
	if strings.TrimSpace(chatJID) != "" {
		query += ` where chat_jid = ?`
		args = append(args, chatJID)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[int64]Message{}
	for rows.Next() {
		var message Message
		var ts, editTS int64
		var fromMe, starred, pinned int
		if err := rows.Scan(&message.SourcePK, &message.ChatJID, &message.ChatName, &message.MessageID, &message.SenderJID, &message.SenderName, &ts, &editTS, &fromMe, &message.Text, &message.RawType, &message.MessageType, &message.MediaType, &message.MediaTitle, &message.MediaPath, &message.MediaURL, &message.MediaSize, &message.MetadataType, &message.MetadataTitle, &message.MetadataURL, &message.MetadataJSON, &starred, &message.TopicID, &message.ReplyToID, &message.ReplyToChat, &message.ThreadID, &message.ForwardJSON, &message.ReactionsJSON, &message.Views, &message.Forwards, &message.RepliesCount, &pinned); err != nil {
			return nil, err
		}
		message.Timestamp = fromUnix(ts)
		message.EditTime = fromUnix(editTS)
		message.FromMe = fromMe != 0
		message.Starred = starred != 0
		message.Pinned = pinned != 0
		out[message.SourcePK] = message
	}
	return out, rows.Err()
}
