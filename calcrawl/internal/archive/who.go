package archive

import (
	"context"
	"sort"
	"strings"
	"time"
	"unicode"
)

type whoRecord struct {
	displayName string
	email       string
	phone       string
	address     string
	lastSeen    string
	eventUID    string
}

type whoBuilder struct {
	names       map[string]int
	identifiers map[string]string
	events      map[string]struct{}
	lastSeen    string
}

func (s *Store) ResolveWho(ctx context.Context, query string) ([]WhoCandidate, error) {
	query = normalizeWho(query)
	if query == "" {
		return nil, nil
	}
	candidates, err := s.WhoCandidates(ctx)
	if err != nil {
		return nil, err
	}
	type scoredCandidate struct {
		candidate WhoCandidate
		score     int
	}
	scored := []scoredCandidate{}
	for _, candidate := range candidates {
		score, ok := whoCandidateScore(query, candidate)
		if ok {
			scored = append(scored, scoredCandidate{candidate: candidate, score: score})
		}
	}
	sort.SliceStable(scored, func(i, j int) bool {
		left := scored[i]
		right := scored[j]
		if left.score != right.score {
			return left.score < right.score
		}
		if left.candidate.LastSeen != right.candidate.LastSeen {
			return lastSeenAfter(left.candidate.LastSeen, right.candidate.LastSeen)
		}
		if left.candidate.Messages != right.candidate.Messages {
			return left.candidate.Messages > right.candidate.Messages
		}
		return strings.ToLower(left.candidate.Who) < strings.ToLower(right.candidate.Who)
	})
	out := make([]WhoCandidate, 0, len(scored))
	for _, item := range scored {
		out = append(out, item.candidate)
	}
	return out, nil
}

func (s *Store) ResolveWhoIdentifier(ctx context.Context, identifier string) ([]WhoCandidate, error) {
	identifier = normalizeWho(identifier)
	if identifier == "" {
		return nil, nil
	}
	candidates, err := s.WhoCandidates(ctx)
	if err != nil {
		return nil, err
	}
	out := []WhoCandidate{}
	for _, candidate := range candidates {
		for _, value := range candidate.Identifiers {
			if sameWhoValue(identifier, value) {
				out = append(out, candidate)
				break
			}
		}
	}
	return out, nil
}

