package archive

import (
	"sort"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
)

func cleanStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		key := strings.ToLower(value)
		if value == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func cleanAccounts(accounts map[string][]string) map[string][]string {
	if len(accounts) == 0 {
		return nil
	}
	out := map[string][]string{}
	for service, values := range accounts {
		service = strings.TrimSpace(strings.ToLower(service))
		if service == "" {
			continue
		}
		cleaned := cleanStrings(values)
		if len(cleaned) > 0 {
			out[service] = cleaned
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cleanSources(sources map[string]model.PersonSource) map[string]model.PersonSource {
	if len(sources) == 0 {
		return nil
	}
	out := make(map[string]model.PersonSource, len(sources))
	for source, value := range sources {
		source = strings.TrimSpace(strings.ToLower(source))
		if source == "" {
			continue
		}
		cleaned := model.PersonSource{
			Names:      cleanStrings(value.Names),
			Emails:     cleanStrings(value.Emails),
			Phones:     cleanStrings(value.Phones),
			Addresses:  cleanStrings(value.Addresses),
			Accounts:   cleanAccounts(value.Accounts),
			LastSeenAt: value.LastSeenAt.UTC(),
		}
		if len(cleaned.Names) == 0 && len(cleaned.Emails) == 0 && len(cleaned.Phones) == 0 && len(cleaned.Addresses) == 0 && len(cleaned.Accounts) == 0 {
			continue
		}
		out[source] = cleaned
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func indexNames(person model.Person) []string {
	return cleanStrings([]string{person.Name, person.SortName, model.Slug(person.Name)})
}

func indexAliases(person model.Person) []string {
	values := append([]string{}, person.AKA...)
	values = append(values, person.Tags...)
	for _, source := range person.Sources {
		values = append(values, source.Names...)
	}
	return cleanStrings(values)
}

func appendMissingStrings(existing []string, incoming []string) []string {
	seen := map[string]bool{}
	for _, value := range existing {
		seen[strings.ToLower(strings.TrimSpace(value))] = true
	}
	for _, value := range incoming {
		value = strings.TrimSpace(value)
		key := strings.ToLower(value)
		if value == "" || seen[key] {
			continue
		}
		existing = append(existing, value)
		seen[key] = true
	}
	sort.Strings(existing)
	return existing
}

func appendMissingValues(existing []model.ContactValue, incoming []model.ContactValue, source string, normalize func(string) string) []model.ContactValue {
	for _, value := range incoming {
		key := normalize(value.Value)
		if key == "" {
			continue
		}
		found := false
		for _, current := range existing {
			if normalize(current.Value) == key {
				found = true
				break
			}
		}
		if found {
			continue
		}
		value.Source = source
		if value.Label == "" {
			value.Label = "other"
		}
		existing = append(existing, value)
	}
	return existing
}
