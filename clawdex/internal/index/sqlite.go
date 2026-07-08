package index

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/openclaw/clawdex/internal/model"
	"github.com/openclaw/crawlkit/render"

	// C SQLite via cgo, matching crawlkit/store after the modernc production
	// incidents. Requires -tags sqlite_fts5; the monorepo devenv sets GOFLAGS.
	_ "github.com/mattn/go-sqlite3"
)

const (
	indexDBName        = "index.db"
	indexSchemaVersion = "3"
)

type IndexStatus struct {
	People int
}

type markdownFingerprint struct {
	Count       int
	NewestMTime int64
	FrontHash   string
}

type identifierKey struct {
	kind  string
	value string
}

func (s Store) IndexStatus() (IndexStatus, error) {
	people, _, err := s.ensureIndex()
	return IndexStatus{People: people}, err
}

func (s Store) ensureIndex() (int, bool, error) {
	fp, err := s.markdownFingerprint()
	if err != nil {
		return 0, false, err
	}
	matches, err := s.indexMatches(fp)
	if err != nil {
		return 0, false, err
	}
	if matches {
		return fp.Count, false, nil
	}
	people, err := s.readPeople()
	if err != nil {
		return 0, false, err
	}
	count, err := s.rebuildIndex(people, fp)
	if err != nil {
		return 0, false, err
	}
	if s.Log != nil {
		_, _ = fmt.Fprintf(s.Log, "index rebuilt: %s people\n", render.FormatInteger(int64(count)))
	}
	return count, true, nil
}

func (s Store) indexPath() string {
	return filepath.Join(s.Repo.IndexDir(), indexDBName)
}

func (s Store) indexMatches(fp markdownFingerprint) (bool, error) {
	db, err := sql.Open("sqlite3", s.indexPath())
	if err != nil {
		return false, nil
	}
	defer func() { _ = db.Close() }()
	rows, err := db.Query(`select key, value from meta where key in ('schema_version', 'count', 'newest_mtime', 'frontmatter_hash')`)
	if err != nil {
		return false, nil
	}
	defer func() { _ = rows.Close() }()
	values := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return false, nil
		}
		values[key] = value
	}
	if err := rows.Err(); err != nil {
		return false, nil
	}
	return values["schema_version"] == indexSchemaVersion &&
		values["count"] == strconv.Itoa(fp.Count) &&
		values["newest_mtime"] == strconv.FormatInt(fp.NewestMTime, 10) &&
		values["frontmatter_hash"] == fp.FrontHash, nil
}

