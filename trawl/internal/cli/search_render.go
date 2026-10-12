package cli

import (
	"strconv"
	"strings"
)

func resolvedWhoName(candidate *personMatchCandidate) string {
	if candidate == nil {
		return ""
	}
	return strings.Join(strings.Fields(candidate.Who), " ")
}

// trawlCommandArgumentsForPerson repeats this search for one person, named
// by link.
func (c *SearchCmd) trawlCommandArgumentsForPerson(query, trawlerScope, personLink string) []string {
	withPerson := *c
	withPerson.Who = personLink
	return withPerson.trawlCommandArgumentsWithLimit(query, trawlerScope, c.Limit)
}

// trawlCommandArgumentsShowingMoreMatches is the same search with a doubled
// row budget. The output library quotes it for the shell.
func (c *SearchCmd) trawlCommandArgumentsShowingMoreMatches(query, trawlerScope string, shownResultCount int) []string {
	limit := max(c.Limit, shownResultCount)
	return c.trawlCommandArgumentsWithLimit(query, trawlerScope, limit*2)
}

func (c *SearchCmd) trawlCommandArgumentsWithLimit(query, trawlerScope string, limit int) []string {
	query = strings.TrimSpace(query)
	arguments := []string{"search"}
	if query != "" && !strings.HasPrefix(query, "-") {
		arguments = append(arguments, query)
	}
	if trawlerScope != "" {
		arguments = append(arguments, "--trawler", trawlerScope)
	}
	if who := strings.TrimSpace(c.Who); who != "" {
		arguments = append(arguments, "--who", who)
	}
	if after := strings.TrimSpace(c.After); after != "" {
		arguments = append(arguments, "--after", after)
	}
	if before := strings.TrimSpace(c.Before); before != "" {
		arguments = append(arguments, "--before", before)
	}
	arguments = append(arguments, "--limit", strconv.Itoa(limit))
	if strings.HasPrefix(query, "-") {
		arguments = append(arguments, "--", query)
	}
	return arguments
}
