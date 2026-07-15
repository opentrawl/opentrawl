package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

func renderSearchResults(w io.Writer, merged mergedSearchResult, list searchListContext) error {
	hints := []string{"Open: trawl open REF"}
	if merged.More > 0 {
		hints = append(hints, "More: "+list.MoreCmd)
	}
	return render.WriteList(w, render.List{
		Heading:   searchHeading(list.Query, list.Who, len(merged.Rows), merged.TotalMatches, list.Sort),
		Hints:     hints,
		Items:     searchListItems(merged.Rows),
		ClampText: 2,
		Empty:     searchEmptySentence(list.Query),
	})
}

type searchListContext struct {
	Query   string
	Who     string
	Sort    searchSortMode
	MoreCmd string
}

func searchListItems(rows []SearchRow) []render.ListItem {
	items := make([]render.ListItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, render.ListItem{
			Time:     row.parsedTime,
			DateOnly: row.AllDay,
			Source:   row.surface,
			Ref:      row.Ref,
			Text:     trawlkit.SearchResultText(row.Summary, row.Archive, row.Evidence),
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

func searchHeading(query, who string, shown, total int, sortMode searchSortMode) string {
	query = strings.TrimSpace(query)
	who = strings.TrimSpace(who)
	shownText := render.FormatInteger(int64(shown))
	totalText := render.FormatInteger(int64(total))
	order := "newest first"
	if sortMode == searchSortRelevance {
		order = "best matches first"
	}
	switch {
	case query != "" && who != "":
		return fmt.Sprintf("Search %q with %s: showing %s of %s, %s.", query, who, shownText, totalText, order)
	case query != "":
		return fmt.Sprintf("Search %q: showing %s of %s, %s.", query, shownText, totalText, order)
	case who != "":
		return fmt.Sprintf("Search with %s: showing %s of %s, %s.", who, shownText, totalText, order)
	default:
		return fmt.Sprintf("Search filters: showing %s of %s, %s.", shownText, totalText, order)
	}
}

func resolvedWhoName(candidate *WhoCandidate) string {
	if candidate == nil {
		return ""
	}
	return strings.Join(strings.Fields(candidate.Who), " ")
}

// normalizeSelf makes every crawler's self-name read the same way.
func normalizeSelf(name string) string {
	name = strings.TrimSpace(name)
	if strings.EqualFold(name, "me") {
		return "me"
	}
	lower := strings.ToLower(name)
	if strings.HasPrefix(lower, "me (") && strings.HasSuffix(name, ")") {
		identity := strings.TrimSpace(name[len("me (") : len(name)-1])
		if identity == "" {
			return "me"
		}
		return "me (" + identity + ")"
	}
	return name
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
	if strings.TrimSpace(query) != "" {
		if sortMode := strings.TrimSpace(c.Sort); sortMode != "" {
			parts = append(parts, "--sort", sortMode)
		}
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
