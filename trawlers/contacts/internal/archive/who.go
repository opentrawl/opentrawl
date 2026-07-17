package archive

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
	"github.com/opentrawl/opentrawl/trawlkit/whomatch"
)

type WhoCandidate struct {
	Who          string
	Identifiers  []string
	Aliases      []string
	Sources      []string
	LastSeen     time.Time
	MatchQuality string

	matchRank whomatch.Rank
}

func (s *Store) ResolvePeople(ctx context.Context, query string) ([]WhoCandidate, error) {
	query = strings.Join(strings.Fields(query), " ")
	if query == "" {
		return nil, nil
	}
	people, err := s.People(ctx)
	if err != nil {
		return nil, err
	}
	candidates := make([]WhoCandidate, 0)
	for _, person := range people {
		candidate, ok := resolvePersonCandidate(person, query)
		if ok {
			candidates = append(candidates, candidate)
		}
	}
	sortWhoCandidates(candidates)
	return candidates, nil
}

func resolvePersonCandidate(person model.Person, query string) (WhoCandidate, bool) {
	matchCandidate := resolverMatchCandidate(person)
	rank, ok := matchCandidate.MatchRank(query)
	if !ok {
		return WhoCandidate{}, false
	}
	return WhoCandidate{
		Who:          person.Name,
		Identifiers:  matchCandidate.Identifiers,
		Aliases:      resolverIdentityAliases(person),
		Sources:      resolverSources(person),
		LastSeen:     resolverLastSeen(person),
		MatchQuality: rank.String(),
		matchRank:    rank,
	}, true
}

// resolverIdentityAliases is deliberately narrower than the aliases used to
// find a Person. Search conveniences such as a Person ID, slug or tag may help
// `who` locate a record, but they are not evidence that a chat participant is
// that person. Cross-service chat matching gets only real names and account
// handles observed on the reconciled identity.
func resolverIdentityAliases(person model.Person) []string {
	aliases := []string{person.SortName}
	aliases = append(aliases, person.AKA...)
	for _, source := range person.Sources {
		aliases = append(aliases, source.Names...)
	}
	for _, key := range personIdentifierKeys(person) {
		if key.kind == "handle" {
			if _, handle, ok := strings.Cut(key.value, ":"); ok {
				aliases = append(aliases, handle)
			}
		}
	}
	return cleanSortedStrings(aliases)
}

func resolverMatchCandidate(person model.Person) whomatch.Candidate {
	slug := model.Slug(person.Name)
	aliases := []string{person.ID, person.SortName, slug, strings.ReplaceAll(slug, "-", " ")}
	aliases = append(aliases, person.AKA...)
	aliases = append(aliases, person.Tags...)
	for _, source := range person.Sources {
		aliases = append(aliases, source.Names...)
	}
	for _, key := range personIdentifierKeys(person) {
		if key.kind == "handle" {
			if _, handle, ok := strings.Cut(key.value, ":"); ok {
				aliases = append(aliases, handle)
			}
		}
	}
	return whomatch.Candidate{
		Who:         person.Name,
		Identifiers: resolverIdentifiers(person),
		Aliases:     cleanSortedStrings(aliases),
	}
}

func resolverIdentifiers(person model.Person) []string {
	keys := personIdentifierKeys(person)
	values := make([]string, 0, len(keys))
	for _, key := range keys {
		values = append(values, strings.TrimSpace(key.value))
	}
	values = cleanSortedStrings(values)
	if len(values) == 0 {
		values = []string{person.ID}
	}
	return values
}

func resolverSources(person model.Person) []string {
	values := make([]string, 0, len(person.Sources))
	for source := range person.Sources {
		values = append(values, source)
	}
	return cleanSortedStrings(values)
}

func resolverLastSeen(person model.Person) time.Time {
	var latest time.Time
	for _, source := range person.Sources {
		if source.LastSeenAt.IsZero() {
			continue
		}
		if latest.IsZero() || source.LastSeenAt.After(latest) {
			latest = source.LastSeenAt
		}
	}
	return latest.UTC()
}

func sortWhoCandidates(candidates []WhoCandidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		left := candidates[i]
		right := candidates[j]
		if left.matchRank != right.matchRank {
			return left.matchRank.BetterThan(right.matchRank)
		}
		if left.LastSeen.IsZero() != right.LastSeen.IsZero() {
			return !left.LastSeen.IsZero()
		}
		if !left.LastSeen.Equal(right.LastSeen) {
			return left.LastSeen.After(right.LastSeen)
		}
		return strings.ToLower(left.Who) < strings.ToLower(right.Who)
	})
}

func cleanSortedStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := whomatch.Normalize(value)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool {
		return whomatch.Normalize(out[i]) < whomatch.Normalize(out[j])
	})
	return out
}
