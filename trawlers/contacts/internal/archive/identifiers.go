package archive

import (
	"context"
	"database/sql"
	"sort"
	"strings"
	"unicode"

	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
)

type identifierKey struct {
	kind  string
	value string
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
