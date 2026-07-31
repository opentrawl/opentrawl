package federation

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit"
	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
	searchv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search/v1"
)

type searchRunResult struct {
	searchMatchesFromTrawler *federationv1.TrawlerSearchResult
	failure                  *federationv1.TrawlerOperationFailure
	skip                     *federationv1.TrawlerSkippedFromOperation
}

type mergedFederatedSearchMatch struct {
	searchMatch  *federationv1.FederatedSearchMatch
	trawlerIndex int
}

func Search(
	ctx context.Context,
	trawlers []SearchTrawler,
	query trawlkit.Query,
	resultLimit uint32,
) *federationv1.FederatedTrawlerSearchOperation {
	response := &federationv1.FederatedTrawlerSearchOperation{ResultLimit: resultLimit}
	if resultLimit == 0 {
		response.Outcome = federationv1.OperationOutcome_OPERATION_OUTCOME_FAILED
		response.OperationFailures = append(response.OperationFailures, &federationv1.TrawlerOperationFailure{
			FailureCode:    federationv1.FailureCode_FAILURE_CODE_INVALID_INPUT,
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
	trawlerCountContributingSearchMatches := 0
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
		if len(searchMatchesFromTrawlerInDisplayOrder) > 0 {
			trawlerCountContributingSearchMatches++
		}
		for _, searchMatch := range searchMatchesFromTrawlerInDisplayOrder {
			mergedSearchMatches = append(mergedSearchMatches, mergedFederatedSearchMatch{
				searchMatch:  searchMatch,
				trawlerIndex: trawlerIndex,
			})
		}
		successfulTrawlerCount++
	}
	if trawlerCountContributingSearchMatches > 1 {
		sortCrossTrawlerSearchMatchesByNewestAssociatedTime(mergedSearchMatches)
	}
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
			federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH,
			"callback is nil",
			federationv1.FailureCode_FAILURE_CODE_INTERNAL,
		)
		return searchRun
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			searchRun = searchRunResult{failure: panicFailure(
				trawler.Manifest,
				federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH,
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
		searchRun.failure = callbackFailure(ctx, trawler.Manifest, &federationv1.TrawlerOperationFailure{
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
			federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH,
			err,
		)
		return searchRun
	}
	if query.WhoResolved != nil {
		projectedSearchMatches.SearchPersonFilterResolution = &federationv1.SearchPersonFilterResolution{
			PersonFilterText:          query.WhoResolved.Who,
			ResolvedPersonIdentifiers: append([]string(nil), query.WhoResolved.Identifiers...),
		}
	}
	searchRun.searchMatchesFromTrawler = projectedSearchMatches
	return searchRun
}

func convertTrawlerSearchResponseToFederationTrawlerSearchResult(
	manifest *federationv1.RegisteredTrawlerManifest,
	trawlerSearchResponse *searchv1.TrawlerSearchResponse,
	localShortReferencesByCanonicalRecordReference []trawlkit.CanonicalArchiveRecordReferenceWithLocalTrawlerShortReference,
) (*federationv1.TrawlerSearchResult, error) {
	registeredTrawlerIdentity := manifest.GetRegisteredTrawler()
	if trawlkit.RegisteredTrawlerIdentityText(registeredTrawlerIdentity) == "" {
		return nil, fmt.Errorf("manifest trawler identity is empty")
	}
	if trawlerSearchResponse == nil {
		return nil, fmt.Errorf("trawler search response is missing")
	}
	projectedSearchMatches := &federationv1.TrawlerSearchResult{
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
		projectedSearchMatches.SearchMatchesFromTrawlerInDisplayOrder = append(projectedSearchMatches.SearchMatchesFromTrawlerInDisplayOrder, &federationv1.FederatedSearchMatch{
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
		leftTime, leftHasAssociatedTime := searchResultExactAssociatedTimeForCrossTrawlerMerge(leftFederatedSearchMatch.searchMatch.GetSearchMatchPresentation())
		rightTime, rightHasAssociatedTime := searchResultExactAssociatedTimeForCrossTrawlerMerge(rightFederatedSearchMatch.searchMatch.GetSearchMatchPresentation())
		if leftHasAssociatedTime != rightHasAssociatedTime {
			return leftHasAssociatedTime
		}
		if leftHasAssociatedTime && !leftTime.Equal(rightTime) {
			return leftTime.After(rightTime)
		}
		if leftFederatedSearchMatch.trawlerIndex != rightFederatedSearchMatch.trawlerIndex {
			return leftFederatedSearchMatch.trawlerIndex < rightFederatedSearchMatch.trawlerIndex
		}
		return trawlkit.GloballyRoutableTrawlLinkText(leftFederatedSearchMatch.searchMatch.GetTrawlLink()) <
			trawlkit.GloballyRoutableTrawlLinkText(rightFederatedSearchMatch.searchMatch.GetTrawlLink())
	})
}

func searchResultExactAssociatedTimeForCrossTrawlerMerge(presentation *searchv1.SearchMatchPresentation) (time.Time, bool) {
	if presentation == nil {
		return time.Time{}, false
	}
	exactAssociatedTime := presentation.GetMatchingRecordAssociatedTime().GetExactTime()
	if exactAssociatedTime == nil || !exactAssociatedTime.IsValid() {
		return time.Time{}, false
	}
	return exactAssociatedTime.AsTime(), true
}
