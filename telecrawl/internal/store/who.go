package store

import (
	"context"
	"sort"
	"strings"
)

const participantRowsSQL = `
select p.jid, p.stored_name,
       coalesce(c.jid,''),coalesce(c.peer_type,''),coalesce(c.phone,''),coalesce(c.full_name,''),coalesce(c.first_name,''),coalesce(c.last_name,''),coalesce(c.business_name,''),coalesce(c.username,''),coalesce(c.lid,''),coalesce(c.about_text,''),coalesce(c.avatar_path,''),coalesce(c.updated_at,0)
from (
	select coalesce(sender_jid,'') as jid, coalesce(sender_name,'') as stored_name
	from messages
	where coalesce(sender_jid,'') <> '' or coalesce(sender_name,'') <> ''
	union
	select coalesce(chat_jid,'') as jid, coalesce(chat_name,'') as stored_name
	from messages
	where coalesce(chat_jid,'') <> '' or coalesce(chat_name,'') <> ''
	union
	select cast(id as text) as jid, coalesce(name,'') as stored_name
	from chats
	union
	select coalesce(user_jid,'') as jid, coalesce(contact_name,'') as stored_name
	from group_participants
	where coalesce(user_jid,'') <> '' or coalesce(contact_name,'') <> ''
	union
	select coalesce(user_jid,'') as jid, coalesce(first_name,'') as stored_name
	from group_participants
	where coalesce(user_jid,'') <> '' or coalesce(first_name,'') <> ''
) p
left join contacts c on c.jid = p.jid`

