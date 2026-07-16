package archive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
)

// SnapshotStats describes changes to source-owned contact records. People are
// stable groupings of those records, so a newly seen source contact can join an
// existing person without creating a second person.
type SnapshotStats struct {
	Added   int
	Updated int
	Removed int
}

type sourceContactRow struct {
	Source   string
	SourceID string
	PersonID string
	Contact  model.SourceContact
	SyncedAt time.Time
}

// SyncContactSnapshot replaces one source's current contact snapshot. The
// source-contact-to-person link is stored independently, making grouping
// reversible while keeping source facts replaceable on later syncs.
func (s *Store) SyncContactSnapshot(ctx context.Context, source string, contacts []model.SourceContact, now time.Time) (SnapshotStats, error) {
	source = strings.ToLower(strings.TrimSpace(source))
	if source == "" {
		return SnapshotStats{}, fmt.Errorf("contact source is required")
	}
	now = now.UTC()
	existing, err := s.sourceContacts(ctx, source)
	if err != nil {
		return SnapshotStats{}, err
	}
	people, err := s.People(ctx)
	if err != nil {
		return SnapshotStats{}, err
	}
	byID := make(map[string]sourceContactRow, len(existing))
	for _, row := range existing {
		byID[row.SourceID] = row
	}
	seen := make(map[string]bool, len(contacts))
	affected := map[string]bool{}
	stats := SnapshotStats{}
	for _, incoming := range contacts {
		contact := cleanSourceContact(source, incoming)
		if contact.Name == "" {
			continue
		}
		sourceID := sourceContactID(contact)
		if seen[sourceID] {
			continue
		}
		seen[sourceID] = true
		row, found := byID[sourceID]
		changed := false
		if !found {
			idx := matchContact(people, contact, true)
			if idx < 0 {
				person := personFromSourceContact(contact, now)
				if err := s.SavePerson(ctx, person); err != nil {
					return SnapshotStats{}, err
				}
				people = append(people, person)
				row.PersonID = person.ID
			} else {
				row.PersonID = people[idx].ID
				people[idx] = addSourceContactProjection(people[idx], contact, now)
			}
			stats.Added++
			changed = true
		} else if !sameSourceContact(row.Contact, contact) {
			stats.Updated++
			changed = true
		}
		row.Source = source
		row.SourceID = sourceID
		row.Contact = contact
		row.SyncedAt = now
		if err := s.saveSourceContact(ctx, row); err != nil {
			return SnapshotStats{}, err
		}
		if changed {
			affected[row.PersonID] = true
		}
	}
	for _, row := range existing {
		if seen[row.SourceID] {
			continue
		}
		if _, err := s.store.DB().ExecContext(ctx, `delete from source_contacts where source = ? and source_id = ?`, source, row.SourceID); err != nil {
			return SnapshotStats{}, err
		}
		stats.Removed++
		affected[row.PersonID] = true
	}
	for personID := range affected {
		if err := s.rebuildPersonFromSources(ctx, personID, source, now); err != nil {
			return SnapshotStats{}, err
		}
	}
	return stats, nil
}

// MoveSourceContact changes only the grouping link. Calling it again with the
// original person ID reverses the move without recreating or flattening the
// source record.
func (s *Store) MoveSourceContact(ctx context.Context, source, sourceID, personID string, now time.Time) error {
	source = strings.ToLower(strings.TrimSpace(source))
	sourceID = strings.TrimSpace(sourceID)
	personID = strings.TrimSpace(personID)
	if source == "" || sourceID == "" || personID == "" {
		return fmt.Errorf("source, source contact id, and person id are required")
	}
	if _, err := s.Person(ctx, personID); err != nil {
		return err
	}
	var previous string
	err := s.store.DB().QueryRowContext(ctx, `select person_id from source_contacts where source = ? and source_id = ?`, source, sourceID).Scan(&previous)
	if err != nil {
		return err
	}
	if previous == personID {
		return nil
	}
	if _, err := s.store.DB().ExecContext(ctx, `update source_contacts set person_id = ?, synced_at = ? where source = ? and source_id = ?`, personID, timeText(now.UTC()), source, sourceID); err != nil {
		return err
	}
	if err := s.rebuildPersonFromSources(ctx, previous, source, now); err != nil {
		return err
	}
	return s.rebuildPersonFromSources(ctx, personID, source, now)
}

