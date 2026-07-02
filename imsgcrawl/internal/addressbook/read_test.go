package addressbook

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestExtractMatchesPhonesAndEmails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "AddressBook-v22.abcddb")
	createAddressBookFixture(t, path)

	names, err := Extract(context.Background(), []string{path})
	if err != nil {
		t.Fatal(err)
	}
	lookup := NewLookup(names)
	assertLookupName(t, lookup, "+1 (555) 0100", "Katja Example")
	assertLookupName(t, lookup, "0015550100", "Katja Example")
	assertLookupName(t, lookup, "alice@example.com", "Alice Mail")
	assertLookupName(t, lookup, "ALICE@EXAMPLE.COM", "Alice Mail")
	if _, ok := lookup.Match("+15550999"); ok {
		t.Fatal("unmatched phone should not resolve")
	}
}

func TestExtractMergesStoresDeterministically(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "root.abcddb")
	second := filepath.Join(dir, "source.abcddb")
	createAddressBookStore(t, first, []string{
		`insert into ZABCDRECORD(Z_PK, ZFIRSTNAME, ZLASTNAME, ZORGANIZATION) values (1, 'Katja', '', '')`,
		`insert into ZABCDPHONENUMBER(Z_PK, ZFULLNUMBER, ZCOUNTRYCODE, ZAREACODE, ZLOCALNUMBER, ZOWNER) values (1, '+15550100', '', '', '', 1)`,
	})
	createAddressBookStore(t, second, []string{
		`insert into ZABCDRECORD(Z_PK, ZFIRSTNAME, ZLASTNAME, ZORGANIZATION) values (1, 'Katja', 'Example', '')`,
		`insert into ZABCDPHONENUMBER(Z_PK, ZFULLNUMBER, ZCOUNTRYCODE, ZAREACODE, ZLOCALNUMBER, ZOWNER) values (1, '+1 555 0100', '', '', '', 1)`,
	})

	names, err := Extract(context.Background(), []string{first, second})
	if err != nil {
		t.Fatal(err)
	}
	assertLookupName(t, NewLookup(names), "+15550100", "Katja Example")
}

func assertLookupName(t *testing.T, lookup Lookup, handle, want string) {
	t.Helper()
	got, ok := lookup.Match(handle)
	if !ok {
		t.Fatalf("lookup %q did not match", handle)
	}
	if got.DisplayName != want {
		t.Fatalf("lookup %q = %#v, want %q", handle, got, want)
	}
}

func createAddressBookFixture(t *testing.T, path string) {
	t.Helper()
	createAddressBookStore(t, path, []string{
		`insert into ZABCDRECORD(Z_PK, ZFIRSTNAME, ZLASTNAME, ZORGANIZATION) values (1, 'Katja', 'Example', '')`,
		`insert into ZABCDRECORD(Z_PK, ZFIRSTNAME, ZLASTNAME, ZORGANIZATION) values (2, 'Alice', 'Mail', '')`,
		`insert into ZABCDPHONENUMBER(Z_PK, ZFULLNUMBER, ZCOUNTRYCODE, ZAREACODE, ZLOCALNUMBER, ZOWNER) values (1, '555-0100', '+1', '', '5550100', 1)`,
		`insert into ZABCDEMAILADDRESS(Z_PK, ZADDRESS, ZOWNER) values (1, 'ALICE@EXAMPLE.COM', 2)`,
	})
}

func createAddressBookStore(t *testing.T, path string, inserts []string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	schema := []string{
		`create table ZABCDRECORD (Z_PK integer primary key, ZFIRSTNAME text, ZLASTNAME text, ZORGANIZATION text)`,
		`create table ZABCDPHONENUMBER (Z_PK integer primary key, ZFULLNUMBER text, ZCOUNTRYCODE text, ZAREACODE text, ZLOCALNUMBER text, ZOWNER integer)`,
		`create table ZABCDEMAILADDRESS (Z_PK integer primary key, ZADDRESS text, ZOWNER integer)`,
	}
	for _, stmt := range schema {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	for _, stmt := range inserts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
}
