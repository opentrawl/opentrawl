package store

import (
	"context"
	"database/sql"
	"strings"
)

type SyncStats struct {
	Added   int64
	Updated int64
	Removed int64
}

func messageSyncStats(ctx context.Context, tx *sql.Tx, messages []Message, chatJID string) (SyncStats, error) {
	existing, err := syncMessages(ctx, tx, chatJID)
	if err != nil {
		return SyncStats{}, err
	}
	imported := make(map[int64]struct{}, len(messages))
	var stats SyncStats
	for _, message := range messages {
		imported[message.SourcePK] = struct{}{}
		existingMessage, ok := existing[message.SourcePK]
		if !ok {
			stats.Added++
		} else if syncMessageRecord(existingMessage) != syncMessageRecord(message) {
			stats.Updated++
		}
	}
	for sourcePK := range existing {
		if _, ok := imported[sourcePK]; !ok {
			stats.Removed++
		}
	}
	return stats, nil
}

func syncMessages(ctx context.Context, tx *sql.Tx, chatJID string) (map[int64]Message, error) {
	query := `select source_pk,chat_jid,coalesce(chat_name,''),msg_id,coalesce(sender_jid,''),coalesce(sender_name,''),ts,coalesce(edit_ts,0),from_me,coalesce(text,''),raw_type,coalesce(message_type,''),coalesce(media_type,''),coalesce(media_title,''),coalesce(media_path,''),coalesce(media_url,''),coalesce(media_size,0),coalesce(metadata_type,''),coalesce(metadata_title,''),coalesce(metadata_url,''),coalesce(metadata_json,''),starred,coalesce(topic_id,''),coalesce(reply_to_msg_id,''),coalesce(reply_to_chat_jid,''),coalesce(thread_id,''),coalesce(forward_json,''),coalesce(reactions_json,''),coalesce(views,0),coalesce(forwards,0),coalesce(replies_count,0),coalesce(pinned,0) from messages`
	args := []any{}
	if strings.TrimSpace(chatJID) != "" {
		query += ` where chat_jid = ?`
		args = append(args, chatJID)
	}
	rows, err := tx.QueryContext(ctx, query, args...)
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

type syncMessage struct {
	SourcePK      int64
	ChatJID       string
	ChatName      string
	MessageID     string
	SenderJID     string
	SenderName    string
	Timestamp     int64
	EditTime      int64
	FromMe        bool
	Text          string
	RawType       int
	MessageType   string
	MediaType     string
	MediaTitle    string
	MediaPath     string
	MediaURL      string
	MediaSize     int64
	MetadataType  string
	MetadataTitle string
	MetadataURL   string
	MetadataJSON  string
	Starred       bool
	TopicID       string
	ReplyToID     string
	ReplyToChat   string
	ThreadID      string
	ForwardJSON   string
	ReactionsJSON string
	Views         int
	Forwards      int
	RepliesCount  int
	Pinned        bool
}

func syncMessageRecord(message Message) syncMessage {
	return syncMessage{
		SourcePK:      message.SourcePK,
		ChatJID:       message.ChatJID,
		ChatName:      message.ChatName,
		MessageID:     message.MessageID,
		SenderJID:     message.SenderJID,
		SenderName:    message.SenderName,
		Timestamp:     unix(message.Timestamp),
		EditTime:      unix(message.EditTime),
		FromMe:        message.FromMe,
		Text:          message.Text,
		RawType:       message.RawType,
		MessageType:   message.MessageType,
		MediaType:     message.MediaType,
		MediaTitle:    message.MediaTitle,
		MediaPath:     message.MediaPath,
		MediaURL:      message.MediaURL,
		MediaSize:     message.MediaSize,
		MetadataType:  message.MetadataType,
		MetadataTitle: message.MetadataTitle,
		MetadataURL:   message.MetadataURL,
		MetadataJSON:  message.MetadataJSON,
		Starred:       message.Starred,
		TopicID:       message.TopicID,
		ReplyToID:     message.ReplyToID,
		ReplyToChat:   message.ReplyToChat,
		ThreadID:      message.ThreadID,
		ForwardJSON:   message.ForwardJSON,
		ReactionsJSON: message.ReactionsJSON,
		Views:         message.Views,
		Forwards:      message.Forwards,
		RepliesCount:  message.RepliesCount,
		Pinned:        message.Pinned,
	}
}
