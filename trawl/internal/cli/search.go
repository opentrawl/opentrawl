package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"
)

const (
	defaultSearchLimit = 20
	maxSearchLimit     = 200
	searchWorkerLimit  = 4
)

type SearchCmd struct {
	Query  string `arg:"" help:"Search query"`
	Source string `name:"source" help:"Comma-separated source ids"`
	Limit  int    `name:"limit" default:"20" help:"Maximum rows"`
	After  string `name:"after" help:"Start date"`
	Before string `name:"before" help:"End date"`
}

type searchOptions struct {
	limit  int
	after  string
	before string
}

type SearchRow struct {
	Source  string `json:"source"`
	Ref     string `json:"ref"`
	Time    string `json:"time"`
	Who     string `json:"who"`
	Where   string `json:"where"`
	Snippet string `json:"snippet"`

	parsedTime time.Time
	timeOK     bool
}

type searchSourceResult struct {
	Source    Source
	Rows      []SearchRow
	Total     int
	Truncated bool
	Err       error
}

type crawlerSearchResult struct {
	Ref     string `json:"ref"`
	Time    string `json:"time"`
	Who     string `json:"who"`
	Where   string `json:"where"`
	Snippet string `json:"snippet"`
}

type crawlerSearchEnvelope struct {
	Query        string                `json:"query"`
	Results      []crawlerSearchResult `json:"results"`
	TotalMatches int                   `json:"total_matches"`
	Truncated    bool                  `json:"truncated"`
}

type federatedSearchEnvelope struct {
	Query        string      `json:"query"`
	Results      []SearchRow `json:"results"`
	TotalMatches int         `json:"total_matches"`
	Truncated    bool        `json:"truncated"`
}

type mergedSearchResult struct {
	Rows         []SearchRow
	TotalMatches int
	Truncated    bool
	More         int
}

func (c *SearchCmd) Run(r *Runtime) error {
	limit := normalizeSearchLimit(c.Limit)
	sources, err := r.selectedSourcesCSV(c.Source)
	if err != nil {
		return err
	}
	if len(sources) == 0 {
		if r.root.JSON {
			if err := writeJSON(r.stdout, emptySearchEnvelope(c.Query)); err != nil {
				return err
			}
		} else if _, err := fmt.Fprintln(r.stdout, "No crawlers found."); err != nil {
			return err
		}
		return nil
	}

	results := collectSearch(r.ctx, sources, c.Query, searchOptions{
		limit:  limit,
		after:  c.After,
		before: c.Before,
	})
	merged := mergedSearchRows(results, limit)
	if r.root.JSON {
		if err := writeJSON(r.stdout, federatedSearchEnvelope{
			Query:        c.Query,
			Results:      merged.Rows,
			TotalMatches: merged.TotalMatches,
			Truncated:    merged.Truncated,
		}); err != nil {
			return err
		}
		r.reportSearchFailures(results)
		return searchExit(results)
	}
	if len(merged.Rows) > 0 || searchSuccesses(results) > 0 {
		if err := renderSearchTable(r.stdout, merged.Rows, merged.More); err != nil {
			return err
		}
	}
	r.reportSearchFailures(results)
	return searchExit(results)
}

func emptySearchEnvelope(query string) federatedSearchEnvelope {
	return federatedSearchEnvelope{
		Query:        query,
		Results:      []SearchRow{},
		TotalMatches: 0,
		Truncated:    false,
	}
}

func collectSearch(ctx context.Context, sources []Source, query string, options searchOptions) []searchSourceResult {
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
				results[index] = searchSource(ctx, sources[index], query, options)
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

func searchSource(ctx context.Context, source Source, query string, options searchOptions) searchSourceResult {
	result := searchSourceResult{Source: source}
	if source.MetadataErr != nil {
		result.Err = source.MetadataErr
		return result
	}
	args := searchCommandArgs(query, options)
	data, err := runCrawlerCommandJSON(ctx, source.Path, args...)
	if err != nil {
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
			Source:     source.ID,
			Ref:        item.Ref,
			Time:       item.Time,
			Who:        item.Who,
			Where:      item.Where,
			Snippet:    item.Snippet,
			parsedTime: parsed,
			timeOK:     ok,
		})
	}
	return result
}

func searchCommandArgs(query string, options searchOptions) []string {
	args := []string{"search", query, "--json", "--limit", strconv.Itoa(options.limit)}
	if options.after != "" {
		args = append(args, "--after", options.after)
	}
	if options.before != "" {
		args = append(args, "--before", options.before)
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

func mergedSearchRows(results []searchSourceResult, limit int) mergedSearchResult {
	var rows []SearchRow
	total := 0
	truncated := false
	for _, result := range results {
		if result.Err != nil {
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
		if result.Err == nil {
			successes++
		}
	}
	return successes
}

func searchExit(results []searchSourceResult) error {
	failures := 0
	successes := 0
	for _, result := range results {
		if result.Err != nil {
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
		if result.Err == nil {
			continue
		}
		_, _ = fmt.Fprintf(r.stderr, "%s search failed. Remedy: run: trawl doctor %s\n", result.Source.ID, result.Source.ID)
	}
}
