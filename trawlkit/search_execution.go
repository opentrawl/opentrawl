package trawlkit

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit/output"
)

// SearchRunOptions configures the shared lifecycle for one source search.
type SearchRunOptions struct {
	StateRoot string
	Timeout   time.Duration
	Verbosity int
	Stderr    io.Writer
}

type typedSearch struct {
	query  Query
	result SearchResult
}

// RunSearch runs one source search through the same paths, configuration,
// logging, timeout, archive preparation, store and request lifecycle as the
// namespaced source runner.
func RunSearch(ctx context.Context, source Crawler, query Query, opts SearchRunOptions) (SearchResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	r := runner{opts: defaultRunOptions()}
	r.opts.stderr = opts.Stderr
	r.opts.readTimeout = opts.Timeout
	r.opts = r.opts.withDefaults()
	verb, err := resolveVerb(source, []string{"search"})
	if err != nil {
		return SearchResult{}, err
	}
	search := &typedSearch{query: query}
	verb.search = search
	result := r.runInProcess(ctx, source, verb, globalOptions{stateRoot: opts.StateRoot, verbosity: opts.Verbosity}, output.JSON, false)
	if result.err != nil {
		return SearchResult{}, result.err
	}
	return search.result, nil
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
