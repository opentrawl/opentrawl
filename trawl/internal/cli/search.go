package cli

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/openclaw/crawlkit"
	ckflags "github.com/openclaw/crawlkit/flags"
)

const (
	searchWorkerLimit = 4
)

type SearchCmd struct {
	Query  []string `arg:"" optional:"" help:"Search words; optional when --who, --after, or --before is present"`
	Source string   `name:"source" help:"Comma-separated source ids"`
	Limit  int      `name:"limit" default:"20" help:"Rows to return"`
	After  string   `name:"after" help:"Start date"`
	Before string   `name:"before" help:"End date"`
	Who    string   `name:"who" placeholder:"person" help:"Resolve a person or sender, then filter by the exact match"`
}

func (SearchCmd) Help() string {
	return `Examples:
  trawl search invoice --who alex
  trawl search --who "Vendor Support" --after 2026-01-01`
}

type searchOptions struct {
	limit       int
	after       string
	before      string
	who         string
	whoBySource map[string]string
}

type SearchRow struct {
	Source string `json:"source"`
	Ref    string `json:"ref"`
	// ShortRef is human display sugar only. short-refs.md keeps trawl's
	// federated --json on the canonical ref so agents never pick up the
	// weaker, expiring alias; the crawler-level search contract still
	// carries short_ref through crawlkit.Hit.
	ShortRef string `json:"-"`
	Time     string `json:"time"`
	AllDay   bool   `json:"all_day,omitempty"`
	Who      string `json:"who"`
	Where    string `json:"where"`
	Snippet  string `json:"snippet"`

	surface         string
	sourceShortRefs bool
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

type federatedSearchEnvelope struct {
	Query          string         `json:"query"`
	WhoResolved    *WhoCandidate  `json:"who_resolved,omitempty"`
	FailedSources  []failedSource `json:"failed_sources,omitempty"`
	SkippedSources []string       `json:"skipped_sources,omitempty"`
	Results        []SearchRow    `json:"results"`
	TotalMatches   int            `json:"total_matches"`
	Truncated      bool           `json:"truncated"`
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
	if len(sources) == 0 {
		if r.root.JSON {
			if err := writeJSON(r.stdout, emptySearchEnvelope(query)); err != nil {
				return err
			}
		} else if _, err := fmt.Fprintln(r.stdout, "No crawlers found."); err != nil {
			return err
		}
		return nil
	}

	var whoResolved *WhoCandidate
	whoBySource := map[string]string(nil)
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
			whoBySource = searchWhoFilters(candidate, sources)
			sources = searchSourcesForResolvedWho(sources, whoBySource)
		default:
			return r.writeAmbiguousWho(query, whoInput, resolution, skippedWho, surfaceNames(installed))
		}
	}

	results := collectSearch(r, sources, query, searchOptions{
		limit:       limit,
		after:       c.After,
		before:      c.Before,
		who:         whoInput,
		whoBySource: whoBySource,
	})
	merged := mergedSearchRows(results, limit)
	r.reportSearchFailures(results)
	if r.root.JSON {
		if err := writeJSON(r.stdout, federatedSearchEnvelope{
			Query:          query,
			WhoResolved:    whoResolved,
			Results:        merged.Rows,
			TotalMatches:   merged.TotalMatches,
			Truncated:      merged.Truncated,
			FailedSources:  failedSearchSources(results),
			SkippedSources: skippedSearchSources(results),
		}); err != nil {
			return err
		}
		return searchExit(results)
	}
	if len(merged.Rows) > 0 || searchSuccesses(results) > 0 {
		if whoResolved != nil {
			if err := renderWhoResolutionLine(r.stdout, whoInput, *whoResolved, surfaceNames(installed)); err != nil {
				return err
			}
		}
		if err := renderSearchResults(r.stdout, merged, searchListContext{
			Query:   query,
			Who:     resolvedWhoName(whoResolved),
			MoreCmd: c.moreCommand(query, sourceScope, merged.Rows),
		}); err != nil {
			return err
		}
		if err := renderSearchPartialNote(r.stdout, results); err != nil {
			return err
		}
	}
	return searchExit(results)
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
		return strings.Join(words, " "), searchable(installed), "", nil
	}
	sources, err := r.selectSources(installed, splitSourceCSV(sourceCSV))
	if err != nil {
		return "", nil, "", err
	}
	return strings.Join(words, " "), sources, sourceCSV, nil
}

// searchable keeps sources that declare the search capability; a
// contact layer or an uninitialised crawler is not a search failure.
func searchable(sources []Source) []Source {
	var out []Source
	for _, source := range sources {
		if hasCapability(source, "search") {
			out = append(out, source)
		}
	}
	return out
}

func hasCapability(source Source, capability string) bool {
	for _, candidate := range source.Capabilities {
		if strings.EqualFold(strings.TrimSpace(candidate), capability) {
			return true
		}
	}
	return false
}

func emptySearchEnvelope(query string) federatedSearchEnvelope {
	return federatedSearchEnvelope{
		Query:         query,
		Results:       []SearchRow{},
		TotalMatches:  0,
		Truncated:     false,
		FailedSources: nil,
	}
}

