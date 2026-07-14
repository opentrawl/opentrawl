package cli

import (
	"context"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/whomatch"
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
	FailedSources    []failedSource
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
	if contacts, ok := findSource(installed, "contacts"); ok && contacts.MetadataErr == nil {
		add(contacts)
	}
	return out
}

func sourceKey(source Source) string {
	return firstNonEmpty(source.ID, source.Binary, source.Surface)
}

func isClawdex(source Source) bool {
	return strings.EqualFold(source.ID, "contacts") || strings.EqualFold(source.Binary, "contacts")
}

func collectFederatedWho(r *Runtime, sources []Source, query string) federatedWhoResolution {
	results := collectWho(r, sources, query)
	records := make([]whoRecord, 0)
	suggestionRecords := make([]whoRecord, 0)
	var consulted []string
	var failed []failedSource
	for _, result := range results {
		if result.Err != nil {
			failed = append(failed, failedSource{
				Source:       result.Source.ID,
				Reason:       failureReason(result.Err),
				displayName:  sourceHumanName(result.Source),
				commandToken: sourceCommandToken(result.Source),
			})
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
	sort.Slice(failed, func(i, j int) bool {
		return failed[i].Source < failed[j].Source
	})
	return federatedWhoResolution{
		Query:            query,
		Candidates:       candidates,
		DidYouMean:       didYouMean,
		SourcesConsulted: normalisedStringList(consulted),
		FailedSources:    failed,
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

func collectWho(r *Runtime, sources []Source, query string) []whoSourceResult {
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
				results[index] = r.whoSource(sources[index], query)
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

func (r *Runtime) whoSource(source Source, query string) whoSourceResult {
	result := whoSourceResult{Source: source}
	started := r.logSourceStart(source, "who")
	defer func() {
		if result.Err != nil {
			r.logSourceDone(source, "who", started, result.Err)
			return
		}
		r.logSourceDone(source, "who", started, nil, "candidates="+strconv.Itoa(len(result.Candidates)), "suggestions="+strconv.Itoa(len(result.DidYouMean)))
	}()
	matcher, ok := source.Crawler.(trawlkit.WhoMatcher)
	if !ok {
		result.Err = errorsForMetadata(source)
		return result
	}
	var candidates []whomatch.Candidate
	err := r.withSourceRequest(source, "who", sourceStoreRead, outputFormat(true), io.Discard, func(ctx context.Context, req *trawlkit.Request) error {
		var whoErr error
		candidates, whoErr = matcher.Who(ctx, req, query)
		return whoErr
	})
	if err != nil {
		result.Err = err
		return result
	}
	result.Candidates = whoCandidatesFromMatches(candidates, source.ID, query)
	return result
}

func whoCandidatesFromMatches(candidates []whomatch.Candidate, sourceID, query string) []WhoCandidate {
	out := make([]WhoCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		matchQuality := "unknown"
		if rank, ok := candidate.MatchRank(query); ok {
			matchQuality = rank.String()
		}
		lastSeen := ""
		if !candidate.LastSeen.IsZero() {
			lastSeen = candidate.LastSeen.UTC().Format(time.RFC3339)
		}
		out = append(out, normalizeWhoCandidate(crawlerWhoCandidate{
			Who:          candidate.Who,
			Identifiers:  append([]string(nil), candidate.Identifiers...),
			MatchQuality: matchQuality,
			Sources:      []string{sourceID},
			LastSeen:     lastSeen,
			Messages:     int(candidate.Messages),
		}, sourceID))
	}
	return out
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

func (r *Runtime) reportWhoFailures(resolution federatedWhoResolution) {
	for _, failure := range resolution.FailedSources {
		r.reportFailedSourceFailure(failure, "who", r.reasonDetail(failure.Reason))
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
