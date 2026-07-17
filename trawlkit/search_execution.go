package trawlkit

import (
	"context"
	"fmt"
)

type typedSearch struct {
	query  Query
	result SearchResult
}

func (operation *typedSearch) execute(ctx context.Context, source Crawler, req *Request) error {
	result, err := executeSearch(ctx, source.(Searcher), req, operation.query)
	if err != nil {
		return err
	}
	operation.result = result
	return nil
}

func executeSearch(ctx context.Context, searcher Searcher, req *Request, query Query) (SearchResult, error) {
	result, err := searcher.Search(ctx, req, query)
	if err != nil {
		return SearchResult{}, err
	}
	if result.WhoResolved == nil && query.WhoResolved != nil {
		result.WhoResolved = query.WhoResolved
	}
	if result.TotalMatches < len(result.Results) {
		return SearchResult{}, fmt.Errorf("search total_matches is less than results length")
	}
	if err := fillSearchShortRefs(ctx, req, result.Results); err != nil {
		return SearchResult{}, err
	}
	return result, nil
}

func fillSearchShortRefs(ctx context.Context, req *Request, hits []Hit) error {
	if req == nil || req.Store == nil {
		// Verbs declared StoreNone manage their own storage; there is no
		// runner-owned short-ref index to consult.
		return nil
	}
	refs := make([]string, 0, len(hits))
	for _, hit := range hits {
		refs = append(refs, hit.Ref)
	}
	aliases, err := req.ShortRefAliases(ctx, refs)
	if err != nil {
		return err
	}
	for i := range hits {
		if alias := aliases[hits[i].Ref]; alias != "" {
			hits[i].ShortRef = alias
		}
	}
	return nil
}