func (s Store) rebuildIndex(people []model.Person, fp markdownFingerprint) (int, error) {
	if err := os.MkdirAll(s.Repo.IndexDir(), 0o755); err != nil {
		return 0, err
	}
	tmp, err := os.CreateTemp(s.Repo.IndexDir(), "."+indexDBName+".tmp-*")
	if err != nil {
		return 0, err
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return 0, err
	}
	defer func() { _ = os.Remove(tmpPath) }()
	db, err := sql.Open("sqlite3", tmpPath)
	if err != nil {
		return 0, err
	}
	if err := createIndexSchema(db); err != nil {
		_ = db.Close()
		return 0, err
	}
	tx, err := db.Begin()
	if err != nil {
		_ = db.Close()
		return 0, err
	}
	for _, p := range people {
		if err := insertIndexedPerson(tx, p); err != nil {
			_ = tx.Rollback()
			_ = db.Close()
			return 0, err
		}
	}
	for key, value := range map[string]string{
		"schema_version":   indexSchemaVersion,
		"count":            strconv.Itoa(fp.Count),
		"newest_mtime":     strconv.FormatInt(fp.NewestMTime, 10),
		"frontmatter_hash": fp.FrontHash,
	} {
		if _, err := tx.Exec(`insert into meta(key, value) values (?, ?)`, key, value); err != nil {
			_ = tx.Rollback()
			_ = db.Close()
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		_ = db.Close()
		return 0, err
	}
	if err := db.Close(); err != nil {
		return 0, err
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return 0, err
	}
	if err := os.Rename(tmpPath, s.indexPath()); err != nil {
		return 0, err
	}
	if err := removeLegacyJSONIndexes(s.Repo.IndexDir()); err != nil {
		return 0, err
	}
	return len(people), nil
}

func createIndexSchema(db *sql.DB) error {
	statements := []string{
		`pragma foreign_keys = on`,
		`create table meta(key text primary key, value text not null)`,
		`create table people(id text primary key, name text not null)`,
		`create table identifiers(
			person_id text not null references people(id) on delete cascade,
			kind text not null check(kind in ('phone', 'email', 'handle')),
			value text not null,
			primary key(kind, value, person_id)
		)`,
		`create index identifiers_person on identifiers(person_id)`,
		`create virtual table person_fts using fts5(person_id unindexed, names, aliases, handles, tokenize='unicode61')`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

func insertIndexedPerson(tx *sql.Tx, p model.Person) error {
	if _, err := tx.Exec(`insert into people(id, name) values (?, ?)`, p.ID, p.Name); err != nil {
		return err
	}
	identifiers := personIdentifierKeys(p)
	for _, key := range identifiers {
		if _, err := tx.Exec(`insert or ignore into identifiers(person_id, kind, value) values (?, ?, ?)`, p.ID, key.kind, key.value); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`insert into person_fts(person_id, names, aliases, handles) values (?, ?, ?, ?)`,
		p.ID,
		strings.Join(indexNames(p), " "),
		strings.Join(indexAliases(p), " "),
		strings.Join(personHandleIdentifiers(identifiers), " "),
	); err != nil {
		return err
	}
	return nil
}

func (s Store) personIDsForQuery(query string) ([]string, error) {
	if _, _, err := s.ensureIndex(); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", s.indexPath())
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	var ids []string
	if id, ok, err := personIDByExactID(db, query); err != nil {
		return nil, err
	} else if ok {
		return []string{id}, nil
	}
	if id, ok, err := personIDByIdentifier(db, "email", model.NormalizeEmail(query)); err != nil {
		return nil, err
	} else if ok {
		return []string{id}, nil
	}
	if phone := model.NormalizePhone(query); phone != "" {
		if id, ok, err := personIDByIdentifier(db, "phone", phone); err != nil {
			return nil, err
		} else if ok {
			return []string{id}, nil
		}
	}
	if handle := normalizeHandleQuery(query); handle != "" {
		if id, ok, err := personIDByIdentifier(db, "handle", handle); err != nil {
			return nil, err
		} else if ok {
			return []string{id}, nil
		}
	}
	ids, err = personIDsBySlugOrName(db, query)
	if err != nil {
		return nil, err
	}
	if len(ids) > 0 {
		return ids, nil
	}
	return personIDsByFTS(db, query)
}

func personIDByExactID(db *sql.DB, query string) (string, bool, error) {
	var id string
	err := db.QueryRow(`select id from people where id = ?`, strings.TrimSpace(query)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return id, true, nil
}

func personIDByIdentifier(db *sql.DB, kind, value string) (string, bool, error) {
	if value == "" {
		return "", false, nil
	}
	rows, err := db.Query(`select person_id from identifiers where kind = ? and value = ? order by person_id`, kind, value)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", false, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return "", false, err
	}
	if len(ids) != 1 {
		return "", false, nil
	}
	return ids[0], true, nil
}

func personIDsBySlugOrName(db *sql.DB, query string) ([]string, error) {
	rows, err := db.Query(`select id, name from people order by name, id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	nq := model.NormalizeName(query)
	slug := model.Slug(query)
	var ids []string
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		switch {
		case model.Slug(name) == slug:
			ids = append(ids, id)
		case nq != "" && strings.Contains(model.NormalizeName(name), nq):
			ids = append(ids, id)
		}
	}
	return ids, rows.Err()
}

func personIDsByFTS(db *sql.DB, query string) ([]string, error) {
	match := ftsPrefixQuery(query)
	if match == "" {
		return nil, nil
	}
	rows, err := db.Query(`select person_id from person_fts where person_fts match ? order by bm25(person_fts), person_id`, match)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanPersonIDs(rows)
}

func (s Store) searchPersonIndex(query string) ([]model.SearchHit, error) {
	match := ftsPrefixQuery(query)
	if match == "" {
		return nil, nil
	}
	db, err := sql.Open("sqlite3", s.indexPath())
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	rows, err := db.Query(`
		select p.id, p.name
		from person_fts
		join people p on p.id = person_fts.person_id
		where person_fts match ?
		order by bm25(person_fts), p.name, p.id
	`, match)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	people, err := s.readPeople()
	if err != nil {
		return nil, err
	}
	byID := peopleByID(people)
	var hits []model.SearchHit
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		if p, ok := byID[id]; ok {
			hits = append(hits, model.SearchHit{Kind: "person", ID: id, Name: name, Path: p.Path, Score: 100, Snippet: personSnippet(p, query)})
		}
	}
	return hits, rows.Err()
}

func (s Store) indexedIdentifiers() (map[identifierKey][]string, error) {
	if _, _, err := s.ensureIndex(); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", s.indexPath())
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	rows, err := db.Query(`select kind, value, person_id from identifiers order by kind, value, person_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[identifierKey][]string{}
	for rows.Next() {
		var kind, value, personID string
		if err := rows.Scan(&kind, &value, &personID); err != nil {
			return nil, err
		}
		key := identifierKey{kind: kind, value: value}
		out[key] = append(out[key], personID)
	}
	return out, rows.Err()
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

func (s Store) markdownFingerprint() (markdownFingerprint, error) {
	if err := s.Repo.Require(); err != nil {
		return markdownFingerprint{}, err
	}
	files, err := personMarkdownFiles(s.Repo.PeopleDir())
	if err != nil {
		return markdownFingerprint{}, err
	}
	hash := sha256.New()
	var newest int64
	for _, path := range files {
		info, err := os.Stat(path)
		if err != nil {
			return markdownFingerprint{}, err
		}
		if mod := info.ModTime().UnixNano(); mod > newest {
			newest = mod
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return markdownFingerprint{}, err
		}
		rel, err := filepath.Rel(s.Repo.PeopleDir(), path)
		if err != nil {
			return markdownFingerprint{}, err
		}
		hash.Write([]byte(filepath.ToSlash(rel)))
		hash.Write([]byte{0})
		hash.Write(frontmatterBytes(data))
		hash.Write([]byte{0})
	}
	return markdownFingerprint{Count: len(files), NewestMTime: newest, FrontHash: hex.EncodeToString(hash.Sum(nil))}, nil
}

func personMarkdownFiles(peopleDir string) ([]string, error) {
	entries, err := os.ReadDir(peopleDir)
	if err != nil {
		return nil, err
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(peopleDir, entry.Name(), "person.md")
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return nil, err
		}
		files = append(files, path)
	}
	sort.Strings(files)
	return files, nil
}

func frontmatterBytes(data []byte) []byte {
	text := string(data)
	if !strings.HasPrefix(text, "---\n") && !strings.HasPrefix(text, "---\r\n") {
		return nil
	}
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	rest := normalized[4:]
	front, _, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		front, ok = strings.CutSuffix(rest, "\n---")
		if !ok {
			return nil
		}
	}
	return []byte(strings.TrimSpace(front))
}

func indexNames(p model.Person) []string {
	return cleanIndexStrings([]string{p.Name, p.SortName, personPathSlug(p)})
}

func indexAliases(p model.Person) []string {
	var values []string
	values = append(values, p.AKA...)
	for _, source := range p.Sources {
		values = append(values, source.Names...)
	}
	values = append(values, p.Tags...)
	return cleanIndexStrings(values)
}

func personIdentifierKeys(p model.Person) []identifierKey {
	var keys []identifierKey
	add := func(kind, value string) {
		value = strings.TrimSpace(strings.ToLower(value))
		if value != "" {
			keys = append(keys, identifierKey{kind: kind, value: value})
		}
	}
	addEmail := func(value string) {
		add("email", model.NormalizeEmail(value))
	}
	addPhone := func(value string) {
		add("phone", model.NormalizePhone(value))
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
	addExternal := func(service string, ref model.ExternalRef) {
		service = strings.TrimSpace(strings.ToLower(service))
		if service == "" {
			return
		}
		if ref.ID != "" {
			add("handle", service+":"+ref.ID)
		}
		if ref.Resource != "" {
			add("handle", service+":"+ref.Resource)
		}
	}

	for _, email := range p.Emails {
		addEmail(email.Value)
	}
	for _, phone := range p.Phones {
		addPhone(phone.Value)
	}
	addAccounts(p.Accounts)
	for _, source := range p.Sources {
		for _, email := range source.Emails {
			addEmail(email)
		}
		for _, phone := range source.Phones {
			addPhone(phone)
		}
		addAccounts(source.Accounts)
	}
	addExternal("apple", p.Apple)
	addExternal("google", p.Google)
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

func personHandleIdentifiers(keys []identifierKey) []string {
	handles := make([]string, 0, len(keys))
	for _, key := range keys {
		if key.kind == "handle" {
			handles = append(handles, key.value)
		}
	}
	return handles
}

func personPathSlug(p model.Person) string {
	if strings.TrimSpace(p.Path) == "" {
		return ""
	}
	return model.PathSlug(p.Path)
}

func cleanIndexStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return compactSorted(out)
}

func compactSorted(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := values[:0]
	var last string
	for _, value := range values {
		if value == last {
			continue
		}
		out = append(out, value)
		last = value
	}
	return out
}

func normalizeHandleQuery(query string) string {
	query = strings.TrimSpace(strings.ToLower(query))
	if strings.Contains(query, ":") {
		return query
	}
	return ""
}

func removeLegacyJSONIndexes(indexDir string) error {
	for _, name := range []string{"emails.json", "phones.json", "handles.json"} {
		if err := os.Remove(filepath.Join(indexDir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}
