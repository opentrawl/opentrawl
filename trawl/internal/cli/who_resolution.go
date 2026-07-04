package cli

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"

	"github.com/openclaw/crawlkit/whomatch"
)

const whoWorkerLimit = 4

type whoSourceResult struct {
	Source     Source
	Candidates []WhoCandidate
	DidYouMean []WhoCandidate
	Err        error
}

type federatedWhoResolution struct {
	Query            string
	Candidates       []WhoCandidate
	DidYouMean       []WhoCandidate
	SourcesConsulted []string
	FailedSources    []string
}

type whoRecord struct {
	Candidate   WhoCandidate
	Origin      string
	FromClawdex bool
}

type whoGroup struct {
	Candidate   WhoCandidate
	FromClawdex bool
}

type whoResolutionErrorEnvelope struct {
	Error            ErrorBody      `json:"error"`
	TotalCandidates  int            `json:"total_candidates,omitempty"`
	Candidates       []WhoCandidate `json:"candidates,omitempty"`
	TotalDidYouMean  int            `json:"total_did_you_mean,omitempty"`
	DidYouMean       []WhoCandidate `json:"did_you_mean,omitempty"`
	SourcesConsulted []string       `json:"sources_consulted"`
	SkippedSources   []string       `json:"skipped_sources,omitempty"`
	Hint             string         `json:"hint,omitempty"`
}

func resolverSources(sources []Source) []Source {
	out := make([]Source, 0, len(sources))
	seen := map[string]bool{}
	for _, source := range sources {
		if source.MetadataErr != nil {
			continue
		}
		if !hasCapability(source, "who") && !isClawdex(source) {
			continue
		}
		key := sourceKey(source)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, source)
	}
	return out
}

func searchResolverSources(installed, searchSources []Source) []Source {
	out := make([]Source, 0, len(searchSources)+1)
	seen := map[string]bool{}
	add := func(source Source) {
		if source.MetadataErr != nil {
			return
		}
		key := sourceKey(source)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, source)
	}
	for _, source := range searchSources {
		if hasCapability(source, "who") {
			add(source)
		}
	}
	if clawdex, ok := findSource(installed, "clawdex"); ok && clawdex.MetadataErr == nil {
		add(clawdex)
	}
	return out
}

func sourceKey(source Source) string {
	return firstNonEmpty(source.ID, source.Binary, source.Path)
}

func isClawdex(source Source) bool {
	return strings.EqualFold(source.ID, "clawdex") || strings.EqualFold(source.Binary, "clawdex")
}

func collectFederatedWho(ctx context.Context, sources []Source, query string) federatedWhoResolution {
	results := collectWho(ctx, sources, query)
	records := make([]whoRecord, 0)
	suggestionRecords := make([]whoRecord, 0)
	var consulted []string
	var failed []string
	for _, result := range results {
		if result.Err != nil {
			failed = append(failed, result.Source.ID)
			continue
		}
		consulted = append(consulted, result.Source.ID)
		for _, candidate := range result.Candidates {
			records = append(records, whoRecord{
				Candidate:   candidate,
				Origin:      result.Source.ID,
				FromClawdex: isClawdex(result.Source),
			})
		}
		for _, candidate := range result.DidYouMean {
			suggestionRecords = append(suggestionRecords, whoRecord{
				Candidate:   candidate,
				Origin:      result.Source.ID,
				FromClawdex: isClawdex(result.Source),
			})
		}
	}
	candidates := mergeWhoRecords(records)
	didYouMean := mergeWhoRecords(suggestionRecords)
	return federatedWhoResolution{
		Query:            query,
		Candidates:       candidates,
		DidYouMean:       didYouMean,
		SourcesConsulted: normalisedStringList(consulted),
		FailedSources:    normalisedStringList(failed),
	}
}

