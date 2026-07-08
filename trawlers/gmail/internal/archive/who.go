package archive

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/openclaw/crawlkit/whomatch"
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

type whoCandidateArchiveData struct {
	participantKeys map[string]struct{}
	messageIDs      map[string]struct{}
	lastSeenUnix    int64
}

func (s *Store) ResolveWho(ctx context.Context, query string) (WhoResult, error) {
	query = whomatch.Normalize(query)
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
	who = whomatch.Normalize(who)
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
		return searchWhoFilter{}, &UnknownWhoError{Query: who, DidYouMean: []WhoCandidate{}}
	case 1:
		candidate := resolved.Candidates[0]
		if candidate.matchRank == whomatch.RankCloseSpelling {
			return searchWhoFilter{}, &UnknownWhoError{Query: who, DidYouMean: []WhoCandidate{candidate}}
		}
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
	defer func() { _ = rows.Close() }()
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
	baseCandidates := make([]WhoCandidate, 0, len(aggregates))
	for _, aggregate := range aggregates {
		candidate := aggregate.candidate()
		if candidate.Who == "" {
			continue
		}
		baseCandidates = append(baseCandidates, candidate)
	}
	sortWhoCandidatesForMerge(baseCandidates)
	candidates := mergeSameNameWhoCandidates(baseCandidates)
	sortWhoCandidates(candidates)
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
	defer func() { _ = rows.Close() }()
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

func mergeSameNameWhoCandidates(candidates []WhoCandidate) []WhoCandidate {
	if len(candidates) == 0 {
		return nil
	}
	archiveData := map[string]*whoCandidateArchiveData{}
	matcherCandidates := make([]whomatch.Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		if key := whomatch.Normalize(candidate.Who); key != "" {
			data := archiveData[key]
			if data == nil {
				data = newWhoCandidateArchiveData()
				archiveData[key] = data
			}
			data.add(candidate)
		}
		matcherCandidates = append(matcherCandidates, candidate.matcherCandidate())
	}
	merged := whomatch.MergeSameName(matcherCandidates)
	out := make([]WhoCandidate, 0, len(merged))
	for _, candidate := range merged {
		if strings.TrimSpace(candidate.Who) == "" {
			continue
		}
		out = append(out, whoCandidateFromMatcher(candidate, archiveData[whomatch.Normalize(candidate.Who)]))
	}
	return out
}

func newWhoCandidateArchiveData() *whoCandidateArchiveData {
	return &whoCandidateArchiveData{
		participantKeys: map[string]struct{}{},
		messageIDs:      map[string]struct{}{},
	}
}

func (d *whoCandidateArchiveData) add(candidate WhoCandidate) {
	for _, key := range candidate.participantKeys {
		d.participantKeys[key] = struct{}{}
	}
	for _, id := range candidate.messageIDs {
		d.messageIDs[id] = struct{}{}
	}
	if candidate.lastSeenUnix > d.lastSeenUnix {
		d.lastSeenUnix = candidate.lastSeenUnix
	}
}

func whoIdentity(row rawWhoParticipant, ownerEmails map[string]struct{}) (string, string) {
	if isOwnerEmail(row.Address, ownerEmails) {
		return "owner:me", "me"
	}
	display := firstNonEmptyDisplay(row.DisplayName, row.Name, row.Address)
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
	if shouldUseWhoDisplay(a.who, display, a.identifiers) {
		a.who = display
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
	messageIDs := sortedKeys(a.messageIDs)
	return WhoCandidate{
		Who:             a.who,
		Identifiers:     identifiers,
		LastSeen:        formatWhoLastSeen(a.lastSeenUnix),
		Messages:        int64(len(messageIDs)),
		participantKeys: keys,
		lastSeenUnix:    a.lastSeenUnix,
		messageIDs:      messageIDs,
	}
}

func (c WhoCandidate) matcherCandidate() whomatch.Candidate {
	out := whomatch.Candidate{
		Who:         c.Who,
		Identifiers: append([]string(nil), c.Identifiers...),
		Messages:    c.Messages,
	}
	if c.lastSeenUnix > 0 {
		out.LastSeen = time.Unix(c.lastSeenUnix, 0)
	}
	return out
}

func whoCandidateFromMatcher(candidate whomatch.Candidate, data *whoCandidateArchiveData) WhoCandidate {
	out := WhoCandidate{
		Who:         candidate.Who,
		Identifiers: append([]string(nil), candidate.Identifiers...),
		LastSeen:    formatMatcherLastSeen(candidate.LastSeen),
		Messages:    candidate.Messages,
	}
	if !candidate.LastSeen.IsZero() {
		out.lastSeenUnix = candidate.LastSeen.Unix()
	}
	if data == nil {
		return out
	}
	out.participantKeys = sortedKeys(data.participantKeys)
	out.messageIDs = sortedKeys(data.messageIDs)
	out.lastSeenUnix = data.lastSeenUnix
	out.LastSeen = formatWhoLastSeen(data.lastSeenUnix)
	out.Messages = int64(len(out.messageIDs))
	return out
}

func (c WhoCandidate) resolved() *WhoResolved {
	identifiers := append([]string(nil), c.Identifiers...)
	return &WhoResolved{Who: c.Who, Identifiers: identifiers}
}

func matchWhoCandidates(query string, candidates []WhoCandidate) []WhoCandidate {
	query = whomatch.Normalize(query)
	if query == "" {
		return []WhoCandidate{}
	}
	var matches []WhoCandidate
	for _, candidate := range candidates {
		rank, ok := candidate.matcherCandidate().MatchRank(query)
		if !ok {
			continue
		}
		candidate.matchRank = rank
		candidate.Identifiers = matchingIdentifiersFirst(query, candidate.Identifiers)
		matches = append(matches, candidate)
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].matchRank != matches[j].matchRank {
			return matches[i].matchRank.BetterThan(matches[j].matchRank)
		}
		return compareWhoCandidates(matches[i], matches[j]) < 0
	})
	return matches
}

