package cli

import (
	"io"
	"strconv"
	"strings"

	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

func renderSearchResults(w io.Writer, merged mergedSearchResult, list searchListContext) error {
	var hints []string
	if merged.More > 0 {
		hints = append(hints, "More: "+list.MoreCmd)
	}
	switch merged.SemanticSearchAvailability {
	case federation.SemanticSearchAvailability_SEMANTIC_SEARCH_AVAILABILITY_AVAILABLE_BUT_STALE:
		hints = append(hints, "Semantic results are catching up with recent archive updates.")
	case federation.SemanticSearchAvailability_SEMANTIC_SEARCH_AVAILABILITY_INDEX_BUILDING:
		hints = append(hints, "Semantic search is still building; showing keyword results only.")
	case federation.SemanticSearchAvailability_SEMANTIC_SEARCH_AVAILABILITY_INDEX_NOT_BUILT,
		federation.SemanticSearchAvailability_SEMANTIC_SEARCH_AVAILABILITY_MODEL_UNAVAILABLE,
		federation.SemanticSearchAvailability_SEMANTIC_SEARCH_AVAILABILITY_INDEX_INCOMPATIBLE:
		hints = append(hints, "Semantic results are unavailable; showing keyword results only.")
	case federation.SemanticSearchAvailability_SEMANTIC_SEARCH_AVAILABILITY_PERSON_FILTER_UNSUPPORTED:
		hints = append(hints, "Semantic search cannot apply --who; showing keyword results only.")
	}
	heading := render.SearchResultsHeading(list.Query, list.Who, len(merged.Presentations), merged.TotalMatches)
	if !merged.TotalMatchesKnown {
		heading = render.SearchResultsHeadingWithoutTotal(list.Query, list.Who, len(merged.Presentations))
	}
	return render.WriteSearchResults(w, render.SearchResults{
		Heading:                               heading,
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
