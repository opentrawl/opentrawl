package cli

// What a reader (or agent) gets when --who or --with names nobody, names
// several people, or some trawlers could not answer.

import (
	"strings"

	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
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
	if len(resolution.Candidates) > 1 {
		resolution.OperationFailures = append(resolution.OperationFailures,
			r.readPersonMessageCountsFromTrawlers(installedTrawlers, resolution.Candidates)...)
	}
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
		if merged, ok := mergeCandidatesMatchingExactContactIdentifier(personQuery, resolution.Candidates); ok {
			return merged, nil
		}
		matchingPeople := personMatchCandidatesWithHumanReadableDisplayNames(resolution.Candidates)
		return personMatchCandidate{}, render.SeveralPeopleMatched(
			personQuery,
			federatedPersonMatchCandidates(matchingPeople, trawlerDisplayNames),
			trawlCommandArgumentsForFirstPerson(matchingPeople, trawlCommandArgumentsForPersonLink),
		)
	}
}

func mergeCandidatesMatchingExactContactIdentifier(
	personQuery string,
	candidates []personMatchCandidate,
) (personMatchCandidate, bool) {
	queryEmail := normalizeExactEmailForWhoQuery(personQuery)
	queryPhone := ""
	if queryEmail == "" {
		queryPhone = normalizeExactPhoneForWhoQuery(personQuery)
	}
	if queryEmail == "" && queryPhone == "" {
		return personMatchCandidate{}, false
	}
	matching := make([]personMatchCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidateHasExactContactIdentifier(candidate, queryEmail, queryPhone) {
			matching = append(matching, candidate)
		}
	}
	if len(matching) == 0 {
		return personMatchCandidate{}, false
	}
	merged := matching[0]
	allFacts := append([]*person.PersonMatchFactsFromTrawler(nil), merged.PersonMatchFactsFromTrawlers...)
	allAlternatives := append([]string(nil), merged.AlternativeNames...)
	countsByTrawler := map[string]*person.PersonMessageCountFromTrawlerArchive{}
	for _, candidate := range matching {
		if candidate.LatestMatchingArchiveRecordTime.After(merged.LatestMatchingArchiveRecordTime) {
			merged.LatestMatchingArchiveRecordTime = candidate.LatestMatchingArchiveRecordTime
		}
		for _, count := range candidate.PersonMessageCountsFromTrawlerArchives {
			if count == nil {
				continue
			}
			trawlerID := strings.TrimSpace(count.GetRegisteredTrawler().GetRegisteredTrawlerIdentity())
			if trawlerID == "" {
				continue
			}
			existing := countsByTrawler[trawlerID]
			if existing == nil {
				countsByTrawler[trawlerID] = &person.PersonMessageCountFromTrawlerArchive{
					RegisteredTrawler:                           count.GetRegisteredTrawler(),
					RegisteredTrawlerDisplayName:                count.GetRegisteredTrawlerDisplayName(),
					MessageCountInvolvingPersonInTrawlerArchive: count.GetMessageCountInvolvingPersonInTrawlerArchive(),
				}
			} else {
				existing.MessageCountInvolvingPersonInTrawlerArchive += count.GetMessageCountInvolvingPersonInTrawlerArchive()
			}
		}
	}
	for _, candidate := range matching[1:] {
		allFacts = append(allFacts, candidate.PersonMatchFactsFromTrawlers...)
		if !strings.EqualFold(candidate.Who, merged.Who) {
			allAlternatives = append(allAlternatives, candidate.Who)
		}
		allAlternatives = append(allAlternatives, candidate.AlternativeNames...)
	}
	merged.PersonMatchFactsFromTrawlers = normalizedPersonMatchFactsFromTrawlers(allFacts)
	merged.AlternativeNames = normalisedStringList(allAlternatives)
	mergedCounts := make([]*person.PersonMessageCountFromTrawlerArchive, 0, len(countsByTrawler))
	merged.MessageCountInvolvingPerson = 0
	for _, count := range countsByTrawler {
		mergedCounts = append(mergedCounts, count)
		merged.MessageCountInvolvingPerson += int(count.GetMessageCountInvolvingPersonInTrawlerArchive())
	}
	merged.PersonMessageCountsFromTrawlerArchives = normalizedPersonMessageCountsFromTrawlerArchives(mergedCounts)
	return merged, true
}

func candidateHasExactContactIdentifier(candidate personMatchCandidate, queryEmail, queryPhone string) bool {
	for _, identifier := range exactPersonFilterIdentifiersFromWhoCandidate(candidate) {
		if queryEmail != "" {
			cleaned := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(identifier)), "mailto:")
			if idx := strings.IndexByte(cleaned, ':'); idx >= 0 && !strings.Contains(cleaned[:idx], "@") {
				cleaned = cleaned[idx+1:]
			}
			if normalizeExactEmailForWhoQuery(cleaned) == queryEmail {
				return true
			}
		} else if queryPhone != "" {
			if normalizeExactPhoneForWhoQuery(identifier) == queryPhone {
				return true
			}
		}
	}
	return false
}

func normalizeExactEmailForWhoQuery(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.TrimPrefix(value, "mailto:")
	if value == "" || strings.ContainsAny(value, " \t\r\n") {
		return ""
	}
	local, domain, ok := strings.Cut(value, "@")
	if !ok || local == "" || domain == "" || !strings.Contains(domain, ".") {
		return ""
	}
	if domain == "googlemail.com" {
		domain = "gmail.com"
	}
	return local + "@" + domain
}

func normalizeExactPhoneForWhoQuery(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, "@") {
		return ""
	}
	var digits strings.Builder
	for _, r := range value {
		switch {
		case r >= '0' && r <= '9':
			digits.WriteRune(r)
		case r == '+', r == ' ', r == '\t', r == '(', r == ')', r == '-', r == '.':
		default:
			return ""
		}
	}
	out := strings.TrimPrefix(digits.String(), "00")
	if len(out) < 6 {
		return ""
	}
	for _, countryCode := range []string{"31", "44", "49", "33", "39", "34", "46", "47", "45", "32", "43", "41", "48"} {
		prefix := countryCode + "0"
		subscriberLength := len(out) - len(prefix)
		if strings.HasPrefix(out, prefix) && subscriberLength >= 6 && subscriberLength <= 12 {
			return countryCode + out[len(prefix):]
		}
	}
	return out
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
