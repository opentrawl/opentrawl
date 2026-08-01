package cli

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawl/internal/federation"
	"github.com/opentrawl/opentrawl/trawlkit"
	federationcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	"github.com/opentrawl/opentrawl/trawlkit/whomatch"
)

type whoTrawlerResult struct {
	Trawler    InstalledTrawler
	Candidates []personMatchCandidate
	Err        error
}

type federatedWhoResolution struct {
	Query             string
	Candidates        []personMatchCandidate
	DidYouMean        []personMatchCandidate
	TrawlersConsulted []string
	OperationFailures []*federationcontract.TrawlerOperationFailure
}

func resolveWhoThroughContacts(
	r *Runtime,
	installedTrawlers []InstalledTrawler,
	query string,
) federatedWhoResolution {
	resolution := federatedWhoResolution{Query: query}
	contacts, found := findInstalledTrawler(installedTrawlers, "contacts")
	if !found {
		return resolution
	}
	contactsResult := r.whoSource(contacts, query)
	if contactsResult.Err != nil {
		resolution.OperationFailures = []*federationcontract.TrawlerOperationFailure{
			federation.FailureForError(
				contacts.RegisteredTrawlerManifest,
				federationcontract.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_WHO,
				contactsResult.Err,
			),
		}
		return resolution
	}
	resolution.TrawlersConsulted = []string{installedTrawlerIdentityText(contacts)}
	resolution.Candidates = contactsResult.Candidates
	for candidateIndex := range resolution.Candidates {
		resolution.Candidates[candidateIndex].PersonMatchFactsFromTrawlers =
			personMatchFactsFromTrawlersMatchingInstalledTrawlerManifestIdentities(
				resolution.Candidates[candidateIndex].PersonMatchFactsFromTrawlers,
				installedTrawlers,
			)
	}
	sortWhoCandidates(resolution.Candidates)
	return resolution
}

func closeSpellingOnlyResolution(resolution federatedWhoResolution) (federatedWhoResolution, bool) {
	if len(resolution.Candidates) != 1 {
		return resolution, false
	}
	if resolution.Candidates[0].PersonIdentityMatchRank != whomatch.RankCloseSpelling {
		return resolution, false
	}
	resolution.DidYouMean = didYouMeanWithCandidate(resolution.Candidates[0], resolution.DidYouMean)
	resolution.Candidates = []personMatchCandidate{}
	return resolution, true
}

func didYouMeanWithCandidate(candidate personMatchCandidate, suggestions []personMatchCandidate) []personMatchCandidate {
	suggestions = append([]personMatchCandidate{candidate}, suggestions...)
	sortWhoCandidates(suggestions)
	return suggestions
}

func (r *Runtime) whoSource(trawler InstalledTrawler, query string) whoTrawlerResult {
	result := whoTrawlerResult{Trawler: trawler}
	started := r.logTrawlerStart(trawler, "who")
	defer func() {
		if result.Err != nil {
			r.logTrawlerDone(trawler, "who", started, result.Err)
			return
		}
		r.logTrawlerDone(trawler, "who", started, nil, "candidates="+strconv.Itoa(len(result.Candidates)))
	}()
	if !supportsSharedTrawlerOperation(trawler, federationcontract.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_WHO) {
		result.Err = trawlerDiscoveryFailure(trawler)
		return result
	}
	candidates, err := r.trawlerExecutor().Who(r.ctx, trawler.Trawler, query)
	err = trawlerExecutionError("who", err)
	if err != nil {
		result.Err = err
		return result
	}
	result.Candidates = whoCandidatesFromMatches(
		candidates,
		query,
	)
	return result
}

