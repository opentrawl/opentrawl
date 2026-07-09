package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
)

var ErrPersonNotFound = errors.New("person not found")

func (s *Store) People(ctx context.Context) ([]model.Person, error) {
	rows, err := s.store.DB().QueryContext(ctx, `
select id, name, sort_name, aka_json, tags_json, avatar_json, accounts_json,
       sources_json, apple_json, google_json, body, annotation,
       annotation_stated_at, created_at, updated_at
from people
order by lower(name), id`)
	if err != nil {
		return nil, err
	}
	people := []model.Person{}
	for rows.Next() {
		person, err := scanPerson(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		people = append(people, person)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range people {
		if err := s.loadContactValues(ctx, &people[i]); err != nil {
			return nil, err
		}
		if err := s.loadAvatar(ctx, &people[i]); err != nil {
			return nil, err
		}
	}
	return people, nil
}

func (s *Store) Person(ctx context.Context, id string) (model.Person, error) {
	row := s.store.DB().QueryRowContext(ctx, `
select id, name, sort_name, aka_json, tags_json, avatar_json, accounts_json,
       sources_json, apple_json, google_json, body, annotation,
       annotation_stated_at, created_at, updated_at
from people
where id = ?`, strings.TrimSpace(id))
	person, err := scanPerson(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Person{}, fmt.Errorf("%w: %s", ErrPersonNotFound, strings.TrimSpace(id))
	}
	if err != nil {
		return model.Person{}, err
	}
	if err := s.loadContactValues(ctx, &person); err != nil {
		return model.Person{}, err
	}
	if err := s.loadAvatar(ctx, &person); err != nil {
		return model.Person{}, err
	}
	return person, nil
}

func (s *Store) FindPerson(ctx context.Context, query string) (model.Person, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return model.Person{}, errors.New("person query is required")
	}
	ids, err := s.personIDsForQuery(ctx, query)
	if err != nil {
		return model.Person{}, err
	}
	if len(ids) == 0 {
		return model.Person{}, fmt.Errorf("no person matched %q", query)
	}
	if len(ids) > 1 {
		people, err := s.peopleByID(ctx, ids)
		if err != nil {
			return model.Person{}, err
		}
		names := make([]string, 0, len(people))
		for _, person := range people {
			names = append(names, person.Name+" ("+person.ID+")")
		}
		return model.Person{}, fmt.Errorf("ambiguous person %q: %s", query, strings.Join(names, ", "))
	}
	return s.Person(ctx, ids[0])
}

func (s *Store) UpsertPerson(ctx context.Context, person model.Person) (string, error) {
	person = canonicalPerson(person)
	existing, err := s.Person(ctx, person.ID)
	if errors.Is(err, ErrPersonNotFound) {
		if err := s.savePerson(ctx, person); err != nil {
			return "", err
		}
		return "created", nil
	}
	if err != nil {
		return "", err
	}
	if equalPerson(existing, person) {
		return "unchanged", nil
	}
	if err := s.savePerson(ctx, person); err != nil {
		return "", err
	}
	return "updated", nil
}

func (s *Store) SavePerson(ctx context.Context, person model.Person) error {
	return s.savePerson(ctx, canonicalPerson(person))
}

func (s *Store) savePerson(ctx context.Context, person model.Person) error {
	if strings.TrimSpace(person.ID) == "" {
		return errors.New("person id is required")
	}
	if strings.TrimSpace(person.Name) == "" {
		return errors.New("person name is required")
	}
	return s.store.WithTx(ctx, func(tx *sql.Tx) error {
		if err := upsertPersonRow(ctx, tx, person); err != nil {
			return err
		}
		if err := replaceContactValues(ctx, tx, person); err != nil {
			return err
		}
		if err := replaceIdentifiers(ctx, tx, person); err != nil {
			return err
		}
		if err := replaceAvatar(ctx, tx, person); err != nil {
			return err
		}
		return replacePersonFTS(ctx, tx, person)
	})
}

func scanPerson(row interface{ Scan(dest ...any) error }) (model.Person, error) {
	var person model.Person
	var akaJSON, tagsJSON, avatarJSON, accountsJSON, sourcesJSON, appleJSON, googleJSON string
	var createdAt, updatedAt string
	if err := row.Scan(&person.ID, &person.Name, &person.SortName, &akaJSON, &tagsJSON, &avatarJSON,
		&accountsJSON, &sourcesJSON, &appleJSON, &googleJSON, &person.Body, &person.Annotation,
		&person.AnnotationStatedAt, &createdAt, &updatedAt); err != nil {
		return model.Person{}, err
	}
	if err := decodeJSONList(akaJSON, &person.AKA); err != nil {
		return model.Person{}, err
	}
	if err := decodeJSONList(tagsJSON, &person.Tags); err != nil {
		return model.Person{}, err
	}
	if err := decodeJSON(avatarJSON, &person.Avatar); err != nil {
		return model.Person{}, err
	}
	if err := decodeJSON(accountsJSON, &person.Accounts); err != nil {
		return model.Person{}, err
	}
	if err := decodeJSON(sourcesJSON, &person.Sources); err != nil {
		return model.Person{}, err
	}
	if err := decodeJSON(appleJSON, &person.Apple); err != nil {
		return model.Person{}, err
	}
	if err := decodeJSON(googleJSON, &person.Google); err != nil {
		return model.Person{}, err
	}
	person.CreatedAt = parseTime(createdAt)
	person.UpdatedAt = parseTime(updatedAt)
	return person, nil
}

func (s *Store) loadContactValues(ctx context.Context, person *model.Person) error {
	rows, err := s.store.DB().QueryContext(ctx, `
select kind, value, label, source, primary_value
from contact_values
where person_id = ?
order by kind, position`, person.ID)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var kind string
		var value model.ContactValue
		var primary int
		if err := rows.Scan(&kind, &value.Value, &value.Label, &value.Source, &primary); err != nil {
			return err
		}
		value.Primary = primary != 0
		switch kind {
		case "email":
			person.Emails = append(person.Emails, value)
		case "phone":
			person.Phones = append(person.Phones, value)
		case "address":
			person.Addresses = append(person.Addresses, value)
		}
	}
	return rows.Err()
}

