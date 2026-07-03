package archive

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
)

const (
	whoMatchExact = iota
	whoMatchPrefix
	whoMatchContains
	whoMatchClose
)

type searchWhoFilter struct {
	enabled         bool
	participantKeys []string
	resolved        *WhoResolved
	query           string
}

type AmbiguousWhoError struct {
	Query      string
	Candidates []WhoCandidate
}

func (e *AmbiguousWhoError) Error() string {
	return fmt.Sprintf("who %q matched more than one person", e.Query)
}

type UnknownWhoError struct {
	Query      string
	DidYouMean []WhoCandidate
}

func (e *UnknownWhoError) Error() string {
	return fmt.Sprintf("who %q did not match a person", e.Query)
}

type rawWhoParticipant struct {
	MessageID      string
	ParticipantKey string
	DisplayName    string
	Name           string
	Address        string
	TimeUnix       int64
}

type whoAggregate struct {
	who             string
	identifiers     map[string]struct{}
	participantKeys map[string]struct{}
	messageIDs      map[string]struct{}
	lastSeenUnix    int64
}

func (s *Store) ResolveWho(ctx context.Context, query string) (WhoResult, error) {
	query = normalizeWho(query)
	candidates, err := s.allWhoCandidates(ctx)
	if err != nil {
		return WhoResult{}, err
	}
	matches := matchWhoCandidates(query, candidates)
	if matches == nil {
		matches = []WhoCandidate{}
	}
	return WhoResult{Query: query, Candidates: matches}, nil
}

func (s *Store) resolveSearchWho(ctx context.Context, who string) (searchWhoFilter, error) {
	who = normalizeWho(who)
	if who == "" {
		return searchWhoFilter{}, nil
	}
	if _, _, err := s.EnsureParticipants(ctx); err != nil {
		return searchWhoFilter{}, err
	}
	if isExactIdentifierValue(who) {
		keys, err := s.exactIdentifierParticipantKeys(ctx, who)
		if err != nil {
			return searchWhoFilter{}, err
		}
		return searchWhoFilter{enabled: true, participantKeys: keys}, nil
	}
	resolved, err := s.ResolveWho(ctx, who)
	if err != nil {
		return searchWhoFilter{}, err
	}
	switch len(resolved.Candidates) {
	case 0:
		suggestions, err := s.suggestWho(ctx, who)
		if err != nil {
			return searchWhoFilter{}, err
		}
		return searchWhoFilter{}, &UnknownWhoError{Query: who, DidYouMean: suggestions}
	case 1:
		candidate := resolved.Candidates[0]
		return searchWhoFilter{
			enabled:         true,
			participantKeys: candidate.participantKeys,
			resolved:        candidate.resolved(),
			query:           who,
		}, nil
	default:
		return searchWhoFilter{}, &AmbiguousWhoError{Query: who, Candidates: resolved.Candidates}
	}
}

