package cli

import (
	"errors"
	"strconv"
	"strings"

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
	return r.withReadOnlyStore(func(st *store.Store) error {
		results, total, err := st.Search(r.ctx, filter)
		if err != nil {
			return err
		}
		return r.print(newSearchEnvelope(filter.Query, results, total))
	})
}

func parseSearchArgs(args []string) (store.SearchFilter, error) {
	filter := store.SearchFilter{Limit: defaultSearchLimit}
	var positionals []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--limit":
			if i+1 >= len(args) {
				return filter, errors.New("--limit takes a value")
			}
			i++
			limit, err := strconv.Atoi(args[i])
			if err != nil {
				return filter, err
			}
			filter.Limit = limit
		case "--after":
			if i+1 >= len(args) {
				return filter, errors.New("--after takes a value")
			}
			i++
			after, err := parseRFC3339Flag(args[i])
			if err != nil {
				return filter, err
			}
			filter.After = after
		case "--before":
			if i+1 >= len(args) {
				return filter, errors.New("--before takes a value")
			}
			i++
			before, err := parseRFC3339Flag(args[i])
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
	if filter.Limit <= 0 {
		filter.Limit = defaultSearchLimit
	}
	if filter.Limit > maxSearchLimit {
		filter.Limit = maxSearchLimit
	}
	filter.Query = positionals[0]
	return filter, nil
}
