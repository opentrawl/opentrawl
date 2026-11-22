package cli

import (
	"context"
	"strings"

	"github.com/opentrawl/opentrawl/trawl/internal/federation"
	"github.com/opentrawl/opentrawl/trawlkit"
	federationcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	"github.com/opentrawl/opentrawl/trawlkit/unifiedindex"
)

func trawlerExportsArchiveIndexChanges(trawler InstalledTrawler) bool {
	if trawler.TrawlerDiscoveryError != nil || trawler.Trawler == nil {
		return false
	}
	_, ok := trawler.Trawler.(trawlkit.ArchiveIndexChangeExporter)
	return ok
}

// ensureUnifiedIndexBootstrappedForTrawlers lazily syncs any selected
// ArchiveIndexChangeExporter trawler that has an archive on disk but has not
// yet written an initial cursor into <stateRoot>/index.db.
func (r *Runtime) ensureUnifiedIndexBootstrappedForTrawlers(
	ctx context.Context,
	trawlers []InstalledTrawler,
) error {
	if len(trawlers) == 0 {
		return nil
	}
	stateRoot, err := trawlkit.ResolveStateRoot(r.stateRoot)
	if err != nil {
		return err
	}
	indexStore, err := unifiedindex.OpenUnifiedIndexReadOnly(ctx, stateRoot, false)
	if err != nil {
		return err
	}
	indexed := make(map[string]bool, len(trawlers))
	if indexStore != nil {
		for _, t := range trawlers {
			id := installedTrawlerIdentityText(t)
			_, hasIndexed, cursorErr := unifiedindex.ReadTrawlerIndexCursor(ctx, indexStore.DB(), id)
			if cursorErr == nil && hasIndexed {
				indexed[id] = true
			}
		}
		_ = indexStore.Close()
	}
	for _, t := range trawlers {
		if !trawlerExportsArchiveIndexChanges(t) {
			continue
		}
		id := installedTrawlerIdentityText(t)
		if indexed[id] {
			continue
		}
		if _, syncErr := trawlkit.SyncTrawlerArchiveIndex(ctx, stateRoot, t.Trawler, false); syncErr != nil {
			r.logInfo("unified_index_bootstrap_failed", "trawler="+logQuote(id)+" error="+logQuote(syncErr.Error()))
		}
	}
	return nil
}

// runUnifiedAndFederatedSearch queries core index.db for all trawlers that
// implement ArchiveIndexChangeExporter and runs normal federated Search RPCs
// for any remaining trawlers (such as Photos), returning one merged canonical
// FederatedTrawlerSearchOperation used by both `trawl search` and `__app search`.
func (r *Runtime) runUnifiedAndFederatedSearch(
	ctx context.Context,
	selectedTrawlers []InstalledTrawler,
	crawlQuery trawlkit.Query,
	limit int,
	resolvedPersonDisplayName string,
	resolvedPersonFacts []*person.PersonMatchFactsFromTrawler,
) *federationcontract.FederatedTrawlerSearchOperation {
	var exporterTrawlers []InstalledTrawler
	var fallbackTrawlers []InstalledTrawler
	for _, t := range selectedTrawlers {
		if trawlerExportsArchiveIndexChanges(t) {
			exporterTrawlers = append(exporterTrawlers, t)
		} else {
			fallbackTrawlers = append(fallbackTrawlers, t)
		}
	}

	var fallbackResp *federationcontract.FederatedTrawlerSearchOperation
	if len(fallbackTrawlers) > 0 {
		adapters := r.federationSearchTrawlers(fallbackTrawlers)
		if resolvedPersonDisplayName != "" || len(resolvedPersonFacts) > 0 {
			adapters = applyExactResolvedPersonFiltersToSearchTrawlers(
				adapters,
				resolvedPersonDisplayName,
				resolvedPersonFacts,
			)
		}
		fallbackResp = federation.Search(ctx, adapters, crawlQuery, uint32(limit))
	}

	if len(exporterTrawlers) == 0 {
		if fallbackResp != nil {
			return fallbackResp
		}
		return &federationcontract.FederatedTrawlerSearchOperation{
			Outcome: federationcontract.OperationOutcome_OPERATION_OUTCOME_COMPLETE,
		}
	}

	_ = r.ensureUnifiedIndexBootstrappedForTrawlers(ctx, exporterTrawlers)
	stateRoot, err := trawlkit.ResolveStateRoot(r.stateRoot)
	if err != nil {
		return r.fallbackSearchAllTrawlers(ctx, selectedTrawlers, crawlQuery, limit, resolvedPersonDisplayName, resolvedPersonFacts)
	}
	indexStore, err := unifiedindex.OpenUnifiedIndexReadOnly(ctx, stateRoot, false)
	if err != nil || indexStore == nil {
		return r.fallbackSearchAllTrawlers(ctx, selectedTrawlers, crawlQuery, limit, resolvedPersonDisplayName, resolvedPersonFacts)
	}
	defer func() { _ = indexStore.Close() }()

	trawlerIDs := make([]string, 0, len(exporterTrawlers))
	for _, t := range exporterTrawlers {
		trawlerIDs = append(trawlerIDs, installedTrawlerIdentityText(t))
	}
	uq := unifiedindex.UnifiedSearchQuery{
		QueryText:         crawlQuery.Text,
		Trawlers:          trawlerIDs,
		PersonFilterTerms: collectPersonFilterTermsForUnifiedSearch(resolvedPersonDisplayName, resolvedPersonFacts),
		Limit:             limit,
		CollapseThreads:   true,
	}
	if !crawlQuery.After.IsZero() {
		v := crawlQuery.After.Unix()
		uq.AfterUnix = &v
	}
	if !crawlQuery.Before.IsZero() {
		v := crawlQuery.Before.Unix()
		uq.BeforeUnix = &v
	}

	unifiedOut, err := unifiedindex.SearchUnifiedIndex(ctx, indexStore.DB(), uq)
	if err != nil {
		return r.fallbackSearchAllTrawlers(ctx, selectedTrawlers, crawlQuery, limit, resolvedPersonDisplayName, resolvedPersonFacts)
	}

	return mergeUnifiedAndFallbackSearchOperations(
		exporterTrawlers,
		unifiedOut,
		fallbackResp,
		limit,
	)
}

