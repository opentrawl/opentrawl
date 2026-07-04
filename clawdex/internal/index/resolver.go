package index

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/openclaw/clawdex/internal/model"
	"github.com/openclaw/crawlkit/whomatch"
)

type WhoCandidate struct {
	Who          string
	Identifiers  []string
	Addresses    []model.ContactValue
	Sources      []string
	LastSeen     string
	MatchQuality string

	lastSeenAt time.Time
	lastSeenOK bool
	matchRank  whomatch.Rank
}

const (
	WhoErrorAmbiguous = "ambiguous_who"
	WhoErrorUnknown   = "unknown_who"
)

type WhoResolutionError struct {
	Code       string
	Query      string
	Candidates []WhoCandidate
	DidYouMean []WhoCandidate
}

func (e *WhoResolutionError) Error() string {
	switch e.Code {
	case WhoErrorAmbiguous:
		return fmt.Sprintf("ambiguous_who: %q matched %d people", e.Query, len(e.Candidates))
	case WhoErrorUnknown:
		return fmt.Sprintf("unknown_who: %q", e.Query)
	default:
		return fmt.Sprintf("who resolution failed: %q", e.Query)
	}
}

func (s Store) ResolvePeople(query string) ([]WhoCandidate, error) {
	query = strings.Join(strings.Fields(query), " ")
	if query == "" {
		return nil, errors.New("person query is required")
	}
	if _, _, err := s.ensureIndex(); err != nil {
		return nil, err
	}
	people, err := s.readPeople()
	if err != nil {
		return nil, err
	}
	indexed, err := s.indexedIdentifiersByPerson()
	if err != nil {
		return nil, err
	}
	candidates := make([]WhoCandidate, 0)
	for _, person := range people {
		candidate, ok := resolvePersonCandidate(person, indexed[person.ID], query)
		if ok {
			candidates = append(candidates, candidate)
		}
	}
	sortWhoCandidates(candidates)
	return candidates, nil
}

func (s Store) ResolveWho(query string) (WhoCandidate, error) {
	candidates, err := s.ResolvePeople(query)
	if err != nil {
		return WhoCandidate{}, err
	}
	if len(candidates) == 0 {
		return WhoCandidate{}, &WhoResolutionError{Code: WhoErrorUnknown, Query: query}
	}

	bestRank := candidates[0].matchRank
	best := make([]WhoCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.matchRank == bestRank {
			best = append(best, candidate)
		}
	}
	if bestRank == whomatch.RankCloseSpelling {
		return WhoCandidate{}, &WhoResolutionError{Code: WhoErrorUnknown, Query: query, DidYouMean: best}
	}
	if len(best) == 1 {
		return best[0], nil
	}
	return WhoCandidate{}, &WhoResolutionError{Code: WhoErrorAmbiguous, Query: query, Candidates: best}
}

func (s Store) indexedIdentifiersByPerson() (map[string][]identifierKey, error) {
	db, err := sql.Open("sqlite3", s.indexPath())
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	rows, err := db.Query(`select person_id, kind, value from identifiers order by person_id, kind, value`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string][]identifierKey{}
	for rows.Next() {
		var personID, kind, value string
		if err := rows.Scan(&personID, &kind, &value); err != nil {
			return nil, err
		}
		out[personID] = append(out[personID], identifierKey{kind: kind, value: value})
	}
	return out, rows.Err()
}

func resolvePersonCandidate(person model.Person, indexed []identifierKey, query string) (WhoCandidate, bool) {
	matchCandidate := resolverMatchCandidate(person, indexed)
	rank, ok := matchCandidate.MatchRank(query)
	if !ok {
		return WhoCandidate{}, false
	}
	lastSeen, lastSeenAt, lastSeenOK := resolverLastSeen(person)
	return WhoCandidate{
		Who:          person.Name,
		Identifiers:  matchCandidate.Identifiers,
		Addresses:    append([]model.ContactValue(nil), person.Addresses...),
		Sources:      resolverSources(person),
		LastSeen:     lastSeen,
		MatchQuality: rank.String(),
		lastSeenAt:   lastSeenAt,
		lastSeenOK:   lastSeenOK,
		matchRank:    rank,
	}, true
}

func resolverMatchCandidate(person model.Person, indexed []identifierKey) whomatch.Candidate {
	slug := personPathSlug(person)
	aliases := []string{person.ID, person.SortName, slug, strings.ReplaceAll(slug, "-", " ")}
	aliases = append(aliases, person.AKA...)
	aliases = append(aliases, person.Tags...)
	for _, source := range person.Sources {
		aliases = append(aliases, source.Names...)
	}
	for _, key := range resolverIdentifierKeys(person, indexed) {
		if key.kind == "handle" {
			if _, handle, ok := strings.Cut(key.value, ":"); ok {
				aliases = append(aliases, handle)
			}
		}
	}
	return whomatch.Candidate{
		Who:         person.Name,
		Identifiers: resolverIdentifiers(person, indexed),
		Aliases:     cleanSortedStrings(aliases),
	}
}

func resolverIdentifiers(person model.Person, indexed []identifierKey) []string {
	keys := resolverIdentifierKeys(person, indexed)
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

func resolverIdentifierKeys(person model.Person, indexed []identifierKey) []identifierKey {
	keys := append([]identifierKey{}, personIdentifierKeys(person)...)
	keys = append(keys, indexed...)
	return cleanIdentifierKeys(keys)
}

func resolverSources(person model.Person) []string {
	values := make([]string, 0, len(person.Sources))
	for source := range person.Sources {
		values = append(values, source)
	}
	return cleanSortedStrings(values)
}

func resolverLastSeen(person model.Person) (string, time.Time, bool) {
	var latest time.Time
	for _, source := range person.Sources {
		if source.LastSeenAt.IsZero() {
			continue
		}
		if latest.IsZero() || source.LastSeenAt.After(latest) {
			latest = source.LastSeenAt
		}
	}
	if latest.IsZero() {
		return "", time.Time{}, false
	}
	latest = latest.UTC()
	return latest.Format(time.RFC3339), latest, true
}

func sortWhoCandidates(candidates []WhoCandidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		left := candidates[i]
		right := candidates[j]
		if left.matchRank != right.matchRank {
			return left.matchRank.BetterThan(right.matchRank)
		}
		if left.lastSeenOK != right.lastSeenOK {
			return left.lastSeenOK
		}
		if left.lastSeenOK && !left.lastSeenAt.Equal(right.lastSeenAt) {
			return left.lastSeenAt.After(right.lastSeenAt)
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
