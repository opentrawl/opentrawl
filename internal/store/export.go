package store

import (
	"context"
	"fmt"
	"time"
)

type SnapshotData struct {
	Contacts     []Contact
	Chats        []Chat
	Groups       []Group
	Participants []GroupParticipant
	Messages     []Message
}

func (d SnapshotData) ImportStats(sourcePath, dbPath string, finishedAt time.Time) ImportStats {
	if finishedAt.IsZero() {
		finishedAt = time.Now().UTC()
	}
	mediaMessages := 0
	for _, message := range d.Messages {
		if message.MediaType != "" || message.MediaPath != "" || message.MediaURL != "" {
			mediaMessages++
		}
	}
	return ImportStats{
		SourcePath:    sourcePath,
		DBPath:        dbPath,
		Chats:         len(d.Chats),
		Contacts:      len(d.Contacts),
		Groups:        len(d.Groups),
		Participants:  len(d.Participants),
		Messages:      len(d.Messages),
		MediaMessages: mediaMessages,
		StartedAt:     finishedAt,
		FinishedAt:    finishedAt,
	}
}

func (s *Store) ExportAll(ctx context.Context) (SnapshotData, error) {
	contacts, err := s.exportContacts(ctx)
	if err != nil {
		return SnapshotData{}, err
	}
	chats, err := s.exportChats(ctx)
	if err != nil {
		return SnapshotData{}, err
	}
	groups, err := s.exportGroups(ctx)
	if err != nil {
		return SnapshotData{}, err
	}
	participants, err := s.exportParticipants(ctx)
	if err != nil {
		return SnapshotData{}, err
	}
	messages, err := s.Messages(ctx, MessageFilter{Limit: int(^uint(0) >> 1), Asc: true})
	if err != nil {
		return SnapshotData{}, err
	}
	return SnapshotData{Contacts: contacts, Chats: chats, Groups: groups, Participants: participants, Messages: messages}, nil
}

func (s *Store) ImportSnapshot(ctx context.Context, data SnapshotData, sourcePath string, finishedAt time.Time) error {
	return s.ReplaceAll(ctx, data.ImportStats(sourcePath, s.Path(), finishedAt), data.Contacts, data.Chats, data.Groups, data.Participants, data.Messages)
}

func (s *Store) exportContacts(ctx context.Context) ([]Contact, error) {
	rows, err := s.db.QueryContext(ctx, `select jid, phone, full_name, first_name, last_name, business_name, username, lid, about_text, updated_at from contacts order by jid`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Contact
	for rows.Next() {
		var c Contact
		var updatedAt int64
		if err := rows.Scan(&c.JID, &c.Phone, &c.FullName, &c.FirstName, &c.LastName, &c.BusinessName, &c.Username, &c.LID, &c.AboutText, &updatedAt); err != nil {
			return nil, err
		}
		c.UpdatedAt = fromUnix(updatedAt)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) exportChats(ctx context.Context) ([]Chat, error) {
	rows, err := s.db.QueryContext(ctx, `select jid, kind, name, last_message_at, unread_count, archived, removed, hidden, raw_session_type from chats order by jid`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Chat
	for rows.Next() {
		var c Chat
		var lastMessageAt int64
		var archived, removed, hidden int
		if err := rows.Scan(&c.JID, &c.Kind, &c.Name, &lastMessageAt, &c.UnreadCount, &archived, &removed, &hidden, &c.RawSessionType); err != nil {
			return nil, err
		}
		c.LastMessageAt = fromUnix(lastMessageAt)
		c.Archived = archived != 0
		c.Removed = removed != 0
		c.Hidden = hidden != 0
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) exportGroups(ctx context.Context) ([]Group, error) {
	rows, err := s.db.QueryContext(ctx, `select jid, name, owner_jid, created_at from groups order by jid`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Group
	for rows.Next() {
		var g Group
		var createdAt int64
		if err := rows.Scan(&g.JID, &g.Name, &g.OwnerJID, &createdAt); err != nil {
			return nil, err
		}
		g.CreatedAt = fromUnix(createdAt)
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) exportParticipants(ctx context.Context) ([]GroupParticipant, error) {
	rows, err := s.db.QueryContext(ctx, `select group_jid, user_jid, contact_name, first_name, is_admin, is_active from group_participants order by group_jid, user_jid`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []GroupParticipant
	for rows.Next() {
		var p GroupParticipant
		var isAdmin, isActive int
		if err := rows.Scan(&p.GroupJID, &p.UserJID, &p.ContactName, &p.FirstName, &isAdmin, &isActive); err != nil {
			return nil, err
		}
		p.IsAdmin = isAdmin != 0
		p.IsActive = isActive != 0
		out = append(out, p)
	}
	return out, rows.Err()
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
