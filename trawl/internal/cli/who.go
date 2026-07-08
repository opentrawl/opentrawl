package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/openclaw/crawlkit/render"
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
	FailedSources    []failedSource `json:"failed_sources,omitempty"`
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
	Who          string   `json:"who"`
	Identifiers  []string `json:"identifiers"`
	MatchQuality string   `json:"match_quality"`
	Sources      []string `json:"sources"`
	LastSeen     string   `json:"last_seen"`
	Messages     int      `json:"messages"`
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

	installed := discoverCrawlers(r.ctx)
	resolution := collectFederatedWho(r, resolverSources(installed), query)
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
		if err := writeJSON(r.stdout, envelope); err != nil {
			return err
		}
		r.reportWhoFailures(resolution)
		return whoExit(resolution)
	}
	if len(resolution.Candidates) == 0 {
		if _, err := fmt.Fprintf(r.stdout, "No person matched %q.\n", query); err != nil {
			return err
		}
	} else if err := renderWhoTable(r.stdout, resolution.Candidates, surfaceNames(installed)); err != nil {
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
	sources := normalisedStringList(raw.Sources)
	if len(sources) == 0 && strings.TrimSpace(fallbackSource) != "" {
		sources = []string{fallbackSource}
	}
	lastSeenParsed, lastSeenOK := parseWhoTime(raw.LastSeen)
	return WhoCandidate{
		Who:            raw.Who,
		Identifiers:    normalisedStringList(raw.Identifiers),
		MatchQuality:   canonicalMatchQuality(raw.MatchQuality),
		Sources:        sources,
		LastSeen:       raw.LastSeen,
		Messages:       raw.Messages,
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

func renderWhoTable(w io.Writer, candidates []WhoCandidate, surfaces map[string]string) error {
	if len(candidates) == 0 {
		return nil
	}
	displayed := capWhoCandidates(candidates, humanWhoCandidateLimit)
	rows := make([][]string, 0, len(displayed))
	for _, candidate := range displayed {
		rows = append(rows, []string{
			candidate.Who,
			humanLabel(candidate.MatchQuality),
			whoSources(candidate.Sources, surfaces),
			whoLastSeen(candidate),
			render.FormatInteger(int64(candidate.Messages)),
			whoIdentifiers(candidate.Identifiers),
		})
	}
	if err := render.WriteTable(w, []render.TableColumn{
		{Header: "who"},
		{Header: "match"},
		{Header: "sources"},
		{Header: "last seen"},
		{Header: "items", AlignRight: true},
		{Header: "identifiers"},
	}, rows); err != nil {
		return err
	}
	if more := len(candidates) - len(displayed); more > 0 {
		_, err := fmt.Fprintf(w, "…and %s more; narrow the name\n", render.FormatInteger(int64(more)))
		return err
	}
	return nil
}

func whoLastSeen(candidate WhoCandidate) string {
	if candidate.lastSeenOK {
		return render.ShortLocalTime(candidate.lastSeenParsed)
	}
	return candidate.LastSeen
}

func whoSources(sources []string, surfaces map[string]string) string {
	if len(sources) == 0 {
		return ""
	}
	named := make([]string, 0, len(sources))
	for _, source := range sources {
		named = append(named, firstNonEmpty(surfaces[source], source))
	}
	return strings.Join(normalisedStringList(named), ", ")
}

func whoIdentifiers(identifiers []string) string {
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
