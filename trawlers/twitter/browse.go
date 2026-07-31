package twitter

import (
	"errors"
	"fmt"
	"strings"

	ckflags "github.com/opentrawl/opentrawl/trawlkit/flags"
	commandv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command/v1"
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

func (r *runtime) runBrowse(command browseCommand, args []string) (*commandv1.TrawlerCommandResponse, error) {
	filter, err := r.parseListArgs(args)
	if err != nil {
		return nil, usageErr(err)
	}
	var response *commandv1.TrawlerCommandResponse
	err = r.withReadOnlyStore(func(st *store.Store) error {
		results, total, err := st.ListByRole(r.ctx, command.role, filter)
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

func (r *runtime) parseListArgs(args []string) (store.ListFilter, error) {
	if len(args) > 0 {
		return store.ListFilter{}, errors.New("browse commands take no positional arguments")
	}
	var filter store.ListFilter
	if strings.TrimSpace(r.c.browseAfter) != "" {
		after, err := ckflags.Date(r.c.browseAfter)
		if err != nil {
			return filter, fmt.Errorf("--after %w", err)
		}
		filter.After = &after
	}
	if strings.TrimSpace(r.c.browseBefore) != "" {
		before, err := ckflags.ParseDateOrTimeThroughEndOfEnteredPrecision(r.c.browseBefore)
		if err != nil {
			return filter, fmt.Errorf("--before %w", err)
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
