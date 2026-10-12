package federation

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit"
	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	search "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search"
)

type searchRunResult struct {
	searchMatchesFromTrawler *federation.TrawlerSearchResult
	failure                  *federation.TrawlerOperationFailure
	skip                     *federation.TrawlerSkippedFromOperation
}

type mergedFederatedSearchMatch struct {
	searchMatch  *federation.FederatedSearchMatch
	trawlerIndex int
}

func Search(
	ctx context.Context,
	trawlers []SearchTrawler,
	query trawlkit.Query,
	resultLimit uint32,
) *federation.FederatedTrawlerSearchOperation {
	response := &federation.FederatedTrawlerSearchOperation{ResultLimit: resultLimit}
	if resultLimit == 0 {
		response.Outcome = federation.OperationOutcome_OPERATION_OUTCOME_FAILED
		response.OperationFailures = append(response.OperationFailures, &federation.TrawlerOperationFailure{
			FailureCode:    federation.FailureCode_FAILURE_CODE_INVALID_INPUT,
			FailureMessage: "One non-zero global result limit is required.",
		})
		return response
	}
	query.Limit = int(resultLimit)
	searchRunResults := make([]searchRunResult, len(trawlers))
	var waitForTrawlers sync.WaitGroup
	for trawlerIndex := range trawlers {
		waitForTrawlers.Add(1)
		go func(trawlerIndex int) {
			defer waitForTrawlers.Done()
			searchRunResults[trawlerIndex] = runSearchTrawler(ctx, trawlers[trawlerIndex], query)
		}(trawlerIndex)
	}
	waitForTrawlers.Wait()

	mergedSearchMatches := make([]mergedFederatedSearchMatch, 0)
	successfulTrawlerCount := 0
	for trawlerIndex, searchRunResult := range searchRunResults {
		if searchRunResult.skip != nil {
			response.TrawlersSkippedFromOperation = append(response.TrawlersSkippedFromOperation, searchRunResult.skip)
			continue
		}
		if searchRunResult.failure != nil {
			response.OperationFailures = append(response.OperationFailures, searchRunResult.failure)
			continue
		}
		trawlerSearchMatches := searchRunResult.searchMatchesFromTrawler
		response.TrawlerSearchResults = append(response.TrawlerSearchResults, trawlerSearchMatches)
		if trawlerSearchMatches.GetMoreSearchMatchesExist() ||
			trawlerSearchMatches.GetTotalSearchMatches() >
				uint64(len(trawlerSearchMatches.GetSearchMatchesFromTrawlerInDisplayOrder())) {
			response.MoreSearchMatchesExist = true
		}
		searchMatchesFromTrawlerInDisplayOrder := trawlerSearchMatches.GetSearchMatchesFromTrawlerInDisplayOrder()
		for _, searchMatch := range searchMatchesFromTrawlerInDisplayOrder {
			mergedSearchMatches = append(mergedSearchMatches, mergedFederatedSearchMatch{
				searchMatch:  searchMatch,
				trawlerIndex: trawlerIndex,
			})
		}
		successfulTrawlerCount++
	}
	sortCrossTrawlerSearchMatchesByNewestAssociatedTime(mergedSearchMatches)
	if uint64(len(mergedSearchMatches)) > uint64(resultLimit) {
		mergedSearchMatches = mergedSearchMatches[:int(resultLimit)]
		response.MoreSearchMatchesExist = true
	}
	for _, mergedFederatedSearchMatch := range mergedSearchMatches {
		response.SearchMatchesInDisplayOrder = append(response.SearchMatchesInDisplayOrder, mergedFederatedSearchMatch.searchMatch)
	}
	response.Outcome = aggregateOutcome(
		successfulTrawlerCount,
		len(response.OperationFailures),
		len(response.TrawlersSkippedFromOperation),
	)
	return response
}

func runSearchTrawler(ctx context.Context, trawler SearchTrawler, query trawlkit.Query) (searchRun searchRunResult) {
	if strings.TrimSpace(trawler.SkipReason) != "" {
		searchRun.skip = skippedTrawler(trawler.Manifest, trawler.SkipReason)
		return searchRun
	}
	if trawler.Run == nil {
		searchRun.failure = operationFailure(
			trawler.Manifest,
			federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH,
			"callback is nil",
			federation.FailureCode_FAILURE_CODE_INTERNAL,
		)
		return searchRun
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			searchRun = searchRunResult{failure: panicFailure(
				trawler.Manifest,
				federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH,
				recovered,
			)}
		}
	}()
	trawlerSearchResponse, localShortReferencesByCanonicalRecordReference, failure := trawler.Run(ctx, query)
	if failure != nil {
		searchRun.failure = callbackFailure(ctx, trawler.Manifest, failure)
		return searchRun
	}
	if ctx.Err() != nil {
		searchRun.failure = callbackFailure(ctx, trawler.Manifest, &federation.TrawlerOperationFailure{
			FailureMessage: ctx.Err().Error(),
		})
		return searchRun
	}
	projectedSearchMatches, err := convertTrawlerSearchResponseToFederationTrawlerSearchResult(
		trawler.Manifest,
		trawlerSearchResponse,
		localShortReferencesByCanonicalRecordReference,
	)
	if err != nil {
		searchRun.failure = projectionFailure(
			trawler.Manifest,
			federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH,
			err,
		)
		return searchRun
	}
	if resolvedPersonFilter := query.PersonFilter.ResolvedPersonFilter(); resolvedPersonFilter != nil {
		projectedSearchMatches.SearchPersonFilterResolution = &federation.SearchPersonFilterResolution{
			PersonFilterText: resolvedPersonFilter.PersonFilterText,
			ResolvedExactPersonFilterIdentifiers: append(
				[]*person.ExactPersonFilterIdentifier(nil),
				resolvedPersonFilter.ExactPersonFilterIdentifiers...,
			),
		}
	}
	searchRun.searchMatchesFromTrawler = projectedSearchMatches
	return searchRun
}

