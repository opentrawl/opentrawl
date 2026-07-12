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
		Surface:      manifest.DisplayName,
		TotalMatches: uint64(result.TotalMatches),
		Truncated:    result.Truncated,
	}
	if result.WhoResolved != nil {
		out.WhoResolved = &federationv1.WhoResolved{
			Who:         result.WhoResolved.Who,
			Identifiers: append([]string(nil), result.WhoResolved.Identifiers...),
		}
	}
	for _, hit := range result.Results {
		if hit.Source != "" && hit.Source != manifest.ID {
			return nil, fmt.Errorf("search hit source %q does not match manifest id %q", hit.Source, manifest.ID)
		}
		projected := &federationv1.SearchHit{
			SourceId: manifest.ID,
			OpenRef:  hit.Ref,
			ShortRef: hit.ShortRef,
			Who:      hit.Who,
			Where:    hit.Where,
			Calendar: hit.Calendar,
			Snippet:  hit.Snippet,
			AllDay:   hit.AllDay,
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
