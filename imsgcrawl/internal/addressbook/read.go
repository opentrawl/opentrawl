package addressbook

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/openclaw/crawlkit/store"
)

const (
	KindEmail = "email"
	KindPhone = "phone"
)

type ContactName struct {
	Kind        string
	Handle      string
	ContactKey  string
	DisplayName string
}

type Lookup map[string]ContactName

func Extract(ctx context.Context, paths []string) ([]ContactName, error) {
	byHandle := map[string]ContactName{}
	for sourceIndex, path := range dedupePaths(paths) {
		names, err := extractStore(ctx, path, sourceIndex)
		if err != nil {
			return nil, fmt.Errorf("read Contacts store: %w", err)
		}
		for _, name := range names {
			upsertName(byHandle, name)
		}
	}
	return sortedNames(byHandle), nil
}

func ExtractDefault(ctx context.Context) ([]ContactName, error) {
	return Extract(ctx, DefaultStorePaths())
}

func NewLookup(names []ContactName) Lookup {
	out := make(Lookup, len(names))
	for _, name := range names {
		if name.Kind == "" || name.Handle == "" || name.DisplayName == "" {
			continue
		}
		out[mappingKey(name.Kind, name.Handle)] = name
	}
	return out
}

func (l Lookup) Match(raw string) (ContactName, bool) {
	kind, handle, ok := NormalizeHandle(raw)
	if !ok {
		return ContactName{}, false
	}
	name, ok := l[mappingKey(kind, handle)]
	return name, ok
}

func NormalizeHandle(raw string) (kind, handle string, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", false
	}
	if strings.Contains(raw, "@") {
		handle = strings.ToLower(raw)
		if handle == "" {
			return "", "", false
		}
		return KindEmail, handle, true
	}
	if !LooksPhoneLike(raw) {
		return "", "", false
	}
	handle = NormalizePhone(raw)
	if handle == "" {
		return "", "", false
	}
	return KindPhone, handle, true
}

