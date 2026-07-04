package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/openclaw/crawlkit/whomatch"
)

type WhoCmd struct {
	Name []string `arg:"" name:"name" help:"Name, alias, or identifier fragment"`
}

type WhoEnvelope struct {
	Query            string         `json:"query"`
	TotalCandidates  int            `json:"total_candidates"`
	Candidates       []WhoCandidate `json:"candidates"`
	DidYouMean       []WhoCandidate `json:"did_you_mean,omitempty"`
	TotalDidYouMean  int            `json:"total_did_you_mean,omitempty"`
	SourcesConsulted []string       `json:"sources_consulted,omitempty"`
	FailedSources    []string       `json:"failed_sources,omitempty"`
}

type WhoCandidate struct {
	Who          string   `json:"who"`
	Identifiers  []string `json:"identifiers,omitempty"`
	MatchQuality string   `json:"match_quality,omitempty"`
	Sources      []string `json:"sources,omitempty"`
	LastSeen     string   `json:"last_seen,omitempty"`
	Messages     int      `json:"messages"`

	lastSeenParsed time.Time
	lastSeenOK     bool
	sourceFilters  map[string]string
}

type crawlerWhoCandidate struct {
	Who           string   `json:"who,omitempty"`
	Identifiers   []string `json:"identifiers,omitempty"`
	ID            string   `json:"id,omitempty"`
	PersonID      string   `json:"person_id,omitempty"`
	Person        string   `json:"person,omitempty"`
	Identity      string   `json:"identity,omitempty"`
	Name          string   `json:"name,omitempty"`
	DisplayName   string   `json:"display_name,omitempty"`
	Match         string   `json:"match,omitempty"`
	MatchQuality  string   `json:"match_quality,omitempty"`
	NameMatch     string   `json:"name_match,omitempty"`
	Source        string   `json:"source,omitempty"`
	Sources       []string `json:"sources,omitempty"`
	LastSeen      string   `json:"last_seen,omitempty"`
	LastExchanged string   `json:"last_exchanged,omitempty"`
	Latest        string   `json:"latest,omitempty"`
	Messages      int      `json:"messages,omitempty"`
	Volume        int      `json:"volume,omitempty"`
	MessageVolume int      `json:"message_volume,omitempty"`
	MessageCount  int      `json:"message_count,omitempty"`
	TotalMessages int      `json:"total_messages,omitempty"`
}

const (
	humanWhoCandidateLimit = 10
	jsonWhoCandidateLimit  = 50
	whoIdentifierLimit     = 3
)

func (c *WhoCmd) Run(r *Runtime) error {
	query := strings.Join(c.Name, " ")
	query = strings.Join(strings.Fields(query), " ")
	if query == "" {
		return usageErr{fmt.Errorf("who requires a name fragment")}
	}

	resolution := collectFederatedWho(r, resolverSources(discoverCrawlers(r.ctx, r.appsDir)), query)
	envelope := WhoEnvelope{
		Query:            query,
		TotalCandidates:  len(resolution.Candidates),
		Candidates:       capWhoCandidates(resolution.Candidates, jsonWhoCandidateLimit),
		DidYouMean:       capWhoCandidates(resolution.DidYouMean, jsonWhoCandidateLimit),
		TotalDidYouMean:  len(resolution.DidYouMean),
		SourcesConsulted: resolution.SourcesConsulted,
		FailedSources:    resolution.FailedSources,
	}
	if r.root.JSON {
		return writeJSON(r.stdout, envelope)
	}
	if err := renderWhoTable(r.stdout, resolution.Candidates); err != nil {
		return err
	}
	r.reportWhoFailures(resolution)
	return whoExit(resolution)
}

func decodeWhoEnvelope(data []byte, fallbackSource string) (WhoEnvelope, error) {
	var raw struct {
		Query      string          `json:"query"`
		Candidates json.RawMessage `json:"candidates"`
	}
	if err := decodeContractJSON(data, &raw); err != nil {
		return WhoEnvelope{}, err
	}
	query := strings.TrimSpace(raw.Query)
	if query == "" {
		return WhoEnvelope{}, errors.New("who query is missing")
	}
	if len(raw.Candidates) == 0 {
		return WhoEnvelope{}, errors.New("who candidates array is missing")
	}
	candidates, err := decodeWhoCandidateList(raw.Candidates, fallbackSource)
	if err != nil {
		return WhoEnvelope{}, err
	}
	return WhoEnvelope{Query: query, Candidates: candidates}, nil
}

func decodeWhoCandidateList(data json.RawMessage, fallbackSource string) ([]WhoCandidate, error) {
	if len(data) == 0 {
		return []WhoCandidate{}, nil
	}
	var rawCandidates []crawlerWhoCandidate
	if err := decodeContractJSON(data, &rawCandidates); err != nil {
		return nil, err
	}
	candidates := make([]WhoCandidate, 0, len(rawCandidates))
	for _, rawCandidate := range rawCandidates {
		candidate := normalizeWhoCandidate(rawCandidate, fallbackSource)
		if candidate.Who == "" {
			continue
		}
		candidates = append(candidates, candidate)
	}
	if candidates == nil {
		candidates = []WhoCandidate{}
	}
	return candidates, nil
}

