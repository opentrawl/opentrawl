package store

import (
	"context"
	"sort"
	"strings"
)

func (s *Store) ListContacts(ctx context.Context, limit int) ([]Contact, error) {
	if limit <= 0 {
		limit = -1 // --all: no row cap (SQLite LIMIT -1)
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
