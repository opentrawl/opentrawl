package telecrawl

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/openclaw/crawlkit"
	"github.com/openclaw/crawlkit/render"
	"github.com/openclaw/telecrawl/internal/store"
)

func (c *Crawler) Search(ctx context.Context, req *crawlkit.Request, query crawlkit.Query) (crawlkit.SearchResult, error) {
	r := c.handler(ctx, req)
	filter, err := c.searchFilter(query)
	if err != nil {
		return crawlkit.SearchResult{}, err
	}
	st, err := store.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return crawlkit.SearchResult{}, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	defer func() { _ = st.Close() }()
	resolved, err := r.resolveSearchWhoFilter(st, &filter)
	if err != nil {
		return crawlkit.SearchResult{}, err
	}
	messages, err := st.Search(ctx, filter)
	if err != nil {
		return crawlkit.SearchResult{}, err
	}
	total, err := st.CountSearch(ctx, filter)
	if err != nil {
		return crawlkit.SearchResult{}, err
	}
	shortRefs, err := st.ShortRefsFor(ctx, messageRefs(messages))
	if err != nil {
		return crawlkit.SearchResult{}, err
	}
	return crawlkit.SearchResult{
		WhoResolved:  crawlkitWhoResolved(query.WhoResolved, resolved),
		Results:      searchHits(messages, shortRefs),
		TotalMatches: total,
		Truncated:    total > len(messages),
	}, nil
}

func (c *Crawler) searchFilter(query crawlkit.Query) (store.MessageFilter, error) {
	if c.search.FromMe && c.search.FromThem {
		return store.MessageFilter{}, usageErr(errors.New("--from-me and --from-them conflict"))
	}
	filter := store.MessageFilter{
		Query:    strings.Join(strings.Fields(query.Text), " "),
		ChatJID:  strings.TrimSpace(c.search.ChatJID),
		Sender:   strings.TrimSpace(c.search.Sender),
		TopicID:  strings.TrimSpace(c.search.TopicID),
		Who:      normalizeWords(query.Who),
		Limit:    query.Limit,
		HasMedia: c.search.HasMedia,
		Pinned:   c.search.Pinned,
		Asc:      c.search.Asc,
	}
	if !query.After.IsZero() {
		after := query.After
		filter.After = &after
	}
	if !query.Before.IsZero() {
		before := query.Before
		filter.Before = &before
	}
	if c.search.FromMe || c.search.FromThem {
		fromMe := c.search.FromMe
		filter.FromMe = &fromMe
	}
	return filter, nil
}

func messageRefs(messages []store.Message) []string {
	refs := make([]string, 0, len(messages))
	for _, message := range messages {
		refs = append(refs, messageRef(message.SourcePK))
	}
	return refs
}

func searchHits(messages []store.Message, shortRefs map[string]string) []crawlkit.Hit {
	hits := make([]crawlkit.Hit, 0, len(messages))
	for _, message := range messages {
		ref := messageRef(message.SourcePK)
		hits = append(hits, crawlkit.Hit{
			Ref:      ref,
			ShortRef: shortRefs[ref],
			Time:     message.Timestamp.Local(),
			Who:      outputField(messageWho(message)),
			Where:    outputField(messageWhereForList(message)),
			Snippet:  outputField(messageSnippet(message)),
		})
	}
	return hits
}

func crawlkitWhoResolved(queryResolved *crawlkit.WhoResolved, resolved *store.WhoCandidate) *crawlkit.WhoResolved {
	if queryResolved != nil {
		return &crawlkit.WhoResolved{Who: queryResolved.Who, Identifiers: append([]string(nil), queryResolved.Identifiers...)}
	}
	if resolved == nil {
		return nil
	}
	return &crawlkit.WhoResolved{Who: resolved.Who, Identifiers: append([]string(nil), resolved.Identifiers...)}
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
	hints := []string{"Open: trawl telegram open REF"}
	if value.Truncated {
		if more := searchMoreHint(value); more != "" {
			hints = append(hints, more)
		}
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
		return fmt.Sprintf("Search filters: showing %s of %s, newest first.", render.FormatInteger(int64(len(value.Results))), render.FormatInteger(int64(value.TotalMatches)))
	}
	return fmt.Sprintf("Search %q: showing %s of %s, newest first.", value.Query, render.FormatInteger(int64(len(value.Results))), render.FormatInteger(int64(value.TotalMatches)))
}

func searchMoreHint(value searchEnvelope) string {
	nextLimit := nextSearchLimit(value.Limit)
	parts := searchCommandParts(value)
	if len(parts) == 0 {
		return ""
	}
	return fmt.Sprintf("More: trawl telegram search %s --limit %d", strings.Join(parts, " "), nextLimit)
}

func searchCommandParts(value searchEnvelope) []string {
	var parts []string
	if query := strings.TrimSpace(value.Query); query != "" {
		parts = append(parts, strconv.Quote(query))
	}
	if who := strings.TrimSpace(value.WhoQuery); who != "" {
		parts = append(parts, "--who", strconv.Quote(who))
	}
	return parts
}

func nextSearchLimit(limit int) int {
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	return limit * 2
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
			Who:   render.HumanIdentity(item.Who),
			Where: render.HumanIdentity(item.Where),
			Ref:   item.Ref,
			Text:  item.Snippet,
		})
	}
	return items
}
