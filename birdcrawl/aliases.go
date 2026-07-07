package birdcrawl

import (
	"context"

	"github.com/opentrawl/opentrawl/birdcrawl/internal/store"
)

func aliasesForSearch(ctx context.Context, st *store.Store, results []store.SearchResult) (map[string]string, error) {
	refs := make([]string, 0, len(results))
	for _, result := range results {
		refs = append(refs, store.TweetRef(result.ID))
	}
	return st.ShortRefAliases(ctx, refs)
}

func aliasesForStats(ctx context.Context, st *store.Store, rows []store.StatsRow) (map[string]string, error) {
	refs := make([]string, 0, len(rows))
	for _, row := range rows {
		refs = append(refs, row.Ref)
	}
	return st.ShortRefAliases(ctx, refs)
}

func aliasesForOpen(ctx context.Context, st *store.Store, result store.OpenResult) (map[string]string, error) {
	refs := []string{store.TweetRef(result.Tweet.ID)}
	for _, ancestor := range result.Ancestors {
		if ancestor.Available {
			refs = append(refs, store.TweetRef(ancestor.Tweet.ID))
		}
	}
	for _, reply := range result.Replies {
		refs = append(refs, store.TweetRef(reply.ID))
	}
	return st.ShortRefAliases(ctx, refs)
}