func matchingIdentifiersFirst(query string, identifiers []string) []string {
	if len(identifiers) == 0 {
		return identifiers
	}
	type rankedIdentifier struct {
		value   string
		rank    whomatch.Rank
		matched bool
		index   int
	}
	ranked := make([]rankedIdentifier, 0, len(identifiers))
	anyMatched := false
	for index, identifier := range identifiers {
		rank, matched := whomatch.MatchRank(query, []string{identifier})
		if matched {
			anyMatched = true
		}
		ranked = append(ranked, rankedIdentifier{
			value:   identifier,
			rank:    rank,
			matched: matched,
			index:   index,
		})
	}
	if !anyMatched {
		return identifiers
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].matched != ranked[j].matched {
			return ranked[i].matched
		}
		if ranked[i].matched && ranked[i].rank != ranked[j].rank {
			return ranked[i].rank.BetterThan(ranked[j].rank)
		}
		return ranked[i].index < ranked[j].index
	})
	out := make([]string, 0, len(ranked))
	for _, identifier := range ranked {
		out = append(out, identifier.value)
	}
	return out
}

func sortWhoCandidates(candidates []WhoCandidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		return compareWhoCandidates(candidates[i], candidates[j]) < 0
	})
}

func sortWhoCandidatesForMerge(candidates []WhoCandidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		leftName := whomatch.Normalize(candidates[i].Who)
		rightName := whomatch.Normalize(candidates[j].Who)
		if leftName != rightName {
			return leftName < rightName
		}
		leftIdentifiers := strings.Join(candidates[i].Identifiers, "\x00")
		rightIdentifiers := strings.Join(candidates[j].Identifiers, "\x00")
		if leftIdentifiers != rightIdentifiers {
			return leftIdentifiers < rightIdentifiers
		}
		return compareWhoCandidates(candidates[i], candidates[j]) < 0
	})
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
	leftWho := whomatch.Normalize(left.Who)
	rightWho := whomatch.Normalize(right.Who)
	if leftWho < rightWho {
		return -1
	}
	if leftWho > rightWho {
		return 1
	}
	return strings.Compare(strings.Join(left.Identifiers, ","), strings.Join(right.Identifiers, ","))
}

func firstNonEmptyDisplay(values ...string) string {
	for _, value := range values {
		value = cleanWhoDisplay(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func cleanWhoDisplay(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func shouldUseWhoDisplay(existing string, next string, identifiers map[string]struct{}) bool {
	next = cleanWhoDisplay(next)
	if next == "" {
		return false
	}
	if existing == "" {
		return true
	}
	values := sortedKeys(identifiers)
	return whomatch.IsIdentifierLike(existing, values) && !whomatch.IsIdentifierLike(next, values)
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

func formatWhoLastSeen(unix int64) string {
	if unix <= 0 {
		return ""
	}
	return formatArchiveTime(time.Unix(unix, 0))
}

func formatMatcherLastSeen(when time.Time) string {
	if when.IsZero() {
		return ""
	}
	return formatArchiveTime(when)
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
