package cli

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit"
	ckflags "github.com/opentrawl/opentrawl/trawlkit/flags"
)

type searchSortMode string

const (
	searchSortRelevance searchSortMode = "relevance"
	searchSortRecency   searchSortMode = "recency"

	searchQueryDefaultSort = searchSortRecency
)

type SearchCmd struct {
	Query  []string `arg:"" optional:"" help:"Search words; optional when --who, --after, or --before is present"`
	Source string   `name:"source" help:"Comma-separated source ids"`
	Limit  int      `name:"limit" default:"20" help:"Rows to return"`
	After  string   `name:"after" help:"Start date"`
	Before string   `name:"before" help:"End date"`
	Who    string   `name:"who" placeholder:"person" help:"Resolve a person or sender, then filter by the exact match"`
	Sort   string   `name:"sort" placeholder:"mode" help:"Sort rows by relevance or recency"`
}

func (SearchCmd) Help() string {
	return `Examples:
  trawl search invoice --who alex
  trawl search --who "Vendor Support" --after 2026-01-01`
}

type searchOptions struct {
	limit  int
	after  string
	before string
}

type SearchRow struct {
	Source   string `json:"source"`
	Ref      string `json:"ref"`
	AnchorID string `json:"anchor_id"`
	// ShortRef is human display sugar only. short-refs.md keeps trawl's
	// federated --json on the canonical ref so scripts never pick up the
	// weaker, expiring alias; the crawler-level search contract still
	// carries short_ref through trawlkit.Hit.
	ShortRef     string                      `json:"-"`
	Time         string                      `json:"time"`
	AllDay       bool                        `json:"all_day,omitempty"`
	Summary      trawlkit.ResultSummary      `json:"summary"`
	Archive      []trawlkit.ArchiveContext   `json:"archive_context,omitempty"`
	Evidence     []trawlkit.EvidenceFragment `json:"evidence"`
	Availability *int64                      `json:"availability,omitempty"`

	surface         string
	sourceShortRefs bool
	sourceRank      int
	parsedTime      time.Time
	timeOK          bool
}

type searchSourceResult struct {
	Source     Source
	Rows       []SearchRow
	Total      int
	Truncated  bool
	Skipped    bool
	SkipReason string
	Err        error
}

type mergedSearchResult struct {
	Rows         []SearchRow
	TotalMatches int
	Truncated    bool
	More         int
}

func (c *SearchCmd) Run(r *Runtime) error {
	limit, err := normalizeSearchLimit(c.Limit)
	if err != nil {
		return err
	}
	installed := discoverCrawlers(r.ctx)
	query, sources, sourceScope, err := r.resolveSearchTarget(installed, c.Query, c.Source)
	if err != nil {
		return err
	}
	whoInput := strings.TrimSpace(c.Who)
	if strings.TrimSpace(query) == "" && whoInput == "" && strings.TrimSpace(c.After) == "" && strings.TrimSpace(c.Before) == "" {
		return usageErr{fmt.Errorf("search requires a query or at least one filter (--who, --after, --before)")}
	}
	sortMode, err := resolveSearchSort(query, c.Sort)
	if err != nil {
		return err
	}
	if len(sources) == 0 {
		if r.root.JSON {
			response := r.canonicalSearch(nil, trawlkit.Query{Text: strings.TrimSpace(query), Limit: limit}, federationOrder(sortMode), limit)
			if err := writeCanonicalJSON(r.stdout, response); err != nil {
				return err
			}
			return outcomeExit(response.GetOutcome())
		} else if _, err := fmt.Fprintln(r.stdout, "No crawlers found."); err != nil {
			return err
		}
		return nil
	}

	var whoResolved *WhoCandidate
	var whoBySource map[string]string
	if whoInput != "" {
		skippedWho := skippedWhoSources(sources)
		resolution := collectFederatedWho(r, searchResolverSources(installed, sources), whoInput)
		if len(resolution.FailedSources) > 0 {
			r.reportWhoFailures(resolution)
			return exitErr{code: 1}
		}
		switch len(resolution.Candidates) {
		case 0:
			return r.writeUnknownWho(query, whoInput, resolution, skippedWho, surfaceNames(installed))
		case 1:
			if closeResolution, ok := closeSpellingOnlyResolution(resolution); ok {
				return r.writeUnknownWho(query, whoInput, closeResolution, skippedWho, surfaceNames(installed))
			}
			candidate := resolution.Candidates[0]
			whoResolved = &candidate
			whoBySource = make(map[string]string, len(candidate.sourceFilters))
			for sourceID, identifier := range candidate.sourceFilters {
				if containsWhoIdentifier(candidate.Identifiers, identifier) {
					whoBySource[sourceID] = identifier
				}
			}
		default:
			return r.writeAmbiguousWho(query, whoInput, resolution, skippedWho, surfaceNames(installed))
		}
	}

	crawlQuery, err := trawlkitSearchQuery(query, searchOptions{
		limit:  limit,
		after:  c.After,
		before: c.Before,
	}, "")
	if err != nil {
		return err
	}
	adapters := r.federationSearchSources(sources)
	if whoResolved != nil {
		adapters = wrapWhoSearchSources(adapters, whoBySource)
	}
	response := r.canonicalSearch(adapters, crawlQuery, federationOrder(sortMode), limit)
	if r.root.JSON {
		if err := writeCanonicalJSON(r.stdout, response); err != nil {
			return err
		}
		return outcomeExit(response.GetOutcome())
	}
	merged := searchRowsFromResponse(sources, response)
	if len(merged.Rows) > 0 || len(response.GetSources()) > 0 {
		if whoResolved != nil {
			if err := renderWhoResolutionLine(r.stdout, whoInput, *whoResolved, surfaceNames(installed)); err != nil {
				return err
			}
		}
		if err := renderSearchResults(r.stdout, merged, searchListContext{
			Query:   query,
			Who:     resolvedWhoName(whoResolved),
			Sort:    sortMode,
			MoreCmd: c.moreCommand(query, sourceScope, merged.Rows),
		}); err != nil {
			return err
		}
	}
	r.reportFederationOutcomes(response.GetFailures(), response.GetSkippedSources(), "search")
	return outcomeExit(response.GetOutcome())
}

