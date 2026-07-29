package cli

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawl/internal/federation"
	"github.com/opentrawl/opentrawl/trawlkit"
	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
	personv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person/v1"
	"github.com/opentrawl/opentrawl/trawlkit/whomatch"
)

type whoSourceResult struct {
	Source     InstalledTrawler
	Candidates []WhoCandidate
	Err        error
}

type federatedWhoResolution struct {
	Query             string
	Candidates        []WhoCandidate
	DidYouMean        []WhoCandidate
	SourcesConsulted  []string
	OperationFailures []*federationv1.TrawlerOperationFailure
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
		resolution.OperationFailures = []*federationv1.TrawlerOperationFailure{
			federation.FailureForError(contacts.RegisteredTrawlerManifest, "who", contactsResult.Err),
		}
		return resolution
	}
	resolution.SourcesConsulted = []string{contacts.RegisteredTrawlerManifestIdentity}
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
	rank, ok := matchQualityRank(resolution.Candidates[0].MatchQuality)
	if !ok || rank != whomatch.RankCloseSpelling {
		return resolution, false
	}
	resolution.DidYouMean = didYouMeanWithCandidate(resolution.Candidates[0], resolution.DidYouMean)
	resolution.Candidates = []WhoCandidate{}
	return resolution, true
}

func didYouMeanWithCandidate(candidate WhoCandidate, suggestions []WhoCandidate) []WhoCandidate {
	suggestions = append([]WhoCandidate{candidate}, suggestions...)
	sortWhoCandidates(suggestions)
	return suggestions
}

func (r *Runtime) whoSource(source InstalledTrawler, query string) whoSourceResult {
	result := whoSourceResult{Source: source}
	started := r.logTrawlerStart(source, "who")
	defer func() {
		if result.Err != nil {
			r.logTrawlerDone(source, "who", started, result.Err)
			return
		}
		r.logTrawlerDone(source, "who", started, nil, "candidates="+strconv.Itoa(len(result.Candidates)))
	}()
	if _, ok := source.Trawler.(trawlkit.WhoMatcher); !ok {
		result.Err = trawlerDiscoveryFailure(source)
		return result
	}
	candidates, err := r.trawlerExecutor().Who(r.ctx, source.Trawler, query)
	err = trawlerExecutionError("who", err)
	if err != nil {
		result.Err = err
		return result
	}
	result.Candidates = whoCandidatesFromMatches(candidates, source.RegisteredTrawlerManifestIdentity, query)
	return result
}

func whoCandidatesFromMatches(response *personv1.TrawlerPersonMatchResponse, trawlerIdentity, query string) []WhoCandidate {
	candidates := response.GetPersonMatchCandidates()
	out := make([]WhoCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate == nil {
			continue
		}
		matchingValues := []string{candidate.GetPersonDisplayName()}
		matchingValues = append(matchingValues, candidate.GetAlternativePersonDisplayNames()...)
		for _, personMatchFactsFromTrawler := range candidate.GetPersonMatchFactsFromTrawlers() {
			matchingValues = append(
				matchingValues,
				personMatchFactsFromTrawler.GetExactPersonFilterIdentifiersObservedByTrawlerArchive()...,
			)
			matchingValues = append(
				matchingValues,
				personMatchFactsFromTrawler.GetPersonDisplayNamesObservedByTrawlerArchive()...,
			)
		}
		matchQuality := "unknown"
		if rank, ok := whomatch.MatchRank(query, matchingValues); ok {
			matchQuality = rank.String()
		}
		lastSeen := ""
		if timestamp := candidate.GetLatestMatchingArchiveRecordTime(); timestamp != nil && timestamp.IsValid() {
			lastSeen = timestamp.AsTime().UTC().Format(time.RFC3339)
		}
		normalizedCandidate := normalizeWhoCandidate(trawlerWhoCandidate{
			Who:              candidate.GetPersonDisplayName(),
			AlternativeNames: append([]string(nil), candidate.GetAlternativePersonDisplayNames()...),
			PersonNameOrHumanReadableContactValueThatMatchedQuery: candidate.GetPersonNameOrHumanReadableContactValueThatMatchedQuery(),
			PersonMatchFactsFromTrawlers:                          candidate.GetPersonMatchFactsFromTrawlers(),
			MatchQuality:                                          matchQuality,
			LastSeen:                                              lastSeen,
			MessageCountInvolvingPerson:                           int(candidate.GetMessageCountInvolvingPerson()),
			GloballyRoutableTrawlLinkForPerson:                    candidate.GetGloballyRoutableTrawlLinkForPerson(),
		}, trawlerIdentity)
		out = append(out, normalizedCandidate)
	}
	return out
}

