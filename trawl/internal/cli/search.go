package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultSearchLimit = 20
	maxSearchLimit     = 200
	searchWorkerLimit  = 4
)

type SearchCmd struct {
	Query  []string `arg:"" optional:"" help:"Search words; optional when --who, --after, or --before is present"`
	Source string   `name:"source" help:"Comma-separated source ids"`
	Limit  int      `name:"limit" default:"20" help:"Maximum rows"`
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
	Source  string `json:"source"`
	Ref     string `json:"ref"`
	Time    string `json:"time"`
	Who     string `json:"who"`
	Where   string `json:"where"`
	Snippet string `json:"snippet"`

	ShortRef        string `json:"-"`
	SourceShortRefs bool   `json:"-"`
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

type crawlerSearchResult struct {
	Ref      string `json:"ref"`
	ShortRef string `json:"short_ref,omitempty"`
	Alias    string `json:"alias,omitempty"`
	Time     string `json:"time"`
	Who      string `json:"who"`
	Where    string `json:"where"`
	Snippet  string `json:"snippet"`
}

type crawlerSearchEnvelope struct {
	Query        string                `json:"query"`
	Results      []crawlerSearchResult `json:"results"`
	TotalMatches int                   `json:"total_matches"`
	Truncated    bool                  `json:"truncated"`
}

type federatedSearchEnvelope struct {
	Query          string        `json:"query"`
	WhoResolved    *WhoCandidate `json:"who_resolved,omitempty"`
	Results        []SearchRow   `json:"results"`
	TotalMatches   int           `json:"total_matches"`
	Truncated      bool          `json:"truncated"`
	FailedSources  []string      `json:"failed_sources,omitempty"`
	SkippedSources []string      `json:"skipped_sources,omitempty"`
}

type mergedSearchResult struct {
	Rows         []SearchRow
	TotalMatches int
	Truncated    bool
	More         int
}

func (c *SearchCmd) Run(r *Runtime) error {
	limit := normalizeSearchLimit(c.Limit)
	query, sources, err := r.resolveSearchTarget(c.Query, c.Source)
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
		installed := discoverCrawlers(r.ctx, r.appsDir)
		skippedWho := skippedWhoSources(sources)
		resolution := collectFederatedWho(r, searchResolverSources(installed, sources), whoInput)
		if len(resolution.FailedSources) > 0 {
			r.reportWhoFailures(resolution)
			return exitErr{code: 1}
		}
		switch len(resolution.Candidates) {
		case 0:
			return r.writeUnknownWho(query, whoInput, resolution, skippedWho)
		case 1:
			if closeResolution, ok := closeSpellingOnlyResolution(resolution); ok {
				return r.writeUnknownWho(query, whoInput, closeResolution, skippedWho)
			}
			candidate := resolution.Candidates[0]
			whoResolved = &candidate
			whoBySource = searchWhoFilters(candidate, sources)
			sources = searchSourcesForResolvedWho(sources, whoBySource)
		default:
			return r.writeAmbiguousWho(query, whoInput, resolution, skippedWho)
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
			if err := renderWhoResolutionLine(r.stdout, whoInput, *whoResolved); err != nil {
				return err
			}
		}
		if err := renderSearchTable(r.stdout, merged.Rows, merged.More); err != nil {
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
// starts with a source name still works there.
func (r *Runtime) resolveSearchTarget(words []string, sourceCSV string) (string, []Source, error) {
	installed := discoverCrawlers(r.ctx, r.appsDir)
	if sourceCSV == "" && len(words) >= 2 {
		if source, ok := findSource(installed, words[0]); ok {
			return strings.Join(words[1:], " "), []Source{source}, nil
		}
	}
	if sourceCSV == "" {
		return strings.Join(words, " "), searchable(installed), nil
	}
	sources, err := r.selectedSourcesCSV(sourceCSV)
	if err != nil {
		return "", nil, err
	}
	return strings.Join(words, " "), sources, nil
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
	args := searchCommandArgs(query, options, who)
	args = append(args, r.childVerboseArgs(source)...)
	data, err := r.runSourceCommandJSON(source, args...)
	if err != nil {
		if searchContractErrorCode(data) == "unknown_who" {
			return result
		}
		result.Err = err
		return result
	}
	envelope, err := decodeSearchEnvelope(data)
	if err != nil {
		result.Err = err
		return result
	}
	result.Total = envelope.TotalMatches
	result.Truncated = envelope.Truncated
	result.Rows = make([]SearchRow, 0, len(envelope.Results))
	for _, item := range envelope.Results {
		parsed, ok := parseSearchTime(item.Time)
		result.Rows = append(result.Rows, SearchRow{
			Source:          source.ID,
			Ref:             item.Ref,
			Time:            item.Time,
			Who:             item.Who,
			Where:           item.Where,
			Snippet:         item.Snippet,
			ShortRef:        firstNonEmpty(item.ShortRef, item.Alias),
			SourceShortRefs: hasCapability(source, "short_refs"),
			parsedTime:      parsed,
			timeOK:          ok,
		})
	}
	return result
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

func searchCommandArgs(query string, options searchOptions, who string) []string {
	args := []string{"search"}
	if strings.TrimSpace(query) != "" {
		args = append(args, query)
	}
	args = append(args, "--json", "--limit", strconv.Itoa(options.limit))
	if options.after != "" {
		args = append(args, "--after", options.after)
	}
	if options.before != "" {
		args = append(args, "--before", options.before)
	}
	if who != "" {
		args = append(args, "--who", who)
	}
	return args
}

func decodeSearchEnvelope(data []byte) (crawlerSearchEnvelope, error) {
	var raw struct {
		Query        string          `json:"query"`
		Results      json.RawMessage `json:"results"`
		TotalMatches int             `json:"total_matches"`
		Truncated    bool            `json:"truncated"`
	}
	if err := decodeContractJSON(data, &raw); err != nil {
		return crawlerSearchEnvelope{}, err
	}
	trimmed := bytes.TrimSpace(raw.Results)
	if len(trimmed) == 0 || !bytes.HasPrefix(trimmed, []byte("[")) {
		return crawlerSearchEnvelope{}, errors.New("search results array is missing")
	}
	var results []crawlerSearchResult
	if err := decodeContractJSON(trimmed, &results); err != nil {
		return crawlerSearchEnvelope{}, err
	}
	return crawlerSearchEnvelope{
		Query:        raw.Query,
		Results:      results,
		TotalMatches: raw.TotalMatches,
		Truncated:    raw.Truncated,
	}, nil
}

func searchContractErrorCode(data []byte) string {
	if len(bytes.TrimSpace(data)) == 0 {
		return ""
	}
	var envelope ErrorEnvelope
	if err := decodeContractJSON(data, &envelope); err != nil {
		return ""
	}
	return strings.TrimSpace(envelope.Error.Code)
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

func normalizeSearchLimit(limit int) int {
	if limit <= 0 {
		return defaultSearchLimit
	}
	if limit > maxSearchLimit {
		return maxSearchLimit
	}
	return limit
}

func searchSuccesses(results []searchSourceResult) int {
	successes := 0
	for _, result := range results {
		if result.Err == nil && !result.Skipped {
			successes++
		}
	}
	return successes
}

func searchExit(results []searchSourceResult) error {
	failures := 0
	successes := 0
	for _, result := range results {
		if result.Err != nil || result.Skipped {
			failures++
			continue
		}
		successes++
	}
	if failures == 0 {
		return nil
	}
	if successes > 0 {
		return exitErr{code: 3}
	}
	return exitErr{code: 1}
}

func (r *Runtime) reportSearchFailures(results []searchSourceResult) {
	for _, result := range results {
		if result.Skipped {
			_, _ = fmt.Fprintf(r.stderr, "%s cannot filter by person yet\n", result.Source.ID)
			continue
		}
		if result.Err == nil {
			continue
		}
		remedy := fmt.Sprintf("run: trawl doctor %s", result.Source.ID)
		if logPath := sourceLogPath(result.Source); logPath != "" {
			remedy += "; read " + logPath
		}
		_, _ = fmt.Fprintf(r.stderr, "%s search failed. Remedy: %s\n", result.Source.ID, remedy)
	}
}

func failedSearchSources(results []searchSourceResult) []string {
	var failures []string
	for _, result := range results {
		if result.Err != nil {
			failures = append(failures, result.Source.ID)
		}
	}
	return failures
}

func skippedSearchSources(results []searchSourceResult) []string {
	var skipped []string
	for _, result := range results {
		if result.Skipped {
			skipped = append(skipped, result.Source.ID)
		}
	}
	return skipped
}

func renderSearchPartialNote(w io.Writer, results []searchSourceResult) error {
	failures := len(failedSearchSources(results))
	skipped := len(skippedSearchSources(results))
	blocked := failures + skipped
	if blocked == 0 || blocked == len(results) {
		return nil
	}
	reason := "unavailable"
	if skipped > 0 && failures == 0 {
		reason = "skipped"
	} else if skipped > 0 {
		reason = "skipped or unavailable"
	}
	_, err := fmt.Fprintf(w, "note: %d of %d sources %s — results are partial (see stderr)\n", blocked, len(results), reason)
	return err
}
