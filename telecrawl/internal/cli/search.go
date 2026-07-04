package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/openclaw/crawlkit/render"
	"github.com/openclaw/telecrawl/internal/store"
)

func (r *runtime) runSearch(args []string) error {
	filter, err := r.messageFilter("telecrawl search", args, true, defaultSearchLimit)
	if err != nil {
		return err
	}
	if filter.Limit <= 0 {
		filter.Limit = defaultSearchLimit
	}
	if filter.Limit > maxSearchLimit {
		filter.Limit = maxSearchLimit
	}
	return r.withStore(func(st *store.Store) error {
		resolved, err := r.resolveSearchWhoFilter(st, &filter)
		if err != nil {
			return err
		}
		messages, err := st.Search(r.ctx, filter)
		if err != nil {
			return err
		}
		total, err := st.CountSearch(r.ctx, filter)
		if err != nil {
			return err
		}
		shortRefs, err := st.ShortRefsFor(r.ctx, messageRefs(messages))
		if err != nil {
			return err
		}
		return r.print(newSearchEnvelope(filter.Query, messages, total, filter.Limit, filter.Who, resolved, shortRefs))
	})
}

func messageRefs(messages []store.Message) []string {
	refs := make([]string, 0, len(messages))
	for _, message := range messages {
		refs = append(refs, messageRef(message.SourcePK))
	}
	return refs
}

func (r *runtime) resolveSearchWhoFilter(st *store.Store, filter *store.MessageFilter) (*store.WhoCandidate, error) {
	if strings.TrimSpace(filter.Who) == "" {
		return nil, nil
	}
	candidates, err := st.ResolveWho(r.ctx, filter.Who)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, r.unknownWhoError(filter.Who, candidates)
	}
	if len(candidates) > 1 {
		return nil, r.ambiguousWhoError(filter.Query, filter.Who, candidates)
	}
	candidate := candidates[0]
	if candidate.MatchedOnlyByCloseSpelling() {
		return nil, r.unknownWhoError(filter.Who, candidates)
	}
	filter.WhoParticipants = candidate.Participants
	filter.WhoResolved = true
	return &candidate, nil
}

func (r *runtime) printSearch(value searchEnvelope) error {
	hints := []string{"Open: telecrawl open REF"}
	if value.Truncated {
		hints = append(hints, searchMoreHint(value))
	}
	return render.WriteList(r.stdout, render.List{
		Heading:   searchHeading(value),
		Hints:     hints,
		Items:     searchListItems(value.Results),
		ClampText: 2,
		Empty:     searchEmptyText(value.Query),
	})
}

func searchHeading(value searchEnvelope) string {
	if strings.TrimSpace(value.Query) == "" {
		return fmt.Sprintf("Search filters: showing %d of %d.", len(value.Results), value.TotalMatches)
	}
	return fmt.Sprintf("Search %q: showing %d of %d.", value.Query, len(value.Results), value.TotalMatches)
}

func searchMoreHint(value searchEnvelope) string {
	nextLimit := nextSearchLimit(value.Limit)
	if strings.TrimSpace(value.Query) == "" {
		return fmt.Sprintf("More: telecrawl search --limit %d", nextLimit)
	}
	return fmt.Sprintf("More: telecrawl search %s --limit %d", strconv.Quote(value.Query), nextLimit)
}

func nextSearchLimit(limit int) int {
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	next := limit * 2
	if next > maxSearchLimit {
		return maxSearchLimit
	}
	return next
}

func searchEmptyText(query string) string {
	if strings.TrimSpace(query) == "" {
		return "No matches."
	}
	return fmt.Sprintf("No matches for %q.", query)
}

func searchListItems(results []searchResult) []render.ListItem {
	items := make([]render.ListItem, 0, len(results))
	for _, item := range results {
		items = append(items, render.ListItem{
			Time:  parseRenderTime(item.Time),
			Who:   item.Who,
			Where: item.Where,
			Ref:   displayRef(item.Ref, item.ShortRef),
			Text:  item.Snippet,
		})
	}
	return items
}
