package store

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/openclaw/crawlkit/whomatch"
)

const (
	ownerWhoDisplayName = "me"
	ownerWhoParticipant = "owner:me"
)

const participantRowsSQL = `
select p.jid, p.stored_name,
       coalesce(c.jid,''),coalesce(c.peer_type,''),coalesce(c.phone,''),coalesce(c.full_name,''),coalesce(c.first_name,''),coalesce(c.last_name,''),coalesce(c.business_name,''),coalesce(c.username,''),coalesce(c.lid,''),coalesce(c.about_text,''),coalesce(c.avatar_path,''),coalesce(c.updated_at,0)
from (
	select coalesce(cx.jid,'') as jid, '' as stored_name
	from contacts cx
	where coalesce(cx.peer_type,'') not in ('group','channel')
	  and coalesce(cx.jid,'') <> ''
	  and not exists (
		select 1
		from chats ch
		where cast(ch.id as text)=cx.jid
		  and ch.kind in ('group','channel')
	  )
	  and (
		coalesce(cx.phone,'') <> ''
		or coalesce(cx.full_name,'') <> ''
		or coalesce(cx.first_name,'') <> ''
		or coalesce(cx.last_name,'') <> ''
		or coalesce(cx.business_name,'') <> ''
		or coalesce(cx.username,'') <> ''
		or coalesce(cx.lid,'') <> ''
	  )
	union
	select coalesce(sender_jid,'') as jid, coalesce(sender_name,'') as stored_name
	from messages m
	where (coalesce(sender_jid,'') <> '' or coalesce(sender_name,'') <> '')
	  and not exists (
		select 1
		from chats ch
		where cast(ch.id as text)=m.sender_jid
		  and ch.kind in ('group','channel')
	  )
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

func (s *Store) ResolveWho(ctx context.Context, query string) ([]WhoCandidate, error) {
	query = normalizeDisplayName(query)
	if query == "" {
		return nil, nil
	}
	candidates, err := s.allWhoCandidates(ctx)
	if err != nil {
		return nil, err
	}
	if query == ownerWhoDisplayName {
		for _, candidate := range candidates {
			if candidateHasOwnerParticipant(candidate) {
				candidate.matchRank = int(whomatch.RankExact)
				return s.hydrateWhoCandidates(ctx, []WhoCandidate{candidate})
			}
		}
	}
	var matches []WhoCandidate
	for _, candidate := range candidates {
		rank, ok := whoMatchCandidate(candidate).MatchRank(query)
		if !ok {
			continue
		}
		candidate.matchRank = int(rank)
		matches = append(matches, candidate)
	}
	return s.hydrateWhoCandidates(ctx, matches)
}

func (s *Store) MatchParticipants(ctx context.Context, identity string) ([]ParticipantMatch, error) {
	identity = normalizeDisplayName(identity)
	if identity == "" {
		return nil, nil
	}
	candidates, err := s.ResolveWho(ctx, identity)
	if err != nil {
		return nil, err
	}
	if len(candidates) != 1 || candidates[0].MatchedOnlyByCloseSpelling() {
		return nil, nil
	}
	seen := map[string]ParticipantMatch{}
	for _, participant := range candidates[0].Participants {
		key := participantKey(participant.JID, participant.DisplayName)
		if _, ok := seen[key]; !ok {
			seen[key] = participant
		}
	}
	matches := make([]ParticipantMatch, 0, len(seen))
	for _, match := range seen {
		matches = append(matches, match)
	}
	sortParticipantMatches(matches)
	return matches, nil
}

func (s *Store) allWhoCandidates(ctx context.Context) ([]WhoCandidate, error) {
	ownerIDs, err := s.ownerIdentifierSet(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, participantRowsSQL)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	byKey := map[string]*WhoCandidate{}
	for rows.Next() {
		var jid, storedName string
		var contact Contact
		var updatedAt int64
		if err := rows.Scan(&jid, &storedName, &contact.JID, &contact.PeerType, &contact.Phone, &contact.FullName, &contact.FirstName, &contact.LastName, &contact.BusinessName, &contact.Username, &contact.LID, &contact.AboutText, &contact.AvatarPath, &updatedAt); err != nil {
			return nil, err
		}
		contact.UpdatedAt = fromUnix(updatedAt)
		names := participantDisplayNames(storedName, contact, jid)
		identifiers := participantIdentifiers(jid, contact)
		who := firstNonEmptyString(names...)
		var ownerMatchTerms []string
		owner := ownerIDs.Contains(jid, storedName, contact.JID)
		if owner {
			ownerMatchTerms = appendUniqueStrings(ownerMatchTerms, names...)
			ownerMatchTerms = appendUniqueStrings(ownerMatchTerms, identifiers...)
			ownerMatchTerms = appendIdentifier(ownerMatchTerms, storedName)
			ownerMatchTerms = appendIdentifier(ownerMatchTerms, contact.JID)
			ownerMatchTerms = appendIdentifier(ownerMatchTerms, jid)
			who = ownerWhoDisplayName
			names = []string{ownerWhoDisplayName}
			identifiers = []string{ownerWhoDisplayName}
		}
		if who == "" {
			who = firstNonEmptyString(identifiers...)
		}
		if who == "" {
			continue
		}
		key := participantKey(jid, who)
		if owner {
			key = ownerWhoParticipant
		}
		candidate := byKey[key]
		if candidate == nil {
			candidate = &WhoCandidate{Who: who}
			byKey[key] = candidate
		}
		candidate.Identifiers = appendUniqueStrings(candidate.Identifiers, identifiers...)
		candidate.aliases = appendUniqueStrings(candidate.aliases, names...)
		if owner {
			candidate.aliases = appendUniqueStrings(candidate.aliases, ownerMatchTerms...)
			addOwnerParticipantMatch(candidate)
		} else {
			addParticipantMatches(candidate, jid, who, names)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if ownerIDs.HasMessages() {
		candidate := byKey[ownerWhoParticipant]
		if candidate == nil {
			candidate = &WhoCandidate{Who: ownerWhoDisplayName}
			byKey[ownerWhoParticipant] = candidate
		}
		candidate.Identifiers = appendUniqueStrings(candidate.Identifiers, ownerWhoDisplayName)
		candidate.aliases = appendUniqueStrings(candidate.aliases, append([]string{ownerWhoDisplayName}, ownerIDs.Values()...)...)
		addOwnerParticipantMatch(candidate)
	}
	candidates := make([]WhoCandidate, 0, len(byKey))
	for _, candidate := range byKey {
		sortParticipantMatches(candidate.Participants)
		if candidateHasOwnerParticipant(*candidate) {
			candidate.Who = ownerWhoDisplayName
			candidates = append(candidates, *candidate)
			continue
		}
		// Rows for one jid merge in scan order, so an unnamed row can
		// have claimed Who with a bare identifier while a later row
		// carried the real name. A human name always wins.
		if whomatch.IsIdentifierLike(candidate.Who, candidate.Identifiers) {
			for _, alias := range candidate.aliases {
				if !whomatch.IsIdentifierLike(alias, candidate.Identifiers) {
					candidate.Who = alias
					break
				}
			}
		}
		candidates = append(candidates, *candidate)
	}
	return candidates, nil
}

type ownerIdentifierSet struct {
	values      map[string]struct{}
	hasMessages bool
}

func (s *Store) ownerIdentifierSet(ctx context.Context) (ownerIdentifierSet, error) {
	ids := ownerIdentifierSet{values: map[string]struct{}{}}
	var hasMessages int
	if err := s.db.QueryRowContext(ctx, `select exists(select 1 from messages where from_me = 1)`).Scan(&hasMessages); err != nil {
		return ids, err
	}
	ids.hasMessages = hasMessages != 0
	rows, err := s.db.QueryContext(ctx, `
