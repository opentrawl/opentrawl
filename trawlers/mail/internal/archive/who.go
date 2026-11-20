package archive

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit/mailparse"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	"github.com/opentrawl/opentrawl/trawlkit/whomatch"
)

const ownerIdentityKey = "owner:me"

type MailPersonCandidate struct {
	PersonDisplayName           string
	PersonIdentifiers           []string
	LatestMessageTime           time.Time
	MessageCountInvolvingPerson int64

	participantKeys []string
	matchRank       whomatch.Rank
}

type AmbiguousPersonFilterError struct {
	PersonFilterText string
	Candidates       []MailPersonCandidate
}

func (e *AmbiguousPersonFilterError) Error() string {
	return fmt.Sprintf("who %q matched more than one person", e.PersonFilterText)
}

type UnknownPersonFilterError struct {
	PersonFilterText string
}

func (e *UnknownPersonFilterError) Error() string {
	return fmt.Sprintf("who %q did not match a person", e.PersonFilterText)
}

func (s *Store) ResolveMailPersonCandidates(ctx context.Context, personQuery string) ([]MailPersonCandidate, error) {
	personQuery = whomatch.Normalize(personQuery)
	if personQuery == "" {
		return []MailPersonCandidate{}, nil
	}
	candidates, err := s.allMailPersonCandidates(ctx)
	if err != nil {
		return nil, err
	}
	matches := []MailPersonCandidate{}
	for _, candidate := range candidates {
		rank, matched := candidate.matcherCandidate().MatchRank(personQuery)
		if !matched {
			continue
		}
		candidate.matchRank = rank
		matches = append(matches, candidate)
	}
	sort.SliceStable(matches, func(left, right int) bool {
		if matches[left].matchRank != matches[right].matchRank {
			return matches[left].matchRank.BetterThan(matches[right].matchRank)
		}
		return matches[left].LatestMessageTime.After(matches[right].LatestMessageTime)
	})
	return matches, nil
}

// personFilterParticipantKeys turns a search person filter into participant
// keys. Exact identifiers come from cross-trawler resolution; free text is
// resolved here and must name exactly one person.
func (s *Store) personFilterParticipantKeys(
	ctx context.Context,
	unresolvedPersonFilterText string,
	exactPersonFilterIdentifiers []*person.ExactPersonFilterIdentifier,
) (bool, []string, error) {
	if len(exactPersonFilterIdentifiers) > 0 {
		var keys []string
		for _, exactIdentifier := range exactPersonFilterIdentifiers {
			keys = append(keys, exactIdentifierParticipantKeys(exactIdentifier.GetExactPersonFilterIdentifier())...)
		}
		return true, keys, nil
	}
	unresolvedPersonFilterText = whomatch.Normalize(unresolvedPersonFilterText)
	if unresolvedPersonFilterText == "" {
		return false, nil, nil
	}
	if keys := exactIdentifierParticipantKeys(unresolvedPersonFilterText); len(keys) > 0 {
		return true, keys, nil
	}
	candidates, err := s.ResolveMailPersonCandidates(ctx, unresolvedPersonFilterText)
	if err != nil {
		return false, nil, err
	}
	switch {
	case len(candidates) == 0 || (len(candidates) == 1 && candidates[0].matchRank == whomatch.RankCloseSpelling):
		return false, nil, &UnknownPersonFilterError{PersonFilterText: unresolvedPersonFilterText}
	case len(candidates) > 1:
		return false, nil, &AmbiguousPersonFilterError{PersonFilterText: unresolvedPersonFilterText, Candidates: candidates}
	}
	return true, candidates[0].participantKeys, nil
}

func exactIdentifierParticipantKeys(identifier string) []string {
	if !mailparse.IsEmailAddressText(identifier) {
		return nil
	}
	return []string{"addr:" + string(mailparse.NormalizeEmailAddress(identifier))}
}

type mailPersonAggregate struct {
	displayNameCounts map[string]int
	identifiers       map[string]struct{}
	participantKeys   map[string]struct{}
	messages          map[ArchivedMailMessageIdentifier]struct{}
	latestMessageUnix int64
}

