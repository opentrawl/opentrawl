package store

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/openclaw/crawlkit/whomatch"
)

type whoCandidateBuilder struct {
	key             string
	names           map[string]*whoNameEvidence
	identifiers     map[string]string
	participantKeys map[string]string
	lastSeen        time.Time
	messages        int
}

type whoNameEvidence struct {
	value       string
	contactFull bool
	pushCount   int
}

func (s *Store) whoCandidateRecords(ctx context.Context) ([]whoCandidateRecord, error) {
	return s.readWhoCandidateRecords(ctx, true)
}

func (s *Store) whoCandidateRecordsWithoutNameMerge(ctx context.Context) ([]whoCandidateRecord, error) {
	return s.readWhoCandidateRecords(ctx, false)
}

func (s *Store) readWhoCandidateRecords(ctx context.Context, mergeSameName bool) ([]whoCandidateRecord, error) {
	builders, err := s.readWhoCandidateAliases(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.readWhoCandidateStats(ctx, builders); err != nil {
		return nil, err
	}
	records := make([]whoCandidateRecord, 0, len(builders))
	for _, builder := range builders {
		if builder.messages == 0 {
			continue
		}
		identifiers := sortedValues(builder.identifiers)
		participantKeys := sortedValues(builder.participantKeys)
		names := builder.nameValues()
		who := chooseWhoName(builder.names, identifiers)
		if who == "" || len(participantKeys) == 0 {
			continue
		}
		records = append(records, whoCandidateRecord{
			WhoCandidate: WhoCandidate{
				Who:             who,
				Identifiers:     identifiers,
				LastSeen:        builder.lastSeen,
				Messages:        builder.messages,
				ParticipantKeys: participantKeys,
			},
			aliases: uniqueStrings(names),
		})
	}
	if mergeSameName {
		records = mergeWhoCandidateRecords(records)
	}
	sort.SliceStable(records, func(i, j int) bool {
		if !records[i].LastSeen.Equal(records[j].LastSeen) {
			return records[i].LastSeen.After(records[j].LastSeen)
		}
		if records[i].Messages != records[j].Messages {
			return records[i].Messages > records[j].Messages
		}
		return strings.ToLower(records[i].Who) < strings.ToLower(records[j].Who)
	})
	return records, nil
}

func mergeWhoCandidateRecords(records []whoCandidateRecord) []whoCandidateRecord {
	if len(records) == 0 {
		return nil
	}
	type extra struct {
		aliases         []string
		participantKeys []string
	}
	extras := map[string]*extra{}
	candidates := make([]whomatch.Candidate, 0, len(records))
	for _, record := range records {
		key := whomatch.Normalize(record.Who)
		if key == "" {
			continue
		}
		candidates = append(candidates, record.matchCandidate())
		group, ok := extras[key]
		if !ok {
			group = &extra{}
			extras[key] = group
		}
		group.aliases = append(group.aliases, record.aliases...)
		group.participantKeys = append(group.participantKeys, record.ParticipantKeys...)
	}
	merged := whomatch.MergeSameName(candidates)
	out := make([]whoCandidateRecord, 0, len(merged))
	for _, candidate := range merged {
		group := extras[whomatch.Normalize(candidate.Who)]
		if group == nil {
			continue
		}
		out = append(out, whoCandidateRecord{
			WhoCandidate: WhoCandidate{
				Who:             candidate.Who,
				Identifiers:     sortedUniqueValues(candidate.Identifiers),
				LastSeen:        candidate.LastSeen,
				Messages:        int(candidate.Messages),
				ParticipantKeys: sortedUniqueValues(group.participantKeys),
			},
			aliases: sortedUniqueValues(append(group.aliases, candidate.Aliases...)),
		})
	}
	return out
}

func (s *Store) readWhoCandidateAliases(ctx context.Context) (map[string]*whoCandidateBuilder, error) {
	rows, err := s.db.QueryContext(ctx, whoCandidateAliasesQuery())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	builders := map[string]*whoCandidateBuilder{}
	for rows.Next() {
		var key, participantKey, displayName, identifier, nameKind string
		if err := rows.Scan(&key, &participantKey, &displayName, &identifier, &nameKind); err != nil {
			return nil, err
		}
		builder := whoBuilder(builders, key)
		if value := normalizeWhoIdentity(participantKey); value != "" {
			builder.participantKeys[strings.ToLower(value)] = value
		}
		if value := normalizeWhoIdentity(displayName); value != "" {
			builder.addName(value, nameKind)
		}
		if value := normalizeWhoIdentifier(identifier); value != "" {
			builder.identifiers[strings.ToLower(value)] = value
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return builders, nil
}

func (s *Store) readWhoCandidateStats(ctx context.Context, builders map[string]*whoCandidateBuilder) error {
	rows, err := s.db.QueryContext(ctx, whoCandidateStatsQuery())
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var key string
		var lastSeen int64
		var messages int
		if err := rows.Scan(&key, &lastSeen, &messages); err != nil {
			return err
		}
		builder := whoBuilder(builders, key)
		seen := fromUnix(lastSeen)
		if builder.lastSeen.IsZero() || seen.After(builder.lastSeen) {
			builder.lastSeen = seen
		}
		builder.messages = messages
	}
	return rows.Err()
}

func whoBuilder(builders map[string]*whoCandidateBuilder, key string) *whoCandidateBuilder {
	key = strings.TrimSpace(key)
	if builder, ok := builders[key]; ok {
		return builder
	}
	builder := &whoCandidateBuilder{
		key:             key,
		names:           map[string]*whoNameEvidence{},
		identifiers:     map[string]string{},
		participantKeys: map[string]string{},
	}
	builders[key] = builder
	return builder
}

func (b *whoCandidateBuilder) addName(value, kind string) {
	value = normalizeWhoIdentity(value)
	if value == "" {
		return
	}
	key := strings.ToLower(value)
	evidence, ok := b.names[key]
	if !ok {
		evidence = &whoNameEvidence{value: value}
		b.names[key] = evidence
	}
	switch kind {
	case "contact_full":
		evidence.contactFull = true
	case "push":
		evidence.pushCount++
	}
}

func (b *whoCandidateBuilder) nameValues() []string {
	return sortedWhoNameValues(b.names)
}
