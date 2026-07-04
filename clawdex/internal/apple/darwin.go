//go:build darwin

package apple

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	// C SQLite via cgo, matching crawlkit/store. Requires -tags sqlite_fts5.
	_ "github.com/mattn/go-sqlite3"
)

const addressBookDBName = "AddressBook-v22.abcddb"

type addressBookAccessError struct {
	Path string
	Err  error
}

func (e addressBookAccessError) Error() string {
	return fmt.Sprintf("read Apple AddressBook database %s: %v. Grant Full Disk Access to clawdex, Codex or the terminal running it in System Settings > Privacy & Security > Full Disk Access, then retry", e.Path, e.Err)
}

func (e addressBookAccessError) Unwrap() error {
	return e.Err
}

type addressBookSchema struct {
	contactEntity int64
	recordColumns map[string]bool
	phoneColumns  map[string]bool
	emailColumns  map[string]bool
	postalColumns map[string]bool
}

type addressBookRecord struct {
	contact Contact
	phones  []labeledValue
	emails  []labeledValue
	postal  []PostalAddress
}

type labeledValue struct {
	value string
	label string
}

func ReadSystem(ctx context.Context) ([]Contact, error) {
	dir, err := addressBookDir()
	if err != nil {
		return nil, err
	}
	return readAddressBookDir(ctx, dir)
}

func addressBookDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Application Support", "AddressBook"), nil
}

func readAddressBookDir(ctx context.Context, dir string) ([]Contact, error) {
	paths, err := addressBookDatabasePaths(dir)
	if err != nil {
		return nil, err
	}
	contacts := make([]Contact, 0)
	contactIndex := map[string]int{}
	for _, path := range paths {
		dbContacts, err := readAddressBookDatabase(ctx, path)
		if err != nil {
			return nil, err
		}
		for _, contact := range dbContacts {
			if strings.TrimSpace(contact.Identifier) == "" {
				continue
			}
			if index, ok := contactIndex[contact.Identifier]; ok {
				contacts[index] = mergeContact(contacts[index], contact)
				continue
			}
			contactIndex[contact.Identifier] = len(contacts)
			contacts = append(contacts, contact)
		}
	}
	return contacts, nil
}

func addressBookDatabasePaths(dir string) ([]string, error) {
	if _, err := os.Stat(dir); err != nil {
		return nil, addressBookAccessError{Path: dir, Err: err}
	}

	var paths []string
	rootDB := filepath.Join(dir, addressBookDBName)
	if info, err := os.Stat(rootDB); err == nil && !info.IsDir() {
		paths = append(paths, rootDB)
	} else if err != nil && !os.IsNotExist(err) {
		return nil, addressBookAccessError{Path: rootDB, Err: err}
	}

	sourcesDir := filepath.Join(dir, "Sources")
	entries, err := os.ReadDir(sourcesDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, addressBookAccessError{Path: sourcesDir, Err: err}
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(sourcesDir, entry.Name(), addressBookDBName)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			paths = append(paths, path)
		} else if err != nil && !os.IsNotExist(err) {
			return nil, addressBookAccessError{Path: path, Err: err}
		}
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return nil, fmt.Errorf("read Apple AddressBook: no %s files found under %s", addressBookDBName, dir)
	}
	return paths, nil
}

