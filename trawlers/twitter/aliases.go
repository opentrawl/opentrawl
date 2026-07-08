package birdcrawl

import (
	"context"

	"github.com/openclaw/crawlkit"
	"github.com/opentrawl/opentrawl/birdcrawl/internal/store"
)

func aliasesForSearch(ctx context.Context, req *crawlkit.Request, results []store.SearchResult) (map[string]string, error) {
	refs := make([]string, 0, len(results))
	for _, result := range results {
		refs = append(refs, store.TweetRef(result.ID))
	}
	return req.ShortRefAliases(ctx, refs)
}

func aliasesForStats(ctx context.Context, req *crawlkit.Request, rows []store.StatsRow) (map[string]string, error) {
	refs := make([]string, 0, len(rows))
	for _, row := range rows {
		refs = append(refs, row.Ref)
	}
	return req.ShortRefAliases(ctx, refs)
}

func aliasesForOpen(ctx context.Context, req *crawlkit.Request, result store.OpenResult) (map[string]string, error) {
	refs := []string{store.TweetRef(result.Tweet.ID)}
	for _, ancestor := range result.Ancestors {
		if ancestor.Available {
			refs = append(refs, store.TweetRef(ancestor.Tweet.ID))
		}
	}
	for _, reply := range result.Replies {
		refs = append(refs, store.TweetRef(reply.ID))
	}
	return req.ShortRefAliases(ctx, refs)
}
