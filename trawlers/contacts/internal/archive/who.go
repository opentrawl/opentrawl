package archive

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
	identity "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/identity"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	"github.com/opentrawl/opentrawl/trawlkit/whomatch"
)

type ResolvedPersonMatchCandidate struct {
	PersonDisplayName                                     string
	AlternativePersonDisplayNames                         []string
	PersonMatchFactsFromTrawlers                          []*person.PersonMatchFactsFromTrawler
	LatestArchiveRecordTimeInvolvingPersonAcrossTrawlers  time.Time
	MessageCountInvolvingPerson                           uint64
	PersonNameOrHumanReadableContactValueThatMatchedQuery string
	CanonicalPersonRecordReference                        string

	personIdentityMatchRank whomatch.Rank
}

func (candidate ResolvedPersonMatchCandidate) ExactPersonFilterIdentifiersFromTrawlerArchives() []string {
	return exactPersonFilterIdentifiersFromTrawlerArchives(
		candidate.PersonMatchFactsFromTrawlers,
	)
}

func (s *Store) ResolvePeople(ctx context.Context, query string) ([]ResolvedPersonMatchCandidate, error) {
	query = strings.Join(strings.Fields(query), " ")
	if query == "" {
		return nil, nil
	}
	people, err := s.PeopleMatchingQuery(ctx, query)
	if err != nil {
		return nil, err
	}
	candidates := make([]ResolvedPersonMatchCandidate, 0)
	for _, person := range people {
		candidate, ok := resolvePersonCandidate(person, query)
		if ok {
			candidates = append(candidates, candidate)
		}
	}
	sortResolvedPersonMatchCandidates(candidates)
	return candidates, nil
}

func (s *Store) PeopleMatchingQuery(ctx context.Context, query string) ([]model.Person, error) {
	query = strings.Join(strings.Fields(query), " ")
	if query == "" {
		return nil, nil
	}
	matchingPersonIdentifiers, err := s.personIdentifiersMatchingResolverQuery(ctx, query)
	if err != nil {
		return nil, err
	}
	matchingPeople := make([]model.Person, 0, len(matchingPersonIdentifiers))
	for _, personIdentifier := range matchingPersonIdentifiers {
		person, err := s.Person(ctx, personIdentifier)
		if err != nil {
			return nil, err
		}
		matchingPeople = append(matchingPeople, person)
	}
	return matchingPeople, nil
}

