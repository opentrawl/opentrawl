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
	"github.com/opentrawl/opentrawl/trawlkit/render"
	"github.com/opentrawl/opentrawl/trawlkit/whomatch"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type WhoCmd struct {
	Name  []string `arg:"" name:"name" help:"Person name or Contacts link"`
	Limit int      `name:"limit" default:"20" placeholder:"COUNT" help:"Maximum number of people"`
}

type personMatchCandidate struct {
	Who                                                   string
	AlternativeNames                                      []string
	PersonNameOrHumanReadableContactValueThatMatchedQuery string
	PersonIdentityMatchRank                               whomatch.Rank
	PersonMatchFactsFromTrawlers                          []*person.PersonMatchFactsFromTrawler
	PersonMessageCountsFromTrawlerArchives                []*person.PersonMessageCountFromTrawlerArchive
	LatestMatchingArchiveRecordTime                       time.Time
	MessageCountInvolvingPerson                           int
	PersonTrawlLink                                       *trawlkit.GloballyRoutableTrawlLink
}

func (c *WhoCmd) Run(r *Runtime) error {
	query := strings.Join(strings.Fields(strings.Join(c.Name, " ")), " ")
	if query == "" {
		return render.WhoNeedsName()
	}
	if c.Limit < 1 {
		return render.LimitMustBeAtLeastOne()
	}

	installed := discoverInstalledTrawlers(r.ctx)
	resolution := resolveWhoThroughContacts(r, installed, query)
	resolution.OperationFailures = append(resolution.OperationFailures,
		r.readPersonMessageCountsFromTrawlers(installed, resolution.Candidates)...)
	operation := federatedPersonMatchOperation(resolution, trawlerDisplayNamesByIdentity(installed))
	r.logFederationOutcomes(operation.GetOperationFailures(), operation.GetTrawlersSkippedFromOperation())
	numberOfMatchingPeople := len(operation.GetPersonMatchCandidates())
	if numberOfMatchingPeople == 0 && len(operation.GetOperationFailures()) == 0 {
		return unknownPersonFailure(query, resolution, trawlerDisplayNamesByIdentity(installed), func(personLink string) []string {
			return []string{"who", personLink}
		})
	}
	var nextCommandShowingMorePeople []render.NextCommand
	if numberOfMatchingPeople > c.Limit {
		operation.PersonMatchCandidates = operation.GetPersonMatchCandidates()[:c.Limit]
		nextCommandShowingMorePeople = []render.NextCommand{render.NextCommandShowingMoreRows(
			[]string{"who", query, "--limit", strconv.Itoa(c.Limit * 2)},
		)}
	}
	if err := r.output.WritePersonMatches(operation, nextCommandShowingMorePeople); err != nil {
		return err
	}
	return outcomeExit(operation.GetOutcome())
}

