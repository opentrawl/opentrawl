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
			Archive:  tweetArchiveContext(result.Roles, result.AuthorID, ownerAuthorID),
			Evidence: evidence,
		})
	}
	return hits
}

func tweetArchiveContext(roles []string, authorID, ownerAuthorID string) []trawlkit.ArchiveContext {
	seen := map[string]bool{}
	out := make([]trawlkit.ArchiveContext, 0, len(roles)+1)
	for _, role := range roles {
		context, ok := tweetRoleContext(role)
		if !ok || seen[context.Kind] {
			continue
		}
		seen[context.Kind] = true
		out = append(out, context)
	}
	if len(out) == 0 && strings.TrimSpace(ownerAuthorID) != "" && authorID == ownerAuthorID {
		out = append(out, trawlkit.ArchiveContext{Kind: "your_post", Label: "Your post"})
	}
	if len(out) == 0 {
		out = append(out, trawlkit.ArchiveContext{Kind: "archived_post", Label: "Archived post"})
	}
	return out
}

func tweetRoleContext(role string) (trawlkit.ArchiveContext, bool) {
	switch strings.TrimSpace(role) {
	case "authored":
		return trawlkit.ArchiveContext{Kind: "your_post", Label: "Your post"}, true
	case "bookmark":
		return trawlkit.ArchiveContext{Kind: "bookmarked", Label: "Bookmarked"}, true
	case "like":
		return trawlkit.ArchiveContext{Kind: "liked", Label: "Liked"}, true
	case "mention":
		return trawlkit.ArchiveContext{Kind: "mention", Label: "Mention"}, true
	default:
		return trawlkit.ArchiveContext{}, false
	}
}
