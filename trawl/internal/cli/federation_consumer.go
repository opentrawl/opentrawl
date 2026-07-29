package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawl/internal/federation"
	"github.com/opentrawl/opentrawl/trawlkit"
	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
	personv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person/v1"
	searchv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search/v1"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

type canonicalConsumerObserver interface {
	observeStatus([]federation.StatusSource, *federationv1.FederatedTrawlerStatusOperation)
	observeSearch([]federation.SearchSource, trawlkit.Query, int, *federationv1.FederatedTrawlerSearchOperation)
	observeOpen([]federation.OpenSource, string, string, *openv1.OpenResponse)
}

func outcomeExit(outcome federationv1.OperationOutcome) error {
	switch outcome {
	case federationv1.OperationOutcome_OPERATION_OUTCOME_COMPLETE:
		return nil
	case federationv1.OperationOutcome_OPERATION_OUTCOME_PARTIAL:
		return exitErr{code: 3}
	default:
		return exitErr{code: 1}
	}
}

func (r *Runtime) canonicalStatus(sources []InstalledTrawler) *federationv1.FederatedTrawlerStatusOperation {
	adapters := r.federationStatusTrawlers(sources)
	response := federation.Status(r.ctx, adapters)
	if r.canonicalObserver != nil {
		r.canonicalObserver.observeStatus(adapters, response)
	}
	return response
}

func (r *Runtime) canonicalSearch(sources []federation.SearchSource, query trawlkit.Query, limit int) *federationv1.FederatedTrawlerSearchOperation {
	response := federation.Search(r.ctx, sources, query, uint32(limit))
	if r.canonicalObserver != nil {
		r.canonicalObserver.observeSearch(sources, query, limit, response)
	}
	return response
}

func (r *Runtime) canonicalOpen(sources []federation.OpenSource, sourceID, sourceRef, requestedGloballyRoutableTrawlLink string) *openv1.OpenResponse {
	response := federation.Open(r.ctx, sources, sourceID, sourceRef, "")
	response.RequestedGloballyRoutableTrawlLink = requestedGloballyRoutableTrawlLink
	if r.canonicalObserver != nil {
		r.canonicalObserver.observeOpen(sources, sourceID, sourceRef, response)
	}
	return response
}

func (r *Runtime) reportFederationOutcomes(failures []*federationv1.TrawlerOperationFailure, skips []*federationv1.TrawlerSkippedFromOperation) {
	seen := make(map[string]struct{}, len(failures)+len(skips))
	for _, failure := range failures {
		if failure == nil {
			continue
		}
		r.logInfo("trawler_operation_failed", strings.Join(nonEmpty(
			"trawler="+logQuote(failure.GetRegisteredTrawlerManifestIdentity()),
			"code="+logQuote(failure.GetFailureCode().String()),
			"error="+logQuote(failure.GetFailureMessage()),
		), " "))
		name := strings.TrimSpace(failure.GetRegisteredTrawlerDisplayName())
		if name == "" {
			name = strings.TrimSpace(failure.GetRegisteredTrawlerManifestIdentity())
		}
		if name == "" {
			continue
		}
		seen[strings.ToLower(name)] = struct{}{}
		if failure.GetFailureCode() == federationv1.FailureCode_FAILURE_CODE_UNAVAILABLE {
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
			"trawler="+logQuote(skip.GetRegisteredTrawlerManifestIdentity()),
			"reason="+logQuote(skip.GetSkipReason()),
		), " "))
		name := strings.TrimSpace(skip.GetRegisteredTrawlerDisplayName())
		if name == "" {
			name = strings.TrimSpace(skip.GetRegisteredTrawlerManifestIdentity())
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

func (r *Runtime) writeTrawlerArchiveUnavailableError(trawlerName string) {
	_, _ = fmt.Fprintf(r.stderr, "The %s archive is not available.\n", trawlerName)
}

func searchPresentationsFromResponse(response *federationv1.FederatedTrawlerSearchOperation) (mergedSearchResult, error) {
	presentations := make([]render.SearchResultPresentationForRootTrawlHumanOutput, 0, len(response.GetSearchMatchesInDisplayOrder()))
	for searchMatchIndex, searchMatch := range response.GetSearchMatchesInDisplayOrder() {
		if searchMatch != nil && searchMatch.GetSearchMatchPresentation() != nil {
			globallyRoutableTrawlLink := strings.TrimSpace(searchMatch.GetGloballyRoutableTrawlLink())
			if _, err := trawlkit.ParseGloballyRoutableTrawlLink(globallyRoutableTrawlLink); err != nil {
				return mergedSearchResult{}, fmt.Errorf("search match %d globally routable trawl link: %w", searchMatchIndex, err)
			}
			presentations = append(presentations, render.SearchResultPresentationForRootTrawlHumanOutput{
				SearchMatchPresentation:   searchMatch.GetSearchMatchPresentation(),
				GloballyRoutableTrawlLink: globallyRoutableTrawlLink,
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
	searchTrawlers []federation.SearchSource,
	resolvedPersonMatchFactsFromTrawlers []*personv1.PersonMatchFactsFromTrawler,
) []federation.SearchSource {
	searchTrawlersWithExactResolvedPersonFilters := append([]federation.SearchSource(nil), searchTrawlers...)
	for searchTrawlerIndex := range searchTrawlersWithExactResolvedPersonFilters {
		searchTrawler := &searchTrawlersWithExactResolvedPersonFilters[searchTrawlerIndex]
		if searchTrawler.SkipReason != "" {
			continue
		}
		personMatchFactsFromTrawler := personMatchFactsForTrawlerFromFacts(
			resolvedPersonMatchFactsFromTrawlers,
			searchTrawler.Manifest.GetRegisteredTrawlerManifestIdentity(),
		)
		exactPersonFilterIdentifiers := personMatchFactsFromTrawler.GetExactPersonFilterIdentifiersObservedByTrawlerArchive()
		exactResolvedPersonFilter := ""
		if len(exactPersonFilterIdentifiers) > 0 {
			exactResolvedPersonFilter = strings.TrimSpace(exactPersonFilterIdentifiers[0])
		}
		if exactResolvedPersonFilter == "" {
			continue
		}
		runTrawlerSearch := searchTrawler.Run
		searchTrawler.Run = func(ctx context.Context, query trawlkit.Query) (*searchv1.TrawlerSearchResponse, map[string]string, *federationv1.TrawlerOperationFailure) {
			query.Who = exactResolvedPersonFilter
			return runTrawlerSearch(ctx, query)
		}
	}
	return searchTrawlersWithExactResolvedPersonFilters
}
