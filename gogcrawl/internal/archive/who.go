package archive

import (
	"context"
	"sort"
	"strings"
)

type searchWhoMatch struct {
	enabled         bool
	participantKeys []string
	displayNames    []string
}

type searchWhoCandidate struct {
	Key         string
	DisplayName string
	Name        string
	Address     string
}

func (s *Store) searchWhoMatched(ctx context.Context, who string) (searchWhoMatch, error) {
	who = normalizeWho(who)
	if who == "" {
		return searchWhoMatch{}, nil
	}
	if _, _, err := s.EnsureParticipants(ctx); err != nil {
		return searchWhoMatch{}, err
	}
	ownerEmails, err := s.OwnerEmails(ctx)
	if err != nil {
		return searchWhoMatch{}, err
	}
	candidates, err := s.whoCandidates(ctx)
	if err != nil {
		return searchWhoMatch{}, err
	}
	return resolveSearchWho(who, candidates, ownerEmails), nil
}

func (s *Store) whoCandidates(ctx context.Context) ([]searchWhoCandidate, error) {
	rows, err := s.store.DB().QueryContext(ctx, `
select participant_key, display_name, name, address
from (
  select distinct participant_key, display_name, name, address
  from message_participants
)
order by display_name, participant_key, name, address
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []searchWhoCandidate
	for rows.Next() {
		var candidate searchWhoCandidate
		if err := rows.Scan(&candidate.Key, &candidate.DisplayName, &candidate.Name, &candidate.Address); err != nil {
			return nil, err
		}
		candidate.DisplayName = normalizeWho(candidate.DisplayName)
		candidate.Name = normalizeWho(candidate.Name)
		candidate.Address = strings.TrimSpace(candidate.Address)
		out = append(out, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func resolveSearchWho(who string, candidates []searchWhoCandidate, ownerEmails map[string]struct{}) searchWhoMatch {
	out := searchWhoMatch{enabled: true}
	seenParticipants := map[string]struct{}{}
	seenKeys := map[string]struct{}{}
	for _, candidate := range candidates {
		displayName := candidate.DisplayName
		participantID := candidate.Key
		if isOwnerEmail(candidate.Address, ownerEmails) {
			displayName = "me"
			participantID = "owner:me"
		}
		if displayName == "" {
			displayName = normalizeWho(candidate.Address)
		}
		if !matchesSearchWho(who, displayName, candidate.Name, candidate.Address) {
			continue
		}
		if _, seen := seenKeys[candidate.Key]; !seen {
			seenKeys[candidate.Key] = struct{}{}
			out.participantKeys = append(out.participantKeys, candidate.Key)
		}
		if _, seen := seenParticipants[participantID]; seen {
			continue
		}
		seenParticipants[participantID] = struct{}{}
		out.displayNames = append(out.displayNames, displayName)
	}
	sort.Strings(out.participantKeys)
	sort.SliceStable(out.displayNames, func(i, j int) bool {
		left := strings.ToLower(out.displayNames[i])
		right := strings.ToLower(out.displayNames[j])
		if left != right {
			return left < right
		}
		return out.displayNames[i] < out.displayNames[j]
	})
	return out
}

func matchesSearchWho(who string, values ...string) bool {
	seen := map[string]struct{}{}
	for _, value := range values {
		value = normalizeWho(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		if strings.EqualFold(value, who) {
			return true
		}
	}
	return false
}

func ambiguousWhoMatches(names []string) []string {
	if len(names) <= 1 {
		return nil
	}
	return names
}

func normalizeWho(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func isOwnerEmail(address string, ownerEmails map[string]struct{}) bool {
	_, ok := ownerEmails[normalizeEmail(address)]
	return ok
}
