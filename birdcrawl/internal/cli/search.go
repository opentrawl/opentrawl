package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	ckflags "github.com/openclaw/crawlkit/flags"
	"github.com/opentrawl/opentrawl/birdcrawl/internal/store"
)

func (r *runtime) runSearch(args []string) error {
	filter, err := parseSearchArgs(args)
	if err != nil {
		return usageErr(err)
	}
	if strings.TrimSpace(filter.Query) == "" {
		return usageErr(errors.New("search takes a query, e.g. birdcrawl search QUERY"))
	}
	return r.withStore(func(st *store.Store) error {
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

func parseSearchArgs(args []string) (store.SearchFilter, error) {
	var filter store.SearchFilter
	limit := defaultSearchLimit
	limitSet := false
	all := false
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
		case "--all":
			all = true
		case "--after":
			if i+1 >= len(args) {
				return filter, errors.New("--after takes a value")
			}
			i++
			after, err := parseTimeFlag("--after", args[i])
			if err != nil {
				return filter, err
			}
			filter.After = after
		case "--before":
			if i+1 >= len(args) {
				return filter, errors.New("--before takes a value")
			}
			i++
			before, err := parseTimeFlag("--before", args[i])
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
	// below 1 is a usage error, --all returns everything (Limit 0).
	resolved, err := ckflags.Limit(limit, limitSet, all)
	if err != nil {
		return filter, err
	}
	filter.Limit = resolved
	filter.Query = positionals[0]
	return filter, nil
}
