package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawl/internal/federation"
	"github.com/opentrawl/opentrawl/trawlkit"
	federationcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	open "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	search "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

type canonicalConsumerObserver interface {
	observeStatus([]federation.StatusTrawler, *federationcontract.FederatedTrawlerStatusOperation)
	observeSearch([]federation.SearchTrawler, trawlkit.Query, int, *federationcontract.FederatedTrawlerSearchOperation)
	observeOpen(
		[]federation.OpenTrawler,
		*trawlkit.RegisteredTrawlerIdentity,
		*trawlkit.LocalTrawlerShortReference,
		*open.OpenResponse,
	)
}

func outcomeExit(outcome federationcontract.OperationOutcome) error {
	switch outcome {
	case federationcontract.OperationOutcome_OPERATION_OUTCOME_COMPLETE:
		return nil
	case federationcontract.OperationOutcome_OPERATION_OUTCOME_PARTIAL:
		return exitErr{code: 3}
	default:
		return exitErr{code: 1}
	}
}

func userInputErrorFromFederatedTrawlerOperationFailures(
	failures []*federationcontract.TrawlerOperationFailure,
) error {
	for _, failure := range failures {
		if failure == nil ||
			failure.GetFailureCode() != federationcontract.FailureCode_FAILURE_CODE_INVALID_INPUT {
			continue
		}
		message := strings.TrimSpace(failure.GetFailureMessage())
		if message != "" {
			return usageErr{humanFacingUsageErrorMessage(message)}
		}
	}
	return nil
}

func (r *Runtime) canonicalStatus(trawlers []InstalledTrawler) *federationcontract.FederatedTrawlerStatusOperation {
	adapters := r.federationStatusTrawlers(trawlers)
	response := federation.Status(r.ctx, adapters)
	if r.canonicalObserver != nil {
		r.canonicalObserver.observeStatus(adapters, response)
	}
	return response
}

func (r *Runtime) canonicalSearch(trawlers []federation.SearchTrawler, query trawlkit.Query, limit int) *federationcontract.FederatedTrawlerSearchOperation {
	response := federation.Search(r.ctx, trawlers, query, uint32(limit))
	if r.canonicalObserver != nil {
		r.canonicalObserver.observeSearch(trawlers, query, limit, response)
	}
	return response
}

func (r *Runtime) canonicalOpen(
	trawlers []federation.OpenTrawler,
	selectedTrawler *trawlkit.RegisteredTrawlerIdentity,
	localShortReference *trawlkit.LocalTrawlerShortReference,
	requestedTrawlLink *trawlkit.GloballyRoutableTrawlLink,
) *open.OpenResponse {
	response := federation.Open(r.ctx, trawlers, selectedTrawler, localShortReference, nil)
	response.RequestedTrawlLink = requestedTrawlLink
	if r.canonicalObserver != nil {
		r.canonicalObserver.observeOpen(trawlers, selectedTrawler, localShortReference, response)
	}
	return response
}

func (r *Runtime) reportFederationOutcomes(
	failures []*federationcontract.TrawlerOperationFailure,
	skips []*federationcontract.TrawlerSkippedFromOperation,
) {
	r.reportFederationOutcomesWithArchiveAvailability(failures, skips, false)
}

func (r *Runtime) reportStatusFederationOutcomes(
	failures []*federationcontract.TrawlerOperationFailure,
	skips []*federationcontract.TrawlerSkippedFromOperation,
) {
	r.reportFederationOutcomesWithArchiveAvailability(failures, skips, true)
}

func (r *Runtime) reportFederationOutcomesWithArchiveAvailability(
	failures []*federationcontract.TrawlerOperationFailure,
	skips []*federationcontract.TrawlerSkippedFromOperation,
	archiveAvailabilityIsAlreadyShown bool,
) {
	seen := make(map[string]struct{}, len(failures)+len(skips))
	for _, failure := range failures {
		if failure == nil {
			continue
		}
		r.logInfo("trawler_operation_failed", strings.Join(nonEmpty(
			"trawler="+logQuote(trawlkit.RegisteredTrawlerIdentityText(failure.GetFailedTrawler())),
			"code="+logQuote(failure.GetFailureCode().String()),
			"error="+logQuote(failure.GetFailureMessage()),
		), " "))
		name := strings.TrimSpace(failure.GetRegisteredTrawlerDisplayName())
		if name == "" {
			name = trawlkit.RegisteredTrawlerIdentityText(failure.GetFailedTrawler())
		}
		if name == "" {
			continue
		}
		seen[strings.ToLower(name)] = struct{}{}
		if failureMeansArchiveUnavailable(failure.GetFailureCode()) {
			if archiveAvailabilityIsAlreadyShown {
				continue
			}
			r.writeTrawlerArchiveUnavailableError(name)
			continue
		}
		_, _ = fmt.Fprintf(r.stderr, "The command did not complete for %s.\n", name)
	}
	for _, skip := range skips {
		if skip == nil {
			continue
		}
		r.logInfo("trawler_operation_skipped", strings.Join(nonEmpty(
			"trawler="+logQuote(trawlkit.RegisteredTrawlerIdentityText(skip.GetSkippedTrawler())),
			"reason="+logQuote(skip.GetSkipReason()),
		), " "))
		name := strings.TrimSpace(skip.GetRegisteredTrawlerDisplayName())
		if name == "" {
			name = trawlkit.RegisteredTrawlerIdentityText(skip.GetSkippedTrawler())
		}
		if name == "" {
			continue
		}
		if _, alreadyReported := seen[strings.ToLower(name)]; alreadyReported {
			continue
		}
		_, _ = fmt.Fprintf(r.stderr, "The command did not run for %s.\n", name)
	}
}

