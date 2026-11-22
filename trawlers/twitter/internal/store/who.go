package store

import (
	"context"
	"strings"
	"time"
)

type TwitterWhoCandidate struct {
	AuthorID    string
	Handle      string
	DisplayName string
	Identifiers []string
	Messages    int
	LastSeen    time.Time
}

func (s *Store) ResolveTwitterWho(ctx context.Context, query string) ([]TwitterWhoCandidate, error) {
	normalized := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(query), "@"))
	if normalized == "" {
		return nil, nil
	}
	like := "%" + normalized + "%"
	rows, err := s.db.QueryContext(ctx, `
select p.author_id, coalesce(p.handle, ''), coalesce(p.display_name, ''),
(
	select count(*) from dm_messages m
	join dm_conversations c on c.id = m.conversation_id
	where m.sender_author_id = p.author_id or c.participant_author_id = p.author_id
) as dm_count,
coalesce((
	select max(m.created_at) from dm_messages m
	join dm_conversations c on c.id = m.conversation_id
	where m.sender_author_id = p.author_id or c.participant_author_id = p.author_id
), p.last_seen_at) as last_seen
from profiles p
where lower(coalesce(p.handle, '')) like ?
   or lower(coalesce(p.display_name, '')) like ?
   or p.author_id = ?
order by dm_count desc, last_seen desc, p.author_id asc
limit 25`, like, like, strings.TrimSpace(query))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []TwitterWhoCandidate
	for rows.Next() {
		var c TwitterWhoCandidate
		var lastSeen string
		if err := rows.Scan(&c.AuthorID, &c.Handle, &c.DisplayName, &c.Messages, &lastSeen); err != nil {
			return nil, err
		}
		c.LastSeen = parseStoredTime(lastSeen)
		c.Identifiers = twitterPersonIdentifiers(c.AuthorID, c.Handle)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) ExportTwitterPeopleSnapshot(ctx context.Context) ([]TwitterWhoCandidate, error) {
	ownerAuthorID, err := s.OwnerAuthorID(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
select p.author_id, coalesce(p.handle, ''), coalesce(p.display_name, ''),
count(distinct m.id) as dm_count,
max(m.created_at) as last_seen
from profiles p
join dm_conversations c on c.participant_author_id = p.author_id
join dm_messages m on m.conversation_id = c.id
where p.author_id <> ?
group by p.author_id, p.handle, p.display_name
order by dm_count desc, last_seen desc, p.author_id asc`, ownerAuthorID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []TwitterWhoCandidate
	for rows.Next() {
		var c TwitterWhoCandidate
		var lastSeen string
		if err := rows.Scan(&c.AuthorID, &c.Handle, &c.DisplayName, &c.Messages, &lastSeen); err != nil {
			return nil, err
		}
		c.LastSeen = parseStoredTime(lastSeen)
		c.Identifiers = twitterPersonIdentifiers(c.AuthorID, c.Handle)
		out = append(out, c)
	}
	return out, rows.Err()
}

func twitterPersonIdentifiers(authorID, handle string) []string {
	var ids []string
	if authorID = strings.TrimSpace(authorID); authorID != "" {
		ids = append(ids, authorID)
	}
	if handle = strings.TrimPrefix(strings.TrimSpace(handle), "@"); handle != "" {
		ids = append(ids, "@"+strings.ToLower(handle))
	}
	return ids
}