func readAddressBookDatabase(ctx context.Context, path string) ([]Contact, error) {
	db, err := sql.Open("sqlite3", readOnlySQLiteURI(path))
	if err != nil {
		return nil, addressBookAccessError{Path: path, Err: err}
	}
	defer func() { _ = db.Close() }()
	if err := db.PingContext(ctx); err != nil {
		return nil, addressBookAccessError{Path: path, Err: err}
	}

	schema, err := inspectAddressBookSchema(ctx, db, path)
	if err != nil {
		return nil, err
	}
	records, order, err := readAddressBookRecords(ctx, db, schema)
	if err != nil {
		return nil, err
	}
	if err := readAddressBookPhones(ctx, db, schema, records); err != nil {
		return nil, err
	}
	if err := readAddressBookEmails(ctx, db, schema, records); err != nil {
		return nil, err
	}
	if err := readAddressBookPostalAddresses(ctx, db, schema, records); err != nil {
		return nil, err
	}

	contacts := make([]Contact, 0, len(records))
	for _, pk := range order {
		record := records[pk]
		if strings.TrimSpace(record.contact.Name()) == "" {
			continue
		}
		for _, email := range record.emails {
			record.contact.Emails = append(record.contact.Emails, email.value)
		}
		for _, phone := range record.phones {
			record.contact.Phones = append(record.contact.Phones, phone.value)
		}
		record.contact.Addresses = append(record.contact.Addresses, record.postal...)
		if len(record.contact.Emails) == 0 && len(record.contact.Phones) == 0 && len(record.contact.Addresses) == 0 {
			continue
		}
		contacts = append(contacts, record.contact)
	}
	return contacts, nil
}

func readOnlySQLiteURI(path string) string {
	uri := url.URL{Scheme: "file", Path: path}
	query := uri.Query()
	query.Set("mode", "ro")
	uri.RawQuery = query.Encode()
	return uri.String()
}

func inspectAddressBookSchema(ctx context.Context, db *sql.DB, path string) (addressBookSchema, error) {
	schema := addressBookSchema{}
	var err error
	schema.recordColumns, err = tableColumns(ctx, db, "ZABCDRECORD")
	if err != nil {
		return schema, err
	}
	schema.phoneColumns, err = tableColumns(ctx, db, "ZABCDPHONENUMBER")
	if err != nil {
		return schema, err
	}
	schema.emailColumns, err = tableColumns(ctx, db, "ZABCDEMAILADDRESS")
	if err != nil {
		return schema, err
	}
	schema.postalColumns, err = tableColumns(ctx, db, "ZABCDPOSTALADDRESS")
	if err != nil {
		return schema, err
	}
	primaryKeyColumns, err := tableColumns(ctx, db, "Z_PRIMARYKEY")
	if err != nil {
		return schema, err
	}
	for _, column := range []string{"Z_ENT", "Z_NAME"} {
		if !primaryKeyColumns[column] {
			return schema, unrecognisedAddressBookLayout(path, "Z_PRIMARYKEY", column)
		}
	}
	for _, column := range []string{"Z_PK", "Z_ENT", "ZFIRSTNAME", "ZLASTNAME", "ZORGANIZATION"} {
		if !schema.recordColumns[column] {
			return schema, unrecognisedAddressBookLayout(path, "ZABCDRECORD", column)
		}
	}
	if !schema.recordColumns["ZUNIQUEID"] && !schema.recordColumns["ZEXTERNALUUID"] && !schema.recordColumns["ZEXTERNALIDENTIFIER"] {
		return schema, fmt.Errorf("unrecognised AddressBook database layout in %s: missing identifier column in ZABCDRECORD", path)
	}
	for _, table := range []struct {
		name    string
		columns map[string]bool
		value   string
	}{
		{name: "ZABCDPHONENUMBER", columns: schema.phoneColumns, value: "ZFULLNUMBER"},
		{name: "ZABCDEMAILADDRESS", columns: schema.emailColumns, value: "ZADDRESS"},
	} {
		if !table.columns[table.value] {
			return schema, unrecognisedAddressBookLayout(path, table.name, table.value)
		}
		if _, err := ownerExpression(table.columns); err != nil {
			return schema, fmt.Errorf("unrecognised AddressBook database layout in %s: missing owner column in %s", path, table.name)
		}
	}
	if _, err := ownerExpression(schema.postalColumns); err != nil {
		return schema, fmt.Errorf("unrecognised AddressBook database layout in %s: missing owner column in ZABCDPOSTALADDRESS", path)
	}
	if !hasAnyColumn(schema.postalColumns, "ZSTREET", "ZCITY", "ZZIPCODE", "ZCOUNTRYNAME", "ZCOUNTRYCODE") {
		return schema, fmt.Errorf("unrecognised AddressBook database layout in %s: missing postal address columns in ZABCDPOSTALADDRESS", path)
	}

	if err := db.QueryRowContext(ctx, `select Z_ENT from Z_PRIMARYKEY where Z_NAME = 'ABCDContact'`).Scan(&schema.contactEntity); err != nil {
		return schema, fmt.Errorf("unrecognised AddressBook database layout in %s: missing ABCDContact entity in Z_PRIMARYKEY", path)
	}
	return schema, nil
}

