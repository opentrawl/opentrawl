package cli

import (
	"errors"
	"strconv"
	"strings"

	"github.com/opentrawl/opentrawl/birdcrawl/internal/store"
)

type browseCommand struct {
	kind  string
	role  string
	title string
	empty string
}

var browseCommands = map[string]browseCommand{
	"tweets":    {kind: "tweets", role: "authored", title: "Tweets", empty: "No tweets archived yet. Run 'birdcrawl sync' or 'birdcrawl import archive PATH'."},
	"bookmarks": {kind: "bookmarks", role: "bookmark", title: "Bookmarks", empty: "No bookmarks archived yet. Run 'birdcrawl sync' or 'birdcrawl import archive PATH'."},
	"likes":     {kind: "likes", role: "like", title: "Likes", empty: "No likes archived yet. Run 'birdcrawl sync' or 'birdcrawl import archive PATH'."},
	"mentions":  {kind: "mentions", role: "mention", title: "Mentions", empty: "No mentions archived yet. Run 'birdcrawl sync' or 'birdcrawl import archive PATH'."},
}

func (r *runtime) runBrowse(command browseCommand, args []string) error {
	filter, err := parseListArgs(args)
	if err != nil {
		return usageErr(err)
	}
	return r.withStore(func(st *store.Store) error {
		results, total, err := st.ListByRole(r.ctx, command.role, filter)
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
		return r.print(newListEnvelope(command.kind, results, total, filter.Limit, aliases, ownerAuthorID))
	})
}

func parseListArgs(args []string) (store.ListFilter, error) {
	filter := store.ListFilter{Limit: defaultSearchLimit}
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
				return filter, errors.New("unknown browse flag " + args[i])
			}
			return filter, errors.New("browse commands take no positional arguments")
		}
	}
	if filter.Limit <= 0 {
		filter.Limit = defaultSearchLimit
	}
	if filter.Limit > maxSearchLimit {
		filter.Limit = maxSearchLimit
	}
	return filter, nil
}
