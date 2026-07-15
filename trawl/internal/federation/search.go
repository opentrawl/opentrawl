package federation

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
)

type searchRunResult struct {
	result  *federationv1.SearchSourceResult
	failure *federationv1.SourceFailure
	skip    *federationv1.SkippedSource
}

type mergedHit struct {
	hit         *federationv1.SearchHit
	sourceIndex int
	rank        int
}

func Search(ctx context.Context, sources []SearchSource, query trawlkit.Query, order federationv1.SearchOrder, resultLimit uint32) *federationv1.SearchResponse {
	response := &federationv1.SearchResponse{Order: order, ResultLimit: resultLimit}
	if resultLimit == 0 || order == federationv1.SearchOrder_SEARCH_ORDER_UNSPECIFIED || query.Limit != 0 && query.Limit != int(resultLimit) {
		response.Outcome = federationv1.OperationOutcome_OPERATION_OUTCOME_FAILED
		response.Failures = append(response.Failures, &federationv1.SourceFailure{
			Code:    federationv1.FailureCode_FAILURE_CODE_INVALID_INPUT,
			Message: "Search order and one non-zero global result limit are required.",
		})
		return response
	}
	query.Limit = int(resultLimit)
	results := make([]searchRunResult, len(sources))
	var wait sync.WaitGroup
	for index := range sources {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			results[index] = runSearchSource(ctx, sources[index], query)
		}(index)
	}
	wait.Wait()

	var merged []mergedHit
	successes := 0
	for sourceIndex, result := range results {
		if result.skip != nil {
			response.SkippedSources = append(response.SkippedSources, result.skip)
			continue
		}
		if result.failure != nil {
			response.Failures = append(response.Failures, result.failure)
			continue
		}
		response.Sources = append(response.Sources, result.result)
		if result.result.Truncated || result.result.TotalMatches > uint64(len(result.result.Hits)) {
			response.Truncated = true
		}
		for rank, hit := range result.result.Hits {
			merged = append(merged, mergedHit{hit: cloneSearchHit(hit), sourceIndex: sourceIndex, rank: rank})
		}
		successes++
	}
	sortMergedHits(merged, order)
	if uint64(len(merged)) > uint64(resultLimit) {
		merged = merged[:int(resultLimit)]
		response.Truncated = true
	}
	for _, item := range merged {
		response.Hits = append(response.Hits, item.hit)
	}
	response.Outcome = aggregateOutcome(successes, len(response.Failures), len(response.SkippedSources))
	return response
}

func runSearchSource(ctx context.Context, source SearchSource, query trawlkit.Query) (result searchRunResult) {
	if strings.TrimSpace(source.SkipReason) != "" {
		result.skip = skippedSource(source.Manifest, source.SkipReason)
		return result
	}
	if source.Run == nil {
		result.failure = operationFailure(source.Manifest, "search", "callback is nil", federationv1.FailureCode_FAILURE_CODE_INTERNAL)
		return result
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			result = searchRunResult{failure: panicFailure(source.Manifest, "search", recovered)}
		}
	}()
	searchResult, failure := source.Run(ctx, query)
	if failure != nil {
		result.failure = callbackFailure(ctx, source.Manifest, failure)
		return result
	}
	if ctx.Err() != nil {
		result.failure = callbackFailure(ctx, source.Manifest, &federationv1.SourceFailure{Message: ctx.Err().Error()})
		return result
	}
	projected, err := ProjectSearch(source.Manifest, searchResult)
	if err != nil {
		result.failure = projectionFailure(source.Manifest, "search", err)
		return result
	}
	result.result = projected
	return result
}

func ProjectSearch(manifest control.Manifest, result trawlkit.SearchResult) (*federationv1.SearchSourceResult, error) {
	if strings.TrimSpace(manifest.ID) == "" {
		return nil, fmt.Errorf("manifest source id is empty")
	}
	if result.TotalMatches < 0 {
		return nil, fmt.Errorf("total matches is negative")
	}
	out := &federationv1.SearchSourceResult{
		SourceId:     manifest.ID,
		DisplayName:  manifest.DisplayName,
		TotalMatches: uint64(result.TotalMatches),
		Truncated:    result.Truncated,
		TotalIsExact: !result.TotalIsLowerBound,
	}
	if result.WhoResolved != nil {
		out.WhoResolved = &federationv1.WhoResolved{
			Who:         result.WhoResolved.Who,
			Identifiers: append([]string(nil), result.WhoResolved.Identifiers...),
		}
	}
	seenMatches := map[string]struct{}{}
	for _, hit := range result.Results {
		if hit.Source != "" && hit.Source != manifest.ID {
			return nil, fmt.Errorf("search hit source %q does not match manifest id %q", hit.Source, manifest.ID)
		}
		if !openrecord.ValidSourceRef(manifest.ID, hit.Ref) {
			return nil, fmt.Errorf("search hit open ref is outside the source namespace")
		}
		if hit.AnchorID != strings.TrimSpace(hit.AnchorID) || !openrecord.ValidAnchorID(hit.AnchorID) {
			return nil, fmt.Errorf("search hit anchor id is invalid")
		}
		matchIdentity := hit.Ref + "\x00" + hit.AnchorID
		if _, exists := seenMatches[matchIdentity]; exists {
			return nil, fmt.Errorf("search hit ref and anchor are duplicated")
		}
		seenMatches[matchIdentity] = struct{}{}
		if strings.TrimSpace(hit.Summary.Title) == "" {
			return nil, fmt.Errorf("search hit summary title is empty")
		}
		if len(hit.Evidence) == 0 {
			return nil, fmt.Errorf("search hit evidence is empty")
		}
		projected := &federationv1.SearchHit{
			SourceId: manifest.ID,
			OpenRef:  hit.Ref,
			ShortRef: hit.ShortRef,
			AllDay:   hit.AllDay,
			AnchorId: hit.AnchorID,
			Summary:  &federationv1.ResultSummary{Title: hit.Summary.Title, Subtitle: hit.Summary.Subtitle},
		}
		for _, context := range hit.Archive {
			if !validArchiveContextKind(context.Kind) {
				return nil, fmt.Errorf("search archive context kind is invalid")
			}
			if strings.TrimSpace(context.Label) == "" {
				return nil, fmt.Errorf("search archive context label is empty")
			}
			projected.ArchiveContext = append(projected.ArchiveContext, &federationv1.ArchiveContext{Kind: context.Kind, Label: context.Label})
		}
		for _, evidence := range hit.Evidence {
			fragment, err := projectEvidence(manifest.ID, evidence)
			if err != nil {
				return nil, err
			}
			projected.Evidence = append(projected.Evidence, fragment)
		}
		if hit.Availability != nil {
			availability := *hit.Availability
			projected.Availability = &availability
		}
		if hit.Unread != nil {
			unread := *hit.Unread
			projected.Unread = &unread
		}
		if !hit.Time.IsZero() {
			projected.TimeRfc3339 = hit.Time.Format(time.RFC3339Nano)
		}
		out.Hits = append(out.Hits, projected)
	}
	return out, nil
}