func tableColumns(ctx context.Context, db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, "pragma table_info("+table+")")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	columns := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, pk int
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return nil, err
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(columns) == 0 {
		return nil, fmt.Errorf("unrecognised AddressBook database layout: missing table %s", table)
	}
	return columns, nil
}

func unrecognisedAddressBookLayout(path, table, column string) error {
	return fmt.Errorf("unrecognised AddressBook database layout in %s: missing column %s.%s", path, table, column)
}

func readAddressBookRecords(ctx context.Context, db *sql.DB, schema addressBookSchema) (map[int64]*addressBookRecord, []int64, error) {
	idExpr := firstPresentExpression(schema.recordColumns, "ZUNIQUEID", "ZEXTERNALUUID", "ZEXTERNALIDENTIFIER")
	middleExpr := optionalTextExpression(schema.recordColumns, "ZMIDDLENAME")
	avatarExpr := "null"
	if schema.recordColumns["ZTHUMBNAILIMAGEDATA"] {
		avatarExpr = "ZTHUMBNAILIMAGEDATA"
	}
	query := fmt.Sprintf(`
select Z_PK, %s, coalesce(ZFIRSTNAME, ''), %s, coalesce(ZLASTNAME, ''), coalesce(ZORGANIZATION, ''), %s
from ZABCDRECORD
where Z_ENT = ?
order by Z_PK`, idExpr, middleExpr, avatarExpr)
	rows, err := db.QueryContext(ctx, query, schema.contactEntity)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	records := map[int64]*addressBookRecord{}
	var order []int64
	for rows.Next() {
		var pk int64
		var identifier, firstName, middleName, lastName, organisation string
		var avatar []byte
		if err := rows.Scan(&pk, &identifier, &firstName, &middleName, &lastName, &organisation, &avatar); err != nil {
			return nil, nil, err
		}
		identifier = strings.TrimSpace(identifier)
		if identifier == "" {
			continue
		}
		contact := Contact{
			Identifier: identifier,
			FirstName:  strings.TrimSpace(firstName),
			LastName:   strings.TrimSpace(lastName),
			FullName:   fullName(firstName, middleName, lastName, organisation),
			AvatarData: append([]byte(nil), avatar...),
		}
		records[pk] = &addressBookRecord{contact: contact}
		order = append(order, pk)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return records, order, nil
}

func readAddressBookPhones(ctx context.Context, db *sql.DB, schema addressBookSchema, records map[int64]*addressBookRecord) error {
	owner, err := ownerExpression(schema.phoneColumns)
	if err != nil {
		return err
	}
	labelExpr := optionalTextExpression(schema.phoneColumns, "ZLABEL")
	orderExpr := orderingExpression(schema.phoneColumns)
	query := fmt.Sprintf(`
select %s, coalesce(ZFULLNUMBER, ''), %s
from ZABCDPHONENUMBER
where trim(coalesce(ZFULLNUMBER, '')) <> ''
order by %s`, owner, labelExpr, orderExpr)
	return readLabeledValues(ctx, db, query, func(owner int64, value labeledValue) {
		if record := records[owner]; record != nil {
			record.phones = appendUniqueLabeledValue(record.phones, value)
		}
	})
}

func readAddressBookEmails(ctx context.Context, db *sql.DB, schema addressBookSchema, records map[int64]*addressBookRecord) error {
	owner, err := ownerExpression(schema.emailColumns)
	if err != nil {
		return err
	}
	labelExpr := optionalTextExpression(schema.emailColumns, "ZLABEL")
	orderExpr := orderingExpression(schema.emailColumns)
	query := fmt.Sprintf(`
select %s, coalesce(ZADDRESS, ''), %s
from ZABCDEMAILADDRESS
where trim(coalesce(ZADDRESS, '')) <> ''
order by %s`, owner, labelExpr, orderExpr)
	return readLabeledValues(ctx, db, query, func(owner int64, value labeledValue) {
		if record := records[owner]; record != nil {
			record.emails = appendUniqueLabeledValue(record.emails, value)
		}
	})
}

func readAddressBookPostalAddresses(ctx context.Context, db *sql.DB, schema addressBookSchema, records map[int64]*addressBookRecord) error {
	owner, err := ownerExpression(schema.postalColumns)
	if err != nil {
		return err
	}
	labelExpr := optionalTextExpression(schema.postalColumns, "ZLABEL")
	orderExpr := orderingExpression(schema.postalColumns)
	query := fmt.Sprintf(`
select %s, %s, %s, %s, %s, %s, %s, %s
from ZABCDPOSTALADDRESS
order by %s`,
		owner,
		labelExpr,
		optionalTextExpression(schema.postalColumns, "ZSTREET"),
		optionalTextExpression(schema.postalColumns, "ZCITY"),
		optionalTextExpression(schema.postalColumns, "ZSTATE"),
		optionalTextExpression(schema.postalColumns, "ZZIPCODE"),
		optionalTextExpression(schema.postalColumns, "ZCOUNTRYNAME"),
		optionalTextExpression(schema.postalColumns, "ZCOUNTRYCODE"),
		orderExpr,
	)
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var owner int64
		var label, street, city, state, zipCode, countryName, countryCode string
		if err := rows.Scan(&owner, &label, &street, &city, &state, &zipCode, &countryName, &countryCode); err != nil {
			return err
		}
		value := postalAddressValue(street, city, state, zipCode, countryName, countryCode)
		if value == "" {
			continue
		}
		if record := records[owner]; record != nil {
			record.postal = appendUniquePostalAddress(record.postal, PostalAddress{Value: value, Label: label})
		}
	}
	return rows.Err()
}