select distinct trim(value)
from (
	select coalesce(sender_jid, '') as value from messages where from_me = 1
	union all
	select coalesce(sender_name, '') as value from messages where from_me = 1
)
where trim(value) <> ''`)
	if err != nil {
		return ids, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return ids, err
		}
		ids.values[strings.ToLower(strings.TrimSpace(value))] = struct{}{}
	}
	return ids, rows.Err()
}

func (ids ownerIdentifierSet) Contains(values ...string) bool {
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if _, ok := ids.values[value]; ok {
			return true
		}
	}
	return false
}

func (ids ownerIdentifierSet) HasMessages() bool {
	return ids.hasMessages
}

func (ids ownerIdentifierSet) Values() []string {
	out := make([]string, 0, len(ids.values))
	for value := range ids.values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func (s *Store) hydrateWhoCandidates(ctx context.Context, candidates []WhoCandidate) ([]WhoCandidate, error) {
	for i := range candidates {
		messages, lastSeen, err := s.whoCandidateStats(ctx, candidates[i])
		if err != nil {
			return nil, err
		}
		candidates[i].Messages = messages
		candidates[i].LastSeen = lastSeen
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].matchRank != candidates[j].matchRank {
			return candidates[i].matchRank > candidates[j].matchRank
		}
		if !candidates[i].LastSeen.Equal(candidates[j].LastSeen) {
			return candidates[i].LastSeen.After(candidates[j].LastSeen)
		}
		if candidates[i].Messages != candidates[j].Messages {
			return candidates[i].Messages > candidates[j].Messages
		}
		left := strings.ToLower(candidates[i].Who)
		right := strings.ToLower(candidates[j].Who)
		if left != right {
			return left < right
		}
		return candidates[i].Who < candidates[j].Who
	})
	return candidates, nil
}

func (candidate WhoCandidate) MatchedOnlyByCloseSpelling() bool {
	return candidate.matchRank == int(whomatch.RankCloseSpelling)
}

func whoMatchCandidate(candidate WhoCandidate) whomatch.Candidate {
	return whomatch.Candidate{
		Who:         candidate.Who,
		Identifiers: candidate.Identifiers,
		Aliases:     candidate.aliases,
		LastSeen:    candidate.LastSeen,
		Messages:    int64(candidate.Messages),
	}
}

func (s *Store) whoCandidateStats(ctx context.Context, candidate WhoCandidate) (int, time.Time, error) {
	filter := MessageFilter{
		Who:             candidate.Who,
		WhoParticipants: candidate.Participants,
		WhoResolved:     true,
	}
	query := `select count(*), coalesce(max(ts), 0) from messages where 1=1`
	args := []any{}
	query, args = appendWhoParticipantFilter(query, args, "", filter)
	var messages int
	var lastSeen int64
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&messages, &lastSeen); err != nil {
		return 0, time.Time{}, err
	}
	return messages, fromUnix(lastSeen), nil
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

func participantIdentifiers(jid string, contact Contact) []string {
	var identifiers []string
	identifiers = appendIdentifier(identifiers, contact.Phone)
	if username := strings.TrimSpace(strings.TrimPrefix(contact.Username, "@")); username != "" {
		identifiers = appendIdentifier(identifiers, "@"+username)
	}
	identifiers = appendIdentifier(identifiers, jid)
	identifiers = appendIdentifier(identifiers, contact.LID)
	return identifiers
}

func appendIdentifier(identifiers []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return identifiers
	}
	return appendUniqueStrings(identifiers, value)
}

func participantDisplayNames(storedName string, contact Contact, jid string) []string {
	refs := []string{jid, contact.JID, contact.Phone, contact.Username, contact.LID}
	var out []string
	addDisplayName(&out, ContactDisplayName(contact))
	addDisplayName(&out, cleanPeerName(contact.BusinessName, refs...))
	addDisplayName(&out, cleanPeerName(storedName, refs...))
	addDisplayName(&out, cleanPeerUsername(contact.Username))
	addDisplayName(&out, cleanPeerFirstName(contact.FirstName, contact))
	return out
}

func addDisplayName(out *[]string, value string) {
	value = normalizeDisplayName(value)
	if value == "" {
		return
	}
	*out = appendUniqueStrings(*out, value)
}

func addParticipantMatches(candidate *WhoCandidate, jid, fallback string, names []string) {
	if len(names) == 0 {
		names = []string{fallback}
	}
	for _, name := range names {
		name = normalizeDisplayName(name)
		if name == "" {
			continue
		}
		match := ParticipantMatch{JID: strings.TrimSpace(jid), DisplayName: name}
		key := participantKey(match.JID, match.DisplayName)
		duplicate := false
		for _, existing := range candidate.Participants {
			if participantKey(existing.JID, existing.DisplayName) == key {
				duplicate = true
				break
			}
		}
		if !duplicate {
			candidate.Participants = append(candidate.Participants, match)
		}
	}
}

func addOwnerParticipantMatch(candidate *WhoCandidate) {
	addParticipantMatches(candidate, ownerWhoParticipant, ownerWhoDisplayName, []string{ownerWhoDisplayName})
}

func candidateHasOwnerParticipant(candidate WhoCandidate) bool {
	for _, participant := range candidate.Participants {
		if participant.JID == ownerWhoParticipant {
			return true
		}
	}
	return false
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func appendUniqueStrings(out []string, values ...string) []string {
	seen := map[string]struct{}{}
	for _, value := range out {
		seen[strings.ToLower(value)] = struct{}{}
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func participantKey(jid, displayName string) string {
	jid = strings.TrimSpace(jid)
	if jid != "" {
		return "jid:" + jid
	}
	return "name:" + strings.ToLower(displayName)
}

func sortParticipantMatches(matches []ParticipantMatch) {
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
}

func appendWhoParticipantFilter(query string, args []any, prefix string, filter MessageFilter) (string, []any) {
	if normalizeDisplayName(filter.Who) == "" {
		return query, args
	}
	owner, ids, names := whoParticipantFilterValues(filter.WhoParticipants)
	if !owner && len(ids) == 0 && len(names) == 0 {
		return query + " and 0=1", args
	}
	var clauses []string
	if owner {
		clauses = append(clauses, prefix+"from_me = 1")
	}
	if len(ids) > 0 {
		placeholders := sqlPlaceholders(len(ids))
		clauses = append(clauses, prefix+"sender_jid in ("+placeholders+")")
		args = appendStringArgs(args, ids)
		clauses = append(clauses, prefix+"chat_jid in ("+placeholders+") and exists (select 1 from chats c where cast(c.id as text) = "+prefix+"chat_jid and c.kind = 'user')")
		args = appendStringArgs(args, ids)
		clauses = append(clauses, "exists (select 1 from group_participants gp where gp.group_jid = "+prefix+"chat_jid and gp.user_jid in ("+placeholders+"))")
		args = appendStringArgs(args, ids)
	}
	if len(names) > 0 {
		placeholders := sqlPlaceholders(len(names))
		clauses = append(clauses, "trim("+prefix+"sender_name) in ("+placeholders+")")
		args = appendStringArgs(args, names)
		clauses = append(clauses, "trim("+prefix+"chat_name) in ("+placeholders+") and exists (select 1 from chats c where cast(c.id as text) = "+prefix+"chat_jid and c.kind = 'user')")
		args = appendStringArgs(args, names)
		clauses = append(clauses, "exists (select 1 from group_participants gp where gp.group_jid = "+prefix+"chat_jid and trim(gp.contact_name) in ("+placeholders+"))")
		args = appendStringArgs(args, names)
		clauses = append(clauses, "exists (select 1 from group_participants gp where gp.group_jid = "+prefix+"chat_jid and trim(gp.first_name) in ("+placeholders+"))")
		args = appendStringArgs(args, names)
	}
	return query + " and (" + strings.Join(clauses, " or ") + ")", args
}

func whoParticipantFilterValues(matches []ParticipantMatch) (bool, []string, []string) {
	idSeen := map[string]struct{}{}
	nameSeen := map[string]struct{}{}
	var owner bool
	var ids []string
	var names []string
	for _, match := range matches {
		id := strings.TrimSpace(match.JID)
		if id == ownerWhoParticipant {
			owner = true
			continue
		}
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
	return owner, ids, names
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