func normalizeWhoCandidate(raw crawlerWhoCandidate, fallbackSource string) WhoCandidate {
	who := firstNonEmpty(raw.Who, raw.Person, raw.DisplayName, raw.Name, raw.Identity, raw.ID, raw.PersonID)
	matchQuality := canonicalMatchQuality(firstNonEmpty(raw.MatchQuality, raw.NameMatch, raw.Match))
	sources := append([]string(nil), raw.Sources...)
	if len(sources) == 0 && strings.TrimSpace(raw.Source) != "" {
		sources = []string{raw.Source}
	}
	if len(sources) == 0 && strings.TrimSpace(fallbackSource) != "" {
		sources = []string{fallbackSource}
	}
	sources = normalisedStringList(sources)
	identifiers := normalisedStringList(raw.Identifiers)
	lastSeen := firstNonEmpty(raw.LastSeen, raw.LastExchanged, raw.Latest)
	lastSeenParsed, lastSeenOK := parseWhoTime(lastSeen)
	return WhoCandidate{
		Who:            who,
		Identifiers:    identifiers,
		MatchQuality:   matchQuality,
		Sources:        sources,
		LastSeen:       lastSeen,
		Messages:       firstNonZero(raw.Messages, raw.Volume, raw.MessageVolume, raw.MessageCount, raw.TotalMessages),
		lastSeenParsed: lastSeenParsed,
		lastSeenOK:     lastSeenOK,
	}
}

func normalisedStringList(values []string) []string {
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
	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(out[i]) < strings.ToLower(out[j])
	})
	return out
}

func firstNonZero(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func parseWhoTime(value string) (time.Time, bool) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339, value)
	return parsed, err == nil
}

func sortWhoCandidates(candidates []WhoCandidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		left := candidates[i]
		right := candidates[j]
		leftRank, leftOK := matchQualityRank(left.MatchQuality)
		rightRank, rightOK := matchQualityRank(right.MatchQuality)
		if leftOK != rightOK {
			return leftOK
		}
		if leftOK && rightOK && leftRank != rightRank {
			return leftRank.BetterThan(rightRank)
		}
		if left.lastSeenOK != right.lastSeenOK {
			return left.lastSeenOK
		}
		if left.lastSeenOK && !left.lastSeenParsed.Equal(right.lastSeenParsed) {
			return left.lastSeenParsed.After(right.lastSeenParsed)
		}
		if left.Messages != right.Messages {
			return left.Messages > right.Messages
		}
		return strings.ToLower(left.Who) < strings.ToLower(right.Who)
	})
}

func canonicalMatchQuality(value string) string {
	rank, ok := matchQualityRank(value)
	if !ok {
		return "unknown"
	}
	return rank.String()
}

func matchQualityRank(value string) (whomatch.Rank, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "exact":
		return whomatch.RankExact, true
	case "prefix":
		return whomatch.RankPrefix, true
	case "substring", "contains":
		return whomatch.RankSubstring, true
	case "close_spelling", "close-spelling", "close spelling":
		return whomatch.RankCloseSpelling, true
	default:
		return 0, false
	}
}

func renderWhoTable(w io.Writer, candidates []WhoCandidate) error {
	if len(candidates) == 0 {
		_, err := fmt.Fprintln(w, "No people found.")
		return err
	}
	displayed := capWhoCandidates(candidates, humanWhoCandidateLimit)
	rows := make([][]string, 0, len(displayed))
	for _, candidate := range displayed {
		rows = append(rows, []string{
			candidate.Who,
			candidate.MatchQuality,
			whoSources(candidate.Sources),
			whoLastSeen(candidate),
			strconv.FormatInt(int64(candidate.Messages), 10),
			whoIdentifiers(candidate.Identifiers),
		})
	}
	if err := writeFittedTable(w, []string{"WHO", "MATCH", "SOURCES", "LAST SEEN", "MESSAGES", "IDENTIFIERS"}, rows); err != nil {
		return err
	}
	if more := len(candidates) - len(displayed); more > 0 {
		_, err := fmt.Fprintf(w, "…and %d more; narrow the name\n", more)
		return err
	}
	return nil
}

func whoLastSeen(candidate WhoCandidate) string {
	if candidate.lastSeenOK {
		return candidate.lastSeenParsed.Format("2006-01-02")
	}
	return firstNonEmpty(candidate.LastSeen, unknownFreshness)
}

func whoSources(sources []string) string {
	if len(sources) == 0 {
		return unknownFreshness
	}
	return strings.Join(sources, ", ")
}

func whoIdentifiers(identifiers []string) string {
	if len(identifiers) == 0 {
		return unknownFreshness
	}
	displayed := identifiers
	if len(displayed) > whoIdentifierLimit {
		displayed = displayed[:whoIdentifierLimit]
	}
	parts := append([]string(nil), displayed...)
	if more := len(identifiers) - len(displayed); more > 0 {
		parts = append(parts, fmt.Sprintf("+%d more", more))
	}
	return strings.Join(parts, ", ")
}

func capWhoCandidates(candidates []WhoCandidate, limit int) []WhoCandidate {
	if len(candidates) <= limit {
		return candidates
	}
	out := make([]WhoCandidate, limit)
	copy(out, candidates[:limit])
	return out
}
