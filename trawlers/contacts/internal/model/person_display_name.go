package model

import (
	"strings"
	"unicode"
)

func PersonDisplayNameIsSuitableForHumanPresentation(value string, identifierValuesNotSuitableAsPersonDisplayNames []string) bool {
	value = strings.Join(strings.Fields(value), " ")
	valueIsOneASCIICharacter := len(value) == 1
	if value == "" || valueIsOneASCIICharacter || personDisplayNameIsAccountHandle(value) || personDisplayNameIsHashLike(value) {
		return false
	}
	normalizedValue := strings.ToLower(value)
	for _, technicalIdentifier := range identifierValuesNotSuitableAsPersonDisplayNames {
		normalizedTechnicalIdentifier := strings.ToLower(strings.TrimSpace(technicalIdentifier))
		if normalizedValue == normalizedTechnicalIdentifier {
			return false
		}
		if _, identifierWithoutService, hasService := strings.Cut(normalizedTechnicalIdentifier, ":"); hasService && normalizedValue == identifierWithoutService {
			return false
		}
	}
	return true
}

func PersonIdentifierValuesNotSuitableAsPersonDisplayNames(person Person) []string {
	identifiers := []string{
		person.ID,
		person.Apple.ID,
		person.Apple.Resource,
		person.Google.ID,
		person.Google.Resource,
	}
	appendAccountIdentifiers := func(accounts map[string][]string) {
		for _, accountIdentifiers := range accounts {
			identifiers = append(identifiers, accountIdentifiers...)
		}
	}
	appendAccountIdentifiers(person.Accounts)
	for _, source := range person.Sources {
		appendAccountIdentifiers(source.Accounts)
	}
	return identifiers
}

func personDisplayNameIsAccountHandle(value string) bool {
	value = strings.TrimSpace(value)
	if strings.Contains(value, "@") || strings.HasPrefix(value, "+") {
		return true
	}
	for _, firstCharacter := range value {
		return !unicode.IsLetter(firstCharacter)
	}
	return true
}

func personDisplayNameIsHashLike(value string) bool {
	compact := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "-", ""))
	if len(compact) < 16 {
		return false
	}
	for _, character := range compact {
		if character < '0' || character > '9' && character < 'a' || character > 'f' {
			return false
		}
	}
	return true
}
