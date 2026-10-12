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

const (
	processExitCodeForFailedCommandAlreadyShown = 1
	processExitCodeForPartlyCompletedCommand    = 3
)

func outcomeExit(outcome federationcontract.OperationOutcome) error {
	switch outcome {
	case federationcontract.OperationOutcome_OPERATION_OUTCOME_COMPLETE:
		return nil
	case federationcontract.OperationOutcome_OPERATION_OUTCOME_PARTIAL:
		return commandOutcomeAlreadyShown{processExitCode: processExitCodeForPartlyCompletedCommand}
	default:
		return commandOutcomeAlreadyShown{processExitCode: processExitCodeForFailedCommandAlreadyShown}
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
			return render.CommandFailureFromTrawler(render.CommandFailureKindCommandIsWrong, message)
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

// logFederationOutcomes records why trawlers failed or did not run. The
// output library tells the person; the log keeps the cause.
func (r *Runtime) logFederationOutcomes(
	failures []*federationcontract.TrawlerOperationFailure,
	skips []*federationcontract.TrawlerSkippedFromOperation,
) {
	for _, failure := range failures {
		if failure == nil {
			continue
		}
		r.logInfo("trawler_operation_failed", strings.Join(nonEmpty(
			"trawler="+logQuote(trawlkit.RegisteredTrawlerIdentityText(failure.GetFailedTrawler())),
			"code="+logQuote(failure.GetFailureCode().String()),
			"error="+logQuote(failure.GetFailureMessage()),
		), " "))
	}
	for _, skip := range skips {
		if skip == nil {
			continue
		}
		r.logInfo("trawler_operation_skipped", strings.Join(nonEmpty(
			"trawler="+logQuote(trawlkit.RegisteredTrawlerIdentityText(skip.GetSkippedTrawler())),
			"reason="+logQuote(skip.GetSkipReason()),
		), " "))
	}
}

// reportFederationOutcomes logs trawler failures and shows them.
func (r *Runtime) reportFederationOutcomes(
	failures []*federationcontract.TrawlerOperationFailure,
	skips []*federationcontract.TrawlerSkippedFromOperation,
) error {
	r.logFederationOutcomes(failures, skips)
	return r.output.WriteTrawlerOperationFailures(failures, skips, false)
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
		exactPersonFilterIdentifiers := normalisedExactPersonFilterIdentifierList(
			personMatchFactsFromTrawler.GetExactPersonFilterIdentifiersObservedByTrawlerArchive(),
		)
		if len(exactPersonFilterIdentifiers) == 0 && !searchTrawler.Manifest.GetSearchMatchesResolvedPersonDisplayNames() {
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
			query.PersonFilter = trawlkit.NewResolvedSearchPersonFilter(
				resolvedPersonFilterText,
				exactPersonFilterIdentifiers,
			)
			return runTrawlerSearch(ctx, query)
		}
	}
	return searchTrawlersWithExactResolvedPersonFilters
}
