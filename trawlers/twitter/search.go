package birdcrawl

import (
	"context"
	"strings"

	"github.com/opentrawl/opentrawl/birdcrawl/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit"
)

func (r *runtime) search(ctx context.Context, query trawlkit.Query) (trawlkit.SearchResult, error) {
	filter := store.SearchFilter{
		Query:  query.Text,
		Limit:  query.Limit,
		After:  timePtr(query.After),
		Before: timePtr(query.Before),
	}
	var out trawlkit.SearchResult
	err := r.withReadOnlyStore(func(st *store.Store) error {
		results, total, err := st.Search(ctx, filter)
		if err != nil {
			return err
		}
		ownerAuthorID, err := st.OwnerAuthorID(ctx)
		if err != nil {
			return err
		}
		out.Results = searchHits(results, ownerAuthorID)
		out.TotalMatches = total
		out.Truncated = total > len(out.Results)
		return nil
	})
	return out, err
}

func searchHits(results []store.SearchResult, ownerAuthorID string) []trawlkit.Hit {
	hits := make([]trawlkit.Hit, 0, len(results))
	for _, result := range results {
		ref := store.TweetRef(result.ID)
		who := jsonWho(result.Who, result.AuthorID, result.InReplyTo, result.InReplyToAuthorID, ownerAuthorID)
		if strings.TrimSpace(who) == "" {
			who = "Post"
		}
		evidence := []trawlkit.EvidenceFragment{trawlkit.TextMatch("Post text", result.Snippet)}
		if strings.TrimSpace(result.InReplyTo) != "" {
			evidence = append(evidence, trawlkit.RelationMatch("Replying to", "reply", result.InReplyTo))
		}
		hits = append(hits, trawlkit.Hit{
			Ref: ref, Time: result.CreatedAt.Local(), AnchorID: trawlkit.MatchAnchorID,
			Summary:  trawlkit.ResultSummary{Title: who, Subtitle: "Twitter (X)"},
			Evidence: evidence,
		})
	}
	return hits
}