func (s *Store) personIdentifiersMatchingResolverQuery(ctx context.Context, query string) ([]string, error) {
	rows, err := s.database().QueryContext(ctx, `
select id, name, sort_name, aka_json, tags_json, accounts_json, sources_json, apple_json, google_json
from people
order by lower(name), id`)
	if err != nil {
		return nil, err
	}
	var matchingPersonIdentifiers []string
	for rows.Next() {
		var person model.Person
		var akaJSON, tagsJSON, accountsJSON, sourcesJSON, appleJSON, googleJSON string
		if err := rows.Scan(
			&person.ID,
			&person.Name,
			&person.SortName,
			&akaJSON,
			&tagsJSON,
			&accountsJSON,
			&sourcesJSON,
			&appleJSON,
			&googleJSON,
		); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := decodeJSONList(akaJSON, &person.AKA); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := decodeJSONList(tagsJSON, &person.Tags); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := decodeJSON(accountsJSON, &person.Accounts); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := decodeJSON(sourcesJSON, &person.Sources); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := decodeJSON(appleJSON, &person.Apple); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := decodeJSON(googleJSON, &person.Google); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if _, matches := resolverMatchCandidate(person).MatchRank(query); matches {
			matchingPersonIdentifiers = append(matchingPersonIdentifiers, person.ID)
		}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return matchingPersonIdentifiers, nil
}

func (s *Store) ResolveCanonicalPersonRecordReference(ctx context.Context, canonicalPersonRecordReference string) ([]ResolvedPersonMatchCandidate, error) {
	canonicalPersonRecordReference = strings.TrimSpace(canonicalPersonRecordReference)
	prefix := AppID + ":person/"
	if !strings.HasPrefix(canonicalPersonRecordReference, prefix) {
		return nil, fmt.Errorf("canonical Contacts person record reference is invalid")
	}
	personID := strings.TrimSpace(strings.TrimPrefix(canonicalPersonRecordReference, prefix))
	if personID == "" || strings.ContainsAny(personID, "\r\n\t") {
		return nil, fmt.Errorf("canonical Contacts person record reference is invalid")
	}
	person, err := s.Person(ctx, personID)
	if err != nil {
		return nil, err
	}
	return []ResolvedPersonMatchCandidate{personMatchCandidateForPerson(person)}, nil
}

func resolvePersonCandidate(person model.Person, query string) (ResolvedPersonMatchCandidate, bool) {
	matchCandidate := resolverMatchCandidate(person)
	rank, ok := matchCandidate.MatchRank(query)
	if !ok {
		return ResolvedPersonMatchCandidate{}, false
	}
	candidate := personMatchCandidateForPerson(person)
	candidate.PersonNameOrHumanReadableContactValueThatMatchedQuery =
		personNameOrHumanReadableContactValueThatMatchedQuery(person, query)
	candidate.personIdentityMatchRank = rank
	return candidate, true
}

func personMatchCandidateForPerson(person model.Person) ResolvedPersonMatchCandidate {
	return ResolvedPersonMatchCandidate{
		PersonDisplayName:                                    person.Name,
		AlternativePersonDisplayNames:                        resolverIdentityAliases(person),
		PersonMatchFactsFromTrawlers:                         personMatchFactsFromTrawlers(person),
		LatestArchiveRecordTimeInvolvingPersonAcrossTrawlers: latestArchiveRecordTimeInvolvingPerson(person),
		MessageCountInvolvingPerson:                          messageCountInvolvingPerson(person),
		CanonicalPersonRecordReference:                       PersonRef(person.ID),
	}
}

func personNameOrHumanReadableContactValueThatMatchedQuery(
	person model.Person,
	query string,
) string {
	humanReadableNamesAndContactValues := []string{person.Name, person.SortName}
	humanReadableNamesAndContactValues = append(humanReadableNamesAndContactValues, person.AKA...)
	for _, source := range person.Sources {
		humanReadableNamesAndContactValues = append(
			humanReadableNamesAndContactValues,
			source.Names...,
		)
		humanReadableNamesAndContactValues = append(
			humanReadableNamesAndContactValues,
			source.Emails...,
		)
		humanReadableNamesAndContactValues = append(
			humanReadableNamesAndContactValues,
			source.Phones...,
		)
		for serviceName, accountIdentifiers := range source.Accounts {
			for _, accountIdentifier := range accountIdentifiers {
				if humanReadableAccountIdentifier := model.AccountIdentifierForHumanPresentation(
					serviceName,
					accountIdentifier,
				); humanReadableAccountIdentifier != "" {
					humanReadableNamesAndContactValues = append(
						humanReadableNamesAndContactValues,
						humanReadableAccountIdentifier,
					)
				}
			}
		}
	}
	for _, emailAddress := range person.Emails {
		humanReadableNamesAndContactValues = append(
			humanReadableNamesAndContactValues,
			emailAddress.Value,
		)
	}
	for _, phoneNumber := range person.Phones {
		humanReadableNamesAndContactValues = append(
			humanReadableNamesAndContactValues,
			phoneNumber.Value,
		)
	}
	for serviceName, accountIdentifiers := range person.Accounts {
		for _, accountIdentifier := range accountIdentifiers {
			if humanReadableAccountIdentifier := model.AccountIdentifierForHumanPresentation(
				serviceName,
				accountIdentifier,
			); humanReadableAccountIdentifier != "" {
				humanReadableNamesAndContactValues = append(
					humanReadableNamesAndContactValues,
					humanReadableAccountIdentifier,
				)
			}
		}
	}

	bestMatchRank := whomatch.Rank(0)
	bestHumanReadableMatch := ""
	seenNormalizedValues := make(map[string]struct{}, len(humanReadableNamesAndContactValues))
	for _, humanReadableNameOrContactValue := range humanReadableNamesAndContactValues {
		humanReadableNameOrContactValue = strings.Join(
			strings.Fields(humanReadableNameOrContactValue),
			" ",
		)
		normalizedValue := whomatch.Normalize(humanReadableNameOrContactValue)
		if normalizedValue == "" {
			continue
		}
		if _, alreadyChecked := seenNormalizedValues[normalizedValue]; alreadyChecked {
			continue
		}
		seenNormalizedValues[normalizedValue] = struct{}{}
		matchRank, matches := whomatch.MatchRank(
			query,
			[]string{humanReadableNameOrContactValue},
		)
		if !matches || !matchRank.BetterThan(bestMatchRank) {
			continue
		}
		bestMatchRank = matchRank
		bestHumanReadableMatch = humanReadableNameOrContactValue
	}
	return bestHumanReadableMatch
}

// resolverIdentityAliases is deliberately narrower than the aliases used to
// find a Person. Search conveniences such as a Person ID, slug or tag may help
// `who` locate a record, but they are not evidence that a chat participant is
// that person. Cross-service chat matching gets only real names and account
// handles observed on the reconciled identity.
func resolverIdentityAliases(person model.Person) []string {
	aliases := []string{person.SortName}
	aliases = append(aliases, person.AKA...)
	for _, source := range person.Sources {
		aliases = append(aliases, source.Names...)
		aliases = appendPersonAccountIdentifiers(aliases, source.Accounts)
	}
	aliases = appendPersonAccountIdentifiers(aliases, person.Accounts)
	return cleanSortedStrings(aliases)
}

func resolverMatchCandidate(person model.Person) whomatch.Candidate {
	slug := model.Slug(person.Name)
	aliases := []string{person.ID, person.SortName, slug, strings.ReplaceAll(slug, "-", " ")}
	aliases = append(aliases, person.AKA...)
	aliases = append(aliases, person.Tags...)
	for _, source := range person.Sources {
		aliases = append(aliases, source.Names...)
		aliases = appendPersonAccountIdentifiers(aliases, source.Accounts)
	}
	aliases = appendPersonAccountIdentifiers(aliases, person.Accounts)
	return whomatch.Candidate{
		Who:         person.Name,
		Identifiers: exactPersonFilterIdentifiersFromTrawlerArchives(personMatchFactsFromTrawlers(person)),
		Aliases:     cleanSortedStrings(aliases),
	}
}

func personMatchFactsFromTrawlers(contactsPerson model.Person) []*person.PersonMatchFactsFromTrawler {
	personMatchFactsByRegisteredTrawlerIdentity := map[string]*person.PersonMatchFactsFromTrawler{}
	factsForTrawler := func(registeredTrawlerIdentity string) *person.PersonMatchFactsFromTrawler {
		registeredTrawlerIdentity = strings.TrimSpace(registeredTrawlerIdentity)
		facts := personMatchFactsByRegisteredTrawlerIdentity[registeredTrawlerIdentity]
		if facts == nil {
			facts = &person.PersonMatchFactsFromTrawler{
				RegisteredTrawler: &identity.RegisteredTrawlerIdentity{
					RegisteredTrawlerIdentity: registeredTrawlerIdentity,
				},
			}
			personMatchFactsByRegisteredTrawlerIdentity[registeredTrawlerIdentity] = facts
		}
		return facts
	}
	contactsFacts := factsForTrawler(AppID)
	contactsFacts.ExactPersonFilterIdentifiersObservedByTrawlerArchive = append(
		contactsFacts.ExactPersonFilterIdentifiersObservedByTrawlerArchive,
		contactsPerson.ID,
	)
	contactsFacts.PersonDisplayNamesObservedByTrawlerArchive = append(
		contactsFacts.PersonDisplayNamesObservedByTrawlerArchive,
		contactsPerson.Name,
		contactsPerson.SortName,
	)
	contactsFacts.PersonDisplayNamesObservedByTrawlerArchive = append(
		contactsFacts.PersonDisplayNamesObservedByTrawlerArchive,
		contactsPerson.AKA...,
	)
	for registeredTrawlerIdentity, source := range contactsPerson.Sources {
		sourceFacts := factsForTrawler(registeredTrawlerIdentity)
		sourceFacts.ExactPersonFilterIdentifiersObservedByTrawlerArchive = append(
			sourceFacts.ExactPersonFilterIdentifiersObservedByTrawlerArchive,
			source.Emails...,
		)
		sourceFacts.ExactPersonFilterIdentifiersObservedByTrawlerArchive = append(
			sourceFacts.ExactPersonFilterIdentifiersObservedByTrawlerArchive,
			source.Phones...,
		)
		sourceFacts.ExactPersonFilterIdentifiersObservedByTrawlerArchive = appendPersonAccountIdentifiers(
			sourceFacts.ExactPersonFilterIdentifiersObservedByTrawlerArchive,
			source.Accounts,
		)
		sourceFacts.PersonDisplayNamesObservedByTrawlerArchive = append(
			sourceFacts.PersonDisplayNamesObservedByTrawlerArchive,
			source.Names...,
		)
	}
	for registeredTrawlerIdentity, personAccountIdentifiers := range contactsPerson.Accounts {
		sourceFacts := factsForTrawler(registeredTrawlerIdentity)
		sourceFacts.ExactPersonFilterIdentifiersObservedByTrawlerArchive = append(
			sourceFacts.ExactPersonFilterIdentifiersObservedByTrawlerArchive,
			personAccountIdentifiers...,
		)
	}
	registeredTrawlerIdentities := make(
		[]string,
		0,
		len(personMatchFactsByRegisteredTrawlerIdentity),
	)
	for registeredTrawlerIdentity := range personMatchFactsByRegisteredTrawlerIdentity {
		registeredTrawlerIdentities = append(
			registeredTrawlerIdentities,
			registeredTrawlerIdentity,
		)
	}
	sort.Strings(registeredTrawlerIdentities)
	personMatchFacts := make(
		[]*person.PersonMatchFactsFromTrawler,
		0,
		len(registeredTrawlerIdentities),
	)
	for _, registeredTrawlerIdentity := range registeredTrawlerIdentities {
		facts := personMatchFactsByRegisteredTrawlerIdentity[registeredTrawlerIdentity]
		facts.ExactPersonFilterIdentifiersObservedByTrawlerArchive = cleanSortedStrings(
			facts.ExactPersonFilterIdentifiersObservedByTrawlerArchive,
		)
		facts.PersonDisplayNamesObservedByTrawlerArchive = cleanSortedStrings(
			facts.PersonDisplayNamesObservedByTrawlerArchive,
		)
		personMatchFacts = append(personMatchFacts, facts)
	}
	return personMatchFacts
}

func exactPersonFilterIdentifiersFromTrawlerArchives(
	personMatchFacts []*person.PersonMatchFactsFromTrawler,
) []string {
	var exactPersonFilterIdentifiers []string
	for _, trawlerFacts := range personMatchFacts {
		if trawlerFacts == nil {
			continue
		}
		exactPersonFilterIdentifiers = append(
			exactPersonFilterIdentifiers,
			trawlerFacts.GetExactPersonFilterIdentifiersObservedByTrawlerArchive()...,
		)
	}
	return cleanSortedStrings(exactPersonFilterIdentifiers)
}

func appendPersonAccountIdentifiers(values []string, personAccountIdentifiersByServiceName map[string][]string) []string {
	for _, personAccountIdentifiers := range personAccountIdentifiersByServiceName {
		values = append(values, personAccountIdentifiers...)
	}
	return values
}

func resolverIdentifiers(person model.Person) []string {
	keys := personIdentifierKeys(person)
	values := make([]string, 0, len(keys))
	for _, key := range keys {
		values = append(values, strings.TrimSpace(key.value))
	}
	return cleanSortedStrings(values)
}

func latestArchiveRecordTimeInvolvingPerson(person model.Person) time.Time {
	var latest time.Time
	for _, source := range person.Sources {
		if source.LatestArchiveRecordTimeInvolvingPersonInSourceArchive.IsZero() {
			continue
		}
		if latest.IsZero() || source.LatestArchiveRecordTimeInvolvingPersonInSourceArchive.After(latest) {
			latest = source.LatestArchiveRecordTimeInvolvingPersonInSourceArchive
		}
	}
	return latest.UTC()
}

func messageCountInvolvingPerson(person model.Person) uint64 {
	var messageCount uint64
	for _, source := range person.Sources {
		messageCount += source.MessageCountInvolvingPersonInSourceArchive
	}
	return messageCount
}

func sortResolvedPersonMatchCandidates(candidates []ResolvedPersonMatchCandidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		left := candidates[i]
		right := candidates[j]
		if left.MessageCountInvolvingPerson != right.MessageCountInvolvingPerson {
			return left.MessageCountInvolvingPerson > right.MessageCountInvolvingPerson
		}
		if left.personIdentityMatchRank != right.personIdentityMatchRank {
			return left.personIdentityMatchRank.BetterThan(right.personIdentityMatchRank)
		}
		if left.LatestArchiveRecordTimeInvolvingPersonAcrossTrawlers.IsZero() !=
			right.LatestArchiveRecordTimeInvolvingPersonAcrossTrawlers.IsZero() {
			return !left.LatestArchiveRecordTimeInvolvingPersonAcrossTrawlers.IsZero()
		}
		if !left.LatestArchiveRecordTimeInvolvingPersonAcrossTrawlers.Equal(
			right.LatestArchiveRecordTimeInvolvingPersonAcrossTrawlers,
		) {
			return left.LatestArchiveRecordTimeInvolvingPersonAcrossTrawlers.After(
				right.LatestArchiveRecordTimeInvolvingPersonAcrossTrawlers,
			)
		}
		return strings.ToLower(left.PersonDisplayName) < strings.ToLower(right.PersonDisplayName)
	})
}

func cleanSortedStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := whomatch.Normalize(value)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool {
		return whomatch.Normalize(out[i]) < whomatch.Normalize(out[j])
	})
	return out
}
