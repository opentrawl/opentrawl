package birdcrawl

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/openclaw/crawlkit"
	ckflags "github.com/openclaw/crawlkit/flags"
	"github.com/opentrawl/opentrawl/birdcrawl/internal/store"
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
		aliases, err := aliasesForSearch(r.ctx, st, results)
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

func (r *runtime) search(ctx context.Context, query crawlkit.Query) (crawlkit.SearchResult, error) {
	filter := store.SearchFilter{
		Query:  query.Text,
		Limit:  query.Limit,
		After:  timePtr(query.After),
		Before: timePtr(query.Before),
	}
	var out crawlkit.SearchResult
	err := r.withReadOnlyStore(func(st *store.Store) error {
		results, total, err := st.Search(ctx, filter)
		if err != nil {
			return err
		}
		aliases, err := aliasesForSearch(ctx, st, results)
		if err != nil {
			return err
		}
		ownerAuthorID, err := st.OwnerAuthorID(ctx)
		if err != nil {
			return err
		}
		out.Results = searchHits(results, aliases, ownerAuthorID)
		out.TotalMatches = total
		out.Truncated = total > len(out.Results)
		return nil
	})
	return out, err
}

func searchHits(results []store.SearchResult, aliases map[string]string, ownerAuthorID string) []crawlkit.Hit {
	hits := make([]crawlkit.Hit, 0, len(results))
	for _, result := range results {
		ref := store.TweetRef(result.ID)
		hits = append(hits, crawlkit.Hit{
			Ref:      ref,
			ShortRef: aliases[ref],
			Time:     result.CreatedAt.Local(),
			Who:      jsonWho(result.Who, result.AuthorID, result.InReplyTo, result.InReplyToAuthorID, ownerAuthorID),
			Where:    result.Where,
			Snippet:  result.Snippet,
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
	// The one --limit contract (crawlkit/flags): honored exactly as given,
	// and below 1 is a usage error.
	resolved, err := ckflags.Limit(limit, limitSet)
	if err != nil {
		return filter, err
	}
	filter.Limit = resolved
	filter.Query = positionals[0]
	return filter, nil
}