func (s *Store) WhoCandidates(ctx context.Context) ([]WhoCandidate, error) {
	rows, err := s.store.DB().QueryContext(ctx, `
select trim(organizer_name), trim(organizer_email), trim(organizer_phone), '', start_time, event_uid
from events
where trim(organizer_name) <> '' or trim(organizer_email) <> '' or trim(organizer_phone) <> ''
union all
select trim(p.display_name), trim(p.email), trim(p.phone_number), trim(p.address), e.start_time, e.event_uid
from participants p
join events e on e.event_uid = p.event_uid
where trim(p.display_name) <> '' or trim(p.email) <> '' or trim(p.phone_number) <> '' or trim(p.address) <> ''
order by 6, 5`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	records := []whoRecord{}
	for rows.Next() {
		var record whoRecord
		if err := rows.Scan(&record.displayName, &record.email, &record.phone, &record.address, &record.lastSeen, &record.eventUID); err != nil {
			return nil, err
		}
		record = cleanWhoRecord(record)
		if record.displayName == "" && len(record.identifiers()) == 0 {
			continue
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return buildWhoCandidates(records), nil
}

func buildWhoCandidates(records []whoRecord) []WhoCandidate {
	parent := make([]int, len(records))
	for i := range parent {
		parent[i] = i
	}
	find := func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	union := func(left, right int) {
		leftRoot := find(left)
		rightRoot := find(right)
		if leftRoot != rightRoot {
			parent[rightRoot] = leftRoot
		}
	}
	identifierOwner := map[string]int{}
	nameOwner := map[string]int{}
	for index, record := range records {
		if key := foldWho(record.displayName); key != "" {
			if owner, ok := nameOwner[key]; ok {
				union(owner, index)
			} else {
				nameOwner[key] = index
			}
		}
		for _, key := range record.identifierKeys() {
			if owner, ok := identifierOwner[key]; ok {
				union(owner, index)
			} else {
				identifierOwner[key] = index
			}
		}
	}

	builders := map[int]*whoBuilder{}
	for index, record := range records {
		root := find(index)
		builder := builders[root]
		if builder == nil {
			builder = &whoBuilder{
				names:       map[string]int{},
				identifiers: map[string]string{},
				events:      map[string]struct{}{},
			}
			builders[root] = builder
		}
		if record.displayName != "" {
			builder.names[record.displayName]++
		}
		for _, identifier := range record.identifiers() {
			key := foldWho(identifier)
			if _, ok := builder.identifiers[key]; !ok {
				builder.identifiers[key] = identifier
			}
		}
		if record.eventUID != "" {
			builder.events[record.eventUID] = struct{}{}
		}
		if lastSeenAfter(record.lastSeen, builder.lastSeen) {
			builder.lastSeen = record.lastSeen
		}
	}

	candidates := make([]WhoCandidate, 0, len(builders))
	for _, builder := range builders {
		identifiers := sortedIdentifiers(builder.identifiers)
		candidates = append(candidates, WhoCandidate{
			Who:         bestWhoName(builder.names, identifiers),
			Identifiers: identifiers,
			LastSeen:    canonicalEventTime(builder.lastSeen),
			Messages:    int64(len(builder.events)),
		})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		left := candidates[i]
		right := candidates[j]
		if strings.ToLower(left.Who) != strings.ToLower(right.Who) {
			return strings.ToLower(left.Who) < strings.ToLower(right.Who)
		}
		if left.LastSeen != right.LastSeen {
			return lastSeenAfter(left.LastSeen, right.LastSeen)
		}
		return strings.Join(left.Identifiers, "\x00") < strings.Join(right.Identifiers, "\x00")
	})
	return candidates
}

func cleanWhoRecord(record whoRecord) whoRecord {
	return whoRecord{
		displayName: strings.TrimSpace(record.displayName),
		email:       strings.TrimSpace(record.email),
		phone:       strings.TrimSpace(record.phone),
		address:     strings.TrimSpace(record.address),
		lastSeen:    strings.TrimSpace(record.lastSeen),
		eventUID:    strings.TrimSpace(record.eventUID),
	}
}

func (r whoRecord) identifiers() []string {
	return uniqueStrings([]string{r.email, r.phone, r.address})
}

func (r whoRecord) identifierKeys() []string {
	values := r.identifiers()
	keys := make([]string, 0, len(values))
	for _, value := range values {
		keys = append(keys, foldWho(value))
	}
	return keys
}

func bestWhoName(names map[string]int, identifiers []string) string {
	type nameCandidate struct {
		value string
		count int
	}
	values := []nameCandidate{}
	for value, count := range names {
		if strings.TrimSpace(value) != "" {
			values = append(values, nameCandidate{value: value, count: count})
		}
	}
	sort.SliceStable(values, func(i, j int) bool {
		left := values[i]
		right := values[j]
		if left.count != right.count {
			return left.count > right.count
		}
		if nameQuality(left.value) != nameQuality(right.value) {
			return nameQuality(left.value) > nameQuality(right.value)
		}
		if nameCaseQuality(left.value) != nameCaseQuality(right.value) {
			return nameCaseQuality(left.value) > nameCaseQuality(right.value)
		}
		if len([]rune(left.value)) != len([]rune(right.value)) {
			return len([]rune(left.value)) > len([]rune(right.value))
		}
		if strings.ToLower(left.value) != strings.ToLower(right.value) {
			return strings.ToLower(left.value) < strings.ToLower(right.value)
		}
		return left.value < right.value
	})
	if len(values) > 0 {
		return values[0].value
	}
	if len(identifiers) > 0 {
		return identifiers[0]
	}
	return "unknown"
}

func nameQuality(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	score := 1
	hasLetter := false
	hasLower := false
	for _, r := range value {
		if unicode.IsLetter(r) {
			hasLetter = true
			if unicode.IsLower(r) {
				hasLower = true
			}
		}
	}
	if !hasLetter || hasLower {
		score += 2
	}
	if strings.Contains(value, " ") {
		score++
	}
	return score
}

func nameCaseQuality(value string) int {
	hasUpper := false
	hasLower := false
	for _, r := range value {
		if unicode.IsUpper(r) {
			hasUpper = true
		}
		if unicode.IsLower(r) {
			hasLower = true
		}
	}
	switch {
	case hasUpper && hasLower:
		return 2
	case hasLower:
		return 1
	default:
		return 0
	}
}

func sortedIdentifiers(values map[string]string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if identifierRank(out[i]) != identifierRank(out[j]) {
			return identifierRank(out[i]) < identifierRank(out[j])
		}
		return strings.ToLower(out[i]) < strings.ToLower(out[j])
	})
	return out
}

