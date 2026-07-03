package index

import (
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/openclaw/clawdex/internal/model"
)

type WhoCandidate struct {
	Who          string
	Identifiers  []string
	Sources      []string
	LastSeen     string
	MatchQuality string

	lastSeenAt time.Time
	lastSeenOK bool
	matchRank  int
}

const (
	matchExact = iota
	matchPrefix
	matchContains
	matchClose
	noMatch
)

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

func (s Store) indexedIdentifiersByPerson() (map[string][]identifierKey, error) {
	db, err := sql.Open("sqlite", s.indexPath())
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
	rank := bestResolverMatch(person, indexed, query)
	if rank == noMatch {
		return WhoCandidate{}, false
	}
	lastSeen, lastSeenAt, lastSeenOK := resolverLastSeen(person)
	return WhoCandidate{
		Who:          person.Name,
		Identifiers:  resolverIdentifiers(person, indexed),
		Sources:      resolverSources(person),
		LastSeen:     lastSeen,
		MatchQuality: resolverMatchQuality(rank),
		lastSeenAt:   lastSeenAt,
		lastSeenOK:   lastSeenOK,
		matchRank:    rank,
	}, true
}

func bestResolverMatch(person model.Person, indexed []identifierKey, query string) int {
	rank := noMatch
	queries := resolverQueryValues(query)
	for _, value := range resolverMatchValues(person, indexed) {
		for _, queryValue := range queries {
			rank = min(rank, resolverMatchRank(value, queryValue))
		}
	}
	return rank
}

func resolverQueryValues(query string) []string {
	values := []string{query}
	if slug := model.Slug(query); slug != "" && slug != "person" {
		values = append(values, slug, strings.ReplaceAll(slug, "-", " "))
	}
	if email := model.NormalizeEmail(query); email != "" {
		values = append(values, email)
	}
	if phone := model.NormalizePhone(query); phone != "" {
		values = append(values, phone)
	}
	return cleanSortedStrings(values)
}

func resolverMatchValues(person model.Person, indexed []identifierKey) []string {
	slug := personPathSlug(person)
	values := []string{person.ID, person.Name, person.SortName, slug, strings.ReplaceAll(slug, "-", " ")}
	values = append(values, person.AKA...)
	values = append(values, person.Tags...)
	for _, source := range person.Sources {
		values = append(values, source.Names...)
	}
	for _, key := range append(personIdentifierKeys(person), indexed...) {
		values = append(values, resolverIdentifierValue(key))
		if key.kind == "handle" {
			if _, handle, ok := strings.Cut(key.value, ":"); ok {
				values = append(values, handle)
			}
		}
	}
	return values
}

func resolverIdentifiers(person model.Person, indexed []identifierKey) []string {
	values := make([]string, 0, len(indexed))
	for _, key := range append(personIdentifierKeys(person), indexed...) {
		values = append(values, resolverIdentifierValue(key))
	}
	values = cleanSortedStrings(values)
	if len(values) == 0 {
		values = []string{person.ID}
	}
	return values
}

func resolverIdentifierValue(key identifierKey) string {
	return strings.TrimSpace(strings.ToLower(key.value))
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

func resolverMatchQuality(rank int) string {
	switch rank {
	case matchExact:
		return "exact"
	case matchPrefix:
		return "prefix"
	case matchContains:
		return "contains"
	case matchClose:
		return "close"
	default:
		return "unknown"
	}
}

func resolverMatchRank(value, query string) int {
	value = resolverComparable(value)
	query = resolverComparable(query)
	if value == "" || query == "" {
		return noMatch
	}
	if value == query {
		return matchExact
	}
	tokens := resolverTokens(value)
	if strings.HasPrefix(value, query) {
		return matchPrefix
	}
	for _, token := range tokens {
		if token == query || strings.HasPrefix(token, query) {
			return matchPrefix
		}
	}
	if strings.Contains(value, query) {
		return matchContains
	}
	for _, token := range tokens {
		if strings.Contains(token, query) {
			return matchContains
		}
	}
	if resolverClose(value, query) {
		return matchClose
	}
	for _, token := range tokens {
		if resolverClose(token, query) {
			return matchClose
		}
	}
	return noMatch
}

func resolverComparable(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
}

func resolverTokens(value string) []string {
	return strings.FieldsFunc(value, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

func resolverClose(value, query string) bool {
	if len([]rune(query)) < 3 {
		return false
	}
	threshold := resolverEditThreshold(query)
	if abs(len([]rune(value))-len([]rune(query))) > threshold {
		return false
	}
	return editDistance(value, query) <= threshold
}

func resolverEditThreshold(query string) int {
	n := len([]rune(query))
	switch {
	case n <= 5:
		return 1
	case n <= 12:
		return 2
	default:
		return 3
	}
}

func editDistance(left, right string) int {
	a := []rune(left)
	b := []rune(right)
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i, ar := range a {
		curr[0] = i + 1
		for j, br := range b {
			cost := 0
			if ar != br {
				cost = 1
			}
			curr[j+1] = min(prev[j+1]+1, curr[j]+1, prev[j]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}

func sortWhoCandidates(candidates []WhoCandidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		left := candidates[i]
		right := candidates[j]
		if left.matchRank != right.matchRank {
			return left.matchRank < right.matchRank
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
		key := strings.ToLower(value)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i]) < strings.ToLower(out[j])
	})
	return out
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
