package cli

import (
	"fmt"
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
	Trawler string   `name:"trawler" help:"Trawler names, separated by commas"`
	Limit   int      `name:"limit" default:"20" help:"Maximum number of results"`
	After   string   `name:"after" help:"Results on or after this date"`
	Before  string   `name:"before" help:"Results on or before this date or time"`
	Who     string   `name:"who" placeholder:"PERSON" help:"Results that involve this person"`

	trawlInvocationDisplay string
}

func (searchCommand SearchCmd) Help() string {
	trawlInvocationDisplay := strings.TrimSpace(searchCommand.trawlInvocationDisplay)
	if trawlInvocationDisplay == "" {
		trawlInvocationDisplay = "./trawl"
	}
	return fmt.Sprintf(`Examples:
  %s search invoice --who alex
  %s search --who "Vendor Support" --after 2026-01-01`, trawlInvocationDisplay, trawlInvocationDisplay)
}

type searchOptions struct {
	limit  int
	after  string
	before string
}

type mergedSearchResult struct {
	Presentations []render.SearchResultPresentationForRootTrawlHumanOutput
	TotalMatches  int
	Truncated     bool
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
	whoInput := strings.TrimSpace(c.Who)
	if strings.TrimSpace(query) == "" && whoInput == "" && strings.TrimSpace(c.After) == "" && strings.TrimSpace(c.Before) == "" {
		return usageErr{humanFacingUsageErrorMessage("Search needs words, a person, or a date range.")}
	}
	if len(selectedTrawlers) == 0 {
		if _, err := fmt.Fprintln(r.stdout, "No trawlers found."); err != nil {
			return err
		}
		return nil
	}

	var whoResolved *personMatchCandidate
	var resolvedPersonMatchFactsFromTrawlers []*person.PersonMatchFactsFromTrawler
	if whoInput != "" {
		resolution := resolveWhoThroughContacts(r, installed, whoInput)
		if len(resolution.OperationFailures) > 0 {
			r.reportWhoFailures(resolution)
			if len(resolution.TrawlersConsulted) == 0 {
				return exitErr{code: 1}
			}
		}
		switch len(resolution.Candidates) {
		case 0:
			return r.writeUnknownWho(whoInput, resolution, trawlerDisplayNamesByIdentity(installed))
		case 1:
			if closeResolution, ok := closeSpellingOnlyResolution(resolution); ok {
				return r.writeUnknownWho(whoInput, closeResolution, trawlerDisplayNamesByIdentity(installed))
			}
			candidate := resolution.Candidates[0]
			whoResolved = &candidate
			resolvedPersonMatchFactsFromTrawlers =
				candidate.PersonMatchFactsFromTrawlers
		default:
			return r.writeAmbiguousWho(whoInput, resolution, trawlerDisplayNamesByIdentity(installed))
		}
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
			_, err := fmt.Fprintf(
				r.stdout,
				"No exact person match for %s in %s.\n",
				resolvedWhoName(whoResolved),
				strings.Join(selectedTrawlerDisplayNames, ", "),
			)
			return err
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
	if whoResolved != nil && len(response.GetTrawlerSearchResults()) > 0 {
		if err := renderWhoResolutionLine(r.stdout, whoInput, *whoResolved, trawlerDisplayNamesByIdentity(installed)); err != nil {
			return err
		}
	}
	if len(merged.Presentations) > 0 || len(response.GetTrawlerSearchResults()) > 0 {
		if err := renderSearchResults(r.stdout, merged, searchListContext{
			Query:                                 query,
			Who:                                   resolvedWhoName(whoResolved),
			MoreCmd:                               c.moreCommand(query, trawlerScope, len(merged.Presentations), r.stdout),
			SearchWasExplicitlyScopedToOneTrawler: searchWasExplicitlyScopedToOneTrawler,
		}); err != nil {
			return err
		}
	}
	r.reportFederationOutcomes(response.GetOperationFailures(), response.GetTrawlersSkippedFromOperation())
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
		return trawlkit.Query{}, usageErr{humanFacingUsageErrorMessage("--after must not be later than --before.")}
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
		return time.Time{}, usageErr{fmt.Errorf("%s %w", name, err)}
	}
	return parsed, nil
}

func normalizeSearchLimit(limit int) (int, error) {
	if limit <= 0 {
		return 0, usageErr{humanFacingUsageErrorMessage("--limit must be at least 1.")}
	}
	return limit, nil
}
