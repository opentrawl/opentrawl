package trawlkit

import (
	"fmt"
	"strings"

	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
)

func ValidateTrawlerPeopleSnapshot(snapshot *person.TrawlerPeopleSnapshot) error {
	if snapshot == nil {
		return fmt.Errorf("people snapshot is missing")
	}
	seenPersonIdentifiersWithinTrawlerArchive := map[string]struct{}{}
	for personIdentityIndex, personIdentity := range snapshot.GetTrawlerPersonIdentities() {
		if err := validateTrawlerPersonIdentity(personIdentityIndex, personIdentity, seenPersonIdentifiersWithinTrawlerArchive); err != nil {
			return err
		}
	}
	return nil
}

func validateTrawlerPersonIdentity(
	personIdentityIndex int,
	personIdentity *person.TrawlerPersonIdentity,
	seenPersonIdentifiersWithinTrawlerArchive map[string]struct{},
) error {
	if personIdentity == nil {
		return fmt.Errorf("person identity %d is missing", personIdentityIndex)
	}
	personIdentifierWithinTrawlerArchive := strings.TrimSpace(
		personIdentity.GetPersonIdentifierWithinTrawlerArchive().GetPersonIdentifierWithinTrawlerArchive(),
	)
	if personIdentifierWithinTrawlerArchive != "" {
		if _, exists := seenPersonIdentifiersWithinTrawlerArchive[personIdentifierWithinTrawlerArchive]; exists {
			return fmt.Errorf("person identity %d repeats person identifier within trawler archive %q", personIdentityIndex, personIdentifierWithinTrawlerArchive)
		}
		seenPersonIdentifiersWithinTrawlerArchive[personIdentifierWithinTrawlerArchive] = struct{}{}
	}
	if strings.TrimSpace(personIdentity.GetPersonDisplayName()) == "" {
		return fmt.Errorf("person identity %d display name is required", personIdentityIndex)
	}
	if personIdentifierWithinTrawlerArchive == "" &&
		len(personIdentity.GetPersonEmailAddresses()) == 0 &&
		len(personIdentity.GetPersonPhoneNumbers()) == 0 &&
		len(personIdentity.GetPersonAccountIdentifiersForServices()) == 0 {
		return fmt.Errorf("person identity %d requires at least one identifier", personIdentityIndex)
	}
	if err := validateUniquePersonValues(personIdentityIndex, "email address", personIdentity.GetPersonEmailAddresses(), true); err != nil {
		return err
	}
	if err := validateUniquePersonValues(personIdentityIndex, "phone number", personIdentity.GetPersonPhoneNumbers(), false); err != nil {
		return err
	}
	return validatePersonAccountServices(personIdentityIndex, personIdentity.GetPersonAccountIdentifiersForServices())
}

func validateUniquePersonValues(personIdentityIndex int, label string, values []string, lowercase bool) error {
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if lowercase {
			value = strings.ToLower(value)
		}
		if value == "" {
			return fmt.Errorf("person identity %d contains an empty %s", personIdentityIndex, label)
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("person identity %d contains duplicate %s %q", personIdentityIndex, label, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validatePersonAccountServices(
	personIdentityIndex int,
	services []*person.TrawlerPersonAccountIdentifiersForService,
) error {
	seenServices := map[string]struct{}{}
	for _, service := range services {
		if service == nil {
			return fmt.Errorf("person identity %d contains a missing account service", personIdentityIndex)
		}
		serviceName := strings.TrimSpace(service.GetPersonAccountServiceName())
		if serviceName == "" {
			return fmt.Errorf("person identity %d contains an empty account service name", personIdentityIndex)
		}
		serviceKey := strings.ToLower(serviceName)
		if _, exists := seenServices[serviceKey]; exists {
			return fmt.Errorf("person identity %d contains duplicate account service name %q", personIdentityIndex, serviceName)
		}
		seenServices[serviceKey] = struct{}{}
		identifiers := service.GetPersonAccountIdentifiersWithinService()
		if len(identifiers) == 0 {
			return fmt.Errorf("person identity %d contains no %s account identifiers", personIdentityIndex, serviceName)
		}
		if err := validateServiceAccountIdentifiers(personIdentityIndex, serviceName, identifiers); err != nil {
			return err
		}
	}
	return nil
}

func validateServiceAccountIdentifiers(
	personIdentityIndex int,
	serviceName string,
	identifiers []*person.PersonAccountIdentifierWithinService,
) error {
	seen := map[string]struct{}{}
	for _, entry := range identifiers {
		identifier := strings.TrimSpace(entry.GetPersonAccountIdentifierWithinService())
		if identifier == "" {
			return fmt.Errorf("person identity %d contains an empty %s account identifier", personIdentityIndex, serviceName)
		}
		key := strings.ToLower(identifier)
		if _, exists := seen[key]; exists {
			return fmt.Errorf("person identity %d contains duplicate %s account identifier %q", personIdentityIndex, serviceName, identifier)
		}
		seen[key] = struct{}{}
	}
	return nil
}