func (s *Store) sourceContacts(ctx context.Context, source string) ([]sourceContactRow, error) {
	rows, err := s.store.DB().QueryContext(ctx, `select source, source_id, person_id, contact_json, synced_at from source_contacts where source = ? order by source_id`, source)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []sourceContactRow
	for rows.Next() {
		var row sourceContactRow
		var raw, syncedAt string
		if err := rows.Scan(&row.Source, &row.SourceID, &row.PersonID, &raw, &syncedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &row.Contact); err != nil {
			return nil, err
		}
		row.SyncedAt = parseTime(syncedAt)
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) sourceContactsForPerson(ctx context.Context, personID string) ([]sourceContactRow, error) {
	rows, err := s.store.DB().QueryContext(ctx, `select source, source_id, person_id, contact_json, synced_at from source_contacts where person_id = ? order by source, source_id`, personID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []sourceContactRow
	for rows.Next() {
		var row sourceContactRow
		var raw, syncedAt string
		if err := rows.Scan(&row.Source, &row.SourceID, &row.PersonID, &raw, &syncedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &row.Contact); err != nil {
			return nil, err
		}
		row.SyncedAt = parseTime(syncedAt)
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) saveSourceContact(ctx context.Context, row sourceContactRow) error {
	raw, err := json.Marshal(row.Contact)
	if err != nil {
		return err
	}
	_, err = s.store.DB().ExecContext(ctx, `
insert into source_contacts(source, source_id, person_id, contact_json, synced_at)
values (?, ?, ?, ?, ?)
on conflict(source, source_id) do update set
  person_id = excluded.person_id,
  contact_json = excluded.contact_json,
  synced_at = excluded.synced_at`, row.Source, row.SourceID, row.PersonID, string(raw), timeText(row.SyncedAt))
	return err
}

func (s *Store) rebuildPersonFromSources(ctx context.Context, personID, reconciledSource string, now time.Time) error {
	person, err := s.Person(ctx, personID)
	if err != nil {
		if errorsIsPersonMissing(err) {
			return nil
		}
		return err
	}
	rows, err := s.sourceContactsForPerson(ctx, personID)
	if err != nil {
		return err
	}
	managed := map[string]bool{reconciledSource: true}
	for _, row := range rows {
		managed[row.Source] = true
	}
	oldManagedNames := []string{}
	oldManagedTags := []string{}
	oldManagedAccounts := map[string][]string{}
	for source, snapshot := range person.Sources {
		if !managed[source] {
			continue
		}
		oldManagedNames = append(oldManagedNames, snapshot.Names...)
		oldManagedTags = append(oldManagedTags, snapshot.Tags...)
		oldManagedAccounts = mergeAccounts(oldManagedAccounts, snapshot.Accounts)
		delete(person.Sources, source)
	}
	person.Emails = removeManagedValues(person.Emails, managed)
	person.Phones = removeManagedValues(person.Phones, managed)
	person.Addresses = removeManagedValues(person.Addresses, managed)
	person.Tags = subtractStrings(person.Tags, oldManagedTags)
	person.Accounts = subtractAccounts(person.Accounts, oldManagedAccounts)
	if managed[person.Avatar.Source] {
		person.Avatar = model.AvatarRef{}
	}
	if managed["apple"] {
		person.Apple = model.ExternalRef{}
	}
	if managed["google"] {
		person.Google = model.ExternalRef{}
	}
	newNames := []string{}
	for _, row := range rows {
		contact := row.Contact
		newNames = append(newNames, contact.Name)
		person.Tags = appendMissingStrings(person.Tags, contact.Tags)
		person.Emails = appendMissingValues(person.Emails, contact.Emails, row.Source, model.NormalizeEmail)
		person.Phones = appendMissingValues(person.Phones, contact.Phones, row.Source, model.NormalizePhone)
		person.Addresses = appendMissingValues(person.Addresses, contact.Addresses, row.Source, model.NormalizeAddress)
		person.Accounts = mergeAccounts(person.Accounts, contact.Accounts)
		person.Sources = mergePersonSource(person.Sources, row)
		setExternal(&person, row.Source, contact, row.SyncedAt)
		setImportedAvatar(&person, contact.Avatar, row.Source, row.SyncedAt)
	}
	if stringIn(person.Name, oldManagedNames) || strings.TrimSpace(person.Name) == "" {
		if len(newNames) > 0 {
			person.Name = newNames[0]
		}
	}
	person.UpdatedAt = now.UTC()
	if len(rows) == 0 && personHasNoIndependentContent(ctx, s, person) {
		_, err := s.store.DB().ExecContext(ctx, `delete from people where id = ?`, person.ID)
		return err
	}
	return s.SavePerson(ctx, person)
}

func cleanSourceContact(source string, contact model.SourceContact) model.SourceContact {
	contact.Source = source
	contact.ExternalID = strings.TrimSpace(contact.ExternalID)
	contact.Name = strings.Join(strings.Fields(contact.Name), " ")
	contact.Tags = cleanStrings(contact.Tags)
	contact.Emails = sourceValues(contact.Emails, source, model.NormalizeEmail)
	contact.Phones = sourceValues(contact.Phones, source, model.NormalizePhone)
	contact.Addresses = sourceValues(contact.Addresses, source, model.NormalizeAddress)
	contact.Accounts = cleanAccounts(contact.Accounts)
	return contact
}

func sourceContactID(contact model.SourceContact) string {
	if contact.ExternalID != "" {
		return contact.ExternalID
	}
	parts := []string{}
	for _, value := range contact.Emails {
		parts = append(parts, "email:"+model.NormalizeEmail(value.Value))
	}
	for _, value := range contact.Phones {
		parts = append(parts, "phone:"+model.NormalizePhone(value.Value))
	}
	for service, values := range contact.Accounts {
		for _, value := range values {
			parts = append(parts, service+":"+strings.ToLower(value))
		}
	}
	if len(parts) == 0 {
		parts = append(parts, "name:"+model.NormalizeName(contact.Name))
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "derived-" + hex.EncodeToString(sum[:8])
}

func personFromSourceContact(contact model.SourceContact, now time.Time) model.Person {
	person := model.NewPerson(contact.Name, now)
	return addSourceContactProjection(person, contact, now)
}

func addSourceContactProjection(person model.Person, contact model.SourceContact, now time.Time) model.Person {
	person.Tags = appendMissingStrings(person.Tags, contact.Tags)
	person.Emails = appendMissingValues(person.Emails, contact.Emails, contact.Source, model.NormalizeEmail)
	person.Phones = appendMissingValues(person.Phones, contact.Phones, contact.Source, model.NormalizePhone)
	person.Addresses = appendMissingValues(person.Addresses, contact.Addresses, contact.Source, model.NormalizeAddress)
	person.Accounts = mergeAccounts(person.Accounts, contact.Accounts)
	setExternal(&person, contact.Source, contact, now)
	return person
}

func mergePersonSource(sources map[string]model.PersonSource, row sourceContactRow) map[string]model.PersonSource {
	if sources == nil {
		sources = map[string]model.PersonSource{}
	}
	current := sources[row.Source]
	current.Names = appendMissingStrings(current.Names, []string{row.Contact.Name})
	current.Tags = appendMissingStrings(current.Tags, row.Contact.Tags)
	for _, value := range row.Contact.Emails {
		current.Emails = appendMissingStrings(current.Emails, []string{value.Value})
	}
	for _, value := range row.Contact.Phones {
		current.Phones = appendMissingStrings(current.Phones, []string{value.Value})
	}
	for _, value := range row.Contact.Addresses {
		current.Addresses = appendMissingStrings(current.Addresses, []string{value.Value})
	}
	current.Accounts = mergeAccounts(current.Accounts, row.Contact.Accounts)
	if row.SyncedAt.After(current.LastSeenAt) {
		current.LastSeenAt = row.SyncedAt
	}
	sources[row.Source] = current
	return sources
}

func removeManagedValues(values []model.ContactValue, managed map[string]bool) []model.ContactValue {
	out := values[:0]
	for _, value := range values {
		if !managed[strings.ToLower(strings.TrimSpace(value.Source))] {
			out = append(out, value)
		}
	}
	return out
}

func subtractStrings(values, remove []string) []string {
	removed := map[string]bool{}
	for _, value := range remove {
		removed[strings.ToLower(strings.TrimSpace(value))] = true
	}
	out := values[:0]
	for _, value := range values {
		if !removed[strings.ToLower(strings.TrimSpace(value))] {
			out = append(out, value)
		}
	}
	return out
}

func subtractAccounts(accounts, remove map[string][]string) map[string][]string {
	out := map[string][]string{}
	for service, values := range accounts {
		out[service] = subtractStrings(values, remove[service])
		if len(out[service]) == 0 {
			delete(out, service)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func stringIn(value string, values []string) bool {
	key := model.NormalizeName(value)
	for _, candidate := range values {
		if key != "" && key == model.NormalizeName(candidate) {
			return true
		}
	}
	return false
}

func sameSourceContact(a, b model.SourceContact) bool {
	a.Source, b.Source = "", ""
	rawA, _ := json.Marshal(a)
	rawB, _ := json.Marshal(b)
	return string(rawA) == string(rawB)
}

func personHasNoIndependentContent(ctx context.Context, s *Store, person model.Person) bool {
	if person.Annotation != "" || person.Body != "" || len(person.Sources) > 0 || len(person.Emails) > 0 || len(person.Phones) > 0 || len(person.Addresses) > 0 || len(person.Accounts) > 0 || len(person.Tags) > 0 {
		return false
	}
	var count int
	if err := s.store.DB().QueryRowContext(ctx, `select count(*) from notes where person_id = ?`, person.ID).Scan(&count); err != nil {
		return false
	}
	return count == 0
}

func errorsIsPersonMissing(err error) bool {
	return errors.Is(err, ErrPersonNotFound)
}
