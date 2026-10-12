package twitter

import (
	"strings"

	ckflags "github.com/opentrawl/opentrawl/trawlkit/flags"
	command "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command"
	"github.com/opentrawl/opentrawl/trawlkit/render"
	"github.com/opentrawl/opentrawl/twitter/internal/store"
)

type browseCommand struct {
	kind  string
	role  string
	title string
	empty string
}

var browseCommands = map[string]browseCommand{
	"tweets":    {kind: "tweets", role: "authored", title: "Tweets", empty: "No tweets archived yet. Run 'trawl update twitter' or 'trawl twitter import archive PATH'."},
	"bookmarks": {kind: "bookmarks", role: "bookmark", title: "Bookmarks", empty: "No bookmarks archived yet. Run 'trawl update twitter' or 'trawl twitter import archive PATH'."},
	"likes":     {kind: "likes", role: "like", title: "Likes", empty: "No likes archived yet. Run 'trawl update twitter' or 'trawl twitter import archive PATH'."},
	"mentions":  {kind: "mentions", role: "mention", title: "Mentions", empty: "No mentions archived yet. Run 'trawl update twitter' or 'trawl twitter import archive PATH'."},
}

func (r *runtime) runBrowse(selectedBrowseCommand browseCommand) (*command.TrawlerCommandResponse, error) {
	filter, err := r.parseListFlags()
	if err != nil {
		return nil, usageErr(err)
	}
	var response *command.TrawlerCommandResponse
	err = r.withReadOnlyStore(func(st *store.Store) error {
		results, total, err := st.ListByRole(r.ctx, selectedBrowseCommand.role, filter)
		if err != nil {
			return err
		}
		ownerAuthorID, err := st.OwnerAuthorID(r.ctx)
		if err != nil {
			return err
		}
		response = twitterMessageListCommandResponse(newListEnvelope(results, total, ownerAuthorID))
		return nil
	})
	return response, err
}

func (r *runtime) parseListFlags() (store.ListFilter, error) {
	var filter store.ListFilter
	if strings.TrimSpace(r.c.browseAfter) != "" {
		after, err := ckflags.Date(r.c.browseAfter)
		if err != nil {
			return filter, render.DateOptionNeedsTime("after")
		}
		filter.After = &after
	}
	if strings.TrimSpace(r.c.browseBefore) != "" {
		before, err := ckflags.ParseDateOrTimeThroughEndOfEnteredPrecision(r.c.browseBefore)
		if err != nil {
			return filter, render.DateOptionNeedsTime("before")
		}
		filter.Before = &before
	}
	resolved, err := ckflags.Limit(r.c.browseLimit, r.c.browseLimitSet)
	if err != nil {
		return filter, err
	}
	filter.Limit = resolved
	return filter, nil
}
