package cli

import (
	"errors"
	"sort"
	"strings"
	"time"

	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
	personv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person/v1"
	"github.com/opentrawl/opentrawl/trawlkit/render"
	"github.com/opentrawl/opentrawl/trawlkit/whomatch"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type WhoCmd struct {
	Name []string `arg:"" name:"name" help:"Person name or Contacts link"`
}

type WhoCandidate struct {
	Who                                                   string
	AlternativeNames                                      []string
	PersonNameOrHumanReadableContactValueThatMatchedQuery string
	MatchQuality                                          string
	PersonMatchFactsFromTrawlers                          []*personv1.PersonMatchFactsFromTrawler
	LastSeen                                              string
	MessageCountInvolvingPerson                           int
	GloballyRoutableTrawlLinkForPerson                    string

	lastSeenParsed time.Time
	lastSeenOK     bool
}

type trawlerWhoCandidate struct {
	Who                                                   string
	AlternativeNames                                      []string
	PersonNameOrHumanReadableContactValueThatMatchedQuery string
	MatchQuality                                          string
	PersonMatchFactsFromTrawlers                          []*personv1.PersonMatchFactsFromTrawler
	LastSeen                                              string
	MessageCountInvolvingPerson                           int
	GloballyRoutableTrawlLinkForPerson                    string
}

func (c *WhoCmd) Run(r *Runtime) error {
	query := strings.Join(c.Name, " ")
	query = strings.Join(strings.Fields(query), " ")
	if query == "" {
		return usageErr{errors.New("Who needs a name.")}
	}

	installed := discoverInstalledTrawlers(r.ctx)
	resolution := resolveWhoThroughContacts(r, installed, query)
	operation := federatedPersonMatchOperation(resolution, trawlerDisplayNamesByIdentity(installed))
	if err := render.WriteFederatedTrawlerPersonMatchOperation(r.stdout, operation); err != nil {
		return err
	}
	r.reportFederationOutcomes(operation.GetOperationFailures(), operation.GetTrawlersSkippedFromOperation())
	if len(operation.GetPersonMatchCandidates()) == 0 && len(operation.GetOperationFailures()) == 0 {
		return exitErr{code: 5}
	}
	return outcomeExit(operation.GetOutcome())
}

func normalizeWhoCandidate(raw trawlerWhoCandidate, fallbackTrawlerIdentity string) WhoCandidate {
	personMatchFactsFromTrawlers := normalizedPersonMatchFactsFromTrawlers(
		raw.PersonMatchFactsFromTrawlers,
	)
	if len(personMatchFactsFromTrawlers) == 0 && strings.TrimSpace(fallbackTrawlerIdentity) != "" {
		personMatchFactsFromTrawlers = []*personv1.PersonMatchFactsFromTrawler{{
			RegisteredTrawlerManifestIdentity: fallbackTrawlerIdentity,
		}}
	}
	lastSeenParsed, lastSeenOK := parseWhoTime(raw.LastSeen)
	return WhoCandidate{
		Who:              raw.Who,
		AlternativeNames: normalisedStringList(raw.AlternativeNames),
		PersonNameOrHumanReadableContactValueThatMatchedQuery: strings.TrimSpace(raw.PersonNameOrHumanReadableContactValueThatMatchedQuery),
		PersonMatchFactsFromTrawlers:                          personMatchFactsFromTrawlers,
		MatchQuality:                                          canonicalMatchQuality(raw.MatchQuality),
		LastSeen:                                              raw.LastSeen,
		MessageCountInvolvingPerson:                           raw.MessageCountInvolvingPerson,
		GloballyRoutableTrawlLinkForPerson:                    strings.TrimSpace(raw.GloballyRoutableTrawlLinkForPerson),
		lastSeenParsed:                                        lastSeenParsed,
		lastSeenOK:                                            lastSeenOK,
	}
}

func normalisedStringList(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(out[i]) < strings.ToLower(out[j])
	})
	return out
}

func parseWhoTime(value string) (time.Time, bool) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339, value)
	return parsed, err == nil
}

func sortWhoCandidates(candidates []WhoCandidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		left := candidates[i]
		right := candidates[j]
		if left.MessageCountInvolvingPerson != right.MessageCountInvolvingPerson {
			return left.MessageCountInvolvingPerson > right.MessageCountInvolvingPerson
		}
		leftRank, leftOK := matchQualityRank(left.MatchQuality)
		rightRank, rightOK := matchQualityRank(right.MatchQuality)
		if leftOK != rightOK {
			return leftOK
		}
		if leftOK && rightOK && leftRank != rightRank {
			return leftRank.BetterThan(rightRank)
		}
		if left.lastSeenOK != right.lastSeenOK {
			return left.lastSeenOK
		}
		if left.lastSeenOK && !left.lastSeenParsed.Equal(right.lastSeenParsed) {
			return left.lastSeenParsed.After(right.lastSeenParsed)
		}
		return strings.ToLower(left.Who) < strings.ToLower(right.Who)
	})
}