func whoCandidatesFromMatches(response *person.TrawlerPersonMatchResponse, query string) []personMatchCandidate {
	candidates := response.GetPersonMatchCandidates()
	out := make([]personMatchCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate == nil {
			continue
		}
		matchingValues := []string{candidate.GetPersonDisplayName()}
		matchingValues = append(matchingValues, candidate.GetAlternativePersonDisplayNames()...)
		for _, personMatchFactsFromTrawler := range candidate.GetPersonMatchFactsFromTrawlers() {
			matchingValues = append(
				matchingValues,
				exactPersonFilterIdentifierTexts(
					personMatchFactsFromTrawler.GetExactPersonFilterIdentifiersObservedByTrawlerArchive(),
				)...,
			)
			matchingValues = append(
				matchingValues,
				personMatchFactsFromTrawler.GetPersonDisplayNamesObservedByTrawlerArchive()...,
			)
		}
		personIdentityMatchRank, _ := whomatch.MatchRank(query, matchingValues)
		latestMatchingArchiveRecordTime := time.Time{}
		if timestamp := candidate.GetLatestMatchingArchiveRecordTime(); timestamp != nil && timestamp.IsValid() {
			latestMatchingArchiveRecordTime = timestamp.AsTime()
		}
		normalizedCandidate := personMatchCandidate{
			Who:              candidate.GetPersonDisplayName(),
			AlternativeNames: normalisedStringList(candidate.GetAlternativePersonDisplayNames()),
			PersonNameOrHumanReadableContactValueThatMatchedQuery: candidate.GetPersonNameOrHumanReadableContactValueThatMatchedQuery(),
			PersonMatchFactsFromTrawlers: normalizedPersonMatchFactsFromTrawlers(
				candidate.GetPersonMatchFactsFromTrawlers(),
			),
			PersonMessageCountsFromTrawlerArchives: normalizedPersonMessageCountsFromTrawlerArchives(
				candidate.GetPersonMessageCountsFromTrawlerArchives(),
			),
			PersonIdentityMatchRank:         personIdentityMatchRank,
			LatestMatchingArchiveRecordTime: latestMatchingArchiveRecordTime,
			MessageCountInvolvingPerson:     int(candidate.GetMessageCountInvolvingPerson()),
			PersonTrawlLink:                 candidate.GetPersonTrawlLink(),
		}
		out = append(out, normalizedCandidate)
	}
	return out
}

func personMatchFactsFromTrawlersMatchingInstalledTrawlerManifestIdentities(
	personMatchFacts []*person.PersonMatchFactsFromTrawler,
	installedTrawlers []InstalledTrawler,
) []*person.PersonMatchFactsFromTrawler {
	registeredTrawlerByNormalizedTrawlerIdentity := make(
		map[string]*trawlkit.RegisteredTrawlerIdentity,
		len(installedTrawlers),
	)
	for _, installedTrawler := range installedTrawlers {
		registeredTrawlerIdentityText := installedTrawlerIdentityText(installedTrawler)
		if registeredTrawlerIdentityText != "" {
			registeredTrawlerByNormalizedTrawlerIdentity[strings.ToLower(registeredTrawlerIdentityText)] =
				installedTrawler.RegisteredTrawlerManifest.GetRegisteredTrawler()
		}
	}
	personMatchFactsFromInstalledTrawlers := make(
		[]*person.PersonMatchFactsFromTrawler,
		0,
		len(personMatchFacts),
	)
	for _, personMatchFactsFromTrawler := range personMatchFacts {
		if personMatchFactsFromTrawler == nil {
			continue
		}
		normalizedTrawlerIdentity := strings.ToLower(strings.TrimSpace(
			trawlkit.RegisteredTrawlerIdentityText(personMatchFactsFromTrawler.GetRegisteredTrawler()),
		))
		registeredTrawler, installed :=
			registeredTrawlerByNormalizedTrawlerIdentity[normalizedTrawlerIdentity]
		if !installed {
			continue
		}
		personMatchFactsFromInstalledTrawlers = append(
			personMatchFactsFromInstalledTrawlers,
			&person.PersonMatchFactsFromTrawler{
				RegisteredTrawler: registeredTrawler,
				ExactPersonFilterIdentifiersObservedByTrawlerArchive: append(
					[]*person.ExactPersonFilterIdentifier(nil),
					personMatchFactsFromTrawler.GetExactPersonFilterIdentifiersObservedByTrawlerArchive()...,
				),
				PersonDisplayNamesObservedByTrawlerArchive: append(
					[]string(nil),
					personMatchFactsFromTrawler.GetPersonDisplayNamesObservedByTrawlerArchive()...,
				),
			},
		)
	}
	return normalizedPersonMatchFactsFromTrawlers(
		personMatchFactsFromInstalledTrawlers,
	)
}