func closeSpellingOnlyResolution(resolution federatedWhoResolution) (federatedWhoResolution, bool) {
	if len(resolution.Candidates) != 1 {
		return resolution, false
	}
	rank, ok := matchQualityRank(resolution.Candidates[0].MatchQuality)
	if !ok || rank != whomatch.RankCloseSpelling {
		return resolution, false
	}
	resolution.DidYouMean = didYouMeanWithCandidate(resolution.Candidates[0], resolution.DidYouMean)
	resolution.Candidates = []WhoCandidate{}
	return resolution, true
}

func didYouMeanWithCandidate(candidate WhoCandidate, suggestions []WhoCandidate) []WhoCandidate {
	records := make([]whoRecord, 0, 1+len(suggestions))
	records = append(records, whoRecord{Candidate: candidate})
	for _, suggestion := range suggestions {
		records = append(records, whoRecord{Candidate: suggestion})
	}
	return mergeWhoRecords(records)
}

func collectWho(ctx context.Context, sources []Source, query string) []whoSourceResult {
	results := make([]whoSourceResult, len(sources))
	workers := whoWorkerLimit
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
				results[index] = whoSource(ctx, sources[index], query)
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

func whoSource(ctx context.Context, source Source, query string) whoSourceResult {
	result := whoSourceResult{Source: source}
	data, err := runCrawlerJSONWithArgs(ctx, source.Path, "who", query)
	if err != nil {
		result.Err = err
		return result
	}
	envelope, err := decodeWhoEnvelope(data, source.ID)
	if err != nil {
		result.Err = err
		return result
	}
	result.Candidates = envelope.Candidates
	result.DidYouMean = envelope.DidYouMean
	return result
}

func mergeWhoRecords(records []whoRecord) []WhoCandidate {
	if len(records) == 0 {
		return []WhoCandidate{}
	}
	var groups []*whoGroup
	nameIndex := map[string]*whoGroup{}
	clawdexIdentifierIndex := map[string]*whoGroup{}
	addGroup := func(record whoRecord) *whoGroup {
		group := &whoGroup{}
		mergeWhoRecord(group, record)
		groups = append(groups, group)
		nameKey := normalisePersonName(group.Candidate.Who)
		if nameKey != "" {
			nameIndex[nameKey] = group
		}
		if group.FromClawdex {
			for _, identifier := range group.Candidate.Identifiers {
				if key := normaliseIdentifier(identifier); key != "" {
					clawdexIdentifierIndex[key] = group
				}
			}
		}
		return group
	}
	findByClawdexIdentifier := func(candidate WhoCandidate) *whoGroup {
		for _, identifier := range candidate.Identifiers {
			if group := clawdexIdentifierIndex[normaliseIdentifier(identifier)]; group != nil {
				return group
			}
		}
		return nil
	}
	for _, record := range records {
		if !record.FromClawdex {
			continue
		}
		nameKey := normalisePersonName(record.Candidate.Who)
		group := nameIndex[nameKey]
		if group == nil {
			group = addGroup(record)
		} else {
			mergeWhoRecord(group, record)
		}
		for _, identifier := range group.Candidate.Identifiers {
			if key := normaliseIdentifier(identifier); key != "" {
				clawdexIdentifierIndex[key] = group
			}
		}
	}
	// The clawdex upgrade join is deliberately narrow: identifier overlap
	// plus exact normalized-name equality. Sparse clawdex identifiers mean
	// sparse joins until contact imports enrich the person layer; do not
	// widen matching here to compensate for sparse data.
	for _, record := range records {
		if record.FromClawdex {
			continue
		}
		group := findByClawdexIdentifier(record.Candidate)
		if group == nil {
			group = nameIndex[normalisePersonName(record.Candidate.Who)]
		}
		if group == nil {
			group = addGroup(record)
		} else {
			mergeWhoRecord(group, record)
		}
		nameKey := normalisePersonName(group.Candidate.Who)
		if nameKey != "" {
			nameIndex[nameKey] = group
		}
	}
	candidates := make([]WhoCandidate, 0, len(groups))
	for _, group := range groups {
		group.Candidate.Identifiers = normalisedStringList(group.Candidate.Identifiers)
		group.Candidate.Sources = normalisedStringList(group.Candidate.Sources)
		candidates = append(candidates, group.Candidate)
	}
	sortWhoCandidates(candidates)
	return candidates
}

func mergeWhoRecord(group *whoGroup, record whoRecord) {
	candidate := record.Candidate
	if group.Candidate.Who == "" || (record.FromClawdex && !group.FromClawdex) {
		group.Candidate.Who = candidate.Who
	}
	group.Candidate.Identifiers = append(group.Candidate.Identifiers, candidate.Identifiers...)
	group.Candidate.Sources = append(group.Candidate.Sources, candidate.Sources...)
	group.Candidate.MatchQuality = bestMatchQuality(candidate.MatchQuality, group.Candidate.MatchQuality)
	if candidate.lastSeenOK && (!group.Candidate.lastSeenOK || candidate.lastSeenParsed.After(group.Candidate.lastSeenParsed)) {
		group.Candidate.LastSeen = candidate.LastSeen
		group.Candidate.lastSeenParsed = candidate.lastSeenParsed
		group.Candidate.lastSeenOK = true
	} else if group.Candidate.LastSeen == "" {
		group.Candidate.LastSeen = candidate.LastSeen
	}
	if candidate.Messages > group.Candidate.Messages {
		group.Candidate.Messages = candidate.Messages
	}
	if group.Candidate.sourceFilters == nil {
		group.Candidate.sourceFilters = map[string]string{}
	}
	filter := whoFilterValue(candidate)
	if record.FromClawdex {
		for _, source := range candidate.Sources {
			if _, ok := group.Candidate.sourceFilters[source]; !ok && filter != "" {
				group.Candidate.sourceFilters[source] = filter
			}
		}
	} else if record.Origin != "" && filter != "" {
		group.Candidate.sourceFilters[record.Origin] = filter
	}
	group.FromClawdex = group.FromClawdex || record.FromClawdex
}

func bestMatchQuality(left, right string) string {
	leftRank, leftOK := matchQualityRank(left)
	rightRank, rightOK := matchQualityRank(right)
	switch {
	case leftOK && !rightOK:
		return leftRank.String()
	case !leftOK && rightOK:
		return rightRank.String()
	case leftOK && rightOK:
		if leftRank.BetterThan(rightRank) {
			return leftRank.String()
		}
		return rightRank.String()
	default:
		return firstNonEmpty(left, right, "unknown")
	}
}

func normalisePersonName(value string) string {
	return whomatch.Normalize(value)
}

func normaliseIdentifier(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	digits := 0
	phoneLike := true
	var phone strings.Builder
	for i, char := range trimmed {
		switch {
		case char >= '0' && char <= '9':
			digits++
			phone.WriteRune(char)
		case char == '+' && i == 0:
			phone.WriteRune(char)
		case strings.ContainsRune(" ()-.", char):
		default:
			phoneLike = false
		}
	}
	if phoneLike && digits >= 5 {
		return phone.String()
	}
	return whomatch.Normalize(trimmed)
}

func whoFilterValue(candidate WhoCandidate) string {
	if len(candidate.Identifiers) > 0 {
		return candidate.Identifiers[0]
	}
	return candidate.Who
}

func skippedWhoSources(sources []Source) []string {
	var skipped []string
	for _, source := range sources {
		if source.MetadataErr == nil && !hasCapability(source, "who") {
			skipped = append(skipped, source.ID)
		}
	}
	return normalisedStringList(skipped)
}

func reportSkippedWhoSources(w io.Writer, skipped []string) {
	for _, source := range skipped {
		_, _ = fmt.Fprintf(w, "%s cannot filter by person yet\n", source)
	}
}

func (r *Runtime) reportWhoFailures(resolution federatedWhoResolution) {
	for _, source := range resolution.FailedSources {
		_, _ = fmt.Fprintf(r.stderr, "%s who failed. Remedy: run: trawl doctor %s\n", source, source)
	}
}

func whoExit(resolution federatedWhoResolution) error {
	if len(resolution.FailedSources) == 0 {
		return nil
	}
	if len(resolution.SourcesConsulted) > 0 {
		return exitErr{code: 3}
	}
	return exitErr{code: 1}
}

func renderWhoResolutionLine(w io.Writer, input string, candidate WhoCandidate) error {
	_, err := fmt.Fprintf(w, "%s → %s (%s)\n", input, candidate.Who, whoSources(candidate.Sources))
	return err
}

func (r *Runtime) writeAmbiguousWho(query, input string, resolution federatedWhoResolution, skipped []string) error {
	if r.root.JSON {
		_ = writeJSON(r.stdout, whoResolutionErrorEnvelope{
			Error: ErrorBody{
				Code:    "ambiguous_who",
				Message: fmt.Sprintf("Who %q matched more than one person.", input),
				Remedy:  "retry with one identifier from candidates",
			},
			TotalCandidates:  len(resolution.Candidates),
			Candidates:       capWhoCandidates(resolution.Candidates, jsonWhoCandidateLimit),
			SourcesConsulted: resolution.SourcesConsulted,
			SkippedSources:   skipped,
		})
		return exitErr{code: 4}
	}
	reportSkippedWhoSources(r.stderr, skipped)
	_, _ = fmt.Fprintf(r.stderr, "Who %q matched more than one person. Search was not run.\n", input)
	_ = renderWhoTable(r.stderr, resolution.Candidates)
	_, _ = fmt.Fprintf(r.stderr, "Retry example: %s\n", searchRetryExample(query, resolution.Candidates[0]))
	return exitErr{code: 4}
}

func (r *Runtime) writeUnknownWho(query, input string, resolution federatedWhoResolution, skipped []string) error {
	hint := searchWithoutWhoHint(query)
	if r.root.JSON {
		_ = writeJSON(r.stdout, whoResolutionErrorEnvelope{
			Error: ErrorBody{
				Code:    "unknown_who",
				Message: fmt.Sprintf("No person matched %q.", input),
				Remedy:  "retry with a suggestion or search without --who",
			},
			TotalDidYouMean:  len(resolution.DidYouMean),
			DidYouMean:       capWhoCandidates(resolution.DidYouMean, jsonWhoCandidateLimit),
			SourcesConsulted: resolution.SourcesConsulted,
			SkippedSources:   skipped,
			Hint:             hint,
		})
		return exitErr{code: 5}
	}
	reportSkippedWhoSources(r.stderr, skipped)
	_, _ = fmt.Fprintf(r.stderr, "No person matched %q. Search was not run.\n", input)
	if len(resolution.DidYouMean) > 0 {
		_, _ = fmt.Fprintln(r.stderr, "Did you mean:")
		_ = renderWhoTable(r.stderr, resolution.DidYouMean)
	}
	_, _ = fmt.Fprintf(r.stderr, "Hint: %s\n", hint)
	return exitErr{code: 5}
}

func searchRetryExample(query string, candidate WhoCandidate) string {
	parts := []string{"trawl", "search"}
	if strings.TrimSpace(query) != "" {
		parts = append(parts, quoteExampleArg(query))
	}
	parts = append(parts, "--who", quoteExampleArg(whoFilterValue(candidate)))
	return strings.Join(parts, " ")
}

func searchWithoutWhoHint(query string) string {
	if strings.TrimSpace(query) == "" {
		return "search again without --who to list matching items"
	}
	return "run " + strings.Join([]string{"trawl", "search", quoteExampleArg(query)}, " ") + " without --who"
}

func quoteExampleArg(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return `""`
	}
	if strings.ContainsAny(value, " \t\"'") {
		return strconv.Quote(value)
	}
	return value
}

func searchWhoFilters(candidate WhoCandidate, sources []Source) map[string]string {
	filters := map[string]string{}
	for _, source := range sources {
		if !hasCapability(source, "who") {
			continue
		}
		filter := strings.TrimSpace(candidate.sourceFilters[source.ID])
		if filter == "" {
			continue
		}
		filters[source.ID] = filter
	}
	return filters
}