func readLabeledValues(ctx context.Context, db *sql.DB, query string, add func(int64, labeledValue)) error {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var owner int64
		var value, label string
		if err := rows.Scan(&owner, &value, &label); err != nil {
			return err
		}
		value = strings.TrimSpace(value)
		if owner == 0 || value == "" {
			continue
		}
		add(owner, labeledValue{value: value, label: label})
	}
	return rows.Err()
}

func firstPresentExpression(columns map[string]bool, names ...string) string {
	var expressions []string
	for _, name := range names {
		if columns[name] {
			expressions = append(expressions, "nullif("+name+", '')")
		}
	}
	if len(expressions) == 1 {
		return "coalesce(" + expressions[0] + ", '')"
	}
	return "coalesce(" + strings.Join(expressions, ", ") + ", '')"
}

func optionalTextExpression(columns map[string]bool, name string) string {
	if columns[name] {
		return "coalesce(" + name + ", '')"
	}
	return "''"
}

func ownerExpression(columns map[string]bool) (string, error) {
	var expressions []string
	if columns["ZOWNER"] {
		expressions = append(expressions, "nullif(ZOWNER, 0)")
	}
	var versioned []string
	for column := range columns {
		if strings.HasPrefix(column, "Z") && strings.HasSuffix(column, "_OWNER") && column != "ZOWNER" {
			versioned = append(versioned, column)
		}
	}
	sort.Strings(versioned)
	for _, column := range versioned {
		expressions = append(expressions, "nullif("+column+", 0)")
	}
	if len(expressions) == 0 {
		return "", fmt.Errorf("missing owner column")
	}
	if len(expressions) == 1 {
		return "coalesce(" + expressions[0] + ", 0)", nil
	}
	return "coalesce(" + strings.Join(expressions, ", ") + ", 0)", nil
}