func containsWhoIdentifier(identifiers []string, value string) bool {
	value = strings.TrimSpace(value)
	for _, identifier := range identifiers {
		if strings.EqualFold(strings.TrimSpace(identifier), value) {
			return true
		}
	}
	return false
}

// resolveSearchTarget joins the query words, honouring one convenience:
// when the first word names an installed source and more words follow,
// it scopes the search — `trawl search imessage dinner` reads the way
// people type it. --source always wins, and a query that genuinely
// starts with a source name still works there. The returned scope is
// the --source value that reproduces the selection ("" for all).
func (r *Runtime) resolveSearchTarget(installed []Source, words []string, sourceCSV string) (string, []Source, string, error) {
	sourceCSV = strings.TrimSpace(sourceCSV)
	if sourceCSV == "" && len(words) >= 2 {
		if source, ok := findSource(installed, words[0]); ok {
			// The scope echoes the token the user typed (a surface name
			// like "imessage" stays a surface name in the More: hint).
			return strings.Join(words[1:], " "), []Source{source}, words[0], nil
		}
	}
	if sourceCSV == "" {
		return strings.Join(words, " "), installed, "", nil
	}
	sources, err := r.selectSources(installed, splitSourceCSV(sourceCSV))
	if err != nil {
		return "", nil, "", err
	}
	return strings.Join(words, " "), sources, sourceCSV, nil
}

func hasCapability(source Source, capability string) bool {
	for _, candidate := range source.Capabilities {
		if strings.EqualFold(strings.TrimSpace(candidate), capability) {
			return true
		}
	}
	return false
}

func trawlkitSearchQuery(query string, options searchOptions, who string) (trawlkit.Query, error) {
	after, err := parseSearchDateFlag("--after", options.after)
	if err != nil {
		return trawlkit.Query{}, err
	}
	before, err := parseSearchDateFlag("--before", options.before)
	if err != nil {
		return trawlkit.Query{}, err
	}
	return trawlkit.Query{
		Text:   strings.TrimSpace(query),
		Limit:  options.limit,
		After:  after,
		Before: before,
		Who:    strings.TrimSpace(who),
	}, nil
}

func parseSearchDateFlag(name, raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	parsed, err := ckflags.Date(raw)
	if err != nil {
		return time.Time{}, usageErr{fmt.Errorf("%s: %w", name, err)}
	}
	if name == "--before" {
		if day, err := time.ParseInLocation("2006-01-02", raw, time.Local); err == nil {
			return day.Add(24*time.Hour - time.Second).UTC(), nil
		}
	}
	return parsed, nil
}

func mergedSearchRows(results []searchSourceResult, limit int, sortMode searchSortMode) mergedSearchResult {
	var rows []SearchRow
	total := 0
	truncated := false
	for _, result := range results {
		if result.Err != nil || result.Skipped {
			continue
		}
		rows = append(rows, result.Rows...)
		total += result.Total
		truncated = truncated || result.Truncated
	}
	switch sortMode {
	case searchSortRelevance:
		rankTierSort(rows)
	default:
		stableSearchSort(rows)
	}
	if len(rows) > limit {
		rows = rows[:limit]
		truncated = true
	}
	more := total - len(rows)
	if more < 0 {
		more = 0
	}
	if rows == nil {
		rows = []SearchRow{}
	}
	return mergedSearchResult{
		Rows:         rows,
		TotalMatches: total,
		Truncated:    truncated,
		More:         more,
	}
}

func stableSearchSort(rows []SearchRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		left := rows[i]
		right := rows[j]
		if left.timeOK != right.timeOK {
			return left.timeOK
		}
		if !left.timeOK {
			return false
		}
		return left.parsedTime.After(right.parsedTime)
	})
}

func rankTierSort(rows []SearchRow) {
	sort.Slice(rows, func(i, j int) bool {
		left := rows[i]
		right := rows[j]
		if left.sourceRank != right.sourceRank {
			return left.sourceRank < right.sourceRank
		}
		if left.timeOK != right.timeOK {
			return left.timeOK
		}
		if left.timeOK && !left.parsedTime.Equal(right.parsedTime) {
			return left.parsedTime.After(right.parsedTime)
		}
		if left.Source != right.Source {
			return left.Source < right.Source
		}
		return left.Ref < right.Ref
	})
}

func parseSearchTime(value string) (time.Time, bool) {
	parsed, err := time.Parse(time.RFC3339, value)
	return parsed, err == nil
}

func resolveSearchSort(query, raw string) (searchSortMode, error) {
	query = strings.TrimSpace(query)
	switch strings.TrimSpace(raw) {
	case "":
		if query == "" {
			return searchSortRecency, nil
		}
		return searchQueryDefaultSort, nil
	case string(searchSortRelevance):
		if query == "" {
			return searchSortRecency, nil
		}
		return searchSortRelevance, nil
	case string(searchSortRecency):
		return searchSortRecency, nil
	default:
		return "", usageErr{fmt.Errorf("search --sort must be relevance or recency")}
	}
}

func normalizeSearchLimit(limit int) (int, error) {
	if limit <= 0 {
		return 0, usageErr{fmt.Errorf("search --limit must be at least 1")}
	}
	return limit, nil
}
