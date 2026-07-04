package store

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

type SnapshotData struct {
	Contacts     []Contact
	Chats        []Chat
	Folders      []Folder
	FolderChats  []FolderChat
	Groups       []Group
	Participants []GroupParticipant
	Topics       []Topic
	Messages     []Message
}

func (d SnapshotData) Validate() error {
	seen := map[int64]struct{}{}
	for _, message := range d.Messages {
		if message.SourcePK == 0 {
			return fmt.Errorf("message with empty source_pk")
		}
		if _, ok := seen[message.SourcePK]; ok {
			return fmt.Errorf("duplicate message source_pk %d", message.SourcePK)
		}
		seen[message.SourcePK] = struct{}{}
	}
	return nil
}

func (s *Store) ExportAll(ctx context.Context) (SnapshotData, error) {
	contacts, err := s.allContacts(ctx)
	if err != nil {
		return SnapshotData{}, err
	}
	chats, err := s.ListChats(ctx, int(^uint(0)>>1), false)
	if err != nil {
		return SnapshotData{}, err
	}
	folders, err := s.ListFolders(ctx)
	if err != nil {
		return SnapshotData{}, err
	}
	folderChats, err := s.allFolderChats(ctx)
	if err != nil {
		return SnapshotData{}, err
	}
	topics, err := s.allTopics(ctx)
	if err != nil {
		return SnapshotData{}, err
	}
	participants, err := s.allGroupParticipants(ctx)
	if err != nil {
		return SnapshotData{}, err
	}
	messages, err := s.Messages(ctx, MessageFilter{Limit: int(^uint(0) >> 1), Asc: true})
	if err != nil {
		return SnapshotData{}, err
	}
	return SnapshotData{Contacts: contacts, Chats: chats, Folders: folders, FolderChats: folderChats, Participants: participants, Topics: topics, Messages: messages}, nil
}

func (s *Store) ImportSnapshot(ctx context.Context, data SnapshotData, sourcePath string, finishedAt time.Time) error {
	if finishedAt.IsZero() {
		finishedAt = time.Now().UTC()
	}
	stats := ImportStats{SourcePath: sourcePath, DBPath: s.Path(), Chats: len(data.Chats), Messages: len(data.Messages), StartedAt: finishedAt, FinishedAt: finishedAt}
	for _, message := range data.Messages {
		if message.MediaType != "" || message.MediaPath != "" || message.MediaURL != "" {
			stats.MediaMessages++
		}
	}
	return s.ReplaceAll(ctx, stats, data.Contacts, data.Chats, data.Folders, data.FolderChats, data.Topics, data.Participants, data.Messages)
}

func (s *Store) ListContacts(ctx context.Context, limit int) ([]Contact, error) {
	if limit <= 0 {
		limit = 100
	}
	return s.contacts(ctx, limit)
}

func (s *Store) CountContacts(ctx context.Context) (int, error) {
	var total int
	if err := s.db.QueryRowContext(ctx, `select count(*) from contacts`).Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}

func (s *Store) ExportContacts(ctx context.Context) ([]Contact, error) {
	query := `select jid,coalesce(peer_type,''),coalesce(phone,''),coalesce(full_name,''),coalesce(first_name,''),coalesce(last_name,''),coalesce(business_name,''),coalesce(username,''),coalesce(lid,''),coalesce(about_text,''),coalesce(avatar_path,''),coalesce(updated_at,0)
from contacts c
where exists (select 1 from chats ch where cast(ch.id as text)=c.jid)
   or exists (select 1 from messages m where m.chat_jid=c.jid or m.sender_jid=c.jid)
order by jid`
	return s.queryContacts(ctx, query, nil)
}

func (s *Store) allContacts(ctx context.Context) ([]Contact, error) {
	return s.contacts(ctx, 0)
}

func (s *Store) contacts(ctx context.Context, limit int) ([]Contact, error) {
	query := `select jid,coalesce(peer_type,''),coalesce(phone,''),coalesce(full_name,''),coalesce(first_name,''),coalesce(last_name,''),coalesce(business_name,''),coalesce(username,''),coalesce(lid,''),coalesce(about_text,''),coalesce(avatar_path,''),coalesce(updated_at,0) from contacts order by jid`
	contacts, err := s.queryContacts(ctx, query, nil)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(contacts, func(i, j int) bool {
		left := contactSortKey(contacts[i])
		right := contactSortKey(contacts[j])
		if left == right {
			return contacts[i].JID < contacts[j].JID
		}
		return left < right
	})
	if limit > 0 && limit < len(contacts) {
		contacts = contacts[:limit]
	}
	return contacts, nil
}

func contactSortKey(contact Contact) string {
	for _, value := range []string{
		ContactDisplayName(contact),
		cleanPeerUsername(contact.Username),
		strings.TrimSpace(contact.Phone),
		strings.TrimSpace(contact.JID),
	} {
		if value != "" {
			return strings.ToLower(value)
		}
	}
	return ""
}

func (s *Store) queryContacts(ctx context.Context, query string, args []any) ([]Contact, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]Contact, 0)
	for rows.Next() {
		var c Contact
		var updatedAt int64
		if err := rows.Scan(&c.JID, &c.PeerType, &c.Phone, &c.FullName, &c.FirstName, &c.LastName, &c.BusinessName, &c.Username, &c.LID, &c.AboutText, &c.AvatarPath, &updatedAt); err != nil {
			return nil, err
		}
		c.UpdatedAt = fromUnix(updatedAt)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) allFolderChats(ctx context.Context) ([]FolderChat, error) {
	rows, err := s.db.QueryContext(ctx, `select folder_id,chat_jid,position from folder_chats order by folder_id, position, chat_jid`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]FolderChat, 0)
	for rows.Next() {
		var fc FolderChat
		if err := rows.Scan(&fc.FolderID, &fc.ChatJID, &fc.Position); err != nil {
			return nil, err
		}
		out = append(out, fc)
	}
	return out, rows.Err()
}

func (s *Store) allTopics(ctx context.Context) ([]Topic, error) {
	rows, err := s.db.QueryContext(ctx, `select chat_jid,topic_id,title,top_message_id,icon_color,icon_emoji_id,unread_count,unread_mentions_count,unread_reactions_count,pinned,closed,hidden,last_message_at from topics order by chat_jid, cast(topic_id as integer)`)
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

func (s *Store) allGroupParticipants(ctx context.Context) ([]GroupParticipant, error) {
	rows, err := s.db.QueryContext(ctx, `select group_jid,user_jid,coalesce(contact_name,''),coalesce(first_name,''),is_admin,is_active from group_participants order by group_jid, user_jid`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]GroupParticipant, 0)
	for rows.Next() {
		var p GroupParticipant
		var admin, active int
		if err := rows.Scan(&p.GroupJID, &p.UserJID, &p.ContactName, &p.FirstName, &admin, &active); err != nil {
			return nil, err
		}
		p.IsAdmin = admin != 0
		p.IsActive = active != 0
		out = append(out, p)
	}
	return out, rows.Err()
}