func (r *Runtime) readPersonMessageCountsFromTrawlers(
	installedTrawlers []InstalledTrawler,
	candidates []personMatchCandidate,
) []*federationcontract.TrawlerOperationFailure {
	var failures []*federationcontract.TrawlerOperationFailure
	for candidateIndex := range candidates {
		candidate := &candidates[candidateIndex]
		for _, installedTrawler := range installedTrawlers {
			if _, supported := installedTrawler.Trawler.(trawlkit.PersonMessageCounter); !supported {
				continue
			}
			trawlerIdentity := installedTrawlerIdentityText(installedTrawler)
			facts := personMatchFactsForTrawlerFromFacts(candidate.PersonMatchFactsFromTrawlers, trawlerIdentity)
			if len(facts.GetExactPersonFilterIdentifiersObservedByTrawlerArchive()) == 0 {
				continue
			}
			messageCount, err := r.trawlerExecutor().CountPersonMessages(r.ctx, installedTrawler.Trawler,
				&person.ExactPersonMessageCountRequest{ExactPersonFilterIdentifiers: facts.GetExactPersonFilterIdentifiersObservedByTrawlerArchive()})
			if err != nil {
				failures = append(failures, federation.FailureForError(installedTrawler.RegisteredTrawlerManifest,
					federationcontract.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_WHO, err))
				continue
			}
			currentSourceCounts := make([]*person.PersonMessageCountFromTrawlerArchive, 0, len(candidate.PersonMessageCountsFromTrawlerArchives)+1)
			for _, previousCount := range candidate.PersonMessageCountsFromTrawlerArchives {
				if trawlkit.RegisteredTrawlerIdentityText(previousCount.GetRegisteredTrawler()) != trawlerIdentity {
					currentSourceCounts = append(currentSourceCounts, previousCount)
				}
			}
			candidate.PersonMessageCountsFromTrawlerArchives = append(currentSourceCounts,
				&person.PersonMessageCountFromTrawlerArchive{
					RegisteredTrawler: installedTrawler.RegisteredTrawlerManifest.GetRegisteredTrawler(),
					MessageCountInvolvingPersonInTrawlerArchive: messageCount.GetMessageCountInvolvingPerson(),
				})
		}
		candidate.MessageCountInvolvingPerson = 0
		for _, messageCount := range candidate.PersonMessageCountsFromTrawlerArchives {
			candidate.MessageCountInvolvingPerson += int(messageCount.GetMessageCountInvolvingPersonInTrawlerArchive())
		}
	}
	sortWhoCandidates(candidates)
	return failures
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

func sortWhoCandidates(candidates []personMatchCandidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		left := candidates[i]
		right := candidates[j]
		if left.MessageCountInvolvingPerson != right.MessageCountInvolvingPerson {
			return left.MessageCountInvolvingPerson > right.MessageCountInvolvingPerson
		}
		if left.PersonIdentityMatchRank != right.PersonIdentityMatchRank {
			return left.PersonIdentityMatchRank.BetterThan(right.PersonIdentityMatchRank)
		}
		leftHasMatchingArchiveRecordTime := !left.LatestMatchingArchiveRecordTime.IsZero()
		rightHasMatchingArchiveRecordTime := !right.LatestMatchingArchiveRecordTime.IsZero()
		if leftHasMatchingArchiveRecordTime != rightHasMatchingArchiveRecordTime {
			return leftHasMatchingArchiveRecordTime
		}
		if leftHasMatchingArchiveRecordTime && !left.LatestMatchingArchiveRecordTime.Equal(right.LatestMatchingArchiveRecordTime) {
			return left.LatestMatchingArchiveRecordTime.After(right.LatestMatchingArchiveRecordTime)
		}
		return strings.ToLower(left.Who) < strings.ToLower(right.Who)
	})
}

func federatedPersonMatchOperation(
	resolution federatedWhoResolution,
	trawlerDisplayNames map[string]string,
) *federationcontract.FederatedTrawlerPersonMatchOperation {
	return &federationcontract.FederatedTrawlerPersonMatchOperation{
		Outcome:                         federatedOperationOutcome(len(resolution.TrawlersConsulted), len(resolution.OperationFailures), 0),
		PersonMatchCandidates:           federatedPersonMatchCandidates(resolution.Candidates, trawlerDisplayNames),
		OperationFailures:               append([]*federationcontract.TrawlerOperationFailure(nil), resolution.OperationFailures...),
		PersonQueryUsedToFindCandidates: resolution.Query,
	}
}

