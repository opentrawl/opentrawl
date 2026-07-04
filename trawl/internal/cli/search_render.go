package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/openclaw/crawlkit/render"
)

func renderSearchResults(w io.Writer, merged mergedSearchResult, list searchListContext) error {
	hints := []string{"Open: trawl open REF"}
	if merged.More > 0 {
		hints = append(hints, "More: "+list.MoreCmd)
	}
	return render.WriteList(w, render.List{
		Heading:   searchHeading(list.Query, len(merged.Rows), merged.TotalMatches),
		Hints:     hints,
		Items:     searchListItems(merged.Rows),
		ClampText: 2,
		Empty:     searchEmptySentence(list.Query),
	})
}

type searchListContext struct {
	Query   string
	MoreCmd string
}

func searchListItems(rows []SearchRow) []render.ListItem {
	items := make([]render.ListItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, render.ListItem{
			Time:   row.parsedTime,
			Source: row.surface,
			Who:    normalizeSelf(row.Who),
			Where:  normalizeSelf(row.Where),
			Ref:    searchDisplayRef(row),
			Text:   row.Snippet,
		})
	}
	return items
}

func searchEmptySentence(query string) string {
	if strings.TrimSpace(query) == "" {
		return "No matches."
	}
	return fmt.Sprintf("No matches for %q.", query)
}

func searchHeading(query string, shown, total int) string {
	if strings.TrimSpace(query) == "" {
		return fmt.Sprintf("Search filters: showing %d of %d, newest first.", shown, total)
	}
	return fmt.Sprintf("Search %q: showing %d of %d, newest first.", query, shown, total)
}

// searchDisplayRef degrades per row: the short alias when this row's
// source can resolve one, its full ref otherwise. One source without
// short refs never drags the whole table down to machine refs.
func searchDisplayRef(row SearchRow) string {
	if row.sourceShortRefs && strings.TrimSpace(row.ShortRef) != "" {
		return row.ShortRef
	}
	return row.Ref
}

// normalizeSelf makes every crawler's self-name read the same way.
func normalizeSelf(name string) string {
	if strings.EqualFold(strings.TrimSpace(name), "me") {
		return "me"
	}
	return strings.TrimSpace(name)
}

// moreCommand is the copy-pasteable next page: the same search, run
// with a doubled row budget.
func (c *SearchCmd) moreCommand(query, sourceScope string, shown []SearchRow) string {
	parts := []string{"trawl", "search"}
	if strings.TrimSpace(query) != "" {
		parts = append(parts, quoteExampleArg(query))
	}
	if sourceScope != "" {
		parts = append(parts, "--source", sourceScope)
	}
	if who := strings.TrimSpace(c.Who); who != "" {
		parts = append(parts, "--who", quoteExampleArg(who))
	}
	if after := strings.TrimSpace(c.After); after != "" {
		parts = append(parts, "--after", after)
	}
	if before := strings.TrimSpace(c.Before); before != "" {
		parts = append(parts, "--before", before)
	}
	limit := c.Limit
	if len(shown) > limit {
		limit = len(shown)
	}
	parts = append(parts, "--limit", strconv.Itoa(limit*2))
	return strings.Join(parts, " ")
}