func (s *Store) loadAvatar(ctx context.Context, person *model.Person) error {
	row := s.store.DB().QueryRowContext(ctx, `
select data, mime, sha256, source, updated_at
from person_avatars
where person_id = ?`, person.ID)
	var updatedAt string
	err := row.Scan(&person.Avatar.Data, &person.Avatar.MIME, &person.Avatar.SHA256, &person.Avatar.Source, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	person.Avatar.UpdatedAt = parseTime(updatedAt)
	return nil
}

func upsertPersonRow(ctx context.Context, tx *sql.Tx, person model.Person) error {
	_, err := tx.ExecContext(ctx, `
insert into people(
  id, name, sort_name, aka_json, tags_json, avatar_json, accounts_json,
  sources_json, apple_json, google_json, body, annotation, annotation_stated_at,
  created_at, updated_at
) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
on conflict(id) do update set
  name = excluded.name,
  sort_name = excluded.sort_name,
  aka_json = excluded.aka_json,
  tags_json = excluded.tags_json,
  avatar_json = excluded.avatar_json,
  accounts_json = excluded.accounts_json,
  sources_json = excluded.sources_json,
  apple_json = excluded.apple_json,
  google_json = excluded.google_json,
  body = excluded.body,
  annotation = excluded.annotation,
  annotation_stated_at = excluded.annotation_stated_at,
  created_at = excluded.created_at,
  updated_at = excluded.updated_at`,
		person.ID, person.Name, person.SortName, mustJSONList(person.AKA), mustJSONList(person.Tags),
		mustJSON(avatarMetadata(person.Avatar)), mustJSON(person.Accounts), mustJSON(person.Sources),
		mustJSON(person.Apple), mustJSON(person.Google), person.Body, person.Annotation,
		person.AnnotationStatedAt, timeText(person.CreatedAt), timeText(person.UpdatedAt))
	if err != nil {
		return fmt.Errorf("upsert person: %w", err)
	}
	return nil
}

func replaceAvatar(ctx context.Context, tx *sql.Tx, person model.Person) error {
	if _, err := tx.ExecContext(ctx, `delete from person_avatars where person_id = ?`, person.ID); err != nil {
		return err
	}
	if len(person.Avatar.Data) == 0 {
		return nil
	}
	_, err := tx.ExecContext(ctx, `
insert into person_avatars(person_id, data, mime, sha256, source, updated_at)
values (?, ?, ?, ?, ?, ?)`,
		person.ID, person.Avatar.Data, person.Avatar.MIME, person.Avatar.SHA256,
		person.Avatar.Source, timeText(person.Avatar.UpdatedAt))
	return err
}

func avatarMetadata(avatar model.AvatarRef) model.AvatarRef {
	avatar.Data = nil
	return avatar
}

func replaceContactValues(ctx context.Context, tx *sql.Tx, person model.Person) error {
	if _, err := tx.ExecContext(ctx, `delete from contact_values where person_id = ?`, person.ID); err != nil {
		return err
	}
	for kind, values := range map[string][]model.ContactValue{
		"email":   person.Emails,
		"phone":   person.Phones,
		"address": person.Addresses,
	} {
		for i, value := range values {
			if strings.TrimSpace(value.Value) == "" {
				continue
			}
			if _, err := tx.ExecContext(ctx, `
insert into contact_values(person_id, kind, position, value, label, source, primary_value)
values (?, ?, ?, ?, ?, ?, ?)`,
				person.ID, kind, i, value.Value, value.Label, value.Source, boolInt(value.Primary)); err != nil {
				return fmt.Errorf("insert contact value: %w", err)
			}
		}
	}
	return nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func canonicalPerson(person model.Person) model.Person {
	person.ID = strings.TrimSpace(person.ID)
	person.Name = strings.Join(strings.Fields(person.Name), " ")
	person.SortName = strings.TrimSpace(person.SortName)
	person.Path = ""
	person.Extra = nil
	if person.CreatedAt.IsZero() {
		person.CreatedAt = time.Now().UTC()
	}
	if person.UpdatedAt.IsZero() {
		person.UpdatedAt = person.CreatedAt
	}
	person.CreatedAt = person.CreatedAt.UTC()
	person.UpdatedAt = person.UpdatedAt.UTC()
	person.AnnotationStatedAt = strings.TrimSpace(person.AnnotationStatedAt)
	person.AKA = cleanStrings(person.AKA)
	person.Tags = cleanStrings(person.Tags)
	person.Emails = cleanContactValues(person.Emails)
	person.Phones = cleanContactValues(person.Phones)
	person.Addresses = cleanContactValues(person.Addresses)
	person.Accounts = cleanAccounts(person.Accounts)
	person.Sources = cleanSources(person.Sources)
	person.Avatar = cleanAvatar(person.Avatar)
	return person
}

func cleanAvatar(value model.AvatarRef) model.AvatarRef {
	if len(value.Data) == 0 {
		return model.AvatarRef{}
	}
	value.Source = strings.TrimSpace(value.Source)
	value.MIME = strings.TrimSpace(value.MIME)
	value.SHA256 = strings.TrimSpace(value.SHA256)
	if !value.UpdatedAt.IsZero() {
		value.UpdatedAt = value.UpdatedAt.UTC()
	}
	if len(value.Data) > 0 {
		value.Data = append([]byte(nil), value.Data...)
	}
	return value
}

func cleanContactValues(values []model.ContactValue) []model.ContactValue {
	out := make([]model.ContactValue, 0, len(values))
	for _, value := range values {
		value.Value = strings.TrimSpace(value.Value)
		value.Label = strings.TrimSpace(value.Label)
		value.Source = strings.TrimSpace(value.Source)
		if value.Value != "" {
			out = append(out, value)
		}
	}
	return out
}

func equalPerson(a, b model.Person) bool {
	a = canonicalPerson(a)
	b = canonicalPerson(b)
	return reflect.DeepEqual(a, b)
}

func (s *Store) peopleByID(ctx context.Context, ids []string) ([]model.Person, error) {
	people := make([]model.Person, 0, len(ids))
	for _, id := range ids {
		person, err := s.Person(ctx, id)
		if err != nil {
			return nil, err
		}
		people = append(people, person)
	}
	sort.Slice(people, func(i, j int) bool {
		return strings.ToLower(people[i].Name) < strings.ToLower(people[j].Name)
	})
	return people, nil
}
