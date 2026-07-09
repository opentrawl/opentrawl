package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
)

var ErrMessageNotFound = errors.New("message not found")

type MessageWindow struct {
	Target          Message
	Messages        []Message
	Participants    []string
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
	st, err := ckstore.OpenReadOnly(ctx, path)
	if err != nil {
		return nil, err
	}
	out, err := UseExisting(ctx, st, path)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	out.owned = true
	return out, nil
}

func userVersion(ctx context.Context, db *sql.DB) (int, error) {
	var version int
	if err := db.QueryRowContext(ctx, "pragma user_version").Scan(&version); err != nil {
		return 0, err
	}
	return version, nil
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
	participants, err := s.groupParticipants(ctx, target.ChatJID)
	if err != nil {
		return MessageWindow{}, err
	}
	return MessageWindow{
		Target:          target,
		Messages:        messages,
		Participants:    participants,
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

func (s *Store) groupParticipants(ctx context.Context, chatJID string) ([]string, error) {
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
	nullif(trim(gp.first_name), ''),
	nullif(trim(c.username), ''),
	nullif(trim(gp.user_jid), '')
) as display_name
from group_participants gp
join chats ch on cast(ch.id as text) = gp.group_jid and ch.kind in ('group','channel')
left join contacts c on c.jid = gp.user_jid
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
		displayName = normalizeDisplayName(displayName)
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

// GroupMembers is one group or channel's active membership: Count is the real
// head count (every active row, whatever the store could resolve), Names is
// the deduped display names it could resolve. A caller that shows fewer names
// than Count still has an honest total to render as "+N".
type GroupMembers struct {
	Count int64
	Names []string
}

// GroupMembersByChat resolves every group and channel's active members in one
// query, the same name-resolution rule groupParticipants uses for a single
// chat. The chats lister calls this once for the whole list rather than once
// per chat, so listing N groups costs one query, not N.
func (s *Store) GroupMembersByChat(ctx context.Context) (map[string]GroupMembers, error) {
	rows, err := s.db.QueryContext(ctx, `
select gp.group_jid, coalesce(
	nullif(trim(c.full_name), ''),
	nullif(trim(gp.contact_name), ''),
	nullif(trim(c.business_name), ''),
	nullif(trim(c.first_name || ' ' || c.last_name), ''),
	nullif(trim(gp.first_name), ''),
	nullif(trim(c.username), ''),
	nullif(trim(gp.user_jid), '')
) as display_name
from group_participants gp
join chats ch on cast(ch.id as text) = gp.group_jid and ch.kind in ('group','channel')
left join contacts c on c.jid = gp.user_jid
where gp.is_active != 0
order by gp.group_jid, lower(display_name), display_name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]GroupMembers{}
	seen := map[string]map[string]struct{}{}
	for rows.Next() {
		var groupJID, displayName string
		if err := rows.Scan(&groupJID, &displayName); err != nil {
			return nil, err
		}
		members := out[groupJID]
		members.Count++
		displayName = normalizeDisplayName(displayName)
		if displayName != "" {
			key := strings.ToLower(displayName)
			if seen[groupJID] == nil {
				seen[groupJID] = map[string]struct{}{}
			}
			if _, ok := seen[groupJID][key]; !ok {
				seen[groupJID][key] = struct{}{}
				members.Names = append(members.Names, displayName)
			}
		}
		out[groupJID] = members
	}
	return out, rows.Err()
}
