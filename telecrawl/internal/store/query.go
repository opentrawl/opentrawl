package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	ckstore "github.com/openclaw/crawlkit/store"
)

func (s *Store) Status(ctx context.Context) (Status, error) {
	out := Status{DBPath: s.path}
	for _, c := range []struct {
		dst *int
		q   string
	}{
		{&out.Chats, "select count(*) from chats"},
		{&out.UnreadChats, "select count(*) from chats where unread_count > 0"},
		{&out.UnreadMessages, "select coalesce(sum(unread_count), 0) from chats"},
		{&out.Messages, "select count(*) from messages"},
		{&out.MediaMessages, "select count(*) from messages where media_type <> ''"},
		{&out.Folders, "select count(*) from folders"},
		{&out.Topics, "select count(*) from topics"},
	} {
		if err := s.db.QueryRowContext(ctx, c.q).Scan(c.dst); err != nil {
			return out, err
		}
	}
	var oldest, newest sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `select min(ts), max(ts) from messages`).Scan(&oldest, &newest); err != nil {
		return out, err
	}
	if oldest.Valid {
		out.OldestMessage = fromUnix(oldest.Int64)
	}
	if newest.Valid {
		out.NewestMessage = fromUnix(newest.Int64)
	}
	var lastImport string
	_ = s.db.QueryRowContext(ctx, `select value from sync_state where key='last_import_at'`).Scan(&lastImport)
	if t, err := time.Parse(time.RFC3339Nano, lastImport); err == nil {
		out.LastImportAt = t
	}
	_ = s.db.QueryRowContext(ctx, `select value from sync_state where key='source_path'`).Scan(&out.LastSource)
	return out, nil
}

func (s *Store) ListChats(ctx context.Context, limit int, unread bool) ([]Chat, error) {
	if limit <= 0 {
		limit = 50
	}
	where := ""
	if unread {
		where = "where unread_count > 0"
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`select cast(id as text),kind,name,username,last_message_at,unread_count,message_count,coalesce(folder_id,''),forum from chats %s order by last_message_at desc limit ?`, where), limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]Chat, 0)
	for rows.Next() {
		var c Chat
		var ts int64
		var forum int
		if err := rows.Scan(&c.JID, &c.Kind, &c.Name, &c.Username, &ts, &c.UnreadCount, &c.MessageCount, &c.FolderID, &forum); err != nil {
			return nil, err
		}
		c.LastMessageAt = fromUnix(ts)
		c.Forum = forum != 0
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) CountChats(ctx context.Context, unread bool) (int, error) {
	query := `select count(*) from chats`
	if unread {
		query += ` where unread_count > 0`
	}
	var total int
	if err := s.db.QueryRowContext(ctx, query).Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}

func (s *Store) ListFolders(ctx context.Context) ([]Folder, error) {
	rows, err := s.db.QueryContext(ctx, `select f.id,f.title,f.emoticon,f.color,f.flags_json,count(fc.chat_jid)
from folders f
left join folder_chats fc on fc.folder_id=f.id
group by f.id,f.title,f.emoticon,f.color,f.flags_json
order by cast(f.id as integer), f.title`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]Folder, 0)
	for rows.Next() {
		var f Folder
		if err := rows.Scan(&f.ID, &f.Title, &f.Emoticon, &f.Color, &f.FlagsJSON, &f.ChatCount); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) ChatsInFolder(ctx context.Context, folderID string, limit int) ([]Chat, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `select cast(c.id as text),c.kind,c.name,c.username,c.last_message_at,c.unread_count,c.message_count,coalesce(c.folder_id,''),c.forum
from folder_chats fc join chats c on cast(c.id as text)=fc.chat_jid
where fc.folder_id=?
order by fc.position asc, c.last_message_at desc
limit ?`, folderID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]Chat, 0)
	for rows.Next() {
		var c Chat
		var ts int64
		var forum int
		if err := rows.Scan(&c.JID, &c.Kind, &c.Name, &c.Username, &ts, &c.UnreadCount, &c.MessageCount, &c.FolderID, &forum); err != nil {
			return nil, err
		}
		c.LastMessageAt = fromUnix(ts)
		c.Forum = forum != 0
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) CountChatsInFolder(ctx context.Context, folderID string) (int, error) {
	var total int
	if err := s.db.QueryRowContext(ctx, `select count(*) from folder_chats where folder_id=?`, folderID).Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}

