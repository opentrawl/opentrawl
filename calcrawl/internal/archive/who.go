package archive

import (
	"context"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/openclaw/crawlkit/whomatch"
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
	query = whomatch.Normalize(query)
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
		rank, ok := candidate.MatchRank(query)
		if ok {
			scored = append(scored, scoredCandidate{candidate: candidate, score: int(rank)})
		}
	}
	sort.SliceStable(scored, func(i, j int) bool {
		left := scored[i]
		right := scored[j]
		if left.score != right.score {
			return left.score > right.score
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
		if key := whomatch.Normalize(record.displayName); key != "" {
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
			key := whomatch.Normalize(identifier)
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
		keys = append(keys, whomatch.Normalize(value))
	}
	return keys
}

// Display-name picking is centralized in crawlkit; the structural rules and
// the rules.md §1.5 carve-out are documented on whomatch.BestDisplayName.
func bestWhoName(names map[string]int, identifiers []string) string {
	if who := whomatch.BestDisplayName(names, identifiers); who != "" {
		return who
	}
	if len(identifiers) > 0 {
		return identifiers[0]
	}
	return "unknown"
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

func hasDigit(value string) bool {
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

func (c WhoCandidate) MatchRank(query string) (whomatch.Rank, bool) {
	return whomatch.Candidate{
		Who:         c.Who,
		Identifiers: c.Identifiers,
	}.MatchRank(query)
}

func (c WhoCandidate) ResolvesWho(query string) bool {
	rank, ok := c.MatchRank(query)
	return ok && rank != whomatch.RankCloseSpelling
}
