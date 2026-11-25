package archive

import (
	"sort"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
)

func contactArchiveSourceKeyAllowed(sourceKey string, allowedSources map[string]struct{}) bool {
	normalized := strings.ToLower(strings.TrimSpace(sourceKey))
	if normalized == "" {
		return false
	}
	if normalized == "apple" || normalized == AppID {
		_, allowed := allowedSources[AppID]
		return allowed
	}
	_, allowed := allowedSources[normalized]
	return allowed
}

func noteAllowedBySources(note model.Note, allowedSources map[string]struct{}) bool {
	source := strings.ToLower(strings.TrimSpace(note.Source))
	if source == "" {
		_, allowed := allowedSources[AppID]
		return allowed
	}
	return contactArchiveSourceKeyAllowed(source, allowedSources)
}

// projectPersonForAllowedSources rebuilds a Person strictly from facts
// contributed by allowedSources so merged records never leak display names,
// aliases, tags, contact values, handles, timestamps, or message counts from
// disallowed sources. Returns (zero, false) when no allowed source contributed
// to this person.
func projectPersonForAllowedSources(person model.Person, allowedSources map[string]struct{}) (model.Person, bool) {
	contactsAllowed := contactArchiveSourceKeyAllowed(AppID, allowedSources)
	appleAllowed := contactArchiveSourceKeyAllowed("apple", allowedSources)
	googleAllowed := contactArchiveSourceKeyAllowed("google", allowedSources)

	filteredSources := make(map[string]model.PersonSource, len(person.Sources))
	sourceKeys := make([]string, 0, len(person.Sources))
	for sourceKey := range person.Sources {
		if contactArchiveSourceKeyAllowed(sourceKey, allowedSources) {
			sourceKeys = append(sourceKeys, sourceKey)
		}
	}
	sort.Slice(sourceKeys, func(i, j int) bool {
		leftPriority := sourceProjectionPriority(sourceKeys[i])
		rightPriority := sourceProjectionPriority(sourceKeys[j])
		if leftPriority != rightPriority {
			return leftPriority < rightPriority
		}
		return sourceKeys[i] < sourceKeys[j]
	})

	if len(person.Sources) > 0 {
		if len(sourceKeys) == 0 {
			return model.Person{}, false
		}
		for _, sourceKey := range sourceKeys {
			filteredSources[sourceKey] = person.Sources[sourceKey]
		}
	} else {
		// Unsourced manual/Apple-only card without a Sources map: visible only
		// when Apple Contacts itself is allowed.
		if !contactsAllowed {
			return model.Person{}, false
		}
	}

	allowedNames := make([]string, 0, len(sourceKeys)*2)
	allowedTags := make([]string, 0)
	projectedAccounts := map[string][]string{}
	var latestAllowedTime time.Time
	for _, sourceKey := range sourceKeys {
		src := filteredSources[sourceKey]
		for _, name := range src.Names {
			if trimmed := strings.TrimSpace(name); trimmed != "" {
				allowedNames = append(allowedNames, trimmed)
			}
		}
		allowedTags = append(allowedTags, src.Tags...)
		for serviceName, accountIdentifiers := range src.Accounts {
			projectedAccounts[serviceName] = appendMissingStrings(projectedAccounts[serviceName], accountIdentifiers)
		}
		if src.LatestArchiveRecordTimeInvolvingPersonInSourceArchive.After(latestAllowedTime) {
			latestAllowedTime = src.LatestArchiveRecordTimeInvolvingPersonInSourceArchive
		}
		if src.LastSeenAt.After(latestAllowedTime) {
			latestAllowedTime = src.LastSeenAt
		}
	}

	projectedName := ""
	if len(person.Sources) == 0 {
		projectedName = strings.TrimSpace(person.Name)
	} else {
		if trimmedOriginal := strings.TrimSpace(person.Name); trimmedOriginal != "" {
			for _, candidateName := range allowedNames {
				if strings.EqualFold(candidateName, trimmedOriginal) {
					projectedName = trimmedOriginal
					break
				}
			}
		}
		if projectedName == "" && len(allowedNames) > 0 {
			projectedName = allowedNames[0]
		}
	}

	projectedSortName := ""
	if trimmedSort := strings.TrimSpace(person.SortName); trimmedSort != "" {
		if len(person.Sources) == 0 {
			projectedSortName = trimmedSort
		} else if strings.EqualFold(trimmedSort, projectedName) {
			projectedSortName = trimmedSort
		} else {
			for _, candidateName := range allowedNames {
				if strings.EqualFold(candidateName, trimmedSort) {
					projectedSortName = trimmedSort
					break
				}
			}
		}
	}

	var projectedAKA []string
	if len(person.Sources) == 0 {
		projectedAKA = append([]string(nil), person.AKA...)
	} else {
		seenAKA := map[string]struct{}{}
		if projectedName != "" {
			seenAKA[strings.ToLower(projectedName)] = struct{}{}
		}
		for _, candidateName := range allowedNames {
			key := strings.ToLower(candidateName)
			if _, dup := seenAKA[key]; dup {
				continue
			}
			seenAKA[key] = struct{}{}
			projectedAKA = append(projectedAKA, candidateName)
		}
	}

	var projectedTags []string
	if len(person.Sources) == 0 {
		projectedTags = append([]string(nil), person.Tags...)
	} else {
		projectedTags = cleanSortedStrings(allowedTags)
	}

	projectedEmails := filterContactValuesByAllowedSources(person.Emails, allowedSources, func(src model.PersonSource) []string { return src.Emails }, sourceKeys, filteredSources)
	projectedPhones := filterContactValuesByAllowedSources(person.Phones, allowedSources, func(src model.PersonSource) []string { return src.Phones }, sourceKeys, filteredSources)
	projectedAddresses := filterContactValuesByAllowedSources(person.Addresses, allowedSources, func(src model.PersonSource) []string { return src.Addresses }, sourceKeys, filteredSources)

	if len(person.Sources) == 0 {
		for serviceName, accountIdentifiers := range person.Accounts {
			if contactArchiveSourceKeyAllowed(serviceName, allowedSources) {
				projectedAccounts[serviceName] = append([]string(nil), accountIdentifiers...)
			}
		}
	}
	if len(projectedAccounts) == 0 {
		projectedAccounts = nil
	}

	projected := model.Person{
		ID:        person.ID,
		Name:      projectedName,
		SortName:  projectedSortName,
		AKA:       projectedAKA,
		Tags:      projectedTags,
		Emails:    projectedEmails,
		Phones:    projectedPhones,
		Addresses: projectedAddresses,
		Accounts:  projectedAccounts,
		Sources:   filteredSources,
		UpdatedAt: latestAllowedTime,
	}
	if appleAllowed {
		projected.Apple = person.Apple
	}
	if googleAllowed {
		projected.Google = person.Google
	}
	if contactArchiveSourceKeyAllowed(person.Avatar.Source, allowedSources) {
		projected.Avatar = person.Avatar
	}
	if contactsAllowed {
		projected.Body = person.Body
		projected.PersonRelationshipOrContextDescription = person.PersonRelationshipOrContextDescription
		projected.PersonRelationshipOrContextDescriptionStatedDate = person.PersonRelationshipOrContextDescriptionStatedDate
		if projected.UpdatedAt.IsZero() {
			projected.UpdatedAt = person.UpdatedAt
		}
	}
	return projected, true
}

func sourceProjectionPriority(sourceKey string) int {
	switch strings.ToLower(strings.TrimSpace(sourceKey)) {
	case "apple", AppID:
		return 0
	case "google":
		return 1
	default:
		return 2
	}
}

func filterContactValuesByAllowedSources(
	values []model.ContactValue,
	allowedSources map[string]struct{},
	fromSource func(model.PersonSource) []string,
	sourceKeys []string,
	filteredSources map[string]model.PersonSource,
) []model.ContactValue {
	seen := make(map[string]struct{}, len(values))
	projected := make([]model.ContactValue, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value.Value)
		if trimmed == "" {
			continue
		}
		if !contactArchiveSourceKeyAllowed(value.Source, allowedSources) {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		projected = append(projected, value)
	}
	for _, sourceKey := range sourceKeys {
		for _, raw := range fromSource(filteredSources[sourceKey]) {
			trimmed := strings.TrimSpace(raw)
			if trimmed == "" {
				continue
			}
			key := strings.ToLower(trimmed)
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			projected = append(projected, model.ContactValue{
				Value:  trimmed,
				Source: sourceKey,
			})
		}
	}
	return projected
}