func (s *Store) MatchParticipants(ctx context.Context, identity string) ([]ParticipantMatch, error) {
	identity = normalizeDisplayName(identity)
	if identity == "" {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, participantRowsSQL)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	seen := map[string]ParticipantMatch{}
	for rows.Next() {
		var jid, storedName string
		var contact Contact
		var updatedAt int64
		if err := rows.Scan(&jid, &storedName, &contact.JID, &contact.PeerType, &contact.Phone, &contact.FullName, &contact.FirstName, &contact.LastName, &contact.BusinessName, &contact.Username, &contact.LID, &contact.AboutText, &contact.AvatarPath, &updatedAt); err != nil {
			return nil, err
		}
		contact.UpdatedAt = fromUnix(updatedAt)
		for _, displayName := range participantDisplayNames(storedName, contact, jid) {
			if !strings.EqualFold(displayName, identity) {
				continue
			}
			key := participantKey(jid, displayName)
			if _, ok := seen[key]; !ok {
				seen[key] = ParticipantMatch{JID: strings.TrimSpace(jid), DisplayName: displayName}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	matches := make([]ParticipantMatch, 0, len(seen))
	for _, match := range seen {
		matches = append(matches, match)
	}
	sort.Slice(matches, func(i, j int) bool {
		left := strings.ToLower(matches[i].DisplayName)
		right := strings.ToLower(matches[j].DisplayName)
		if left != right {
			return left < right
		}
		if matches[i].DisplayName != matches[j].DisplayName {
			return matches[i].DisplayName < matches[j].DisplayName
		}
		return matches[i].JID < matches[j].JID
	})
	return matches, nil
}

func (s *Store) resolveWhoFilter(ctx context.Context, filter MessageFilter) (MessageFilter, error) {
	filter.Who = normalizeDisplayName(filter.Who)
	if filter.Who == "" || filter.WhoResolved {
		return filter, nil
	}
	matches, err := s.MatchParticipants(ctx, filter.Who)
	if err != nil {
		return filter, err
	}
	filter.WhoParticipants = matches
	filter.WhoResolved = true
	return filter, nil
}

func participantDisplayNames(storedName string, contact Contact, jid string) []string {
	refs := []string{jid, contact.JID, contact.Phone, contact.Username, contact.LID}
	var out []string
	seen := map[string]struct{}{}
	addDisplayName(&out, seen, cleanPeerName(storedName, refs...))
	addDisplayName(&out, seen, ContactDisplayName(contact))
	addDisplayName(&out, seen, cleanPeerName(contact.BusinessName, refs...))
	addDisplayName(&out, seen, cleanPeerUsername(contact.Username))
	addDisplayName(&out, seen, cleanPeerFirstName(contact.FirstName, contact))
	return out
}

func addDisplayName(out *[]string, seen map[string]struct{}, value string) {
	value = normalizeDisplayName(value)
	if value == "" {
		return
	}
	key := strings.ToLower(value)
	if _, ok := seen[key]; ok {
		return
	}
	seen[key] = struct{}{}
	*out = append(*out, value)
}

func participantKey(jid, displayName string) string {
	jid = strings.TrimSpace(jid)
	if jid != "" {
		return "jid:" + jid
	}
	return "name:" + strings.ToLower(displayName)
}

func appendWhoParticipantFilter(query string, args []any, prefix string, filter MessageFilter) (string, []any) {
	if normalizeDisplayName(filter.Who) == "" {
		return query, args
	}
	ids, names := whoParticipantFilterValues(filter.WhoParticipants)
	if len(ids) == 0 && len(names) == 0 {
		return query + " and 0=1", args
	}
	var clauses []string
	if len(ids) > 0 {
		placeholders := sqlPlaceholders(len(ids))
		clauses = append(clauses, prefix+"sender_jid in ("+placeholders+")")
		args = appendStringArgs(args, ids)
		clauses = append(clauses, prefix+"chat_jid in ("+placeholders+")")
		args = appendStringArgs(args, ids)
		clauses = append(clauses, "exists (select 1 from group_participants gp where gp.group_jid = "+prefix+"chat_jid and gp.user_jid in ("+placeholders+"))")
		args = appendStringArgs(args, ids)
	}
	if len(names) > 0 {
		placeholders := sqlPlaceholders(len(names))
		clauses = append(clauses, "trim("+prefix+"sender_name) in ("+placeholders+")")
		args = appendStringArgs(args, names)
		clauses = append(clauses, "trim("+prefix+"chat_name) in ("+placeholders+")")
		args = appendStringArgs(args, names)
		clauses = append(clauses, "exists (select 1 from group_participants gp where gp.group_jid = "+prefix+"chat_jid and trim(gp.contact_name) in ("+placeholders+"))")
		args = appendStringArgs(args, names)
		clauses = append(clauses, "exists (select 1 from group_participants gp where gp.group_jid = "+prefix+"chat_jid and trim(gp.first_name) in ("+placeholders+"))")
		args = appendStringArgs(args, names)
	}
	return query + " and (" + strings.Join(clauses, " or ") + ")", args
}

func whoParticipantFilterValues(matches []ParticipantMatch) ([]string, []string) {
	idSeen := map[string]struct{}{}
	nameSeen := map[string]struct{}{}
	var ids []string
	var names []string
	for _, match := range matches {
		id := strings.TrimSpace(match.JID)
		if id != "" {
			if _, ok := idSeen[id]; !ok {
				idSeen[id] = struct{}{}
				ids = append(ids, id)
			}
		}
		name := normalizeDisplayName(match.DisplayName)
		if name != "" {
			key := strings.ToLower(name)
			if _, ok := nameSeen[key]; !ok {
				nameSeen[key] = struct{}{}
				names = append(names, name)
			}
		}
	}
	sort.Strings(ids)
	sort.Strings(names)
	return ids, names
}

func sqlPlaceholders(count int) string {
	if count <= 0 {
		return ""
	}
	return strings.TrimRight(strings.Repeat("?,", count), ",")
}

func appendStringArgs(args []any, values []string) []any {
	for _, value := range values {
		args = append(args, value)
	}
	return args
}

func normalizeDisplayName(value string) string {
	return strings.Join(strings.Fields(value), " ")
}
