package beeperstore

import (
	"context"
	"sort"
	"strings"
	"time"

	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	"github.com/opentrawl/opentrawl/trawlkit/whomatch"
)

type rawBeeperPersonAggregate struct {
	userID       string
	displayName  string
	usernames    []string
	phoneNumbers []string
	emails       []string
	networks     []string
	messageCount int
	lastSeen     time.Time
}

// ResolveWho matches a human query against archived Beeper participants and senders.
func (s *BeeperArchiveStore) ResolveWho(ctx context.Context, query string) (BeeperWhoResolution, error) {
	allPeople, err := s.loadAllObservedPeople(ctx)
	if err != nil {
		return BeeperWhoResolution{}, err
	}
	return matchPeopleWithWhomatch(allPeople, query), nil
}

// PeopleSnapshotCandidates returns all observed non-self Beeper people for
// OpenTrawl's cross-source People reconciliation.
func (s *BeeperArchiveStore) PeopleSnapshotCandidates(ctx context.Context) ([]BeeperWhoCandidate, error) {
	allPeople, err := s.loadAllObservedPeople(ctx)
	if err != nil {
		return nil, err
	}
	sort.Slice(allPeople, func(i, j int) bool {
		if allPeople[i].MessageCount != allPeople[j].MessageCount {
			return allPeople[i].MessageCount > allPeople[j].MessageCount
		}
		if !allPeople[i].LastSeen.Equal(allPeople[j].LastSeen) {
			return allPeople[i].LastSeen.After(allPeople[j].LastSeen)
		}
		return strings.ToLower(allPeople[i].DisplayName) < strings.ToLower(allPeople[j].DisplayName)
	})
	return allPeople, nil
}

// CountPersonMessages counts archived Beeper messages involving any of the
// supplied exact person filter identifiers.
func (s *BeeperArchiveStore) CountPersonMessages(ctx context.Context, identifiers []*person.ExactPersonFilterIdentifier) (uint64, error) {
	var exactIDs []string
	for _, ident := range identifiers {
		if value := strings.TrimSpace(ident.GetExactPersonFilterIdentifier()); value != "" {
			exactIDs = append(exactIDs, value)
		}
	}
	exactIDs = uniqueLowerStrings(exactIDs)
	if len(exactIDs) == 0 {
		return 0, nil
	}
	var clauses []string
	var args []any
	for _, id := range exactIDs {
		clauses = append(clauses, `(
			lower(m.sender_id) = lower(?) or
			exists (
				select 1 from beeper_chat_participants p
				where p.chat_id = m.chat_id
				  and (lower(p.user_id) = lower(?) or lower(p.username) = lower(?) or lower(p.phone_number) = lower(?) or lower(p.email) = lower(?))
			)
		)`)
		args = append(args, id, id, id, id, id)
	}
	var count int64
	query := "select count(*) from beeper_messages m where " + strings.Join(clauses, " or ")
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, err
	}
	if count < 0 {
		return 0, nil
	}
	return uint64(count), nil
}