func personMatchFactsFromTrawlersMatchingInstalledTrawlerManifestIdentities(
	personMatchFacts []*personv1.PersonMatchFactsFromTrawler,
	installedTrawlers []InstalledTrawler,
) []*personv1.PersonMatchFactsFromTrawler {
	registeredTrawlerManifestIdentityByNormalizedTrawlerIdentity := make(
		map[string]string,
		len(installedTrawlers),
	)
	for _, installedTrawler := range installedTrawlers {
		registeredTrawlerManifestIdentity := strings.TrimSpace(
			installedTrawler.RegisteredTrawlerManifestIdentity,
		)
		if registeredTrawlerManifestIdentity != "" {
			registeredTrawlerManifestIdentityByNormalizedTrawlerIdentity[strings.ToLower(registeredTrawlerManifestIdentity)] = registeredTrawlerManifestIdentity
		}
	}
	personMatchFactsFromInstalledTrawlers := make(
		[]*personv1.PersonMatchFactsFromTrawler,
		0,
		len(personMatchFacts),
	)
	for _, personMatchFactsFromTrawler := range personMatchFacts {
		if personMatchFactsFromTrawler == nil {
			continue
		}
		normalizedTrawlerIdentity := strings.ToLower(strings.TrimSpace(
			personMatchFactsFromTrawler.GetRegisteredTrawlerManifestIdentity(),
		))
		registeredTrawlerManifestIdentity, installed :=
			registeredTrawlerManifestIdentityByNormalizedTrawlerIdentity[normalizedTrawlerIdentity]
		if !installed {
			continue
		}
		personMatchFactsFromInstalledTrawlers = append(
			personMatchFactsFromInstalledTrawlers,
			&personv1.PersonMatchFactsFromTrawler{
				RegisteredTrawlerManifestIdentity: registeredTrawlerManifestIdentity,
				ExactPersonFilterIdentifiersObservedByTrawlerArchive: append(
					[]string(nil),
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
	personMatchFacts []*personv1.PersonMatchFactsFromTrawler,
) []*personv1.PersonMatchFactsFromTrawler {
	personMatchFactsByTrawlerIdentity := map[string]*personv1.PersonMatchFactsFromTrawler{}
	for _, facts := range personMatchFacts {
		if facts == nil {
			continue
		}
		registeredTrawlerManifestIdentity := strings.TrimSpace(
			facts.GetRegisteredTrawlerManifestIdentity(),
		)
		if registeredTrawlerManifestIdentity == "" {
			continue
		}
		normalizedFacts := personMatchFactsByTrawlerIdentity[registeredTrawlerManifestIdentity]
		if normalizedFacts == nil {
			normalizedFacts = &personv1.PersonMatchFactsFromTrawler{
				RegisteredTrawlerManifestIdentity: registeredTrawlerManifestIdentity,
			}
			personMatchFactsByTrawlerIdentity[registeredTrawlerManifestIdentity] = normalizedFacts
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
		[]*personv1.PersonMatchFactsFromTrawler,
		0,
		len(registeredTrawlerManifestIdentities),
	)
	for _, registeredTrawlerManifestIdentity := range registeredTrawlerManifestIdentities {
		facts := personMatchFactsByTrawlerIdentity[registeredTrawlerManifestIdentity]
		facts.ExactPersonFilterIdentifiersObservedByTrawlerArchive = normalisedStringList(
			facts.ExactPersonFilterIdentifiersObservedByTrawlerArchive,
		)
		facts.PersonDisplayNamesObservedByTrawlerArchive = normalisedStringList(
			facts.PersonDisplayNamesObservedByTrawlerArchive,
		)
		normalizedPersonMatchFacts = append(normalizedPersonMatchFacts, facts)
	}
	return normalizedPersonMatchFacts
}

func registeredTrawlerManifestIdentities(candidate WhoCandidate) []string {
	trawlerIdentities := make(
		[]string,
		0,
		len(candidate.PersonMatchFactsFromTrawlers),
	)
	for _, facts := range candidate.PersonMatchFactsFromTrawlers {
		trawlerIdentities = append(
			trawlerIdentities,
			facts.GetRegisteredTrawlerManifestIdentity(),
		)
	}
	return normalisedStringList(trawlerIdentities)
}

func exactPersonFilterIdentifiersFromWhoCandidate(candidate WhoCandidate) []string {
	var exactPersonFilterIdentifiers []string
	for _, facts := range candidate.PersonMatchFactsFromTrawlers {
		exactPersonFilterIdentifiers = append(
			exactPersonFilterIdentifiers,
			facts.GetExactPersonFilterIdentifiersObservedByTrawlerArchive()...,
		)
	}
	return normalisedStringList(exactPersonFilterIdentifiers)
}

func personMatchFactsForTrawler(
	candidate WhoCandidate,
	registeredTrawlerManifestIdentity string,
) *personv1.PersonMatchFactsFromTrawler {
	return personMatchFactsForTrawlerFromFacts(
		candidate.PersonMatchFactsFromTrawlers,
		registeredTrawlerManifestIdentity,
	)
}

func personMatchFactsForTrawlerFromFacts(
	personMatchFacts []*personv1.PersonMatchFactsFromTrawler,
	registeredTrawlerManifestIdentity string,
) *personv1.PersonMatchFactsFromTrawler {
	for _, facts := range personMatchFacts {
		if strings.EqualFold(
			strings.TrimSpace(facts.GetRegisteredTrawlerManifestIdentity()),
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

func normaliseIdentifier(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	digits := 0
	phoneLike := true
	var phone strings.Builder
	for i, char := range trimmed {
		switch {
		case char >= '0' && char <= '9':
			digits++
			phone.WriteRune(char)
		case char == '+' && i == 0:
			phone.WriteRune(char)
		case strings.ContainsRune(" ()-.", char):
		default:
			phoneLike = false
		}
	}
	if phoneLike && digits >= 5 {
		return phone.String()
	}
	trimmed = strings.TrimPrefix(trimmed, "@")
	return whomatch.Normalize(trimmed)
}

func (r *Runtime) reportWhoFailures(resolution federatedWhoResolution) {
	r.reportFederationOutcomes(resolution.OperationFailures, nil)
}

func whoExit(resolution federatedWhoResolution) error {
	if len(resolution.OperationFailures) == 0 {
		return nil
	}
	if len(resolution.SourcesConsulted) > 0 {
		return exitErr{code: 3}
	}
	return exitErr{code: 1}
}