func validArchiveContextKind(value string) bool {
	if value == "" || value != strings.TrimSpace(value) {
		return false
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' {
			continue
		}
		return false
	}
	return true
}

func projectEvidence(sourceID string, evidence trawlkit.EvidenceFragment) (*federationv1.EvidenceFragment, error) {
	if strings.TrimSpace(evidence.Label) == "" {
		return nil, fmt.Errorf("search evidence label is empty")
	}
	out := &federationv1.EvidenceFragment{Label: evidence.Label}
	contents := 0
	if evidence.Text != nil {
		contents++
		runs, err := projectTextRuns(evidence.Text.Runs)
		if err != nil {
			return nil, err
		}
		out.Content = &federationv1.EvidenceFragment_Text{Text: &federationv1.TextEvidence{Runs: runs}}
	}
	if evidence.Field != nil {
		contents++
		if strings.TrimSpace(evidence.Field.Name) == "" {
			return nil, fmt.Errorf("search field evidence name is empty")
		}
		runs, err := projectTextRuns(evidence.Field.Value)
		if err != nil {
			return nil, err
		}
		out.Content = &federationv1.EvidenceFragment_Field{Field: &federationv1.FieldEvidence{Name: evidence.Field.Name, Value: runs}}
	}
	if evidence.Media != nil {
		contents++
		ref := evidence.Media.ResourceRef
		if ref != "" && !openrecord.ValidSourceRef(sourceID, ref) {
			return nil, fmt.Errorf("search media evidence resource ref is outside the source namespace")
		}
		runs, err := projectTextRuns(evidence.Media.Description)
		if err != nil {
			return nil, err
		}
		out.Content = &federationv1.EvidenceFragment_Media{Media: &federationv1.MediaEvidence{ResourceRef: ref, Description: runs}}
	}
	if evidence.Relation != nil {
		contents++
		if strings.TrimSpace(evidence.Relation.Relation) == "" {
			return nil, fmt.Errorf("search relation evidence kind is empty")
		}
		runs, err := projectTextRuns(evidence.Relation.Target)
		if err != nil {
			return nil, err
		}
		out.Content = &federationv1.EvidenceFragment_Relation{Relation: &federationv1.RelationEvidence{Relation: evidence.Relation.Relation, Target: runs}}
	}
	if contents != 1 {
		return nil, fmt.Errorf("search evidence must contain exactly one typed value")
	}
	return out, nil
}

func projectTextRuns(values []trawlkit.TextRun) ([]*federationv1.TextRun, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("search evidence text is empty")
	}
	out := make([]*federationv1.TextRun, 0, len(values))
	for _, value := range values {
		if value.Text == "" {
			return nil, fmt.Errorf("search evidence text run is empty")
		}
		out = append(out, &federationv1.TextRun{Text: value.Text, Matched: value.Matched})
	}
	return out, nil
}

func sortMergedHits(hits []mergedHit, order federationv1.SearchOrder) {
	sort.SliceStable(hits, func(i, j int) bool {
		left, right := hits[i], hits[j]
		if order == federationv1.SearchOrder_SEARCH_ORDER_RELEVANCE && left.rank != right.rank {
			return left.rank < right.rank
		}
		leftTime, leftTimed := parseTime(left.hit.TimeRfc3339)
		rightTime, rightTimed := parseTime(right.hit.TimeRfc3339)
		if order == federationv1.SearchOrder_SEARCH_ORDER_RECENCY {
			if leftTimed != rightTimed {
				return leftTimed
			}
			if leftTimed && !leftTime.Equal(rightTime) {
				return leftTime.After(rightTime)
			}
		}
		if left.sourceIndex != right.sourceIndex {
			return left.sourceIndex < right.sourceIndex
		}
		if order == federationv1.SearchOrder_SEARCH_ORDER_RELEVANCE && leftTimed && rightTimed && !leftTime.Equal(rightTime) {
			return leftTime.After(rightTime)
		}
		return left.hit.OpenRef < right.hit.OpenRef
	})
}

func parseTime(value string) (time.Time, bool) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	return parsed, err == nil
}