func normalizedPersonMatchFactsFromTrawlers(
	personMatchFacts []*person.PersonMatchFactsFromTrawler,
) []*person.PersonMatchFactsFromTrawler {
	personMatchFactsByTrawlerIdentity := map[string]*person.PersonMatchFactsFromTrawler{}
	for _, facts := range personMatchFacts {
		if facts == nil {
			continue
		}
		registeredTrawlerIdentityText := trawlkit.RegisteredTrawlerIdentityText(facts.GetRegisteredTrawler())
		if registeredTrawlerIdentityText == "" {
			continue
		}
		normalizedFacts := personMatchFactsByTrawlerIdentity[registeredTrawlerIdentityText]
		if normalizedFacts == nil {
			normalizedFacts = &person.PersonMatchFactsFromTrawler{
				RegisteredTrawler: facts.GetRegisteredTrawler(),
			}
			personMatchFactsByTrawlerIdentity[registeredTrawlerIdentityText] = normalizedFacts
		}
		normalizedFacts.ExactPersonFilterIdentifiersObservedByTrawlerArchive = append(
			normalizedFacts.ExactPersonFilterIdentifiersObservedByTrawlerArchive,
			facts.GetExactPersonFilterIdentifiersObservedByTrawlerArchive()...,
		)
		normalizedFacts.PersonDisplayNamesObservedByTrawlerArchive = append(
			normalizedFacts.PersonDisplayNamesObservedByTrawlerArchive,
			facts.GetPersonDisplayNamesObservedByTrawlerArchive()...,
		)
	}
	registeredTrawlerManifestIdentities := make(
		[]string,
		0,
		len(personMatchFactsByTrawlerIdentity),
	)
	for registeredTrawlerManifestIdentity := range personMatchFactsByTrawlerIdentity {
		registeredTrawlerManifestIdentities = append(
			registeredTrawlerManifestIdentities,
			registeredTrawlerManifestIdentity,
		)
	}
	sort.Strings(registeredTrawlerManifestIdentities)
	normalizedPersonMatchFacts := make(
		[]*person.PersonMatchFactsFromTrawler,
		0,
		len(registeredTrawlerManifestIdentities),
	)
	for _, registeredTrawlerManifestIdentity := range registeredTrawlerManifestIdentities {
		facts := personMatchFactsByTrawlerIdentity[registeredTrawlerManifestIdentity]
		facts.ExactPersonFilterIdentifiersObservedByTrawlerArchive = normalisedExactPersonFilterIdentifierList(
			facts.ExactPersonFilterIdentifiersObservedByTrawlerArchive,
		)
		facts.PersonDisplayNamesObservedByTrawlerArchive = normalisedStringList(
			facts.PersonDisplayNamesObservedByTrawlerArchive,
		)
		normalizedPersonMatchFacts = append(normalizedPersonMatchFacts, facts)
	}
	return normalizedPersonMatchFacts
}

func registeredTrawlerManifestIdentities(candidate personMatchCandidate) []string {
	trawlerIdentities := make(
		[]string,
		0,
		len(candidate.PersonMatchFactsFromTrawlers),
	)
	for _, facts := range candidate.PersonMatchFactsFromTrawlers {
		trawlerIdentities = append(
			trawlerIdentities,
			trawlkit.RegisteredTrawlerIdentityText(facts.GetRegisteredTrawler()),
		)
	}
	return normalisedStringList(trawlerIdentities)
}

func exactPersonFilterIdentifiersFromWhoCandidate(candidate personMatchCandidate) []string {
	var exactPersonFilterIdentifiers []string
	for _, facts := range candidate.PersonMatchFactsFromTrawlers {
		exactPersonFilterIdentifiers = append(
			exactPersonFilterIdentifiers,
			exactPersonFilterIdentifierTexts(
				facts.GetExactPersonFilterIdentifiersObservedByTrawlerArchive(),
			)...,
		)
	}
	return normalisedStringList(exactPersonFilterIdentifiers)
}

func normalisedExactPersonFilterIdentifierList(
	exactPersonFilterIdentifiers []*person.ExactPersonFilterIdentifier,
) []*person.ExactPersonFilterIdentifier {
	return trawlkit.NewExactPersonFilterIdentifiers(
		normalisedStringList(exactPersonFilterIdentifierTexts(exactPersonFilterIdentifiers)),
	)
}

func exactPersonFilterIdentifierTexts(
	exactPersonFilterIdentifiers []*person.ExactPersonFilterIdentifier,
) []string {
	exactPersonFilterIdentifierTexts := make([]string, 0, len(exactPersonFilterIdentifiers))
	for _, exactPersonFilterIdentifier := range exactPersonFilterIdentifiers {
		exactPersonFilterIdentifierTexts = append(
			exactPersonFilterIdentifierTexts,
			exactPersonFilterIdentifier.GetExactPersonFilterIdentifier(),
		)
	}
	return exactPersonFilterIdentifierTexts
}

func personMatchFactsForTrawlerFromFacts(
	personMatchFacts []*person.PersonMatchFactsFromTrawler,
	registeredTrawlerManifestIdentity string,
) *person.PersonMatchFactsFromTrawler {
	for _, facts := range personMatchFacts {
		if strings.EqualFold(
			trawlkit.RegisteredTrawlerIdentityText(facts.GetRegisteredTrawler()),
			strings.TrimSpace(registeredTrawlerManifestIdentity),
		) {
			return facts
		}
	}
	return nil
}

func normalisePersonName(value string) string {
	return whomatch.Normalize(value)
}

func (r *Runtime) reportWhoFailures(resolution federatedWhoResolution) {
	r.reportFederationOutcomes(resolution.OperationFailures, nil)
}
