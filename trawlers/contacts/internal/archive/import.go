package archive

import (
	"context"
	"reflect"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/avatar"
	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
)

func (s *Store) ImportContacts(ctx context.Context, source string, contacts []model.SourceContact, dryRun bool, now time.Time) ([]model.ImportChange, error) {
	source = strings.TrimSpace(strings.ToLower(source))
	people, err := s.People(ctx)
	if err != nil {
		return nil, err
	}
	changes := make([]model.ImportChange, 0)
	for _, contact := range contacts {
		contact.Source = source
		if strings.TrimSpace(contact.Name) == "" {
			continue
		}
		idx := matchContact(people, contact, true)
		if idx < 0 {
			person := model.NewPerson(contact.Name, now)
			person.Tags = cleanStrings(contact.Tags)
			person.Emails = sourceValues(contact.Emails, source, model.NormalizeEmail)
			person.Phones = sourceValues(contact.Phones, source, model.NormalizePhone)
			person.Addresses = sourceValues(contact.Addresses, source, model.NormalizeAddress)
			person.Accounts = cleanAccounts(contact.Accounts)
			setExternal(&person, source, contact, now)
			setImportedAvatar(&person, contact.Avatar, source, now)
			change := model.ImportChange{Action: "create", PersonID: person.ID, Name: person.Name, Source: contact}
			if !dryRun {
				if err := s.SavePerson(ctx, person); err != nil {
					return nil, err
				}
			}
			people = append(people, person)
			changes = append(changes, change)
			continue
		}
		person := people[idx]
		before := canonicalPerson(person)
		person.Tags = appendMissingStrings(person.Tags, contact.Tags)
		person.Emails = appendMissingValues(person.Emails, contact.Emails, source, model.NormalizeEmail)
		person.Phones = appendMissingValues(person.Phones, contact.Phones, source, model.NormalizePhone)
		person.Addresses = appendMissingValues(person.Addresses, contact.Addresses, source, model.NormalizeAddress)
		person.Accounts = mergeAccounts(person.Accounts, contact.Accounts)
		setExternal(&person, source, contact, now)
		setImportedAvatar(&person, contact.Avatar, source, now)
		person = canonicalPerson(person)
		if reflect.DeepEqual(before, person) {
			continue
		}
		person.UpdatedAt = now.UTC()
		change := model.ImportChange{Action: "update", PersonID: person.ID, Name: person.Name, Source: contact}
		if !dryRun {
			if err := s.SavePerson(ctx, person); err != nil {
				return nil, err
			}
		}
		people[idx] = person
		changes = append(changes, change)
	}
	return changes, nil
}

func sourceValues(values []model.ContactValue, source string, normalize func(string) string) []model.ContactValue {
	out := make([]model.ContactValue, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		key := normalize(value.Value)
		if key == "" || seen[key] {
			continue
		}
		value.Source = source
		if value.Label == "" {
			value.Label = "other"
		}
		if len(out) == 0 {
			value.Primary = true
		}
		out = append(out, value)
		seen[key] = true
	}
	return out
}

func mergeAccounts(existing map[string][]string, incoming map[string][]string) map[string][]string {
	if len(incoming) == 0 {
		return existing
	}
	if existing == nil {
		existing = map[string][]string{}
	}
	for service, values := range cleanAccounts(incoming) {
		existing[service] = appendMissingStrings(existing[service], values)
	}
	return existing
}

func matchContact(people []model.Person, contact model.SourceContact, matchNames bool) int {
	for i, person := range people {
		if accountsOverlap(person.Accounts, contact.Accounts) {
			return i
		}
	}
	for i, person := range people {
		switch contact.Source {
		case "apple":
			if contact.ExternalID != "" && person.Apple.ID == contact.ExternalID {
				return i
			}
		case "google":
			if contact.ExternalID != "" && person.Google.Resource == contact.ExternalID {
				return i
			}
		}
	}
	for i, person := range people {
		for _, email := range contact.Emails {
			if key := model.NormalizeEmail(email.Value); key != "" && personHasEmail(person, key) {
				return i
			}
		}
	}
	for i, person := range people {
		for _, phone := range contact.Phones {
			if key := model.NormalizePhone(phone.Value); key != "" && personHasPhone(person, key) {
				return i
			}
		}
	}
	if !matchNames {
		return -1
	}
	for i, person := range people {
		if model.NormalizeName(person.Name) != "" && model.NormalizeName(person.Name) == model.NormalizeName(contact.Name) {
			return i
		}
	}
	return -1
}

func accountsOverlap(existing map[string][]string, incoming map[string][]string) bool {
	for service, values := range cleanAccounts(incoming) {
		current := existing[service]
		for _, value := range values {
			for _, cur := range current {
				if strings.EqualFold(strings.TrimSpace(cur), strings.TrimSpace(value)) {
					return true
				}
			}
		}
	}
	return false
}

func personHasEmail(person model.Person, email string) bool {
	for _, value := range person.Emails {
		if model.NormalizeEmail(value.Value) == email {
			return true
		}
	}
	return false
}

func personHasPhone(person model.Person, phone string) bool {
	for _, value := range person.Phones {
		if model.NormalizePhone(value.Value) == phone {
			return true
		}
	}
	return false
}

func setExternal(person *model.Person, source string, contact model.SourceContact, now time.Time) {
	switch source {
	case "apple":
		if contact.ExternalID == "" {
			return
		}
		person.Apple.ID = contact.ExternalID
		person.Apple.LastSeenAt = now.UTC()
	case "google":
		if contact.ExternalID == "" && contact.ETag == "" {
			return
		}
		person.Google.Resource = contact.ExternalID
		person.Google.ETag = contact.ETag
		person.Google.LastSeenAt = now.UTC()
	}
}

func setImportedAvatar(person *model.Person, incoming *model.SourceAvatar, source string, now time.Time) {
	if incoming == nil || len(incoming.Data) == 0 {
		return
	}
	inspected, err := avatar.InspectBytes(incoming.Data)
	if err != nil {
		return
	}
	incoming.MIME = inspected.MIME
	incoming.SHA256 = inspected.SHA256
	if person.Avatar.SHA256 == inspected.SHA256 {
		return
	}
	if len(person.Avatar.Data) > 0 && person.Avatar.Source != "" && person.Avatar.Source != source {
		return
	}
	person.Avatar = model.AvatarRef{
		Source:    source,
		MIME:      inspected.MIME,
		SHA256:    inspected.SHA256,
		Data:      inspected.Data,
		UpdatedAt: now.UTC(),
	}
}
