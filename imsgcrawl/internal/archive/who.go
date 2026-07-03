package archive

import (
	"context"
	"sort"
	"strconv"
	"strings"
)

const whoCandidateLimit = 20

const (
	whoMatchExact = iota
	whoMatchPrefix
	whoMatchContains
	whoMatchClose
	whoMatchNone
)

type searchWhoMatch struct {
	enabled       bool
	includeFromMe bool
	handleRowIDs  []int64
}

type searchWhoHandle struct {
	rowID       int64
	handle      string
	displayName string
}

type searchWhoMapping struct {
	contactKey  string
	displayName string
}

func (s *Store) ResolveWho(ctx context.Context, query string) (WhoResolution, error) {
	query = normalizeSearchWho(query)
	if query == "" {
		return WhoResolution{Query: query, Candidates: []WhoCandidate{}}, nil
	}
	candidates, err := s.whoCandidates(ctx)
	if err != nil {
		return WhoResolution{}, err
	}
	matched := directWhoMatches(query, candidates)
	if len(matched) == 0 {
		matched = closeWhoMatches(query, candidates)
	}
	if err := s.populateWhoStats(ctx, matched); err != nil {
		return WhoResolution{}, err
	}
	sortWhoCandidates(matched)
	totalMatches := len(matched)
	if totalMatches > whoCandidateLimit {
		matched = matched[:whoCandidateLimit]
	}
	if matched == nil {
		matched = []WhoCandidate{}
	}
	return WhoResolution{
		Query:        query,
		Candidates:   matched,
		Returned:     len(matched),
		TotalMatches: totalMatches,
		Truncated:    totalMatches > len(matched),
	}, nil
}

func directWhoMatches(query string, candidates []WhoCandidate) []WhoCandidate {
	matched := make([]WhoCandidate, 0, min(len(candidates), whoCandidateLimit))
	for _, candidate := range candidates {
		rank, ok := whoCandidateDirectMatchRank(query, candidate)
		if !ok {
			continue
		}
		candidate.matchRank = rank
		matched = append(matched, candidate)
	}
	return matched
}

func closeWhoMatches(query string, candidates []WhoCandidate) []WhoCandidate {
	matched := make([]WhoCandidate, 0, whoCandidateLimit)
	for _, candidate := range candidates {
		rank, ok := whoCandidateCloseMatchRank(query, candidate)
		if !ok {
			continue
		}
		candidate.matchRank = rank
		matched = append(matched, candidate)
	}
	return matched
}

func (s *Store) whoCandidates(ctx context.Context) ([]WhoCandidate, error) {
	handles, mappings, err := s.whoRows(ctx)
	if err != nil {
		return nil, err
	}
	owners, err := s.ownerIdentifiers(ctx)
	if err != nil {
		return nil, err
	}
	byParticipant := map[string]int{}
	out := []WhoCandidate{}
	for _, handle := range handles {
		key, name := searchWhoParticipantKey(handle, mappings)
		if key == "" || name == "" {
			continue
		}
		index, ok := byParticipant[key]
		if !ok {
			index = len(out)
			byParticipant[key] = index
			out = append(out, WhoCandidate{Who: name})
		}
		out[index].handleRowIDs = append(out[index].handleRowIDs, handle.rowID)
		out[index].Identifiers = append(out[index].Identifiers, handle.handle)
		if name == ownerDisplayName {
			out[index].includeFromMe = true
		}
	}
	if len(owners) > 0 {
		index, ok := byParticipant["owner"]
		if !ok {
			index = len(out)
			byParticipant["owner"] = index
			out = append(out, WhoCandidate{Who: ownerDisplayName, includeFromMe: true})
		}
		out[index].includeFromMe = true
		out[index].Identifiers = append(out[index].Identifiers, owners...)
	}
	for i := range out {
		out[i].Identifiers = sortedUniqueStrings(out[i].Identifiers)
	}
	sortWhoCandidates(out)
	return out, nil
}

