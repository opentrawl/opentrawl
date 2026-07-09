package archive

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"unicode"

	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
)

type identifierKey struct {
	kind  string
	value string
}

func (s *Store) personIDsForQuery(ctx context.Context, query string) ([]string, error) {
	if id, ok, err := s.personIDByExactID(ctx, query); err != nil {
		return nil, err
	} else if ok {
		return []string{id}, nil
	}
	if id, ok, err := s.personIDByIdentifier(ctx, "email", model.NormalizeEmail(query)); err != nil {
		return nil, err
	} else if ok {
		return []string{id}, nil
	}
	if phone := model.NormalizePhone(query); phone != "" {
		if id, ok, err := s.personIDByIdentifier(ctx, "phone", phone); err != nil {
			return nil, err
		} else if ok {
			return []string{id}, nil
		}
	}
	if handle := normalizeHandleQuery(query); handle != "" {
		if id, ok, err := s.personIDByIdentifier(ctx, "handle", handle); err != nil {
			return nil, err
		} else if ok {
			return []string{id}, nil
		}
	}
	ids, err := s.personIDsBySlugOrName(ctx, query)
	if err != nil || len(ids) > 0 {
		return ids, err
	}
	return s.personIDsByFTS(ctx, query)
}

func (s *Store) personIDByExactID(ctx context.Context, query string) (string, bool, error) {
	var id string
	err := s.store.DB().QueryRowContext(ctx, `select id from people where id = ?`, strings.TrimSpace(query)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return id, true, nil
}

func (s *Store) personIDByIdentifier(ctx context.Context, kind, value string) (string, bool, error) {
	if value == "" {
		return "", false, nil
	}
	rows, err := s.store.DB().QueryContext(ctx, `select person_id from identifiers where kind = ? and value = ? order by person_id`, kind, value)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = rows.Close() }()
	ids, err := scanPersonIDs(rows)
	if err != nil || len(ids) != 1 {
		return "", false, err
	}
	return ids[0], true, nil
}