func NormalizePhone(phone string) string {
	var b strings.Builder
	for _, r := range phone {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return strings.TrimPrefix(b.String(), "00")
}

func LooksPhoneLike(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	hasDigit := false
	for _, r := range value {
		switch {
		case r >= '0' && r <= '9':
			hasDigit = true
		case r == '+', r == ' ', r == '\t', r == '(', r == ')', r == '-', r == '.':
			continue
		default:
			return false
		}
	}
	return hasDigit
}

func extractStore(ctx context.Context, path string, sourceIndex int) ([]ContactName, error) {
	if path == "" {
		return nil, nil
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	snap, err := SnapshotPath(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = snap.Close() }()
	st, err := store.OpenReadOnly(ctx, snap.Path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = st.Close() }()
	if err := requireSchema(ctx, st.DB()); err != nil {
		return nil, err
	}
	names, err := phoneNames(ctx, st.DB(), sourceIndex)
	if err != nil {
		return nil, err
	}
	emailNames, err := emailNames(ctx, st.DB(), sourceIndex)
	if err != nil {
		return nil, err
	}
	names = append(names, emailNames...)
	return names, nil
}

func requireSchema(ctx context.Context, db *sql.DB) error {
	required := map[string][]string{
		"ZABCDRECORD":       {"Z_PK", "ZFIRSTNAME", "ZLASTNAME", "ZORGANIZATION"},
		"ZABCDPHONENUMBER":  {"ZFULLNUMBER", "ZCOUNTRYCODE", "ZAREACODE", "ZLOCALNUMBER", "ZOWNER"},
		"ZABCDEMAILADDRESS": {"ZADDRESS", "ZOWNER"},
	}
	for table, columns := range required {
		if err := requireColumns(ctx, db, table, columns); err != nil {
			return err
		}
	}
	return nil
}

func requireColumns(ctx context.Context, db *sql.DB, table string, columns []string) error {
	var name string
	if err := db.QueryRowContext(ctx, tableExistsSQL, table).Scan(&name); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("Contacts database is missing table %s", table)
		}
		return err
	}
	rows, err := db.QueryContext(ctx, `pragma table_info(`+table+`)`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	found := map[string]struct{}{}
	for rows.Next() {
		var cid int
		var columnName, columnType string
		var notNull, pk int
		var defaultValue any
		if err := rows.Scan(&cid, &columnName, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		found[columnName] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, column := range columns {
		if _, ok := found[column]; !ok {
			return fmt.Errorf("Contacts table %s is missing column %s", table, column)
		}
	}
	return nil
}

func phoneNames(ctx context.Context, db *sql.DB, sourceIndex int) ([]ContactName, error) {
	rows, err := db.QueryContext(ctx, phoneNumbersSQL)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ContactName
	for rows.Next() {
		var full, country, area, local, first, last, organization string
		var owner int64
		if err := rows.Scan(&full, &country, &area, &local, &owner, &first, &last, &organization); err != nil {
			return nil, err
		}
		handle := phoneHandle(full, country, area, local)
		name := displayName(first, last, organization)
		if handle == "" || name == "" {
			continue
		}
		out = append(out, ContactName{Kind: KindPhone, Handle: handle, ContactKey: contactKey(sourceIndex, owner), DisplayName: name})
	}
	return out, rows.Err()
}

func emailNames(ctx context.Context, db *sql.DB, sourceIndex int) ([]ContactName, error) {
	rows, err := db.QueryContext(ctx, emailAddressesSQL)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ContactName
	for rows.Next() {
		var address, first, last, organization string
		var owner int64
		if err := rows.Scan(&address, &owner, &first, &last, &organization); err != nil {
			return nil, err
		}
		_, handle, ok := NormalizeHandle(address)
		name := displayName(first, last, organization)
		if !ok || name == "" {
			continue
		}
		out = append(out, ContactName{Kind: KindEmail, Handle: handle, ContactKey: contactKey(sourceIndex, owner), DisplayName: name})
	}
	return out, rows.Err()
}

func contactKey(sourceIndex int, owner int64) string {
	if owner <= 0 {
		return ""
	}
	return fmt.Sprintf("%d:%d", sourceIndex, owner)
}

func phoneHandle(full, country, area, local string) string {
	fullHandle := NormalizePhone(full)
	countryHandle := NormalizePhone(country)
	if countryHandle == "" {
		return fullHandle
	}
	if fullHandle != "" && strings.HasPrefix(fullHandle, countryHandle) {
		return fullHandle
	}
	localHandle := NormalizePhone(local)
	areaHandle := NormalizePhone(area)
	combined := countryHandle + areaHandle + localHandle
	if combined != countryHandle {
		return combined
	}
	if fullHandle != "" {
		return countryHandle + fullHandle
	}
	return ""
}

func displayName(first, last, organization string) string {
	parts := make([]string, 0, 2)
	if first = strings.TrimSpace(first); first != "" {
		parts = append(parts, first)
	}
	if last = strings.TrimSpace(last); last != "" {
		parts = append(parts, last)
	}
	if len(parts) > 0 {
		return strings.Join(parts, " ")
	}
	return strings.TrimSpace(organization)
}

func upsertName(names map[string]ContactName, name ContactName) {
	key := mappingKey(name.Kind, name.Handle)
	if key == "" || name.DisplayName == "" {
		return
	}
	current, ok := names[key]
	if !ok || preferDisplayName(name.DisplayName, current.DisplayName) {
		names[key] = name
	}
}

func preferDisplayName(candidate, current string) bool {
	candidate = strings.TrimSpace(candidate)
	current = strings.TrimSpace(current)
	if current == "" {
		return candidate != ""
	}
	candidateWords := len(strings.Fields(candidate))
	currentWords := len(strings.Fields(current))
	if candidateWords != currentWords {
		return candidateWords > currentWords
	}
	candidateRunes := len([]rune(candidate))
	currentRunes := len([]rune(current))
	if candidateRunes != currentRunes {
		return candidateRunes > currentRunes
	}
	return strings.ToLower(candidate) < strings.ToLower(current)
}

func sortedNames(names map[string]ContactName) []ContactName {
	out := make([]ContactName, 0, len(names))
	for _, name := range names {
		out = append(out, name)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Handle < out[j].Handle
	})
	return out
}

func mappingKey(kind, handle string) string {
	kind = strings.TrimSpace(kind)
	handle = strings.TrimSpace(handle)
	if kind == "" || handle == "" {
		return ""
	}
	return kind + ":" + handle
}
