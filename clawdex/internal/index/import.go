package index

import (
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/openclaw/clawdex/internal/avatar"
	"github.com/openclaw/clawdex/internal/markdown"
	"github.com/openclaw/clawdex/internal/model"
)

type importOptions struct {
	DryRun         bool
	MatchNames     bool
	TrackSources   bool
	StageUnmatched bool
	UseIndexMatch  bool
}

func (s Store) ImportContacts(source string, contacts []model.SourceContact, dryRun bool, now time.Time) ([]model.ImportChange, error) {
	return s.importContacts(source, contacts, importOptions{DryRun: dryRun, MatchNames: true}, now)
}

func (s Store) ImportCrawlerContacts(source string, contacts []model.SourceContact, dryRun bool, now time.Time) ([]model.ImportChange, error) {
	return s.importContacts(source, contacts, importOptions{DryRun: dryRun, TrackSources: true, StageUnmatched: true, UseIndexMatch: true}, now)
}

func (s Store) importContacts(source string, contacts []model.SourceContact, opts importOptions, now time.Time) ([]model.ImportChange, error) {
	people, err := s.People()
	if err != nil {
		return nil, err
	}
	var identifiers map[identifierKey][]string
	if opts.UseIndexMatch {
		identifiers, err = s.indexedIdentifiers()
		if err != nil {
			return nil, err
		}
	}
	changes := make([]model.ImportChange, 0)
	var unmatched []model.SourceContact
	for _, contact := range contacts {
		contact.Source = source
		if strings.TrimSpace(contact.Name) == "" {
			continue
		}
		idx := -1
		if opts.UseIndexMatch {
			idx = matchContactByIndexedIdentifier(people, identifiers, contact)
		} else {
			idx = matchContact(people, contact, opts.MatchNames)
		}
		if idx < 0 {
			if opts.StageUnmatched {
				unmatched = append(unmatched, contact)
				continue
			}
			p := markdown.NewPerson(contact.Name, now)
			p.Tags = cleanList(contact.Tags)
			p.Emails = sourceValues(contact.Emails, source, model.NormalizeEmail)
			p.Phones = sourceValues(contact.Phones, source, model.NormalizePhone)
			p.Addresses = sourceValues(contact.Addresses, source, model.NormalizeAddress)
			p.Accounts = cleanAccounts(contact.Accounts)
			if opts.TrackSources {
				p.Sources = mergePersonSources(p.Sources, source, contact, now)
			}
			setExternal(&p, source, contact, now)
			change := model.ImportChange{Action: "create", PersonID: p.ID, Name: p.Name, Source: contact}
			if !opts.DryRun {
				created, err := s.createImportedPerson(p)
				if err != nil {
					return nil, err
				}
				if contact.Avatar != nil {
					withAvatar, _, err := avatar.SetImported(created, *contact.Avatar, source, now)
					if err != nil {
						return nil, err
					}
					if withAvatar.Avatar.Path != "" {
						withAvatar.UpdatedAt = now.UTC()
						if err := markdown.WritePerson(withAvatar.Path, withAvatar); err != nil {
							return nil, err
						}
						created = withAvatar
					}
				}
				change.PersonID = created.ID
				change.Path = created.Path
				people = append(people, created)
			} else {
				people = append(people, p)
			}
			changes = append(changes, change)
			continue
		}
		p := people[idx]
		beforeEmails := len(p.Emails)
		beforePhones := len(p.Phones)
		beforeAddresses := len(p.Addresses)
		beforeTags := append([]string(nil), p.Tags...)
		beforeAccounts := cloneAccounts(p.Accounts)
		beforeSources := cloneSources(p.Sources)
		beforeApple := p.Apple
		beforeGoogle := p.Google
		beforeAvatar := p.Avatar
		matchedContact := contact
		if opts.TrackSources {
			matchedContact = contactForPerson(people, idx, contact)
		}
		p.Tags = appendMissingStrings(p.Tags, contact.Tags)
		p.Emails = appendMissingValues(p.Emails, matchedContact.Emails, source, model.NormalizeEmail)
		p.Phones = appendMissingValues(p.Phones, matchedContact.Phones, source, model.NormalizePhone)
		p.Addresses = appendMissingValues(p.Addresses, matchedContact.Addresses, source, model.NormalizeAddress)
		p.Accounts = mergeAccounts(p.Accounts, matchedContact.Accounts)
		if opts.TrackSources {
			p.Sources = mergePersonSources(p.Sources, source, matchedContact, now)
		}
		setExternal(&p, source, contact, now)
		avatarChanged := avatarWouldChange(beforeAvatar, contact.Avatar, source)
		if !opts.DryRun && contact.Avatar != nil {
			var err error
			p, avatarChanged, err = avatar.SetImported(p, *contact.Avatar, source, now)
			if err != nil {
				return nil, err
			}
		}
		externalChanged := p.Apple != beforeApple || p.Google != beforeGoogle
		tagsChanged := strings.Join(beforeTags, "\x00") != strings.Join(p.Tags, "\x00")
		accountsChanged := !accountsEqual(beforeAccounts, p.Accounts)
		sourcesChanged := !reflect.DeepEqual(beforeSources, p.Sources)
		if len(p.Emails) == beforeEmails && len(p.Phones) == beforePhones && len(p.Addresses) == beforeAddresses && !tagsChanged && !accountsChanged && !sourcesChanged && !externalChanged && !avatarChanged {
			continue
		}
		change := model.ImportChange{Action: "update", PersonID: p.ID, Name: p.Name, Source: matchedContact, Path: p.Path}
		if !opts.DryRun {
			p.UpdatedAt = now.UTC()
			if err := markdown.WritePerson(p.Path, p); err != nil {
				return nil, err
			}
		}
		people[idx] = p
		changes = append(changes, change)
	}
	if len(unmatched) > 0 {
		staged, err := s.stageUnmatchedContacts(unmatched, opts.DryRun)
		if err != nil {
			return nil, err
		}
		changes = append(changes, staged...)
	}
	if !opts.DryRun {
		return changes, s.Rebuild()
	}
	return changes, nil
}