func collectSearch(r *Runtime, sources []Source, query string, options searchOptions) []searchSourceResult {
	results := make([]searchSourceResult, len(sources))
	workers := searchWorkerLimit
	if len(sources) < workers {
		workers = len(sources)
	}

	jobs := make(chan int)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				results[index] = r.searchSource(sources[index], query, options)
			}
		}()
	}
	for index := range sources {
		jobs <- index
	}
	close(jobs)
	wg.Wait()
	return results
}

func (r *Runtime) searchSource(source Source, query string, options searchOptions) searchSourceResult {
	result := searchSourceResult{Source: source}
	started := r.now()
	r.logInfo("source_start", strings.Join([]string{
		sourceField(source),
		"verb=search",
	}, " "))
	defer func() {
		r.logSearchOutcome(source, result, started)
	}()
	if source.MetadataErr != nil {
		result.Err = source.MetadataErr
		return result
	}
	who := strings.TrimSpace(options.who)
	if options.whoBySource != nil {
		who = strings.TrimSpace(options.whoBySource[source.ID])
	}
	if options.who != "" && !hasCapability(source, "who") {
		result.Skipped = true
		result.SkipReason = "cannot_filter_who"
		return result
	}
	if options.who != "" && options.whoBySource != nil && who == "" {
		return result
	}
	crawlQuery, err := crawlkitSearchQuery(query, options, who)
	if err != nil {
		result.Err = err
		return result
	}
	searcher, ok := source.Crawler.(crawlkit.Searcher)
	if !ok {
		result.Err = fmt.Errorf("source does not support search")
		return result
	}
	var envelope crawlkit.SearchResult
	err = r.withSourceRequest(source, "search", sourceStoreRead, outputFormat(true), io.Discard, func(ctx context.Context, req *crawlkit.Request) error {
		var searchErr error
		envelope, searchErr = searcher.Search(ctx, req, crawlQuery)
		return searchErr
	})
	if err != nil {
		if sourceErrorBody(err).Code == "unknown_who" {
			return result
		}
		result.Err = err
		return result
	}
	result.Total = envelope.TotalMatches
	result.Truncated = envelope.Truncated
	result.Rows = make([]SearchRow, 0, len(envelope.Results))
	for _, item := range envelope.Results {
		itemTime := formatSearchTime(item.Time)
		parsed, ok := parseSearchTime(itemTime)
		result.Rows = append(result.Rows, SearchRow{
			Source:          source.ID,
			Ref:             item.Ref,
			Time:            itemTime,
			AllDay:          item.AllDay,
			Who:             item.Who,
			Where:           item.Where,
			Snippet:         item.Snippet,
			ShortRef:        item.ShortRef,
			surface:         firstNonEmpty(source.DisplayName, source.ID),
			sourceShortRefs: hasCapability(source, "short_refs"),
			parsedTime:      parsed,
			timeOK:          ok,
		})
	}
	return result
}

func crawlkitSearchQuery(query string, options searchOptions, who string) (crawlkit.Query, error) {
	after, err := parseSearchDateFlag("--after", options.after)
	if err != nil {
		return crawlkit.Query{}, err
	}
	before, err := parseSearchDateFlag("--before", options.before)
	if err != nil {
		return crawlkit.Query{}, err
	}
	return crawlkit.Query{
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

func formatSearchTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format(time.RFC3339)
}

func (r *Runtime) logSearchOutcome(source Source, result searchSourceResult, started time.Time) {
	fields := []string{sourceField(source), "verb=search", elapsedField(started, r.now())}
	switch {
	case result.Skipped:
		fields = append(fields, "outcome=skipped", "reason="+logQuote(result.SkipReason))
	case result.Err != nil:
		if isTimeoutError(result.Err) {
			fields = append(fields, "outcome=timeout")
		} else {
			fields = append(fields, "outcome=error")
		}
		fields = append(fields, "error="+logQuote(result.Err.Error()))
	default:
		fields = append(fields, "outcome=ok", "results="+strconv.Itoa(len(result.Rows)), "total="+strconv.Itoa(result.Total))
	}
	r.logInfo("source_done", strings.Join(fields, " "))
}

func searchSourcesForResolvedWho(sources []Source, whoBySource map[string]string) []Source {
	if whoBySource == nil {
		return sources
	}
	out := make([]Source, 0, len(sources))
	for _, source := range sources {
		if source.MetadataErr != nil || !hasCapability(source, "who") {
			out = append(out, source)
			continue
		}
		if strings.TrimSpace(whoBySource[source.ID]) != "" {
			out = append(out, source)
		}
	}
	return out
}

func mergedSearchRows(results []searchSourceResult, limit int) mergedSearchResult {
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
	stableSearchSort(rows)
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

func parseSearchTime(value string) (time.Time, bool) {
	parsed, err := time.Parse(time.RFC3339, value)
	return parsed, err == nil
}

func normalizeSearchLimit(limit int) (int, error) {
	if limit <= 0 {
		return 0, usageErr{fmt.Errorf("search --limit must be at least 1")}
	}
	return limit, nil
}