func (s *BeeperArchiveStore) loadAllObservedPeople(ctx context.Context) ([]BeeperWhoCandidate, error) {
	byUser := make(map[string]*rawBeeperPersonAggregate)

	partRows, err := s.db.QueryContext(ctx, `
select
	p.user_id,
	p.full_name,
	p.username,
	p.phone_number,
	p.email,
	coalesce(c.network, ''),
	coalesce((select count(*) from beeper_messages m where m.chat_id = p.chat_id), 0),
	coalesce((select max(m.timestamp_unix) from beeper_messages m where m.chat_id = p.chat_id), c.last_activity_unix)
from beeper_chat_participants p
left join beeper_chats c on c.chat_id = p.chat_id
where p.is_self = 0 and trim(p.user_id) <> ''
`)
	if err != nil {
		if isMissingTableError(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = partRows.Close() }()

	for partRows.Next() {
		var userID, fullName, username, phone, email, network string
		var msgCount int
		var lastSeenUnix int64
		if err := partRows.Scan(&userID, &fullName, &username, &phone, &email, &network, &msgCount, &lastSeenUnix); err != nil {
			return nil, err
		}
		key := strings.ToLower(strings.TrimSpace(userID))
		agg := byUser[key]
		if agg == nil {
			agg = &rawBeeperPersonAggregate{userID: strings.TrimSpace(userID)}
			byUser[key] = agg
		}
		if agg.displayName == "" && strings.TrimSpace(fullName) != "" {
			agg.displayName = strings.TrimSpace(fullName)
		}
		if trimmed := strings.TrimSpace(username); trimmed != "" {
			agg.usernames = append(agg.usernames, trimmed)
		}
		if trimmed := strings.TrimSpace(phone); trimmed != "" {
			agg.phoneNumbers = append(agg.phoneNumbers, trimmed)
		}
		if trimmed := strings.TrimSpace(email); trimmed != "" {
			agg.emails = append(agg.emails, trimmed)
		}
		if trimmed := strings.TrimSpace(network); trimmed != "" {
			agg.networks = append(agg.networks, trimmed)
		}
		agg.messageCount += msgCount
		if ts := timeFromUnix(lastSeenUnix); ts.After(agg.lastSeen) {
			agg.lastSeen = ts
		}
	}
	if err := partRows.Err(); err != nil {
		return nil, err
	}

	// Include senders observed on messages who were not present in participant lists.
	senderRows, err := s.db.QueryContext(ctx, `
select
	m.sender_id,
	max(m.sender_name),
	coalesce(max(nullif(m.network, '')), ''),
	count(*),
	max(m.timestamp_unix)
from beeper_messages m
where m.is_sender = 0 and trim(m.sender_id) <> ''
group by m.sender_id
`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = senderRows.Close() }()

	for senderRows.Next() {
		var senderID, senderName, network string
		var msgCount int
		var lastSeenUnix int64
		if err := senderRows.Scan(&senderID, &senderName, &network, &msgCount, &lastSeenUnix); err != nil {
			return nil, err
		}
		key := strings.ToLower(strings.TrimSpace(senderID))
		agg := byUser[key]
		if agg == nil {
			agg = &rawBeeperPersonAggregate{
				userID:       strings.TrimSpace(senderID),
				displayName:  strings.TrimSpace(senderName),
				messageCount: msgCount,
				lastSeen:     timeFromUnix(lastSeenUnix),
			}
			if trimmed := strings.TrimSpace(network); trimmed != "" {
				agg.networks = append(agg.networks, trimmed)
			}
			byUser[key] = agg
			continue
		}
		if agg.displayName == "" && strings.TrimSpace(senderName) != "" {
			agg.displayName = strings.TrimSpace(senderName)
		}
		if msgCount > agg.messageCount {
			agg.messageCount = msgCount
		}
		if ts := timeFromUnix(lastSeenUnix); ts.After(agg.lastSeen) {
			agg.lastSeen = ts
		}
	}
	if err := senderRows.Err(); err != nil {
		return nil, err
	}

	out := make([]BeeperWhoCandidate, 0, len(byUser))
	for _, agg := range byUser {
		usernames := uniqueLowerStrings(agg.usernames)
		phones := uniqueLowerStrings(agg.phoneNumbers)
		emails := uniqueLowerStrings(agg.emails)
		networks := uniqueLowerStrings(agg.networks)

		display := agg.displayName
		if display == "" && len(usernames) > 0 {
			display = usernames[0]
		}
		if display == "" && len(phones) > 0 {
			display = phones[0]
		}
		if display == "" && len(emails) > 0 {
			display = emails[0]
		}
		if display == "" {
			display = agg.userID
		}

		var identifiers []string
		identifiers = append(identifiers, agg.userID)
		identifiers = append(identifiers, usernames...)
		identifiers = append(identifiers, phones...)
		identifiers = append(identifiers, emails...)
		identifiers = uniqueLowerStrings(identifiers)

		out = append(out, BeeperWhoCandidate{
			DisplayName:  display,
			Identifiers:  identifiers,
			UserIDs:      []string{agg.userID},
			Networks:     networks,
			PhoneNumbers: phones,
			Emails:       emails,
			LastSeen:     agg.lastSeen,
			MessageCount: agg.messageCount,
		})
	}
	return out, nil
}

func matchPeopleWithWhomatch(allPeople []BeeperWhoCandidate, query string) BeeperWhoResolution {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" || len(allPeople) == 0 {
		return BeeperWhoResolution{}
	}

	var bestRank whomatch.Rank
	var matched []BeeperWhoCandidate
	for _, personCandidate := range allPeople {
		aliases := make([]string, 0, 1+len(personCandidate.Identifiers))
		aliases = append(aliases, personCandidate.DisplayName)
		aliases = append(aliases, personCandidate.Identifiers...)
		rank, ok := whomatch.MatchRank(trimmed, aliases)
		if !ok {
			continue
		}
		personCandidate.MatchRank = rank
		if rank.BetterThan(bestRank) {
			bestRank = rank
		}
		matched = append(matched, personCandidate)
	}
	if len(matched) == 0 {
		return BeeperWhoResolution{}
	}

	// Keep candidates at the highest matched rank tier.
	filtered := make([]BeeperWhoCandidate, 0, len(matched))
	for _, candidate := range matched {
		if candidate.MatchRank == bestRank {
			filtered = append(filtered, candidate)
		}
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].MessageCount != filtered[j].MessageCount {
			return filtered[i].MessageCount > filtered[j].MessageCount
		}
		if !filtered[i].LastSeen.Equal(filtered[j].LastSeen) {
			return filtered[i].LastSeen.After(filtered[j].LastSeen)
		}
		return strings.ToLower(filtered[i].DisplayName) < strings.ToLower(filtered[j].DisplayName)
	})
	return BeeperWhoResolution{
		Candidates: filtered,
		MatchRank:  bestRank,
	}
}
