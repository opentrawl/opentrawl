package archive

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/mailparse"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	"github.com/opentrawl/opentrawl/trawlkit/whomatch"
	"google.golang.org/protobuf/types/known/timestamppb"
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

// ExportContacts returns correspondents the archive owner has emailed, with
// their full message counts and latest activity across sent and received mail.
func (s *Store) ExportContacts(ctx context.Context) ([]*person.TrawlerPersonIdentity, error) {
	ownerAddresses, err := s.OwnerEmailAddresses(ctx)
	if err != nil {
		return nil, err
	}
	if len(ownerAddresses) == 0 {
		return nil, nil
	}
	ownerList := make([]string, 0, len(ownerAddresses))
	for address := range ownerAddresses {
		if address != "" {
			ownerList = append(ownerList, string(address))
		}
	}
	if len(ownerList) == 0 {
		return nil, nil
	}
	sort.Strings(ownerList)
	ownerArgs := make([]any, 0, len(ownerList))
	for _, address := range ownerList {
		ownerArgs = append(ownerArgs, address)
	}
	ownerPlaceholders := strings.TrimSuffix(strings.Repeat("?,", len(ownerList)), ",")
	rows, err := s.store.DB().QueryContext(ctx, `
select distinct participant.participant_key
from message_participants participant
join messages message on message.id = participant.message_id
where message.deleted_from_source_at = ''
  and message.from_address in (`+ownerPlaceholders+`)
  and participant.role in ('to', 'cc')
  and trim(participant.address) <> ''`, ownerArgs...)
	if err != nil {
		return nil, err
	}
	correspondentKeys := map[string]struct{}{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			_ = rows.Close()
			return nil, err
		}
		address := mailparse.NormalizeEmailAddress(strings.TrimPrefix(key, "addr:"))
		if _, isOwner := ownerAddresses[address]; isOwner {
			continue
		}
		correspondentKeys[key] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(correspondentKeys) == 0 {
		return nil, nil
	}
	allCandidates, err := s.allMailPersonCandidates(ctx)
	if err != nil {
		return nil, err
	}
	identities := make([]*person.TrawlerPersonIdentity, 0, len(correspondentKeys))
	for _, candidate := range allCandidates {
		if candidate.PersonDisplayName == "me" {
			continue
		}
		isCorrespondent := false
		for _, key := range candidate.participantKeys {
			if _, ok := correspondentKeys[key]; ok {
				isCorrespondent = true
				break
			}
		}
		if !isCorrespondent {
			continue
		}
		displayName := strings.Join(strings.Fields(candidate.PersonDisplayName), " ")
		if displayName == "" || whomatch.IsIdentifierLike(displayName, candidate.PersonIdentifiers) {
			continue
		}
		emailAddresses := make([]string, 0, len(candidate.PersonIdentifiers))
		for _, identifier := range candidate.PersonIdentifiers {
			if mailparse.IsEmailAddressText(identifier) {
				emailAddresses = append(emailAddresses, string(mailparse.NormalizeEmailAddress(identifier)))
			}
		}
		if len(emailAddresses) == 0 {
			continue
		}
		identity := &person.TrawlerPersonIdentity{
			PersonIdentifierWithinTrawlerArchive:        trawlkit.NewPersonIdentifierWithinTrawlerArchive("mail:" + emailAddresses[0]),
			PersonDisplayName:                           displayName,
			PersonEmailAddresses:                        emailAddresses,
			MessageCountInvolvingPersonInTrawlerArchive: uint64(max(candidate.MessageCountInvolvingPerson, 0)),
		}
		if candidate.LatestMessageTime.After(time.Unix(0, 0)) {
			identity.LatestArchiveRecordTimeInvolvingPersonInTrawlerArchive = timestamppb.New(candidate.LatestMessageTime)
		}
		identities = append(identities, identity)
	}
	return identities, nil
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
