package trawlkit

import (
	"fmt"
	"sort"
	"strings"

	personv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person/v1"
)

func ValidateTrawlerPeopleSnapshot(snapshot *personv1.TrawlerPeopleSnapshot) error {
	if snapshot == nil {
		return fmt.Errorf("people snapshot is missing")
	}
	seenPersonIdentifiersWithinTrawlerArchive := map[string]struct{}{}
	for personIdentityIndex, personIdentity := range snapshot.GetTrawlerPersonIdentities() {
		if personIdentity == nil {
			return fmt.Errorf("person identity %d is missing", personIdentityIndex)
		}
		if personIdentifierWithinTrawlerArchive := strings.TrimSpace(personIdentity.GetPersonIdentifierWithinTrawlerArchive()); personIdentifierWithinTrawlerArchive != "" {
			if _, exists := seenPersonIdentifiersWithinTrawlerArchive[personIdentifierWithinTrawlerArchive]; exists {
				return fmt.Errorf("person identity %d repeats person identifier within trawler archive %q", personIdentityIndex, personIdentifierWithinTrawlerArchive)
			}
			seenPersonIdentifiersWithinTrawlerArchive[personIdentifierWithinTrawlerArchive] = struct{}{}
		}
		if strings.TrimSpace(personIdentity.GetPersonDisplayName()) == "" {
			return fmt.Errorf("person identity %d display name is required", personIdentityIndex)
		}
		if strings.TrimSpace(personIdentity.GetPersonIdentifierWithinTrawlerArchive()) == "" &&
			len(personIdentity.GetPersonEmailAddresses()) == 0 &&
			len(personIdentity.GetPersonPhoneNumbers()) == 0 &&
			len(personIdentity.GetPersonAccountIdentifiersByServiceName()) == 0 {
			return fmt.Errorf("person identity %d requires at least one identifier", personIdentityIndex)
		}
		seenPersonEmailAddresses := map[string]struct{}{}
		for _, personEmailAddress := range personIdentity.GetPersonEmailAddresses() {
			personEmailAddress = strings.ToLower(strings.TrimSpace(personEmailAddress))
			if personEmailAddress == "" {
				return fmt.Errorf("person identity %d contains an empty email address", personIdentityIndex)
			}
			if _, exists := seenPersonEmailAddresses[personEmailAddress]; exists {
				return fmt.Errorf("person identity %d contains duplicate email address %q", personIdentityIndex, personEmailAddress)
			}
			seenPersonEmailAddresses[personEmailAddress] = struct{}{}
		}
		seenPersonPhoneNumbers := map[string]struct{}{}
		for _, personPhoneNumber := range personIdentity.GetPersonPhoneNumbers() {
			personPhoneNumber = strings.TrimSpace(personPhoneNumber)
			if personPhoneNumber == "" {
				return fmt.Errorf("person identity %d contains an empty phone number", personIdentityIndex)
			}
			if _, exists := seenPersonPhoneNumbers[personPhoneNumber]; exists {
				return fmt.Errorf("person identity %d contains duplicate phone number %q", personIdentityIndex, personPhoneNumber)
			}
			seenPersonPhoneNumbers[personPhoneNumber] = struct{}{}
		}
		seenPersonAccountServiceNames := map[string]struct{}{}
		personAccountServiceNames := make([]string, 0, len(personIdentity.GetPersonAccountIdentifiersByServiceName()))
		for personAccountServiceName := range personIdentity.GetPersonAccountIdentifiersByServiceName() {
			personAccountServiceNames = append(personAccountServiceNames, personAccountServiceName)
		}
		sort.Strings(personAccountServiceNames)
		for _, untrimmedPersonAccountServiceName := range personAccountServiceNames {
			personAccountIdentifiers := personIdentity.GetPersonAccountIdentifiersByServiceName()[untrimmedPersonAccountServiceName]
			personAccountServiceName := strings.TrimSpace(untrimmedPersonAccountServiceName)
			if personAccountServiceName == "" {
				return fmt.Errorf("person identity %d contains an empty account service name", personIdentityIndex)
			}
			personAccountServiceNameKey := strings.ToLower(personAccountServiceName)
			if _, exists := seenPersonAccountServiceNames[personAccountServiceNameKey]; exists {
				return fmt.Errorf("person identity %d contains duplicate account service name %q", personIdentityIndex, personAccountServiceName)
			}
			seenPersonAccountServiceNames[personAccountServiceNameKey] = struct{}{}
			if personAccountIdentifiers == nil || len(personAccountIdentifiers.GetPersonAccountIdentifiers()) == 0 {
				return fmt.Errorf("person identity %d contains no %s account identifiers", personIdentityIndex, personAccountServiceName)
			}
			seenPersonAccountIdentifiers := map[string]struct{}{}
			for _, personAccountIdentifier := range personAccountIdentifiers.GetPersonAccountIdentifiers() {
				personAccountIdentifier = strings.TrimSpace(personAccountIdentifier)
				if personAccountIdentifier == "" {
					return fmt.Errorf("person identity %d contains an empty %s account identifier", personIdentityIndex, personAccountServiceName)
				}
				personAccountIdentifierKey := strings.ToLower(personAccountIdentifier)
				if _, exists := seenPersonAccountIdentifiers[personAccountIdentifierKey]; exists {
					return fmt.Errorf("person identity %d contains duplicate %s account identifier %q", personIdentityIndex, personAccountServiceName, personAccountIdentifier)
				}
				seenPersonAccountIdentifiers[personAccountIdentifierKey] = struct{}{}
			}
		}
	}
	return nil
}