func (s *Store) whoRows(ctx context.Context) ([]searchWhoHandle, map[string]searchWhoMapping, error) {
	rows, err := s.store.DB().QueryContext(ctx, whoRowsSQL)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	var handles []searchWhoHandle
	mappings := map[string]searchWhoMapping{}
	for rows.Next() {
		var rowKind, handle, displayName, mappingKind, normalizedHandle, contactKey, mappingDisplayName string
		var rowID int64
		if err := rows.Scan(&rowKind, &rowID, &handle, &displayName, &mappingKind, &normalizedHandle, &contactKey, &mappingDisplayName); err != nil {
			return nil, nil, err
		}
		switch rowKind {
		case "handle":
			handles = append(handles, searchWhoHandle{
				rowID:       rowID,
				handle:      strings.TrimSpace(handle),
				displayName: strings.TrimSpace(displayName),
			})
		case "mapping":
			key := searchMappingKey(mappingKind, normalizedHandle)
			if key == "" {
				continue
			}
			mappings[key] = searchWhoMapping{
				contactKey:  strings.TrimSpace(contactKey),
				displayName: strings.TrimSpace(mappingDisplayName),
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return handles, mappings, nil
}

func (s *Store) ownerIdentifiers(ctx context.Context) ([]string, error) {
	rows, err := s.store.DB().QueryContext(ctx, ownerIdentifiersSQL)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var identifier string
		if err := rows.Scan(&identifier); err != nil {
			return nil, err
		}
		out = append(out, identifier)
	}
	return sortedUniqueStrings(out), rows.Err()
}

func (s *Store) populateWhoStats(ctx context.Context, candidates []WhoCandidate) error {
	handleRows := 0
	ownerRows := 0
	for _, candidate := range candidates {
		handleRows += len(candidate.handleRowIDs)
		if candidate.includeFromMe {
			ownerRows++
		}
	}
	if handleRows == 0 && ownerRows == 0 {
		return nil
	}
	args := make([]any, 0, handleRows*2+ownerRows)
	for index, candidate := range candidates {
		for _, handleRowID := range candidate.handleRowIDs {
			args = append(args, index, handleRowID)
		}
	}
	for index, candidate := range candidates {
		if candidate.includeFromMe {
			args = append(args, index)
		}
	}
	rows, err := s.store.DB().QueryContext(ctx, whoStatsByCandidateQuery(handleRows, ownerRows), args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var index int
		var messages, lastSeen int64
		if err := rows.Scan(&index, &messages, &lastSeen); err != nil {
			return err
		}
		if index < 0 || index >= len(candidates) {
			continue
		}
		candidates[index].Messages = messages
		candidates[index].lastSeenRaw = lastSeen
		candidates[index].LastSeen = FormatAppleDateTime(lastSeen)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for index := range candidates {
		if candidates[index].Messages == 0 {
			candidates[index].LastSeen = ""
			candidates[index].lastSeenRaw = 0
		}
	}
	return nil
}

func candidateSearchWho(candidate *WhoCandidate) searchWhoMatch {
	if candidate == nil {
		return searchWhoMatch{}
	}
	return searchWhoMatch{
		enabled:       true,
		includeFromMe: candidate.includeFromMe,
		handleRowIDs:  append([]int64(nil), candidate.handleRowIDs...),
	}
}

func searchWhoParticipantKey(handle searchWhoHandle, mappings map[string]searchWhoMapping) (string, string) {
	if normalizeSearchWho(handle.displayName) == ownerDisplayName {
		return "owner", ownerDisplayName
	}
	if mapping, ok := mappings[normalizedSearchHandleKey(handle.handle)]; ok {
		name := normalizeSearchWho(mapping.displayName)
		if name != "" {
			contactKey := strings.TrimSpace(mapping.contactKey)
			if contactKey != "" {
				return "contact:" + contactKey, name
			}
			return "contact-name:" + name, name
		}
	}
	name := normalizeSearchWho(handle.displayName)
	if name == "" {
		name = normalizeSearchWho(handle.handle)
	}
	if name == "" {
		return "", ""
	}
	return "handle:" + strconv.FormatInt(handle.rowID, 10), name
}

func normalizedSearchHandleKey(handle string) string {
	if strings.Contains(handle, "@") {
		return searchMappingKey("email", strings.ToLower(strings.TrimSpace(handle)))
	}
	normalized := normalizeSearchPhone(handle)
	if normalized == "" {
		return ""
	}
	return searchMappingKey("phone", normalized)
}

func searchMappingKey(kind, handle string) string {
	kind = strings.TrimSpace(kind)
	handle = strings.TrimSpace(handle)
	if kind == "" || handle == "" {
		return ""
	}
	return kind + ":" + handle
}

func normalizeSearchPhone(phone string) string {
	var b strings.Builder
	for _, r := range phone {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return strings.TrimPrefix(b.String(), "00")
}

func normalizeSearchWho(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func sortedUniqueStrings(values []string) []string {
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

func sortWhoCandidates(candidates []WhoCandidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		left := candidates[i]
		right := candidates[j]
		if left.matchRank != right.matchRank {
			return left.matchRank < right.matchRank
		}
		if left.lastSeenRaw != right.lastSeenRaw {
			return left.lastSeenRaw > right.lastSeenRaw
		}
		if left.Messages != right.Messages {
			return left.Messages > right.Messages
		}
		if strings.ToLower(left.Who) != strings.ToLower(right.Who) {
			return strings.ToLower(left.Who) < strings.ToLower(right.Who)
		}
		return strings.Join(left.Identifiers, "\x00") < strings.Join(right.Identifiers, "\x00")
	})
}

func whoFilterArgs(who searchWhoMatch) []any {
	args := make([]any, 0, len(who.handleRowIDs))
	for _, id := range who.handleRowIDs {
		args = append(args, id)
	}
	return args
}
