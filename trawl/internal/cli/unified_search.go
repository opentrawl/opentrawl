package cli

import (
	"context"
	"database/sql"
	"strings"
	"sync"

	"github.com/opentrawl/opentrawl/trawl/internal/federation"
	"github.com/opentrawl/opentrawl/trawlkit"
	federationcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	"github.com/opentrawl/opentrawl/trawlkit/semantic"
	"github.com/opentrawl/opentrawl/trawlkit/store"
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
	if _, isAgentRequest := agentServingContextFrom(ctx); isAgentRequest {
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

// openUnifiedIndexForTrawlers opens <stateRoot>/index.db once, verifies in one
// query that every selected exporter trawler already has a cursor, and lazily
// bootstraps only missing trawlers before reopening read-only.
func (r *Runtime) openUnifiedIndexForTrawlers(
	ctx context.Context,
	stateRoot string,
	trawlers []InstalledTrawler,
) (*store.Store, error) {
	indexStore, err := unifiedindex.OpenUnifiedIndexReadOnly(ctx, stateRoot, false)
	if err != nil {
		return nil, err
	}
	if _, isAgentRequest := agentServingContextFrom(ctx); isAgentRequest {
		return indexStore, nil
	}
	indexed := make(map[string]bool, len(trawlers))
	if indexStore != nil {
		indexed = readIndexedTrawlerSet(ctx, indexStore.DB())
	}
	var missing []InstalledTrawler
	for _, t := range trawlers {
		if !trawlerExportsArchiveIndexChanges(t) {
			continue
		}
		if !indexed[installedTrawlerIdentityText(t)] {
			missing = append(missing, t)
		}
	}
	if len(missing) == 0 && indexStore != nil {
		return indexStore, nil
	}
	if indexStore != nil {
		_ = indexStore.Close()
	}
	for _, t := range missing {
		id := installedTrawlerIdentityText(t)
		if _, syncErr := trawlkit.SyncTrawlerArchiveIndex(ctx, stateRoot, t.Trawler, false); syncErr != nil {
			r.logInfo("unified_index_bootstrap_failed", "trawler="+logQuote(id)+" error="+logQuote(syncErr.Error()))
		}
	}
	return unifiedindex.OpenUnifiedIndexReadOnly(ctx, stateRoot, false)
}

func readIndexedTrawlerSet(ctx context.Context, db *sql.DB) map[string]bool {
	out := make(map[string]bool, 16)
	rows, err := db.QueryContext(ctx, `select trawler from indexed_trawler_state where indexed_at_utc <> ''`)
	if err != nil {
		return out
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var trawler string
		if err := rows.Scan(&trawler); err == nil && trawler != "" {
			out[trawler] = true
		}
	}
	return out
}

// runUnifiedAndFederatedSearch queries core index.db for all trawlers that
// implement ArchiveIndexChangeExporter, runs local hybrid semantic search in
// parallel, and concurrently executes normal federated Search RPCs for any
// plugin that does not export to the index, returning one merged canonical
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

	var (
		fallbackWG   sync.WaitGroup
		fallbackResp *federationcontract.FederatedTrawlerSearchOperation
	)
	if len(fallbackTrawlers) > 0 {
		adapters := r.federationSearchTrawlers(fallbackTrawlers)
		if resolvedPersonDisplayName != "" || len(resolvedPersonFacts) > 0 {
			adapters = applyExactResolvedPersonFiltersToSearchTrawlers(
				adapters,
				resolvedPersonDisplayName,
				resolvedPersonFacts,
			)
		}
		fallbackWG.Add(1)
		go func() {
			defer fallbackWG.Done()
			fallbackResp = federation.Search(ctx, adapters, crawlQuery, uint32(limit))
		}()
	}

	if len(exporterTrawlers) == 0 {
		fallbackWG.Wait()
		if fallbackResp != nil {
			return fallbackResp
		}
		return &federationcontract.FederatedTrawlerSearchOperation{
			Outcome: federationcontract.OperationOutcome_OPERATION_OUTCOME_COMPLETE,
		}
	}

	stateRoot, err := trawlkit.ResolveStateRoot(r.stateRoot)
	if err != nil {
		fallbackWG.Wait()
		return r.fallbackSearchAllTrawlers(ctx, selectedTrawlers, crawlQuery, limit, resolvedPersonDisplayName, resolvedPersonFacts)
	}
	indexStore, err := r.openUnifiedIndexForTrawlers(ctx, stateRoot, exporterTrawlers)
	if err != nil || indexStore == nil {
		fallbackWG.Wait()
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

	var (
		searchWG   sync.WaitGroup
		unifiedOut unifiedindex.UnifiedSearchOutput
		unifiedErr error
		semHits    []semantic.SemanticSearchHit
		semErr     error
	)

	if strings.TrimSpace(uq.QueryText) != "" {
		searchWG.Add(1)
		go func() {
			defer searchWG.Done()
			semHits, semErr = semantic.SearchSemanticIndex(ctx, stateRoot, indexStore.DB(), semantic.SemanticSearchQuery{
				QueryText:         uq.QueryText,
				Trawlers:          uq.Trawlers,
				PersonFilterTerms: uq.PersonFilterTerms,
				AfterUnix:         uq.AfterUnix,
				BeforeUnix:        uq.BeforeUnix,
				Limit:             limit,
			})
		}()
	}

	unifiedOut, unifiedErr = unifiedindex.SearchUnifiedIndex(ctx, indexStore.DB(), uq)
	searchWG.Wait()
	fallbackWG.Wait()

	if unifiedErr != nil {
		return r.fallbackSearchAllTrawlers(ctx, selectedTrawlers, crawlQuery, limit, resolvedPersonDisplayName, resolvedPersonFacts)
	}

	if semErr == nil && len(semHits) > 0 {
		fused := semantic.FuseLexicalAndSemanticSearchMatches(unifiedOut.Matches, semHits, limit)
		unifiedOut.Matches = fused
		if uint64(len(fused)) > unifiedOut.TotalRawMatches {
			unifiedOut.TotalRawMatches = uint64(len(fused))
		}
	} else if semErr != nil {
		r.logInfo("semantic_search_fallback_to_lexical", "error="+logQuote(semErr.Error()))
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
		Outcome:     federationcontract.OperationOutcome_OPERATION_OUTCOME_COMPLETE,
		ResultLimit: uint32(max(limit, 0)),
	}

	matches := make([]*federationcontract.FederatedSearchMatch, 0, limit)
	matches = append(matches, unifiedOut.Matches...)

	var fallbackMatches []*federationcontract.FederatedSearchMatch
	if fallbackResp != nil {
		fallbackMatches = fallbackResp.GetSearchMatchesInDisplayOrder()
		op.OperationFailures = append(op.OperationFailures, fallbackResp.GetOperationFailures()...)
		op.TrawlersSkippedFromOperation = append(op.TrawlersSkippedFromOperation, fallbackResp.GetTrawlersSkippedFromOperation()...)
	}

	// Trawlers without an index export have no comparable score, so their
	// matches follow the ranked index matches.
	totalCandidatesBeforeCap := len(matches) + len(fallbackMatches)
	matches = append(matches, fallbackMatches...)
	if len(matches) > limit {
		matches = matches[:limit]
	}
	op.SearchMatchesInDisplayOrder = matches
	op.MoreSearchMatchesExist = unifiedOut.MoreMatchesExist ||
		(fallbackResp != nil && fallbackResp.GetMoreSearchMatchesExist()) ||
		totalCandidatesBeforeCap > len(matches)

	if fallbackResp != nil {
		keptLinks := make(map[string]struct{}, len(matches))
		for _, match := range matches {
			if link := strings.TrimSpace(match.GetTrawlLink().GetGloballyRoutableTrawlLink()); link != "" {
				keptLinks[link] = struct{}{}
			}
		}
		for _, ts := range fallbackResp.GetTrawlerSearchResults() {
			kept := make([]*federationcontract.FederatedSearchMatch, 0, len(ts.GetSearchMatchesFromTrawlerInDisplayOrder()))
			for _, match := range ts.GetSearchMatchesFromTrawlerInDisplayOrder() {
				if link := strings.TrimSpace(match.GetTrawlLink().GetGloballyRoutableTrawlLink()); link != "" {
					if _, ok := keptLinks[link]; ok {
						kept = append(kept, match)
					}
				}
			}
			more := ts.GetMoreSearchMatchesExist() ||
				ts.GetTotalSearchMatches() > uint64(len(kept)) ||
				len(ts.GetSearchMatchesFromTrawlerInDisplayOrder()) > len(kept)
			op.TrawlerSearchResults = append(op.TrawlerSearchResults, &federationcontract.TrawlerSearchResult{
				RegisteredTrawler:                      ts.GetRegisteredTrawler(),
				RegisteredTrawlerDisplayName:           ts.GetRegisteredTrawlerDisplayName(),
				SearchMatchesFromTrawlerInDisplayOrder: kept,
				TotalSearchMatches:                     ts.GetTotalSearchMatches(),
				MoreSearchMatchesExist:                 more,
				TotalSearchMatchesIsLowerBound:         ts.GetTotalSearchMatchesIsLowerBound(),
			})
		}
	}

	// Record one TrawlerSearchResult per indexed trawler that matched using its
	// own source-specific count so per-app totals and more-matches flags stay
	// consistent between all-app and single-app searches.
	if len(exporterTrawlers) > 0 {
		matchesByTrawler := make(map[string][]*federationcontract.FederatedSearchMatch, len(exporterTrawlers))
		for _, match := range matches {
			link := strings.TrimSpace(match.GetTrawlLink().GetGloballyRoutableTrawlLink())
			if trawlerID, _, ok := strings.Cut(link, ":"); ok && trawlerID != "" {
				matchesByTrawler[trawlerID] = append(matchesByTrawler[trawlerID], match)
			}
		}
		var contributing []InstalledTrawler
		for _, t := range exporterTrawlers {
			id := installedTrawlerIdentityText(t)
			if len(matchesByTrawler[id]) > 0 || unifiedOut.TotalMatchesByTrawler[id] > 0 {
				contributing = append(contributing, t)
			}
		}
		if len(contributing) == 0 {
			first := exporterTrawlers[0]
			op.TrawlerSearchResults = append(op.TrawlerSearchResults, &federationcontract.TrawlerSearchResult{
				RegisteredTrawler:              first.RegisteredTrawlerManifest.GetRegisteredTrawler(),
				RegisteredTrawlerDisplayName:   trawlerHumanName(first),
				TotalSearchMatches:             0,
				MoreSearchMatchesExist:         false,
				TotalSearchMatchesIsLowerBound: false,
			})
		} else {
			for _, t := range contributing {
				id := installedTrawlerIdentityText(t)
				trawlerMatches := matchesByTrawler[id]
				observedTotal := unifiedOut.TotalMatchesByTrawler[id]
				isLowerBound := false
				if uint64(len(trawlerMatches)) > observedTotal {
					// Semantic fusion added hits beyond the lexical count for this trawler.
					observedTotal = uint64(len(trawlerMatches))
					isLowerBound = true
				}
				op.TrawlerSearchResults = append(op.TrawlerSearchResults, &federationcontract.TrawlerSearchResult{
					RegisteredTrawler:                      t.RegisteredTrawlerManifest.GetRegisteredTrawler(),
					RegisteredTrawlerDisplayName:           trawlerHumanName(t),
					SearchMatchesFromTrawlerInDisplayOrder: trawlerMatches,
					TotalSearchMatches:                     observedTotal,
					MoreSearchMatchesExist:                 observedTotal > uint64(len(trawlerMatches)),
					TotalSearchMatchesIsLowerBound:         isLowerBound,
				})
			}
		}
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