func (s *Store) allWhoCandidates(ctx context.Context) ([]WhoCandidate, error) {
	if _, _, err := s.EnsureParticipants(ctx); err != nil {
		return nil, err
	}
	ownerEmails, err := s.OwnerEmails(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.DB().QueryContext(ctx, `
select mp.message_id, mp.participant_key, mp.display_name, mp.name, mp.address, m.time_unix
from message_participants mp
join messages m on m.id = mp.message_id
where trim(mp.participant_key) <> ''
order by m.time_unix desc, mp.display_name, mp.address, mp.participant_key
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	aggregates := map[string]*whoAggregate{}
	for rows.Next() {
		var row rawWhoParticipant
		if err := rows.Scan(&row.MessageID, &row.ParticipantKey, &row.DisplayName, &row.Name, &row.Address, &row.TimeUnix); err != nil {
			return nil, err
		}
		identityKey, display := whoIdentity(row, ownerEmails)
		if identityKey == "" {
			continue
		}
		aggregate := aggregates[identityKey]
		if aggregate == nil {
			aggregate = &whoAggregate{
				identifiers:     map[string]struct{}{},
				participantKeys: map[string]struct{}{},
				messageIDs:      map[string]struct{}{},
			}
			aggregates[identityKey] = aggregate
		}
		aggregate.add(row, display, isOwnerIdentity(identityKey))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	displayAggregates := mergeWhoAggregatesByDisplay(aggregates)
	candidates := make([]WhoCandidate, 0, len(displayAggregates))
	for _, aggregate := range displayAggregates {
		candidate := aggregate.candidate()
		if candidate.Who == "" {
			continue
		}
		candidates = append(candidates, candidate)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return compareWhoCandidates(candidates[i], candidates[j]) < 0
	})
	return candidates, nil
}

func (s *Store) exactIdentifierParticipantKeys(ctx context.Context, value string) ([]string, error) {
	value = strings.TrimSpace(value)
	if email := normalizeEmail(value); isEmailIdentifier(email) {
		return []string{"addr:" + email}, nil
	}
	rows, err := s.store.DB().QueryContext(ctx, `
select distinct participant_key
from message_participants
where lower(trim(address)) = lower(trim(?))
order by participant_key
`, value)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return keys, nil
}

func (s *Store) suggestWho(ctx context.Context, query string) ([]WhoCandidate, error) {
	candidates, err := s.allWhoCandidates(ctx)
	if err != nil {
		return nil, err
	}
	suggestions := looseWhoSuggestions(query, candidates)
	if suggestions == nil {
		return []WhoCandidate{}, nil
	}
	return suggestions, nil
}

func mergeWhoAggregatesByDisplay(aggregates map[string]*whoAggregate) []*whoAggregate {
	merged := map[string]*whoAggregate{}
	for _, aggregate := range aggregates {
		key := normalizeMatchValue(aggregate.who)
		if key == "" {
			continue
		}
		existing := merged[key]
		if existing == nil {
			merged[key] = aggregate
			continue
		}
		existing.merge(aggregate)
	}
	out := make([]*whoAggregate, 0, len(merged))
	for _, aggregate := range merged {
		out = append(out, aggregate)
	}
	return out
}

func whoIdentity(row rawWhoParticipant, ownerEmails map[string]struct{}) (string, string) {
	if isOwnerEmail(row.Address, ownerEmails) {
		return "owner:me", "me"
	}
	display := firstNonEmptyWho(row.DisplayName, row.Name, row.Address)
	return row.ParticipantKey, display
}

func (a *whoAggregate) add(row rawWhoParticipant, display string, owner bool) {
	a.participantKeys[row.ParticipantKey] = struct{}{}
	a.messageIDs[row.MessageID] = struct{}{}
	if row.TimeUnix > a.lastSeenUnix {
		a.lastSeenUnix = row.TimeUnix
	}
	if owner {
		a.addIdentifier("me")
	}
	address := strings.TrimSpace(row.Address)
	if address != "" {
		a.addIdentifier(address)
	}
	if shouldReplaceWho(a.who, display) {
		a.who = display
	}
}

func (a *whoAggregate) merge(other *whoAggregate) {
	if shouldReplaceMergedWho(a, other) {
		a.who = normalizeWho(other.who)
	}
	for identifier := range other.identifiers {
		a.identifiers[identifier] = struct{}{}
	}
	for key := range other.participantKeys {
		a.participantKeys[key] = struct{}{}
	}
	for messageID := range other.messageIDs {
		a.messageIDs[messageID] = struct{}{}
	}
	if other.lastSeenUnix > a.lastSeenUnix {
		a.lastSeenUnix = other.lastSeenUnix
	}
}

func (a *whoAggregate) addIdentifier(value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	a.identifiers[value] = struct{}{}
}

func (a *whoAggregate) candidate() WhoCandidate {
	identifiers := sortedKeys(a.identifiers)
	keys := sortedKeys(a.participantKeys)
	lastSeen := ""
	if a.lastSeenUnix > 0 {
		lastSeen = formatArchiveTime(time.Unix(a.lastSeenUnix, 0))
	}
	return WhoCandidate{
		Who:             a.who,
		Identifiers:     identifiers,
		LastSeen:        lastSeen,
		Messages:        int64(len(a.messageIDs)),
		participantKeys: keys,
		lastSeenUnix:    a.lastSeenUnix,
	}
}

func (c WhoCandidate) resolved() *WhoResolved {
	identifiers := append([]string(nil), c.Identifiers...)
	return &WhoResolved{Who: c.Who, Identifiers: identifiers}
}

func matchWhoCandidates(query string, candidates []WhoCandidate) []WhoCandidate {
	query = normalizeMatchValue(query)
	if query == "" {
		return []WhoCandidate{}
	}
	var matches []WhoCandidate
	for _, candidate := range candidates {
		quality, identifier, ok := bestWhoCandidateMatch(query, candidate)
		if !ok {
			continue
		}
		candidate.matchQuality = quality
		candidate.Identifiers = matchingIdentifierFirst(candidate.Identifiers, identifier)
		matches = append(matches, candidate)
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].matchQuality != matches[j].matchQuality {
			return matches[i].matchQuality < matches[j].matchQuality
		}
		return compareWhoCandidates(matches[i], matches[j]) < 0
	})
	return matches
}

func looseWhoSuggestions(query string, candidates []WhoCandidate) []WhoCandidate {
	query = normalizeMatchValue(query)
	if query == "" {
		return []WhoCandidate{}
	}
	var suggestions []WhoCandidate
	for _, candidate := range candidates {
		distance, identifier, ok := closestWhoCandidateDistance(query, candidate)
		if !ok || distance > looseSuggestionDistance(query) {
			continue
		}
		candidate.matchQuality = distance
		candidate.Identifiers = matchingIdentifierFirst(candidate.Identifiers, identifier)
		suggestions = append(suggestions, candidate)
	}
	sort.SliceStable(suggestions, func(i, j int) bool {
		if suggestions[i].matchQuality != suggestions[j].matchQuality {
			return suggestions[i].matchQuality < suggestions[j].matchQuality
		}
		return compareWhoCandidates(suggestions[i], suggestions[j]) < 0
	})
	if len(suggestions) > 5 {
		suggestions = suggestions[:5]
	}
	return suggestions
}

func bestWhoCandidateMatch(query string, candidate WhoCandidate) (int, string, bool) {
	best := 0
	bestIdentifier := ""
	matched := false
	if quality, ok := visibleWhoMatchQuality(query, candidate.Who); ok {
		best = quality
		matched = true
	}
	for _, identifier := range candidate.Identifiers {
		quality, ok := visibleWhoMatchQuality(query, identifier)
		if !ok {
			continue
		}
		if !matched || quality < best {
			best = quality
			bestIdentifier = identifier
			matched = true
			continue
		}
		if quality == best && bestIdentifier == "" {
			bestIdentifier = identifier
		}
	}
	return best, bestIdentifier, matched
}

func visibleWhoMatchQuality(query, value string) (int, bool) {
	value = normalizeMatchValue(value)
	if value == "" {
		return 0, false
	}
	return matchValueQuality(query, value)
}

func matchValueQuality(query, value string) (int, bool) {
	if value == query {
		return whoMatchExact, true
	}
	if strings.HasPrefix(value, query) {
		return whoMatchPrefix, true
	}
	if strings.Contains(value, query) {
		return whoMatchContains, true
	}
	if closeSpelling(query, value) {
		return whoMatchClose, true
	}
	return 0, false
}

func closeSpelling(query, value string) bool {
	query = canonicalSpelling(query)
	if len(query) < 4 {
		return false
	}
	for _, candidate := range spellingCandidates(value) {
		if candidate == "" || len(candidate) < 4 || firstRune(candidate) != firstRune(query) {
			continue
		}
		if levenshteinAtMost(query, candidate, closeSpellingDistance(query)) {
			return true
		}
	}
	return false
}

func closestWhoDistance(query string, values []string) (int, bool) {
	query = canonicalSpelling(query)
	if len(query) < 3 {
		return 0, false
	}
	best := 0
	found := false
	for _, value := range values {
		for _, candidate := range spellingCandidates(value) {
			if candidate == "" {
				continue
			}
			distance := levenshteinDistance(query, candidate, looseSuggestionDistance(query))
			if distance < 0 {
				continue
			}
			if !found || distance < best {
				best = distance
				found = true
			}
		}
	}
	return best, found
}

func closestWhoCandidateDistance(query string, candidate WhoCandidate) (int, string, bool) {
	best := 0
	bestIdentifier := ""
	found := false
	if distance, ok := closestWhoDistance(query, []string{candidate.Who}); ok {
		best = distance
		found = true
	}
	for _, identifier := range candidate.Identifiers {
		distance, ok := closestWhoDistance(query, []string{identifier})
		if !ok {
			continue
		}
		if !found || distance < best {
			best = distance
			bestIdentifier = identifier
			found = true
			continue
		}
		if distance == best && bestIdentifier == "" {
			bestIdentifier = identifier
		}
	}
	return best, bestIdentifier, found
}

func matchingIdentifierFirst(identifiers []string, match string) []string {
	if match == "" || len(identifiers) == 0 || identifiers[0] == match {
		return identifiers
	}
	out := []string{match}
	for _, identifier := range identifiers {
		if identifier != match {
			out = append(out, identifier)
		}
	}
	return out
}

func spellingCandidates(value string) []string {
	value = normalizeMatchValue(value)
	compact := canonicalSpelling(value)
	out := []string{compact}
	for _, token := range strings.FieldsFunc(value, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		out = append(out, canonicalSpelling(token))
	}
	return out
}

func closeSpellingDistance(query string) int {
	if len(query) >= 7 {
		return 2
	}
	return 1
}

func looseSuggestionDistance(query string) int {
	if len(query) >= 8 {
		return 3
	}
	if len(query) >= 5 {
		return 2
	}
	return 1
}

func levenshteinAtMost(left, right string, maxDistance int) bool {
	return levenshteinDistance(left, right, maxDistance) >= 0
}

func levenshteinDistance(left, right string, maxDistance int) int {
	lr, rr := []rune(left), []rune(right)
	if absInt(len(lr)-len(rr)) > maxDistance {
		return -1
	}
	prev := make([]int, len(rr)+1)
	for j := range prev {
		prev[j] = j
	}
	for i, l := range lr {
		cur := make([]int, len(rr)+1)
		cur[0] = i + 1
		rowMin := cur[0]
		for j, r := range rr {
			cost := 0
			if l != r {
				cost = 1
			}
			cur[j+1] = minInt(minInt(cur[j]+1, prev[j+1]+1), prev[j]+cost)
			if cur[j+1] < rowMin {
				rowMin = cur[j+1]
			}
		}
		if rowMin > maxDistance {
			return -1
		}
		prev = cur
	}
	if prev[len(rr)] > maxDistance {
		return -1
	}
	return prev[len(rr)]
}

func compareWhoCandidates(left, right WhoCandidate) int {
	if left.lastSeenUnix != right.lastSeenUnix {
		if left.lastSeenUnix > right.lastSeenUnix {
			return -1
		}
		return 1
	}
	if left.Messages != right.Messages {
		if left.Messages > right.Messages {
			return -1
		}
		return 1
	}
	leftWho := strings.ToLower(left.Who)
	rightWho := strings.ToLower(right.Who)
	if leftWho < rightWho {
		return -1
	}
	if leftWho > rightWho {
		return 1
	}
	return strings.Compare(strings.Join(left.Identifiers, ","), strings.Join(right.Identifiers, ","))
}

func shouldReplaceWho(existing, next string) bool {
	next = normalizeWho(next)
	if next == "" {
		return false
	}
	if existing == "" {
		return true
	}
	return looksLikeAddress(existing) && !looksLikeAddress(next)
}

func shouldReplaceMergedWho(existing, next *whoAggregate) bool {
	nextWho := normalizeWho(next.who)
	if nextWho == "" {
		return false
	}
	existingWho := normalizeWho(existing.who)
	if existingWho == "" {
		return true
	}
	if looksLikeAddress(existingWho) && !looksLikeAddress(nextWho) {
		return true
	}
	if normalizeMatchValue(existingWho) != normalizeMatchValue(nextWho) {
		return false
	}
	if existing.lastSeenUnix != next.lastSeenUnix {
		return next.lastSeenUnix > existing.lastSeenUnix
	}
	return nextWho < existingWho
}

func firstNonEmptyWho(values ...string) string {
	for _, value := range values {
		value = normalizeWho(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func sortedKeys(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.SliceStable(out, func(i, j int) bool {
		left := strings.ToLower(out[i])
		right := strings.ToLower(out[j])
		if left != right {
			return left < right
		}
		return out[i] < out[j]
	})
	return out
}

func normalizeWho(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func normalizeMatchValue(value string) string {
	return strings.ToLower(normalizeWho(value))
}

func canonicalSpelling(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isOwnerIdentity(value string) bool {
	return value == "owner:me"
}

func isOwnerEmail(address string, ownerEmails map[string]struct{}) bool {
	_, ok := ownerEmails[normalizeEmail(address)]
	return ok
}

func isExactIdentifierValue(value string) bool {
	value = strings.TrimSpace(value)
	if isEmailIdentifier(value) {
		return true
	}
	if strings.HasPrefix(value, "@") && !strings.ContainsAny(value[1:], " \t\r\n") {
		return len(value) > 1
	}
	if strings.HasPrefix(value, "+") {
		hasDigit := false
		for _, r := range value[1:] {
			switch {
			case r >= '0' && r <= '9':
				hasDigit = true
			case r == ' ' || r == '-' || r == '(' || r == ')':
			default:
				return false
			}
		}
		return hasDigit
	}
	return false
}

func isEmailIdentifier(value string) bool {
	value = strings.TrimSpace(value)
	if strings.ContainsAny(value, " \t\r\n<>") {
		return false
	}
	before, after, ok := strings.Cut(value, "@")
	return ok && strings.TrimSpace(before) != "" && strings.TrimSpace(after) != ""
}

func looksLikeAddress(value string) bool {
	return isEmailIdentifier(value) || strings.HasPrefix(strings.TrimSpace(value), "+")
}

func firstRune(value string) rune {
	for _, r := range value {
		return r
	}
	return 0
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