func orderingExpression(columns map[string]bool) string {
	var terms []string
	if columns["ZISPRIMARY"] {
		terms = append(terms, "coalesce(ZISPRIMARY, 0) desc")
	}
	if columns["ZORDERINGINDEX"] {
		terms = append(terms, "coalesce(ZORDERINGINDEX, 0)")
	}
	if columns["Z_PK"] {
		terms = append(terms, "Z_PK")
	}
	if len(terms) == 0 {
		return "1"
	}
	return strings.Join(terms, ", ")
}

func hasAnyColumn(columns map[string]bool, names ...string) bool {
	for _, name := range names {
		if columns[name] {
			return true
		}
	}
	return false
}

func fullName(firstName, middleName, lastName, organisation string) string {
	name := strings.Join(nonEmptyStrings(firstName, middleName, lastName), " ")
	if strings.TrimSpace(name) != "" {
		return name
	}
	return strings.TrimSpace(organisation)
}

func postalAddressValue(street, city, state, zipCode, countryName, countryCode string) string {
	street = strings.TrimSpace(street)
	city = strings.TrimSpace(city)
	state = strings.TrimSpace(state)
	zipCode = strings.TrimSpace(zipCode)
	countryName = strings.TrimSpace(countryName)
	countryCode = strings.ToUpper(strings.TrimSpace(countryCode))

	var locality string
	switch {
	case state != "" && city != "":
		locality = strings.Join(nonEmptyStrings(city, state, zipCode), " ")
	case zipCode != "" && city != "":
		locality = strings.Join(nonEmptyStrings(zipCode, city), " ")
	default:
		locality = strings.Join(nonEmptyStrings(city, state, zipCode), " ")
	}
	if countryName == "" {
		countryName = countryCode
	}
	return strings.Join(nonEmptyStrings(street, locality, countryName), "\n")
}

func nonEmptyStrings(values ...string) []string {
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func mergeContact(base, incoming Contact) Contact {
	if strings.TrimSpace(base.FirstName) == "" {
		base.FirstName = incoming.FirstName
	}
	if strings.TrimSpace(base.LastName) == "" {
		base.LastName = incoming.LastName
	}
	if strings.TrimSpace(base.FullName) == "" {
		base.FullName = incoming.FullName
	}
	base.Emails = appendUniqueStrings(base.Emails, incoming.Emails...)
	base.Phones = appendUniqueStrings(base.Phones, incoming.Phones...)
	for _, address := range incoming.Addresses {
		base.Addresses = appendUniquePostalAddress(base.Addresses, address)
	}
	if len(base.AvatarData) == 0 && len(incoming.AvatarData) > 0 {
		base.AvatarData = append([]byte(nil), incoming.AvatarData...)
	}
	return base
}

func appendUniqueStrings(values []string, incoming ...string) []string {
	seen := map[string]bool{}
	for _, value := range values {
		seen[strings.ToLower(strings.TrimSpace(value))] = true
	}
	for _, value := range incoming {
		value = strings.TrimSpace(value)
		key := strings.ToLower(value)
		if value == "" || seen[key] {
			continue
		}
		values = append(values, value)
		seen[key] = true
	}
	return values
}

func appendUniqueLabeledValue(values []labeledValue, incoming labeledValue) []labeledValue {
	key := strings.ToLower(strings.TrimSpace(incoming.value))
	if key == "" {
		return values
	}
	for _, value := range values {
		if strings.ToLower(strings.TrimSpace(value.value)) == key {
			return values
		}
	}
	return append(values, labeledValue{value: strings.TrimSpace(incoming.value), label: strings.TrimSpace(incoming.label)})
}

func appendUniquePostalAddress(values []PostalAddress, incoming PostalAddress) []PostalAddress {
	incoming.Value = strings.TrimSpace(incoming.Value)
	incoming.Label = strings.TrimSpace(incoming.Label)
	if incoming.Value == "" {
		return values
	}
	key := strings.ToLower(incoming.Value + "\x00" + incoming.Label)
	for _, value := range values {
		if strings.ToLower(strings.TrimSpace(value.Value)+"\x00"+strings.TrimSpace(value.Label)) == key {
			return values
		}
	}
	return append(values, incoming)
}
