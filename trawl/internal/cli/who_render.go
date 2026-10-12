package cli

// What a reader (or agent) gets when --who or --with names nobody, names
// several people, or some trawlers could not answer.

import (
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit/render"
	"github.com/opentrawl/opentrawl/trawlkit/whomatch"
)

// resolveExactlyOnePerson finds the one person a name means, or fails with
// the people it could mean and the same command for the first of them.
func (r *Runtime) resolveExactlyOnePerson(
	installedTrawlers []InstalledTrawler,
	personQuery string,
	trawlCommandArgumentsForPersonLink func(personLink string) []string,
) (personMatchCandidate, error) {
	resolution := resolveWhoThroughContacts(r, installedTrawlers, personQuery)
	if len(resolution.OperationFailures) > 0 {
		if err := r.reportWhoFailures(resolution); err != nil {
			return personMatchCandidate{}, err
		}
		if len(resolution.TrawlersConsulted) == 0 {
			return personMatchCandidate{}, commandOutcomeAlreadyShown{processExitCode: processExitCodeForFailedCommandAlreadyShown}
		}
	}
	trawlerDisplayNames := trawlerDisplayNamesByIdentity(installedTrawlers)
	switch len(resolution.Candidates) {
	case 0:
		return personMatchCandidate{}, unknownPersonFailure(personQuery, resolution, trawlerDisplayNames, trawlCommandArgumentsForPersonLink)
	case 1:
		if closeResolution, closeSpellingOnly := closeSpellingOnlyResolution(resolution); closeSpellingOnly {
			return personMatchCandidate{}, unknownPersonFailure(personQuery, closeResolution, trawlerDisplayNames, trawlCommandArgumentsForPersonLink)
		}
		return resolution.Candidates[0], nil
	default:
		matchingPeople := personMatchCandidatesWithHumanReadableDisplayNames(resolution.Candidates)
		return personMatchCandidate{}, render.SeveralPeopleMatched(
			personQuery,
			federatedPersonMatchCandidates(matchingPeople, trawlerDisplayNames),
			trawlCommandArgumentsForFirstPerson(matchingPeople, trawlCommandArgumentsForPersonLink),
		)
	}
}

func unknownPersonFailure(
	personQuery string,
	resolution federatedWhoResolution,
	trawlerDisplayNames map[string]string,
	trawlCommandArgumentsForPersonLink func(personLink string) []string,
) render.CommandFailure {
	return render.NoPersonMatched(
		personQuery,
		federatedPersonMatchCandidates(resolution.DidYouMean, trawlerDisplayNames),
		trawlCommandArgumentsForFirstPerson(resolution.DidYouMean, trawlCommandArgumentsForPersonLink),
	)
}

// trawlCommandArgumentsForFirstPerson repeats the reader's command for the
// first listed person, by link, so it names exactly one person.
func trawlCommandArgumentsForFirstPerson(
	people []personMatchCandidate,
	trawlCommandArgumentsForPersonLink func(personLink string) []string,
) []string {
	if len(people) == 0 {
		return nil
	}
	personLink := strings.TrimSpace(people[0].PersonTrawlLink.GetGloballyRoutableTrawlLink())
	if personLink == "" {
		return nil
	}
	return trawlCommandArgumentsForPersonLink(personLink)
}

func personMatchCandidatesWithHumanReadableDisplayNames(personMatchCandidates []personMatchCandidate) []personMatchCandidate {
	personMatchCandidatesWithHumanReadableDisplayNames := make([]personMatchCandidate, 0, len(personMatchCandidates))
	for _, personMatchCandidate := range personMatchCandidates {
		humanReadableDisplayNames := humanReadablePersonDisplayNames(personMatchCandidate)
		if len(humanReadableDisplayNames) == 0 {
			continue
		}
		personMatchCandidate.Who = humanReadableDisplayNames[0]
		personMatchCandidate.AlternativeNames = humanReadableDisplayNames[1:]
		personMatchCandidatesWithHumanReadableDisplayNames = append(personMatchCandidatesWithHumanReadableDisplayNames, personMatchCandidate)
	}
	return personMatchCandidatesWithHumanReadableDisplayNames
}

func humanReadablePersonDisplayNames(personMatchCandidate personMatchCandidate) []string {
	possiblePersonDisplayNames := append([]string{personMatchCandidate.Who}, personMatchCandidate.AlternativeNames...)
	exactPersonFilterIdentifiers := exactPersonFilterIdentifiersFromWhoCandidate(personMatchCandidate)
	humanReadableDisplayNames := make([]string, 0, len(possiblePersonDisplayNames))
	seenHumanReadablePersonDisplayNames := map[string]bool{}
	for _, possibleDisplayName := range possiblePersonDisplayNames {
		possibleDisplayName = strings.Join(strings.Fields(possibleDisplayName), " ")
		if possibleDisplayName == "" || containsWhoIdentifier(exactPersonFilterIdentifiers, possibleDisplayName) || whomatch.IsIdentifierLike(possibleDisplayName, exactPersonFilterIdentifiers) {
			continue
		}
		if strings.Contains(possibleDisplayName, "@") || strings.HasPrefix(possibleDisplayName, "+") || strings.ContainsAny(possibleDisplayName, ":/") {
			continue
		}
		foldedPersonDisplayName := strings.ToLower(possibleDisplayName)
		if seenHumanReadablePersonDisplayNames[foldedPersonDisplayName] {
			continue
		}
		seenHumanReadablePersonDisplayNames[foldedPersonDisplayName] = true
		humanReadableDisplayNames = append(humanReadableDisplayNames, possibleDisplayName)
	}
	return humanReadableDisplayNames
}