func contactForPerson(people []model.Person, idx int, contact model.SourceContact) model.SourceContact {
	contact.Emails = contactValuesForPerson(people, idx, contact.Emails, model.NormalizeEmail, personHasEmail)
	contact.Phones = contactValuesForPerson(people, idx, contact.Phones, model.NormalizePhone, personHasPhone)
	contact.Accounts = contactAccountsForPerson(people, idx, contact.Accounts)
	return contact
}

func contactValuesForPerson(people []model.Person, idx int, values []model.ContactValue, normalize func(string) string, has func(model.Person, string) bool) []model.ContactValue {
	out := make([]model.ContactValue, 0, len(values))
	for _, value := range values {
		key := normalize(value.Value)
		if key == "" || valueOwnedByOtherPerson(people, idx, key, has) {
			continue
		}
		out = append(out, value)
	}
	return out
}

func valueOwnedByOtherPerson(people []model.Person, idx int, key string, has func(model.Person, string) bool) bool {
	for i, person := range people {
		if i != idx && has(person, key) {
			return true
		}
	}
	return false
}

func contactAccountsForPerson(people []model.Person, idx int, accounts map[string][]string) map[string][]string {
	if len(accounts) == 0 {
		return nil
	}
	out := map[string][]string{}
	for service, values := range cleanAccounts(accounts) {
		for _, value := range values {
			if valueOwnedByOtherAccountPerson(people, idx, service, value) {
				continue
			}
			out[service] = append(out[service], value)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func valueOwnedByOtherAccountPerson(people []model.Person, idx int, service, value string) bool {
	key := strings.ToLower(strings.TrimSpace(value))
	if key == "" {
		return false
	}
	for i, person := range people {
		if i == idx {
			continue
		}
		for _, current := range person.Accounts[service] {
			if strings.ToLower(strings.TrimSpace(current)) == key {
				return true
			}
		}
	}
	return false
}

func avatarWouldChange(current model.AvatarRef, incoming *model.SourceAvatar, source string) bool {
	if incoming == nil || len(incoming.Data) == 0 {
		return false
	}
	sha := incoming.SHA256
	if sha == "" {
		inspected, err := avatar.InspectBytes(incoming.Data)
		if err != nil {
			return false
		}
		sha = inspected.SHA256
	}
	if current.SHA256 == sha {
		return false
	}
	return current.Path == "" || current.Source == "" || current.Source == source
}

func matchContact(people []model.Person, contact model.SourceContact, matchNames bool) int {
	for i, p := range people {
		if accountsOverlap(p.Accounts, contact.Accounts) {
			return i
		}
	}
	for i, p := range people {
		switch contact.Source {
		case "apple":
			if contact.ExternalID != "" && p.Apple.ID == contact.ExternalID {
				return i
			}
		case "google":
			if contact.ExternalID != "" && p.Google.Resource == contact.ExternalID {
				return i
			}
		}
	}
	for i, p := range people {
		for _, email := range contact.Emails {
			if model.NormalizeEmail(email.Value) != "" && personHasEmail(p, model.NormalizeEmail(email.Value)) {
				return i
			}
		}
	}
	for i, p := range people {
		for _, phone := range contact.Phones {
			if model.NormalizePhone(phone.Value) != "" && personHasPhone(p, model.NormalizePhone(phone.Value)) {
				return i
			}
		}
	}
	if !matchNames {
		return -1
	}
	for i, p := range people {
		if model.NormalizeName(p.Name) != "" && model.NormalizeName(p.Name) == model.NormalizeName(contact.Name) {
			return i
		}
	}
	return -1
}

func matchContactByIndexedIdentifier(people []model.Person, identifiers map[identifierKey][]string, contact model.SourceContact) int {
	personIndex := make(map[string]int, len(people))
	for i, person := range people {
		personIndex[person.ID] = i
	}
	for _, key := range contactIdentifierKeys(contact) {
		for _, personID := range identifiers[key] {
			if idx, ok := personIndex[personID]; ok {
				return idx
			}
		}
	}
	return -1
}

func contactIdentifierKeys(contact model.SourceContact) []identifierKey {
	var keys []identifierKey
	seen := map[identifierKey]bool{}
	add := func(key identifierKey) {
		if key.value == "" || seen[key] {
			return
		}
		keys = append(keys, key)
		seen[key] = true
	}
	for _, phone := range contact.Phones {
		add(identifierKey{kind: "phone", value: model.NormalizePhone(phone.Value)})
	}
	for _, email := range contact.Emails {
		add(identifierKey{kind: "email", value: model.NormalizeEmail(email.Value)})
	}
	for service, values := range cleanAccounts(contact.Accounts) {
		for _, value := range values {
			value = strings.TrimSpace(strings.ToLower(value))
			add(identifierKey{kind: "handle", value: service + ":" + value})
		}
	}
	return keys
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
		cleaned := cleanList(values)
		if len(cleaned) > 0 {
			out[service] = cleaned
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cloneAccounts(accounts map[string][]string) map[string][]string {
	if len(accounts) == 0 {
		return nil
	}
	out := make(map[string][]string, len(accounts))
	for service, values := range accounts {
		out[service] = append([]string(nil), values...)
	}
	return out
}

func cloneSources(sources map[string]model.PersonSource) map[string]model.PersonSource {
	if len(sources) == 0 {
		return nil
	}
	out := make(map[string]model.PersonSource, len(sources))
	for source, value := range sources {
		out[source] = model.PersonSource{
			Names:      append([]string(nil), value.Names...),
			Emails:     append([]string(nil), value.Emails...),
			Phones:     append([]string(nil), value.Phones...),
			Addresses:  append([]string(nil), value.Addresses...),
			Accounts:   cloneAccounts(value.Accounts),
			LastSeenAt: value.LastSeenAt,
		}
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

func accountsEqual(a, b map[string][]string) bool {
	if len(a) != len(b) {
		return false
	}
	for service, av := range a {
		bv := b[service]
		if len(av) != len(bv) {
			return false
		}
		for i := range av {
			if av[i] != bv[i] {
				return false
			}
		}
	}
	return true
}

func appendMissingValues(existing []model.ContactValue, incoming []model.ContactValue, source string, normalize func(string) string) []model.ContactValue {
	for _, value := range incoming {
		key := normalize(value.Value)
		if key == "" {
			continue
		}
		found := false
		for _, cur := range existing {
			if normalize(cur.Value) == key {
				found = true
				break
			}
		}
		if !found {
			value.Source = source
			if value.Label == "" {
				value.Label = "other"
			}
			existing = append(existing, value)
		}
	}
	return existing
}

func mergePersonSources(existing map[string]model.PersonSource, source string, contact model.SourceContact, now time.Time) map[string]model.PersonSource {
	source = strings.TrimSpace(strings.ToLower(source))
	if source == "" {
		return existing
	}
	if existing == nil {
		existing = map[string]model.PersonSource{}
	}
	current := existing[source]
	before := model.PersonSource{
		Names:      append([]string(nil), current.Names...),
		Emails:     append([]string(nil), current.Emails...),
		Phones:     append([]string(nil), current.Phones...),
		Addresses:  append([]string(nil), current.Addresses...),
		Accounts:   cloneAccounts(current.Accounts),
		LastSeenAt: current.LastSeenAt,
	}
	current.Names = appendMissingNormalizedStrings(current.Names, []string{contact.Name}, model.NormalizeName)
	current.Emails = appendMissingContactValues(current.Emails, contact.Emails, model.NormalizeEmail)
	current.Phones = appendMissingContactValues(current.Phones, contact.Phones, model.NormalizePhone)
	current.Addresses = appendMissingContactValues(current.Addresses, contact.Addresses, model.NormalizeAddress)
	current.Accounts = mergeAccounts(current.Accounts, contact.Accounts)
	if sourceChanged(before, current) || current.LastSeenAt.IsZero() {
		current.LastSeenAt = now.UTC()
	}
	if len(current.Names) == 0 && len(current.Emails) == 0 && len(current.Phones) == 0 && len(current.Addresses) == 0 && len(current.Accounts) == 0 {
		delete(existing, source)
		return existing
	}
	existing[source] = current
	return existing
}

func sourceChanged(before, after model.PersonSource) bool {
	return strings.Join(before.Names, "\x00") != strings.Join(after.Names, "\x00") ||
		strings.Join(before.Emails, "\x00") != strings.Join(after.Emails, "\x00") ||
		strings.Join(before.Phones, "\x00") != strings.Join(after.Phones, "\x00") ||
		strings.Join(before.Addresses, "\x00") != strings.Join(after.Addresses, "\x00") ||
		!accountsEqual(before.Accounts, after.Accounts)
}

func appendMissingContactValues(existing []string, incoming []model.ContactValue, normalize func(string) string) []string {
	values := make([]string, 0, len(incoming))
	for _, value := range incoming {
		values = append(values, value.Value)
	}
	return appendMissingNormalizedStrings(existing, values, normalize)
}

func appendMissingNormalizedStrings(existing []string, incoming []string, normalize func(string) string) []string {
	seen := map[string]bool{}
	for _, value := range existing {
		if key := normalize(value); key != "" {
			seen[key] = true
		}
	}
	for _, value := range incoming {
		value = strings.TrimSpace(value)
		key := normalize(value)
		if value == "" || key == "" || seen[key] {
			continue
		}
		existing = append(existing, value)
		seen[key] = true
	}
	sort.Strings(existing)
	return existing
}

func setExternal(p *model.Person, source string, contact model.SourceContact, now time.Time) {
	switch source {
	case "apple":
		if contact.ExternalID == "" {
			return
		}
		p.Apple.ID = contact.ExternalID
		p.Apple.LastSeenAt = now.UTC()
	case "google":
		if contact.ExternalID == "" && contact.ETag == "" {
			return
		}
		p.Google.Resource = contact.ExternalID
		p.Google.ETag = contact.ETag
		p.Google.LastSeenAt = now.UTC()
	}
}

func (s Store) createImportedPerson(p model.Person) (model.Person, error) {
	dir, err := s.uniquePersonDir(model.Slug(p.Name))
	if err != nil {
		return model.Person{}, err
	}
	p.Path = filepath.Join(dir, "person.md")
	p.Body = "# " + p.Name + "\n"
	if err := markdown.WritePerson(p.Path, p); err != nil {
		return model.Person{}, err
	}
	return p, nil
}