func federatedPersonMatchCandidates(
	whoCandidates []personMatchCandidate,
	trawlerDisplayNames map[string]string,
) []*federationcontract.FederatedPersonMatchCandidate {
	candidates := make([]*federationcontract.FederatedPersonMatchCandidate, 0, len(whoCandidates))
	for _, candidate := range whoCandidates {
		candidatePersonMatchFactsFromTrawlers := candidate.PersonMatchFactsFromTrawlers
		facts := make([]*person.PersonMatchFactsFromTrawler, 0, len(candidatePersonMatchFactsFromTrawlers))
		for _, personMatchFactsFromTrawler := range candidatePersonMatchFactsFromTrawlers {
			registeredTrawler := personMatchFactsFromTrawler.GetRegisteredTrawler()
			trawlerIdentityText := trawlkit.RegisteredTrawlerIdentityText(registeredTrawler)
			facts = append(facts, &person.PersonMatchFactsFromTrawler{
				RegisteredTrawler:            registeredTrawler,
				RegisteredTrawlerDisplayName: firstNonEmpty(trawlerDisplayNames[trawlerIdentityText], trawlerIdentityText),
				ExactPersonFilterIdentifiersObservedByTrawlerArchive: append(
					[]*person.ExactPersonFilterIdentifier(nil),
					personMatchFactsFromTrawler.GetExactPersonFilterIdentifiersObservedByTrawlerArchive()...,
				),
				PersonDisplayNamesObservedByTrawlerArchive: append(
					[]string(nil),
					personMatchFactsFromTrawler.GetPersonDisplayNamesObservedByTrawlerArchive()...,
				),
			})
		}
		messageCountInvolvingPersonAcrossTrawlers := uint64(0)
		if candidate.MessageCountInvolvingPerson > 0 {
			messageCountInvolvingPersonAcrossTrawlers = uint64(candidate.MessageCountInvolvingPerson)
		}
		converted := &federationcontract.FederatedPersonMatchCandidate{
			PersonDisplayName:                                     candidate.Who,
			AlternativePersonDisplayNames:                         append([]string(nil), candidate.AlternativeNames...),
			PersonNameOrHumanReadableContactValueThatMatchedQuery: candidate.PersonNameOrHumanReadableContactValueThatMatchedQuery,
			MessageCountInvolvingPersonAcrossTrawlers:             messageCountInvolvingPersonAcrossTrawlers,
			PersonMatchFactsFromTrawlers:                          facts,
			PersonMessageCountsFromTrawlerArchives: personMessageCountsWithTrawlerDisplayNames(
				candidate.PersonMessageCountsFromTrawlerArchives,
				trawlerDisplayNames,
			),
			PersonTrawlLink: candidate.PersonTrawlLink,
		}
		if !candidate.LatestMatchingArchiveRecordTime.IsZero() {
			converted.LatestMatchingArchiveRecordTime = timestamppb.New(candidate.LatestMatchingArchiveRecordTime)
		}
		candidates = append(candidates, converted)
	}
	return candidates
}

func normalizedPersonMessageCountsFromTrawlerArchives(
	messageCounts []*person.PersonMessageCountFromTrawlerArchive,
) []*person.PersonMessageCountFromTrawlerArchive {
	normalizedMessageCounts := make([]*person.PersonMessageCountFromTrawlerArchive, 0, len(messageCounts))
	for _, messageCount := range messageCounts {
		if messageCount == nil || messageCount.GetMessageCountInvolvingPersonInTrawlerArchive() == 0 {
			continue
		}
		normalizedMessageCounts = append(normalizedMessageCounts, &person.PersonMessageCountFromTrawlerArchive{
			RegisteredTrawler:                           messageCount.GetRegisteredTrawler(),
			RegisteredTrawlerDisplayName:                strings.TrimSpace(messageCount.GetRegisteredTrawlerDisplayName()),
			MessageCountInvolvingPersonInTrawlerArchive: messageCount.GetMessageCountInvolvingPersonInTrawlerArchive(),
		})
	}
	sort.SliceStable(normalizedMessageCounts, func(left, right int) bool {
		leftCount := normalizedMessageCounts[left].GetMessageCountInvolvingPersonInTrawlerArchive()
		rightCount := normalizedMessageCounts[right].GetMessageCountInvolvingPersonInTrawlerArchive()
		if leftCount != rightCount {
			return leftCount > rightCount
		}
		return trawlkit.RegisteredTrawlerIdentityText(normalizedMessageCounts[left].GetRegisteredTrawler()) <
			trawlkit.RegisteredTrawlerIdentityText(normalizedMessageCounts[right].GetRegisteredTrawler())
	})
	return normalizedMessageCounts
}

func personMessageCountsWithTrawlerDisplayNames(
	messageCounts []*person.PersonMessageCountFromTrawlerArchive,
	trawlerDisplayNames map[string]string,
) []*person.PersonMessageCountFromTrawlerArchive {
	messageCountsWithDisplayNames := normalizedPersonMessageCountsFromTrawlerArchives(messageCounts)
	for _, messageCount := range messageCountsWithDisplayNames {
		registeredTrawlerIdentity := trawlkit.RegisteredTrawlerIdentityText(
			messageCount.GetRegisteredTrawler(),
		)
		messageCount.RegisteredTrawlerDisplayName = firstNonEmpty(
			trawlerDisplayNames[registeredTrawlerIdentity],
			registeredTrawlerIdentity,
		)
	}
	return messageCountsWithDisplayNames
}
