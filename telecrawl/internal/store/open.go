package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
)

var ErrMessageNotFound = errors.New("message not found")

type MessageWindow struct {
	Target          Message
	Messages        []Message
	BeforeTruncated bool
	AfterTruncated  bool
}

const messageOpenColumns = `source_pk,chat_jid,coalesce(chat_name,''),msg_id,coalesce(sender_jid,''),coalesce(sender_name,''),ts,coalesce(edit_ts,0),from_me,coalesce(text,''),raw_type,coalesce(message_type,''),coalesce(media_type,''),coalesce(media_title,''),coalesce(media_path,''),coalesce(media_url,''),coalesce(media_size,0),coalesce(metadata_type,''),coalesce(metadata_title,''),coalesce(metadata_url,''),coalesce(metadata_json,''),starred,coalesce(topic_id,''),coalesce(reply_to_msg_id,''),coalesce(reply_to_chat_jid,''),coalesce(thread_id,''),coalesce(forward_json,''),coalesce(reactions_json,''),coalesce(views,0),coalesce(forwards,0),coalesce(replies_count,0),coalesce(pinned,0),''`

type messageScanner interface {
	Scan(dest ...any) error
}

func OpenReadOnly(ctx context.Context, path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("db path is required")
	}
	if info, err := os.Stat(path); err != nil {
		return nil, err
	} else if info.IsDir() {
		return nil, fmt.Errorf("db path is a directory: %s", path)
	}
	dsn := fmt.Sprintf("file:%s?mode=ro&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db, path: path}, nil
}

func (s *Store) OpenMessageWindow(ctx context.Context, sourcePK int64, radius int) (MessageWindow, error) {
	if radius < 0 {
		radius = 0
	}
	target, err := s.messageBySourcePK(ctx, sourcePK)
	if err != nil {
		return MessageWindow{}, err
	}
	before, beforeTruncated, err := s.neighbourMessages(ctx, target, radius, true)
	if err != nil {
		return MessageWindow{}, err
	}
	after, afterTruncated, err := s.neighbourMessages(ctx, target, radius, false)
	if err != nil {
		return MessageWindow{}, err
	}
	messages := make([]Message, 0, len(before)+1+len(after))
	messages = append(messages, before...)
	messages = append(messages, target)
	messages = append(messages, after...)
	if err := s.humanizeMessages(ctx, messages); err != nil {
		return MessageWindow{}, err
	}
	for _, message := range messages {
		if message.SourcePK == target.SourcePK {
			target = message
			break
		}
	}
	return MessageWindow{
		Target:          target,
		Messages:        messages,
		BeforeTruncated: beforeTruncated,
		AfterTruncated:  afterTruncated,
	}, nil
}

func (s *Store) messageBySourcePK(ctx context.Context, sourcePK int64) (Message, error) {
	row := s.db.QueryRowContext(ctx, `select `+messageOpenColumns+` from messages where source_pk = ?`, sourcePK)
	message, err := scanOpenMessage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, ErrMessageNotFound
	}
	return message, err
}

func (s *Store) neighbourMessages(ctx context.Context, target Message, radius int, before bool) ([]Message, bool, error) {
	limit := radius + 1
	comparator := ">"
	order := "asc"
	if before {
		comparator = "<"
		order = "desc"
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`select %s from messages where chat_jid = ? and (ts %s ? or (ts = ? and source_pk %s ?)) order by ts %s, source_pk %s limit ?`, messageOpenColumns, comparator, comparator, order, order),
		target.ChatJID, unix(target.Timestamp), unix(target.Timestamp), target.SourcePK, limit)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	messages, err := scanOpenMessages(rows)
	if err != nil {
		return nil, false, err
	}
	truncated := len(messages) > radius
	if truncated {
		messages = messages[:radius]
	}
	if before {
		reverseMessages(messages)
	}
	return messages, truncated, nil
}

func scanOpenMessages(rows *sql.Rows) ([]Message, error) {
	var messages []Message
	for rows.Next() {
		message, err := scanOpenMessage(rows)
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

func scanOpenMessage(scanner messageScanner) (Message, error) {
	var m Message
	var ts, editTS int64
	var fromMe, starred, pinned int
	if err := scanner.Scan(&m.SourcePK, &m.ChatJID, &m.ChatName, &m.MessageID, &m.SenderJID, &m.SenderName, &ts, &editTS, &fromMe, &m.Text, &m.RawType, &m.MessageType, &m.MediaType, &m.MediaTitle, &m.MediaPath, &m.MediaURL, &m.MediaSize, &m.MetadataType, &m.MetadataTitle, &m.MetadataURL, &m.MetadataJSON, &starred, &m.TopicID, &m.ReplyToID, &m.ReplyToChat, &m.ThreadID, &m.ForwardJSON, &m.ReactionsJSON, &m.Views, &m.Forwards, &m.RepliesCount, &pinned, &m.Snippet); err != nil {
		return Message{}, err
	}
	m.Timestamp = fromUnix(ts)
	m.EditTime = fromUnix(editTS)
	m.FromMe = fromMe != 0
	m.Starred = starred != 0
	m.Pinned = pinned != 0
	return m, nil
}

func reverseMessages(messages []Message) {
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
}