func (s *Store) allMailPersonCandidates(ctx context.Context) ([]MailPersonCandidate, error) {
	ownerAddresses, err := s.OwnerEmailAddresses(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.DB().QueryContext(ctx, `
select participant.message_id, participant.participant_key, participant.name, participant.address, message.time_unix
from message_participants participant
join messages message on message.id = participant.message_id
where message.deleted_from_source_at = ''`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	aggregates := map[string]*mailPersonAggregate{}
	for rows.Next() {
		var messageIdentifier ArchivedMailMessageIdentifier
		var key, name, address string
		var messageUnix int64
		if err := rows.Scan(&messageIdentifier, &key, &name, &address, &messageUnix); err != nil {
			return nil, err
		}
		identityKey := key
		if _, isOwner := ownerAddresses[mailparse.NormalizedEmailAddress(address)]; isOwner && address != "" {
			identityKey = ownerIdentityKey
		}
		aggregate := aggregates[identityKey]
		if aggregate == nil {
			aggregate = &mailPersonAggregate{
				displayNameCounts: map[string]int{}, identifiers: map[string]struct{}{},
				participantKeys: map[string]struct{}{}, messages: map[ArchivedMailMessageIdentifier]struct{}{},
			}
			aggregates[identityKey] = aggregate
		}
		if name != "" {
			aggregate.displayNameCounts[name]++
		}
		if address != "" {
			aggregate.identifiers[address] = struct{}{}
		}
		aggregate.participantKeys[key] = struct{}{}
		aggregate.messages[messageIdentifier] = struct{}{}
		aggregate.latestMessageUnix = max(aggregate.latestMessageUnix, messageUnix)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	keysByNormalizedName := map[string]map[string]struct{}{}
	matcherCandidates := make([]whomatch.Candidate, 0, len(aggregates))
	for identityKey, aggregate := range aggregates {
		identifiers := sortedSetValues(aggregate.identifiers)
		displayName := whomatch.BestDisplayName(aggregate.displayNameCounts, identifiers)
		if identityKey == ownerIdentityKey {
			displayName = "me"
			identifiers = append([]string{"me"}, identifiers...)
		}
		if displayName == "" && len(identifiers) > 0 {
			displayName = identifiers[0]
		}
		if displayName == "" {
			continue
		}
		normalizedName := whomatch.Normalize(displayName)
		if keysByNormalizedName[normalizedName] == nil {
			keysByNormalizedName[normalizedName] = map[string]struct{}{}
		}
		for key := range aggregate.participantKeys {
			keysByNormalizedName[normalizedName][key] = struct{}{}
		}
		matcherCandidates = append(matcherCandidates, whomatch.Candidate{
			Who: displayName, Identifiers: identifiers,
			LastSeen: time.Unix(aggregate.latestMessageUnix, 0), Messages: int64(len(aggregate.messages)),
		})
	}
	sort.SliceStable(matcherCandidates, func(left, right int) bool {
		return matcherCandidates[left].LastSeen.After(matcherCandidates[right].LastSeen)
	})
	merged := whomatch.MergeSameName(matcherCandidates)
	candidates := make([]MailPersonCandidate, 0, len(merged))
	for _, candidate := range merged {
		candidates = append(candidates, MailPersonCandidate{
			PersonDisplayName:           candidate.Who,
			PersonIdentifiers:           candidate.Identifiers,
			LatestMessageTime:           candidate.LastSeen,
			MessageCountInvolvingPerson: candidate.Messages,
			participantKeys:             sortedSetValues(keysByNormalizedName[whomatch.Normalize(candidate.Who)]),
		})
	}
	return candidates, nil
}

func (candidate MailPersonCandidate) matcherCandidate() whomatch.Candidate {
	return whomatch.Candidate{Who: candidate.PersonDisplayName, Identifiers: candidate.PersonIdentifiers}
}

func sortedSetValues(set map[string]struct{}) []string {
	values := make([]string, 0, len(set))
	for value := range set {
		values = append(values, value)
	}
	sort.Slice(values, func(left, right int) bool { return strings.ToLower(values[left]) < strings.ToLower(values[right]) })
	return values
}