func failureMeansArchiveUnavailable(failureCode federationcontract.FailureCode) bool {
	return failureCode == federationcontract.FailureCode_FAILURE_CODE_UNAVAILABLE ||
		failureCode == federationcontract.FailureCode_FAILURE_CODE_PERMISSION
}

func (r *Runtime) writeTrawlerArchiveUnavailableError(trawlerName string) {
	_, _ = fmt.Fprintf(r.stderr, "The %s archive is not available.\n", trawlerName)
}

func searchPresentationsFromResponse(response *federationcontract.FederatedTrawlerSearchOperation) (mergedSearchResult, error) {
	presentations := make([]render.SearchResultPresentationForRootTrawlHumanOutput, 0, len(response.GetSearchMatchesInDisplayOrder()))
	for searchMatchIndex, searchMatch := range response.GetSearchMatchesInDisplayOrder() {
		if searchMatch != nil && searchMatch.GetSearchMatchPresentation() != nil {
			if _, err := trawlkit.ParseGloballyRoutableTrawlLink(searchMatch.GetTrawlLink()); err != nil {
				return mergedSearchResult{}, fmt.Errorf("search match %d globally routable trawl link: %w", searchMatchIndex, err)
			}
			presentations = append(presentations, render.SearchResultPresentationForRootTrawlHumanOutput{
				SearchMatchPresentation:   searchMatch.GetSearchMatchPresentation(),
				GloballyRoutableTrawlLink: searchMatch.GetTrawlLink(),
			})
		}
	}
	total := 0
	for _, trawlerSearchResult := range response.GetTrawlerSearchResults() {
		total += int(trawlerSearchResult.GetTotalSearchMatches())
	}
	more := 0
	if response.GetMoreSearchMatchesExist() {
		more = total - len(presentations)
		if more < 1 {
			more = 1
		}
	}
	return mergedSearchResult{
		Presentations: presentations,
		TotalMatches:  total,
		Truncated:     response.GetMoreSearchMatchesExist(),
		More:          more,
	}, nil
}

func applyExactResolvedPersonFiltersToSearchTrawlers(
	searchTrawlers []federation.SearchTrawler,
	resolvedPersonFilterText string,
	resolvedPersonMatchFactsFromTrawlers []*person.PersonMatchFactsFromTrawler,
) []federation.SearchTrawler {
	searchTrawlersWithExactResolvedPersonFilters := append([]federation.SearchTrawler(nil), searchTrawlers...)
	for searchTrawlerIndex := range searchTrawlersWithExactResolvedPersonFilters {
		searchTrawler := &searchTrawlersWithExactResolvedPersonFilters[searchTrawlerIndex]
		if searchTrawler.SkipReason != "" {
			continue
		}
		personMatchFactsFromTrawler := personMatchFactsForTrawlerFromFacts(
			resolvedPersonMatchFactsFromTrawlers,
			trawlkit.RegisteredTrawlerIdentityText(searchTrawler.Manifest.GetRegisteredTrawler()),
		)
		exactPersonFilterIdentifiers := personMatchFactsFromTrawler.GetExactPersonFilterIdentifiersObservedByTrawlerArchive()
		exactResolvedPersonFilter := ""
		if len(exactPersonFilterIdentifiers) > 0 {
			exactResolvedPersonFilter = strings.TrimSpace(
				exactPersonFilterIdentifiers[0].GetExactPersonFilterIdentifier(),
			)
		}
		if exactResolvedPersonFilter == "" {
			continue
		}
		runTrawlerSearch := searchTrawler.Run
		searchTrawler.Run = func(
			ctx context.Context,
			query trawlkit.Query,
		) (
			*search.TrawlerSearchResponse,
			[]trawlkit.CanonicalArchiveRecordReferenceWithLocalTrawlerShortReference,
			*federationcontract.TrawlerOperationFailure,
		) {
			query.Who = exactResolvedPersonFilter
			query.ResolvedPersonFilter = &trawlkit.ResolvedPersonFilter{
				PersonFilterText: resolvedPersonFilterText,
				ExactPersonFilterIdentifiers: append(
					[]*person.ExactPersonFilterIdentifier(nil),
					exactPersonFilterIdentifiers...,
				),
			}
			return runTrawlerSearch(ctx, query)
		}
	}
	return searchTrawlersWithExactResolvedPersonFilters
}