func (s *Store) ListTopics(ctx context.Context, chatJID string, limit int) ([]Topic, error) {
	if strings.TrimSpace(chatJID) == "" {
		return nil, errors.New("chat id required")
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `select chat_jid,topic_id,title,top_message_id,icon_color,icon_emoji_id,unread_count,unread_mentions_count,unread_reactions_count,pinned,closed,hidden,last_message_at
from topics where chat_jid=?
order by pinned desc, last_message_at desc, cast(topic_id as integer) desc
limit ?`, chatJID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]Topic, 0)
	for rows.Next() {
		var t Topic
		var ts int64
		var pinned, closed, hidden int
		if err := rows.Scan(&t.ChatJID, &t.TopicID, &t.Title, &t.TopMessageID, &t.IconColor, &t.IconEmojiID, &t.UnreadCount, &t.UnreadMentionsCount, &t.UnreadReactionsCount, &pinned, &closed, &hidden, &ts); err != nil {
			return nil, err
		}
		t.Pinned = pinned != 0
		t.Closed = closed != 0
		t.Hidden = hidden != 0
		t.LastMessageAt = fromUnix(ts)
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) Messages(ctx context.Context, filter MessageFilter) ([]Message, error) {
	return s.messages(ctx, filter, false)
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
		filter.Limit = 50
	}
	query := `select source_pk,chat_jid,coalesce(chat_name,''),msg_id,coalesce(sender_jid,''),coalesce(sender_name,''),ts,coalesce(edit_ts,0),from_me,coalesce(text,''),raw_type,coalesce(message_type,''),coalesce(media_type,''),coalesce(media_title,''),coalesce(media_path,''),coalesce(media_url,''),coalesce(media_size,0),coalesce(metadata_type,''),coalesce(metadata_title,''),coalesce(metadata_url,''),coalesce(metadata_json,''),starred,coalesce(topic_id,''),coalesce(reply_to_msg_id,''),coalesce(reply_to_chat_jid,''),coalesce(thread_id,''),coalesce(forward_json,''),coalesce(reactions_json,''),coalesce(views,0),coalesce(forwards,0),coalesce(replies_count,0),coalesce(pinned,0),'' from messages where 1=1`
	args := []any{}
	prefix := ""
	if search {
		ftsQuery, err := ckstore.FTS5Terms(filter.Query, "")
		if err != nil {
			return nil, err
		}
		query = `select m.source_pk,m.chat_jid,coalesce(m.chat_name,''),m.msg_id,coalesce(m.sender_jid,''),coalesce(m.sender_name,''),m.ts,coalesce(m.edit_ts,0),m.from_me,coalesce(m.text,''),m.raw_type,coalesce(m.message_type,''),coalesce(m.media_type,''),coalesce(m.media_title,''),coalesce(m.media_path,''),coalesce(m.media_url,''),coalesce(m.media_size,0),coalesce(m.metadata_type,''),coalesce(m.metadata_title,''),coalesce(m.metadata_url,''),coalesce(m.metadata_json,''),m.starred,coalesce(m.topic_id,''),coalesce(m.reply_to_msg_id,''),coalesce(m.reply_to_chat_jid,''),coalesce(m.thread_id,''),coalesce(m.forward_json,''),coalesce(m.reactions_json,''),coalesce(m.views,0),coalesce(m.forwards,0),coalesce(m.replies_count,0),coalesce(m.pinned,0),'' from messages_fts f join messages m on m.rowid=f.rowid where messages_fts match ?`
		args = append(args, ftsQuery)
		prefix = "m."
	}
	if filter.ChatJID != "" {
		query += " and " + prefix + "chat_jid = ?"
		args = append(args, filter.ChatJID)
	}
	if filter.Sender != "" {
		query += " and " + prefix + "sender_jid = ?"
		args = append(args, filter.Sender)
	}
	if filter.TopicID != "" {
		query += " and " + prefix + "topic_id = ?"
		args = append(args, filter.TopicID)
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
		if err := rows.Scan(&m.SourcePK, &m.ChatJID, &m.ChatName, &m.MessageID, &m.SenderJID, &m.SenderName, &ts, &editTS, &fromMe, &m.Text, &m.RawType, &m.MessageType, &m.MediaType, &m.MediaTitle, &m.MediaPath, &m.MediaURL, &m.MediaSize, &m.MetadataType, &m.MetadataTitle, &m.MetadataURL, &m.MetadataJSON, &starred, &m.TopicID, &m.ReplyToID, &m.ReplyToChat, &m.ThreadID, &m.ForwardJSON, &m.ReactionsJSON, &m.Views, &m.Forwards, &m.RepliesCount, &pinned, &m.Snippet); err != nil {
			return nil, err
		}
		m.Timestamp = fromUnix(ts)
		m.EditTime = fromUnix(editTS)
		m.FromMe = fromMe != 0
		m.Starred = starred != 0
		m.Pinned = pinned != 0
		if search {
			m.Snippet = contractSnippet(messageSnippetText(m), filter.Query)
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

func messageSnippetText(message Message) string {
	return strings.TrimSpace(message.Text + " " + message.MediaTitle + " " + message.MetadataTitle + " " + message.MetadataURL)
}
