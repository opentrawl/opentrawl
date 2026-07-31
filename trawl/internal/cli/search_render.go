package cli

import (
	"io"
	"strconv"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit/render"
)

func renderSearchResults(w io.Writer, merged mergedSearchResult, list searchListContext) error {
	hints := []string{"Open: " + render.TrawlInvocationDisplay(w) + " open LINK"}
	if merged.More > 0 {
		hints = append(hints, "More: "+list.MoreCmd)
	}
	return render.WriteSearchResults(w, render.SearchResults{
		Heading:                               render.SearchResultsHeading(list.Query, list.Who, len(merged.Presentations), merged.TotalMatches),
		Hints:                                 hints,
		Presentations:                         merged.Presentations,
		Empty:                                 render.SearchResultsEmptySentence(list.Query),
		SearchWasExplicitlyScopedToOneTrawler: list.SearchWasExplicitlyScopedToOneTrawler,
	})
}

type searchListContext struct {
	Query                                 string
	Who                                   string
	MoreCmd                               string
	SearchWasExplicitlyScopedToOneTrawler bool
}

func resolvedWhoName(candidate *personMatchCandidate) string {
	if candidate == nil {
		return ""
	}
	return strings.Join(strings.Fields(candidate.Who), " ")
}

// normalizeSelf makes every trawler's self-name read the same way.
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
func (c *SearchCmd) moreCommand(query, trawlerScope string, shownResultCount int, writer io.Writer) string {
	parts := []string{render.TrawlInvocationDisplay(writer), "search"}
	if strings.TrimSpace(query) != "" {
		parts = append(parts, quoteExampleArg(query))
	}
	if trawlerScope != "" {
		parts = append(parts, "--trawler", trawlerScope)
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
	if shownResultCount > limit {
		limit = shownResultCount
	}
	parts = append(parts, "--limit", strconv.Itoa(limit*2))
	return strings.Join(parts, " ")
}
