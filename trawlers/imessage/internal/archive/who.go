package archive

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/openclaw/crawlkit/whomatch"
)

const whoCandidateLimit = 20

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
	query = whomatch.Normalize(query)
	if query == "" {
		return WhoResolution{Query: query, Candidates: []WhoCandidate{}}, nil
	}
	candidates, err := s.whoCandidates(ctx)
	if err != nil {
		return WhoResolution{}, err
	}
	matched := matchWhoCandidates(query, candidates)
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

func (resolution WhoResolution) FilterCandidate() (WhoCandidate, bool) {
	if resolution.TotalMatches != 1 || len(resolution.Candidates) != 1 {
		return WhoCandidate{}, false
	}
	candidate := resolution.Candidates[0]
	if candidate.matchRank == whomatch.RankCloseSpelling {
		return WhoCandidate{}, false
	}
	return candidate, true
}

func (resolution WhoResolution) MatchesOnlyByCloseSpelling() bool {
	if resolution.TotalMatches == 0 || len(resolution.Candidates) == 0 {
		return false
	}
	for _, candidate := range resolution.Candidates {
		if candidate.matchRank != whomatch.RankCloseSpelling {
			return false
		}
	}
	return true
}

func matchWhoCandidates(query string, candidates []WhoCandidate) []WhoCandidate {
	direct := make([]WhoCandidate, 0, min(len(candidates), whoCandidateLimit))
	close := make([]WhoCandidate, 0, whoCandidateLimit)
	for _, candidate := range candidates {
		rank, ok := candidate.MatchRank(query)
		if !ok {
			continue
		}
		candidate.matchRank = rank
		if rank == whomatch.RankCloseSpelling {
			close = append(close, candidate)
			continue
		}
		direct = append(direct, candidate)
	}
	if len(direct) > 0 {
		return direct
	}
	return close
}

func (candidate WhoCandidate) MatchRank(query string) (whomatch.Rank, bool) {
	return whomatch.Candidate{
		Who:         candidate.Who,
		Identifiers: candidate.Identifiers,
	}.MatchRank(query)
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
		out[i] = cleanWhoCandidate(out[i])
	}
	out = mergeSameNameWhoCandidates(out)
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
	return out, rows.Err()
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
	if whomatch.Normalize(handle.displayName) == ownerDisplayName {
		return "owner", ownerDisplayName
	}
	if mapping, ok := mappings[normalizedSearchHandleKey(handle.handle)]; ok {
		name := cleanWhoDisplay(mapping.displayName)
		if name != "" {
			contactKey := strings.TrimSpace(mapping.contactKey)
			if contactKey != "" {
				return "contact:" + contactKey, name
			}
			return "contact-name:" + name, name
		}
	}
	name := cleanWhoDisplay(handle.displayName)
	if name == "" {
		name = cleanWhoDisplay(handle.handle)
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

func cleanWhoDisplay(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func cleanWhoCandidate(candidate WhoCandidate) WhoCandidate {
	cleaned := whomatch.MergeSameName([]whomatch.Candidate{{
		Who:         candidate.Who,
		Identifiers: candidate.Identifiers,
	}})
	if len(cleaned) == 0 {
		return candidate
	}
	candidate.Who = cleaned[0].Who
	candidate.Identifiers = cleaned[0].Identifiers
	return candidate
}

func mergeSameNameWhoCandidates(candidates []WhoCandidate) []WhoCandidate {
	if len(candidates) == 0 {
		return nil
	}
	shared := make([]whomatch.Candidate, 0, len(candidates))
	metadata := map[string][]WhoCandidate{}
	for _, candidate := range candidates {
		shared = append(shared, whomatch.Candidate{
			Who:         candidate.Who,
			Identifiers: candidate.Identifiers,
		})
		key := whomatch.Normalize(candidate.Who)
		metadata[key] = append(metadata[key], candidate)
	}
	merged := whomatch.MergeSameName(shared)
	out := make([]WhoCandidate, 0, len(merged))
	for _, candidate := range merged {
		key := whomatch.Normalize(candidate.Who)
		item := WhoCandidate{
			Who:         candidate.Who,
			Identifiers: candidate.Identifiers,
		}
		for _, source := range metadata[key] {
			item.includeFromMe = item.includeFromMe || source.includeFromMe
			item.handleRowIDs = append(item.handleRowIDs, source.handleRowIDs...)
		}
		out = append(out, item)
	}
	return out
}

func sortWhoCandidates(candidates []WhoCandidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		left := candidates[i]
		right := candidates[j]
		if left.matchRank != right.matchRank {
			return left.matchRank.BetterThan(right.matchRank)
		}
		if left.lastSeenRaw != right.lastSeenRaw {
			return left.lastSeenRaw > right.lastSeenRaw
		}
		if left.Messages != right.Messages {
			return left.Messages > right.Messages
		}
		leftName := whomatch.Normalize(left.Who)
		rightName := whomatch.Normalize(right.Who)
		if leftName != rightName {
			return leftName < rightName
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
