package cli

import (
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit"
	ckflags "github.com/opentrawl/opentrawl/trawlkit/flags"
	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

type SearchCmd struct {
	Query   []string `arg:"" optional:"" name:"words" help:"Words to find; optional with --who, --after or --before"`
	Trawler string   `name:"trawler" placeholder:"TRAWLERS" help:"Trawler names, separated by commas"`
	Limit   int      `name:"limit" default:"20" placeholder:"COUNT" help:"Maximum number of results"`
	After   string   `name:"after" placeholder:"DATE" help:"${on_or_after_date_option_description}"`
	Before  string   `name:"before" placeholder:"DATE" help:"${on_or_before_date_option_description}"`
	Who     string   `name:"who" placeholder:"PERSON" help:"Results that involve this person"`
}

type searchOptions struct {
	limit  int
	after  string
	before string
}

type mergedSearchResult struct {
	Presentations []render.SearchResultPresentationForRootTrawlHumanOutput
	TotalMatches  int
	More          int
}

func (c *SearchCmd) Run(r *Runtime) error {
	limit, err := normalizeSearchLimit(c.Limit)
	if err != nil {
		return err
	}
	installed := discoverInstalledTrawlers(r.ctx)
	query, selectedTrawlers, trawlerScope, err := r.resolveSearchTarget(installed, c.Query, c.Trawler)
	if err != nil {
		return err
	}
	searchWasExplicitlyScopedToOneTrawler := strings.TrimSpace(trawlerScope) != "" && len(selectedTrawlers) == 1
	selectedTrawlers, err = trawlersForSharedOperation(selectedTrawlers,
		federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH, strings.TrimSpace(trawlerScope) != "")
	if err != nil {
		return err
	}
	whoInput := strings.TrimSpace(c.Who)
	if strings.TrimSpace(query) == "" && whoInput == "" && strings.TrimSpace(c.After) == "" && strings.TrimSpace(c.Before) == "" {
		return render.SearchNeedsWordsPersonOrDates()
	}
	if len(selectedTrawlers) == 0 {
		return render.NoTrawlersAreAvailable()
	}

	var whoResolved *personMatchCandidate
	var resolvedPersonMatchFactsFromTrawlers []*person.PersonMatchFactsFromTrawler
	if whoInput != "" {
		candidate, err := r.resolveExactlyOnePerson(installed, whoInput, func(personLink string) []string {
			return c.trawlCommandArgumentsForPerson(query, trawlerScope, personLink)
		})
		if err != nil {
			return err
		}
		whoResolved = &candidate
		resolvedPersonMatchFactsFromTrawlers = candidate.PersonMatchFactsFromTrawlers
	}

	crawlQuery, err := trawlkitSearchQuery(query, searchOptions{
		limit:  limit,
		after:  c.After,
		before: c.Before,
	}, "")
	if err != nil {
		return err
	}
	if whoResolved != nil {
		selectedTrawlerDisplayNames := make([]string, 0, len(selectedTrawlers))
		trawlersApplicableToResolvedPerson := make([]InstalledTrawler, 0, len(selectedTrawlers))
		for _, selectedTrawler := range selectedTrawlers {
			selectedTrawlerDisplayNames = append(selectedTrawlerDisplayNames, trawlerHumanName(selectedTrawler))
			personMatchFactsFromTrawler := personMatchFactsForTrawlerFromFacts(
				resolvedPersonMatchFactsFromTrawlers,
				installedTrawlerIdentityText(selectedTrawler),
			)
			if len(personMatchFactsFromTrawler.GetExactPersonFilterIdentifiersObservedByTrawlerArchive()) > 0 {
				trawlersApplicableToResolvedPerson = append(trawlersApplicableToResolvedPerson, selectedTrawler)
				continue
			}
			if selectedTrawler.TrawlerDiscoveryError != nil {
				trawlersApplicableToResolvedPerson = append(trawlersApplicableToResolvedPerson, selectedTrawler)
			}
		}
		if len(trawlersApplicableToResolvedPerson) == 0 {
			return render.NothingInTrawlersInvolvesPerson(resolvedWhoName(whoResolved), selectedTrawlerDisplayNames)
		}
		selectedTrawlers = trawlersApplicableToResolvedPerson
	}
	adapters := r.federationSearchTrawlers(selectedTrawlers)
	if whoResolved != nil {
		adapters = applyExactResolvedPersonFiltersToSearchTrawlers(
			adapters,
			resolvedWhoName(whoResolved),
			resolvedPersonMatchFactsFromTrawlers,
		)
	}
	response := r.canonicalSearch(adapters, crawlQuery, limit)
	if searchWasExplicitlyScopedToOneTrawler {
		if err := userInputErrorFromFederatedTrawlerOperationFailures(response.GetOperationFailures()); err != nil {
			return err
		}
	}
	merged, err := searchPresentationsFromResponse(response)
	if err != nil {
		return err
	}
	if len(merged.Presentations) > 0 || len(response.GetTrawlerSearchResults()) > 0 {
		searchResults := render.SearchResults{
			SearchQueryWords:                   query,
			PersonDisplayNameTheSearchInvolves: resolvedWhoName(whoResolved),
			SearchStartDateAsTyped:             c.After,
			SearchEndDateAsTyped:               c.Before,
			SearchMatchesInDisplayOrder:        merged.Presentations,
			TotalSearchMatchCount:              merged.TotalMatches,
		}
		if merged.More > 0 {
			searchResults.TrawlCommandArgumentsShowingMoreMatches = c.trawlCommandArgumentsShowingMoreMatches(
				query,
				trawlerScope,
				len(merged.Presentations),
			)
		}
		if err := r.output.WriteSearchResults(searchResults); err != nil {
			return err
		}
	}
	if err := r.reportFederationOutcomes(response.GetOperationFailures(), response.GetTrawlersSkippedFromOperation()); err != nil {
		return err
	}
	return outcomeExit(response.GetOutcome())
}

func containsWhoIdentifier(identifiers []string, value string) bool {
	value = strings.TrimSpace(value)
	for _, identifier := range identifiers {
		if strings.EqualFold(strings.TrimSpace(identifier), value) {
			return true
		}
	}
	return false
}

// resolveSearchTarget uses --trawler as the only trawler selection path.
func (r *Runtime) resolveSearchTarget(installed []InstalledTrawler, words []string, trawlerCSV string) (string, []InstalledTrawler, string, error) {
	trawlerCSV = strings.TrimSpace(trawlerCSV)
	if trawlerCSV == "" {
		return strings.Join(words, " "), installed, "", nil
	}
	selectedTrawlers, err := r.selectInstalledTrawlers(installed, splitTrawlerCSV(trawlerCSV))
	if err != nil {
		return "", nil, "", err
	}
	return strings.Join(words, " "), selectedTrawlers, trawlerCSV, nil
}

func supportsSharedTrawlerOperation(
	trawler InstalledTrawler,
	operation federation.SharedTrawlerOperation,
) bool {
	for _, command := range trawler.RegisteredTrawlerManifest.GetRegisteredTrawlerCommandDeclarations() {
		if command != nil && command.GetSharedTrawlerOperation() == operation {
			return true
		}
	}
	return false
}

func trawlkitSearchQuery(query string, options searchOptions, who string) (trawlkit.Query, error) {
	after, err := parseSearchDateFlag("--after", options.after)
	if err != nil {
		return trawlkit.Query{}, err
	}
	before, err := parseSearchDateFlag("--before", options.before)
	if err != nil {
		return trawlkit.Query{}, err
	}
	if !after.IsZero() && !before.IsZero() && after.After(before) {
		return trawlkit.Query{}, render.AfterMustNotBeLaterThanBefore()
	}
	return trawlkit.Query{
		Text:         strings.TrimSpace(query),
		Limit:        options.limit,
		After:        after,
		Before:       before,
		PersonFilter: trawlkit.NewUnresolvedSearchPersonFilter(who),
	}, nil
}

func parseSearchDateFlag(name, raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	parseDate := ckflags.Date
	if name == "--before" {
		parseDate = ckflags.ParseDateOrTimeThroughEndOfEnteredPrecision
	}
	parsed, err := parseDate(raw)
	if err != nil {
		return time.Time{}, render.DateOptionNeedsTime(name)
	}
	return parsed, nil
}

func normalizeSearchLimit(limit int) (int, error) {
	if limit <= 0 {
		return 0, render.LimitMustBeAtLeastOne()
	}
	return limit, nil
}
