package telecrawl

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

func (c *Crawler) Search(ctx context.Context, req *trawlkit.Request, query trawlkit.Query) (trawlkit.SearchResult, error) {
	r := c.handler(ctx, req)
	filter, err := c.searchFilter(query)
	if err != nil {
		return trawlkit.SearchResult{}, err
	}
	st, err := store.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return trawlkit.SearchResult{}, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	defer func() { _ = st.Close() }()
	resolved, err := r.resolveSearchWhoFilter(st, &filter)
	if err != nil {
		return trawlkit.SearchResult{}, err
	}
	messages, err := st.Search(ctx, filter)
	if err != nil {
		return trawlkit.SearchResult{}, err
	}
	total, err := st.CountSearch(ctx, filter)
	if err != nil {
		return trawlkit.SearchResult{}, err
	}
	return trawlkit.SearchResult{
		WhoResolved:  trawlkitWhoResolved(query.WhoResolved, resolved),
		Results:      searchHits(messages),
		TotalMatches: total,
		Truncated:    total > len(messages),
	}, nil
}

func (c *Crawler) searchFilter(query trawlkit.Query) (store.MessageFilter, error) {
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

func searchHits(messages []store.Message) []trawlkit.Hit {
	hits := make([]trawlkit.Hit, 0, len(messages))
	for _, message := range messages {
		ref := messageRef(message.SourcePK)
		who := outputField(messageWho(message))
		where := outputField(messageWhereForList(message))
		if where == "" {
			where = "Telegram conversation"
		}
		if who == "" {
			who = "Unknown sender"
		}
		evidenceText := messageSearchEvidenceText(message)
		if evidenceText == "" {
			evidenceText = where
		}
		archiveContext := trawlkit.ArchiveContext{Kind: "received", Label: "Received"}
		if message.FromMe {
			archiveContext = trawlkit.ArchiveContext{Kind: "sent_by_you", Label: "Sent by you"}
		}
		hits = append(hits, trawlkit.Hit{
			Ref: ref, Time: message.Timestamp.Local(), AnchorID: trawlkit.MatchAnchorID,
			Summary:  trawlkit.ResultSummary{Title: where, Subtitle: who},
			Archive:  []trawlkit.ArchiveContext{archiveContext},
			Evidence: []trawlkit.EvidenceFragment{trawlkit.TextMatch("Message from "+who, evidenceText)},
		})
	}
	return hits
}

func messageSearchEvidenceText(message store.Message) string {
	if snippet := outputField(messageSnippet(message)); snippet != "" {
		return snippet
	}
	return outputField(strings.Join([]string{
		message.Text,
		message.MediaTitle,
		message.MetadataTitle,
		message.MetadataURL,
		messageWhereForList(message),
		messageWho(message),
		message.MediaType,
	}, " "))
}

func trawlkitWhoResolved(queryResolved *trawlkit.WhoResolved, resolved *store.WhoCandidate) *trawlkit.WhoResolved {
	if queryResolved != nil {
		return &trawlkit.WhoResolved{Who: queryResolved.Who, Identifiers: append([]string(nil), queryResolved.Identifiers...)}
	}
	if resolved == nil {
		return nil
	}
	return &trawlkit.WhoResolved{Who: resolved.Who, Identifiers: append([]string(nil), resolved.Identifiers...)}
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