func (r *Runtime) fallbackSearchAllTrawlers(
	ctx context.Context,
	selectedTrawlers []InstalledTrawler,
	crawlQuery trawlkit.Query,
	limit int,
	resolvedPersonDisplayName string,
	resolvedPersonFacts []*person.PersonMatchFactsFromTrawler,
) *federationcontract.FederatedTrawlerSearchOperation {
	adapters := r.federationSearchTrawlers(selectedTrawlers)
	if resolvedPersonDisplayName != "" || len(resolvedPersonFacts) > 0 {
		adapters = applyExactResolvedPersonFiltersToSearchTrawlers(
			adapters,
			resolvedPersonDisplayName,
			resolvedPersonFacts,
		)
	}
	return federation.Search(ctx, adapters, crawlQuery, uint32(limit))
}

func collectPersonFilterTermsForUnifiedSearch(
	resolvedDisplayName string,
	facts []*person.PersonMatchFactsFromTrawler,
) []string {
	seen := make(map[string]struct{}, 8)
	var terms []string
	add := func(raw string) {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			return
		}
		key := strings.ToLower(trimmed)
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		terms = append(terms, trimmed)
	}
	add(resolvedDisplayName)
	for _, fact := range facts {
		if fact == nil {
			continue
		}
		for _, name := range fact.GetPersonDisplayNamesObservedByTrawlerArchive() {
			add(name)
		}
		for _, ident := range fact.GetExactPersonFilterIdentifiersObservedByTrawlerArchive() {
			if ident != nil {
				add(ident.GetExactPersonFilterIdentifier())
			}
		}
	}
	return terms
}

func mergeUnifiedAndFallbackSearchOperations(
	exporterTrawlers []InstalledTrawler,
	unifiedOut unifiedindex.UnifiedSearchOutput,
	fallbackResp *federationcontract.FederatedTrawlerSearchOperation,
	limit int,
) *federationcontract.FederatedTrawlerSearchOperation {
	op := &federationcontract.FederatedTrawlerSearchOperation{
		Outcome: federationcontract.OperationOutcome_OPERATION_OUTCOME_COMPLETE,
	}

	matches := make([]*federationcontract.FederatedSearchMatch, 0, limit)
	matches = append(matches, unifiedOut.Matches...)

	var fallbackMatches []*federationcontract.FederatedSearchMatch
	if fallbackResp != nil {
		fallbackMatches = fallbackResp.GetSearchMatchesInDisplayOrder()
		op.OperationFailures = append(op.OperationFailures, fallbackResp.GetOperationFailures()...)
		op.TrawlersSkippedFromOperation = append(op.TrawlersSkippedFromOperation, fallbackResp.GetTrawlersSkippedFromOperation()...)
		op.TrawlerSearchResults = append(op.TrawlerSearchResults, fallbackResp.GetTrawlerSearchResults()...)
	}

	if len(fallbackMatches) > 0 {
		// Reserve a bounded slice for non-indexed trawlers (such as Photos) when
		// searching across multiple sources so neither side crowds out the other.
		fallbackAllowance := max(limit/4, 3)
		if len(matches) < limit {
			fallbackAllowance = max(fallbackAllowance, limit-len(matches))
		}
		if fallbackAllowance > len(fallbackMatches) {
			fallbackAllowance = len(fallbackMatches)
		}
		keepUnified := min(len(matches), max(0, limit-fallbackAllowance))
		merged := make([]*federationcontract.FederatedSearchMatch, 0, keepUnified+fallbackAllowance)
		merged = append(merged, matches[:keepUnified]...)
		merged = append(merged, fallbackMatches[:fallbackAllowance]...)
		matches = merged
	}
	if len(matches) > limit {
		matches = matches[:limit]
	}
	op.SearchMatchesInDisplayOrder = matches

	// Record one unified TrawlerSearchResult per indexed trawler that contributed
	// or was queried so TotalSearchMatches and empty-search reporting stay truthful.
	unifiedTotal := unifiedOut.TotalRawMatches
	if len(exporterTrawlers) > 0 {
		first := exporterTrawlers[0]
		op.TrawlerSearchResults = append(op.TrawlerSearchResults, &federationcontract.TrawlerSearchResult{
			RegisteredTrawler:              first.RegisteredTrawlerManifest.GetRegisteredTrawler(),
			RegisteredTrawlerDisplayName:   trawlerHumanName(first),
			TotalSearchMatches:             unifiedTotal,
			MoreSearchMatchesExist:         unifiedOut.MoreMatchesExist,
			TotalSearchMatchesIsLowerBound: false,
		})
	}

	if len(op.OperationFailures) > 0 {
		if len(op.SearchMatchesInDisplayOrder) > 0 || len(exporterTrawlers) > 0 {
			op.Outcome = federationcontract.OperationOutcome_OPERATION_OUTCOME_PARTIAL
		} else {
			op.Outcome = federationcontract.OperationOutcome_OPERATION_OUTCOME_FAILED
		}
	}
	return op
}