func identifierRank(value string) int {
	switch {
	case strings.Contains(value, "@"):
		return 0
	case strings.HasPrefix(value, "+") || hasDigit(value):
		return 1
	default:
		return 2
	}
}

func whoCandidateScore(query string, candidate WhoCandidate) (int, bool) {
	values := append([]string{candidate.Who}, candidate.Identifiers...)
	best := 99
	for _, value := range values {
		if score, ok := whoValueScore(query, value); ok && score < best {
			best = score
		}
	}
	return best, best != 99
}

func whoValueScore(query, value string) (int, bool) {
	query = foldWho(query)
	value = foldWho(value)
	switch {
	case query == "" || value == "":
		return 0, false
	case query == value:
		return 0, true
	case strings.HasPrefix(value, query):
		return 1, true
	case strings.Contains(value, query):
		return 2, true
	case closeWhoSpelling(query, value):
		return 3, true
	default:
		return 0, false
	}
}

func closeWhoSpelling(query, value string) bool {
	if editDistance(query, value) <= maxWhoDistance(query, value) {
		return true
	}
	queryParts := strings.Fields(query)
	valueParts := strings.Fields(value)
	if len(queryParts) == 0 || len(valueParts) == 0 {
		return false
	}
	for _, queryPart := range queryParts {
		matched := false
		for _, valuePart := range valueParts {
			if queryPart == valuePart || strings.HasPrefix(valuePart, queryPart) || strings.Contains(valuePart, queryPart) {
				matched = true
				break
			}
			if editDistance(queryPart, valuePart) <= maxWhoDistance(queryPart, valuePart) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func maxWhoDistance(left, right string) int {
	size := len([]rune(left))
	if other := len([]rune(right)); other < size {
		size = other
	}
	switch {
	case size <= 3:
		return 0
	case size <= 5:
		return 1
	case size <= 10:
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
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		current := make([]int, len(b)+1)
		current[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 0
			if a[i-1] != b[j-1] {
				cost = 1
			}
			current[j] = minInt(
				current[j-1]+1,
				prev[j]+1,
				prev[j-1]+cost,
			)
		}
		prev = current
	}
	return prev[len(b)]
}

func minInt(values ...int) int {
	best := values[0]
	for _, value := range values[1:] {
		if value < best {
			best = value
		}
	}
	return best
}

func lastSeenAfter(left, right string) bool {
	if right == "" {
		return left != ""
	}
	leftTime, leftErr := time.Parse(time.RFC3339Nano, left)
	rightTime, rightErr := time.Parse(time.RFC3339Nano, right)
	if leftErr == nil && rightErr == nil {
		return leftTime.After(rightTime)
	}
	return left > right
}

func sameWhoValue(left, right string) bool {
	return strings.EqualFold(normalizeWho(left), normalizeWho(right))
}

func foldWho(value string) string {
	return strings.ToLower(normalizeWho(value))
}

func hasDigit(value string) bool {
	for _, r := range value {
		if unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

func LooksLikeWhoIdentifier(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if strings.Contains(value, "@") || strings.Contains(value, ":") || strings.HasPrefix(value, "+") {
		return true
	}
	if strings.Contains(value, " ") {
		return false
	}
	for _, r := range value {
		if unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

func (c WhoCandidate) Resolved() WhoResolved {
	return WhoResolved{Who: c.Who, Identifiers: append([]string(nil), c.Identifiers...)}
}

func (c WhoCandidate) Filter() *WhoFilter {
	return &WhoFilter{Who: c.Who, Identifiers: append([]string(nil), c.Identifiers...)}
}
