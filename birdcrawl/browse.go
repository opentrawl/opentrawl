package birdcrawl

import (
	"errors"
	"strings"

	ckflags "github.com/openclaw/crawlkit/flags"
	"github.com/opentrawl/opentrawl/birdcrawl/internal/store"
)

type browseCommand struct {
	kind  string
	role  string
	title string
	empty string
}

var browseCommands = map[string]browseCommand{
	"tweets":    {kind: "tweets", role: "authored", title: "Tweets", empty: "No tweets archived yet. Run 'trawl twitter sync' or 'trawl twitter import archive PATH'."},
	"bookmarks": {kind: "bookmarks", role: "bookmark", title: "Bookmarks", empty: "No bookmarks archived yet. Run 'trawl twitter sync' or 'trawl twitter import archive PATH'."},
	"likes":     {kind: "likes", role: "like", title: "Likes", empty: "No likes archived yet. Run 'trawl twitter sync' or 'trawl twitter import archive PATH'."},
	"mentions":  {kind: "mentions", role: "mention", title: "Mentions", empty: "No mentions archived yet. Run 'trawl twitter sync' or 'trawl twitter import archive PATH'."},
}

func (r *runtime) runBrowse(command browseCommand, args []string) error {
	filter, err := r.parseListArgs(args)
	if err != nil {
		return usageErr(err)
	}
	return r.withReadOnlyStore(func(st *store.Store) error {
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

func (r *runtime) parseListArgs(args []string) (store.ListFilter, error) {
	if len(args) > 0 {
		return store.ListFilter{}, errors.New("browse commands take no positional arguments")
	}
	var filter store.ListFilter
	if strings.TrimSpace(r.c.browseAfter) != "" {
		after, err := parseTimeFlag("--after", r.c.browseAfter, false)
		if err != nil {
			return filter, err
		}
		filter.After = after
	}
	if strings.TrimSpace(r.c.browseBefore) != "" {
		before, err := parseTimeFlag("--before", r.c.browseBefore, true)
		if err != nil {
			return filter, err
		}
		filter.Before = before
	}
	resolved, err := ckflags.Limit(r.c.browseLimit, r.c.browseLimitSet)
	if err != nil {
		return filter, err
	}
	filter.Limit = resolved
	return filter, nil
}
