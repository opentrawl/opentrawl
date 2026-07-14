package birdcrawl

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/opentrawl/opentrawl/birdcrawl/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit"
	ckflags "github.com/opentrawl/opentrawl/trawlkit/flags"
)

func (r *runtime) runSearch(args []string) error {
	filter, err := parseSearchArgs(args)
	if err != nil {
		return usageErr(err)
	}
	if strings.TrimSpace(filter.Query) == "" {
		return usageErr(errors.New("search takes a query, e.g. trawl twitter search QUERY"))
	}
	return r.withReadOnlyStore(func(st *store.Store) error {
		results, total, err := st.Search(r.ctx, filter)
		if err != nil {
			return err
		}
		aliases, err := aliasesForSearch(r.ctx, r.req, results)
		if err != nil {
			return err
		}
		ownerAuthorID, err := st.OwnerAuthorID(r.ctx)
		if err != nil {
			return err
		}
		return r.print(newSearchEnvelope(filter.Query, results, total, filter.Limit, aliases, ownerAuthorID))
	})
}

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

func parseSearchArgs(args []string) (store.SearchFilter, error) {
	var filter store.SearchFilter
	limit := defaultSearchLimit
	limitSet := false
	var positionals []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--limit":
			if i+1 >= len(args) {
				return filter, errors.New("--limit takes a value")
			}
			i++
			value, err := strconv.Atoi(args[i])
			if err != nil {
				return filter, fmt.Errorf("--limit must be a number: %s", args[i])
			}
			limit = value
			limitSet = true
		case "--after":
			if i+1 >= len(args) {
				return filter, errors.New("--after takes a value")
			}
			i++
			after, err := parseTimeFlag("--after", args[i], false)
			if err != nil {
				return filter, err
			}
			filter.After = after
		case "--before":
			if i+1 >= len(args) {
				return filter, errors.New("--before takes a value")
			}
			i++
			before, err := parseTimeFlag("--before", args[i], true)
			if err != nil {
				return filter, err
			}
			filter.Before = before
		default:
			if strings.HasPrefix(args[i], "-") {
				return filter, errors.New("unknown search flag " + args[i])
			}
			positionals = append(positionals, args[i])
		}
	}
	if len(positionals) != 1 {
		return filter, errors.New("search takes exactly one query")
	}
	// The one --limit contract (trawlkit/flags): honored exactly as given,
	// and below 1 is a usage error.
	resolved, err := ckflags.Limit(limit, limitSet)
	if err != nil {
		return filter, err
	}
	filter.Limit = resolved
	filter.Query = positionals[0]
	return filter, nil
}