func convertTrawlerSearchResponseToFederationTrawlerSearchResult(
	manifest *federation.RegisteredTrawlerManifest,
	trawlerSearchResponse *search.TrawlerSearchResponse,
	localShortReferencesByCanonicalRecordReference []trawlkit.CanonicalArchiveRecordReferenceWithLocalTrawlerShortReference,
) (*federation.TrawlerSearchResult, error) {
	registeredTrawlerIdentity := manifest.GetRegisteredTrawler()
	if trawlkit.RegisteredTrawlerIdentityText(registeredTrawlerIdentity) == "" {
		return nil, fmt.Errorf("manifest trawler identity is empty")
	}
	if trawlerSearchResponse == nil {
		return nil, fmt.Errorf("trawler search response is missing")
	}
	projectedSearchMatches := &federation.TrawlerSearchResult{
		RegisteredTrawler:              registeredTrawlerIdentity,
		RegisteredTrawlerDisplayName:   trawlerDisplayName(manifest),
		TotalSearchMatches:             trawlerSearchResponse.GetTotalSearchMatches(),
		MoreSearchMatchesExist:         trawlerSearchResponse.GetMoreSearchMatchesExist(),
		TotalSearchMatchesIsLowerBound: trawlerSearchResponse.GetTotalSearchMatchesIsLowerBound(),
	}
	for searchMatchIndex, trawlerFederatedSearchMatch := range trawlerSearchResponse.GetTrawlerSearchMatchesInDisplayOrder() {
		if trawlerFederatedSearchMatch == nil || trawlerFederatedSearchMatch.GetSearchMatchPresentation() == nil {
			return nil, fmt.Errorf("search match %d has no presentation", searchMatchIndex)
		}
		localShortReference := trawlkit.LocalTrawlerShortReferenceForCanonicalArchiveRecordReference(
			localShortReferencesByCanonicalRecordReference,
			trawlerFederatedSearchMatch.GetCanonicalRecordReference(),
		)
		globallyRoutableTrawlLink, err := trawlkit.ComposeGloballyRoutableTrawlLink(trawlkit.GloballyRoutableTrawlLinkRoute{
			RegisteredTrawler:   registeredTrawlerIdentity,
			LocalShortReference: localShortReference,
		})
		if err != nil {
			return nil, fmt.Errorf("search match %d globally routable trawl link: %w", searchMatchIndex, err)
		}
		projectedSearchMatches.SearchMatchesFromTrawlerInDisplayOrder = append(projectedSearchMatches.SearchMatchesFromTrawlerInDisplayOrder, &federation.FederatedSearchMatch{
			RecordAnchor:            trawlerFederatedSearchMatch.GetRecordAnchor(),
			SearchMatchPresentation: trawlerFederatedSearchMatch.GetSearchMatchPresentation(),
			TrawlLink:               globallyRoutableTrawlLink,
		})
	}
	return projectedSearchMatches, nil
}

func sortCrossTrawlerSearchMatchesByNewestAssociatedTime(searchMatches []mergedFederatedSearchMatch) {
	sort.SliceStable(searchMatches, func(leftIndex, rightIndex int) bool {
		leftFederatedSearchMatch := searchMatches[leftIndex]
		rightFederatedSearchMatch := searchMatches[rightIndex]
		leftTime, leftHasAssociatedTime := searchResultAssociatedTimeForCrossTrawlerMerge(leftFederatedSearchMatch.searchMatch.GetSearchMatchPresentation())
		rightTime, rightHasAssociatedTime := searchResultAssociatedTimeForCrossTrawlerMerge(rightFederatedSearchMatch.searchMatch.GetSearchMatchPresentation())
		if leftHasAssociatedTime != rightHasAssociatedTime {
			return leftHasAssociatedTime
		}
		if leftHasAssociatedTime && !leftTime.Equal(rightTime) {
			return leftTime.After(rightTime)
		}
		if leftFederatedSearchMatch.trawlerIndex != rightFederatedSearchMatch.trawlerIndex {
			return leftFederatedSearchMatch.trawlerIndex < rightFederatedSearchMatch.trawlerIndex
		}
		return false
	})
}

func searchResultAssociatedTimeForCrossTrawlerMerge(presentation *search.SearchMatchPresentation) (time.Time, bool) {
	if presentation == nil {
		return time.Time{}, false
	}
	associatedTime := presentation.GetMatchingRecordAssociatedTime()
	if calendarDate := associatedTime.GetCalendarDate(); calendarDate != nil {
		return time.Date(
			int(calendarDate.GetCalendarYear()), time.Month(calendarDate.GetCalendarMonthNumber()),
			int(calendarDate.GetCalendarDayOfMonth()), 0, 0, 0, 0,
			time.FixedZone("", int(associatedTime.GetEventUtcOffsetSecondsRecordedBySourceArchive())),
		), true
	}
	exactAssociatedTime := associatedTime.GetExactTime()
	if exactAssociatedTime == nil || !exactAssociatedTime.IsValid() {
		return time.Time{}, false
	}
	return exactAssociatedTime.AsTime(), true
}