func (s *Store) personIDsBySlugOrName(ctx context.Context, query string) ([]string, error) {
	rows, err := s.store.DB().QueryContext(ctx, `select id, name, aka_json, sources_json from people order by name, id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	nq := model.NormalizeName(query)
	slug := model.Slug(query)
	var ids []string
	for rows.Next() {
		var id, name, akaJSON, sourcesJSON string
		if err := rows.Scan(&id, &name, &akaJSON, &sourcesJSON); err != nil {
			return nil, err
		}
		switch {
		case model.Slug(name) == slug:
			ids = append(ids, id)
		case nq != "" && strings.Contains(model.NormalizeName(name), nq):
			ids = append(ids, id)
		default:
			person := model.Person{Name: name}
			if err := decodeJSONList(akaJSON, &person.AKA); err != nil {
				return nil, err
			}
			if err := decodeJSON(sourcesJSON, &person.Sources); err != nil {
				return nil, err
			}
			if personAliasMatches(person, slug, nq) {
				ids = append(ids, id)
			}
		}
	}
	return ids, rows.Err()
}

func personAliasMatches(person model.Person, slug, normalizedQuery string) bool {
	for _, alias := range person.AKA {
		if model.Slug(alias) == slug {
			return true
		}
		if normalizedQuery != "" && strings.Contains(model.NormalizeName(alias), normalizedQuery) {
			return true
		}
	}
	for _, source := range person.Sources {
		for _, name := range source.Names {
			if model.Slug(name) == slug {
				return true
			}
			if normalizedQuery != "" && strings.Contains(model.NormalizeName(name), normalizedQuery) {
				return true
			}
		}
	}
	return false
}

func (s *Store) personIDsByFTS(ctx context.Context, query string) ([]string, error) {
	match := ftsPrefixQuery(query)
	if match == "" {
		return nil, nil
	}
	rows, err := s.store.DB().QueryContext(ctx, `
select person_id
from people_fts
where people_fts match ?
order by bm25(people_fts), person_id`, match)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanPersonIDs(rows)
}

func scanPersonIDs(rows *sql.Rows) ([]string, error) {
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func replaceIdentifiers(ctx context.Context, tx *sql.Tx, person model.Person) error {
	if _, err := tx.ExecContext(ctx, `delete from identifiers where person_id = ?`, person.ID); err != nil {
		return err
	}
	for _, key := range personIdentifierKeys(person) {
		if _, err := tx.ExecContext(ctx, `insert or ignore into identifiers(person_id, kind, value) values (?, ?, ?)`, person.ID, key.kind, key.value); err != nil {
			return err
		}
	}
	return nil
}

func replacePersonFTS(ctx context.Context, tx *sql.Tx, person model.Person) error {
	if _, err := tx.ExecContext(ctx, `delete from people_fts where person_id = ?`, person.ID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `
insert into people_fts(person_id, names, aliases, identifiers, body, tags)
values (?, ?, ?, ?, ?, ?)`,
		person.ID,
		strings.Join(indexNames(person), " "),
		strings.Join(indexAliases(person), " "),
		strings.Join(personIdentifierValues(personIdentifierKeys(person)), " "),
		person.Body,
		strings.Join(person.Tags, " "),
	)
	return err
}

func personIdentifierKeys(person model.Person) []identifierKey {
	var keys []identifierKey
	add := func(kind, value string) {
		value = strings.TrimSpace(strings.ToLower(value))
		if value != "" {
			keys = append(keys, identifierKey{kind: kind, value: value})
		}
	}
	for _, email := range person.Emails {
		add("email", model.NormalizeEmail(email.Value))
	}
	for _, phone := range person.Phones {
		add("phone", model.NormalizePhone(phone.Value))
	}
	addAccounts := func(accounts map[string][]string) {
		for service, values := range accounts {
			service = strings.TrimSpace(strings.ToLower(service))
			if service == "" {
				continue
			}
			for _, value := range values {
				value = strings.TrimSpace(strings.ToLower(value))
				if value != "" {
					add("handle", service+":"+value)
				}
			}
		}
	}
	addAccounts(person.Accounts)
	for _, source := range person.Sources {
		for _, email := range source.Emails {
			add("email", model.NormalizeEmail(email))
		}
		for _, phone := range source.Phones {
			add("phone", model.NormalizePhone(phone))
		}
		addAccounts(source.Accounts)
	}
	addExternal := func(service string, ref model.ExternalRef) {
		if ref.ID != "" {
			add("handle", service+":"+ref.ID)
		}
		if ref.Resource != "" {
			add("handle", service+":"+ref.Resource)
		}
	}
	addExternal("apple", person.Apple)
	addExternal("google", person.Google)
	return cleanIdentifierKeys(keys)
}

func cleanIdentifierKeys(keys []identifierKey) []identifierKey {
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].kind == keys[j].kind {
			return keys[i].value < keys[j].value
		}
		return keys[i].kind < keys[j].kind
	})
	out := keys[:0]
	var last identifierKey
	for _, key := range keys {
		if key == last {
			continue
		}
		out = append(out, key)
		last = key
	}
	return out
}

func personIdentifierValues(keys []identifierKey) []string {
	values := make([]string, 0, len(keys))
	for _, key := range keys {
		values = append(values, key.value)
	}
	return values
}

func normalizeHandleQuery(query string) string {
	query = strings.TrimSpace(strings.ToLower(query))
	if query == "" {
		return ""
	}
	if strings.Contains(query, ":") {
		return query
	}
	return ""
}

func ftsPrefixQuery(query string) string {
	var terms []string
	var b strings.Builder
	flush := func() {
		if b.Len() == 0 {
			return
		}
		terms = append(terms, b.String()+"*")
		b.Reset()
	}
	for _, r := range strings.ToLower(query) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return strings.Join(terms, " ")
}