func canonicalMatchQuality(value string) string {
	rank, ok := matchQualityRank(value)
	if !ok {
		return "unknown"
	}
	return rank.String()
}

func matchQualityRank(value string) (whomatch.Rank, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "exact":
		return whomatch.RankExact, true
	case "prefix":
		return whomatch.RankPrefix, true
	case "substring", "contains":
		return whomatch.RankSubstring, true
	case "close_spelling", "close-spelling", "close spelling":
		return whomatch.RankCloseSpelling, true
	default:
		return 0, false
	}
}

func whoSources(sources []string, surfaces map[string]string) string {
	if len(sources) == 0 {
		return ""
	}
	named := make([]string, 0, len(sources))
	for _, source := range sources {
		named = append(named, firstNonEmpty(surfaces[source], source))
	}
	return strings.Join(normalisedStringList(named), ", ")
}

func federatedPersonMatchOperation(
	resolution federatedWhoResolution,
	trawlerDisplayNames map[string]string,
) *federationv1.FederatedTrawlerPersonMatchOperation {
	return &federationv1.FederatedTrawlerPersonMatchOperation{
		Outcome:                         federatedOperationOutcome(len(resolution.SourcesConsulted), len(resolution.OperationFailures), 0),
		PersonMatchCandidates:           federatedPersonMatchCandidates(resolution.Candidates, trawlerDisplayNames),
		OperationFailures:               append([]*federationv1.TrawlerOperationFailure(nil), resolution.OperationFailures...),
		PersonQueryUsedToFindCandidates: resolution.Query,
	}
}

func federatedPersonMatchCandidates(
	whoCandidates []WhoCandidate,
	trawlerDisplayNames map[string]string,
) []*federationv1.FederatedPersonMatchCandidate {
	candidates := make([]*federationv1.FederatedPersonMatchCandidate, 0, len(whoCandidates))
	for _, candidate := range whoCandidates {
		candidatePersonMatchFactsFromTrawlers := candidate.PersonMatchFactsFromTrawlers
		facts := make([]*federationv1.PersonMatchFactsFromTrawler, 0, len(candidatePersonMatchFactsFromTrawlers))
		for _, personMatchFactsFromTrawler := range candidatePersonMatchFactsFromTrawlers {
			trawlerIdentity := personMatchFactsFromTrawler.GetRegisteredTrawlerManifestIdentity()
			facts = append(facts, &federationv1.PersonMatchFactsFromTrawler{
				RegisteredTrawlerManifestIdentity: trawlerIdentity,
				RegisteredTrawlerDisplayName:      firstNonEmpty(trawlerDisplayNames[trawlerIdentity], trawlerIdentity),
				ExactPersonFilterIdentifiersObservedByTrawlerArchive: append(
					[]string(nil),
					personMatchFactsFromTrawler.GetExactPersonFilterIdentifiersObservedByTrawlerArchive()...,
				),
			})
		}
		messageCountInvolvingPersonAcrossTrawlers := uint64(0)
		if candidate.MessageCountInvolvingPerson > 0 {
			messageCountInvolvingPersonAcrossTrawlers = uint64(candidate.MessageCountInvolvingPerson)
		}
		converted := &federationv1.FederatedPersonMatchCandidate{
			PersonDisplayName:                                     candidate.Who,
			AlternativePersonDisplayNames:                         append([]string(nil), candidate.AlternativeNames...),
			PersonNameOrHumanReadableContactValueThatMatchedQuery: candidate.PersonNameOrHumanReadableContactValueThatMatchedQuery,
			MessageCountInvolvingPersonAcrossTrawlers:             messageCountInvolvingPersonAcrossTrawlers,
			PersonMatchFactsFromTrawlers:                          facts,
			GloballyRoutableTrawlLinkForPerson:                    candidate.GloballyRoutableTrawlLinkForPerson,
		}
		if candidate.lastSeenOK {
			converted.LatestMatchingArchiveRecordTime = timestamppb.New(candidate.lastSeenParsed)
		}
		candidates = append(candidates, converted)
	}
	return candidates
}
