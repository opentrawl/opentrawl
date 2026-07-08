//go:build darwin

package apple

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadAddressBookDirReadsRootAndSourceDatabases(t *testing.T) {
	dir := t.TempDir()
	createAddressBookFixture(t, filepath.Join(dir, addressBookDBName), []fixtureContact{{
		PK:         1,
		Identifier: "root-contact:ABPerson",
		FirstName:  "Root",
		LastName:   "Contact",
		Emails:     []string{"root@example.com"},
	}})
	sourceDir := filepath.Join(dir, "Sources", "source-1")
	createAddressBookFixture(t, filepath.Join(sourceDir, addressBookDBName), []fixtureContact{{
		PK:         1,
		Identifier: "source-contact:ABPerson",
		FirstName:  "Ada",
		MiddleName: "Augusta",
		LastName:   "Lovelace",
		Phones:     []string{"+1 555 0100"},
		Emails:     []string{"ada@example.com"},
		Address: fixtureAddress{
			Label:       "_$!<Work>!$_",
			Street:      "1 Infinite Loop",
			City:        "Cupertino",
			State:       "CA",
			ZipCode:     "95014",
			CountryName: "United States",
			CountryCode: "US",
		},
		Avatar: []byte("avatar"),
	}})

	contacts, err := readAddressBookDir(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(contacts) != 2 {
		t.Fatalf("contacts = %#v", contacts)
	}
	if contacts[0].Identifier != "root-contact:ABPerson" || contacts[1].Identifier != "source-contact:ABPerson" {
		t.Fatalf("contact order = %#v", contacts)
	}
	source := contacts[1]
	if source.Name() != "Ada Augusta Lovelace" {
		t.Fatalf("name = %q", source.Name())
	}
	if len(source.Phones) != 1 || source.Phones[0] != "+1 555 0100" {
		t.Fatalf("phones = %#v", source.Phones)
	}
	if len(source.Emails) != 1 || source.Emails[0] != "ada@example.com" {
		t.Fatalf("emails = %#v", source.Emails)
	}
	if len(source.Addresses) != 1 {
		t.Fatalf("addresses = %#v", source.Addresses)
	}
	if source.Addresses[0].Label != "_$!<Work>!$_" {
		t.Fatalf("raw label = %#v", source.Addresses[0])
	}
	if source.Addresses[0].Value != "1 Infinite Loop\nCupertino CA 95014\nUnited States" {
		t.Fatalf("address value = %q", source.Addresses[0].Value)
	}
	if string(source.AvatarData) != "avatar" {
		t.Fatalf("avatar = %q", source.AvatarData)
	}

	src := source.SourceContact(true)
	if len(src.Addresses) != 1 || src.Addresses[0].Label != "work" || src.Addresses[0].Source != "apple" {
		t.Fatalf("source address = %#v", src.Addresses)
	}
	if src.Avatar == nil || string(src.Avatar.Data) != "avatar" {
		t.Fatalf("source avatar = %#v", src.Avatar)
	}
}

func TestReadAddressBookDatabaseReportsMissingTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), addressBookDBName)
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`create table ZABCDRECORD (Z_PK integer primary key)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = readAddressBookDatabase(t.Context(), path)
	if err == nil || !strings.Contains(err.Error(), "missing table ZABCDPHONENUMBER") {
		t.Fatalf("err = %v", err)
	}
}

func TestReadAddressBookDatabaseDoesNotCreateLiveSidecars(t *testing.T) {
	path := filepath.Join(t.TempDir(), addressBookDBName)
	createAddressBookFixture(t, path, []fixtureContact{{
		PK:         1,
		Identifier: "fixture-contact:ABPerson",
		FirstName:  "Ada",
		LastName:   "Example",
		Emails:     []string{"ada@example.com"},
	}})
	setWALModeWithoutSidecars(t, path)

	contacts, err := readAddressBookDatabase(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if len(contacts) != 1 || contacts[0].Name() != "Ada Example" {
		t.Fatalf("contacts = %#v", contacts)
	}
	assertNoAddressBookSidecars(t, path)
}

type fixtureContact struct {
	PK           int
	Identifier   string
	FirstName    string
	MiddleName   string
	LastName     string
	Organisation string
	Emails       []string
	Phones       []string
	Address      fixtureAddress
	Avatar       []byte
}

type fixtureAddress struct {
	Label       string
	Street      string
	City        string
	State       string
	ZipCode     string
	CountryName string
	CountryCode string
}

func createAddressBookFixture(t *testing.T, path string, contacts []fixtureContact) {
	t.Helper()
	if err := ensureParentDir(path); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	statements := []string{
		`create table Z_PRIMARYKEY (Z_ENT integer, Z_NAME varchar, Z_SUPER integer)`,
		`insert into Z_PRIMARYKEY (Z_ENT, Z_NAME, Z_SUPER) values (22, 'ABCDContact', 17)`,
		`create table ZABCDRECORD (
			Z_PK integer primary key,
			Z_ENT integer,
			ZFIRSTNAME varchar,
			ZMIDDLENAME varchar,
			ZLASTNAME varchar,
			ZORGANIZATION varchar,
			ZUNIQUEID varchar,
			ZEXTERNALUUID varchar,
			ZTHUMBNAILIMAGEDATA blob
		)`,
		`create table ZABCDPHONENUMBER (
			Z_PK integer primary key,
			ZOWNER integer,
			Z22_OWNER integer,
			ZFULLNUMBER varchar,
			ZLABEL varchar,
			ZISPRIMARY integer,
			ZORDERINGINDEX integer
		)`,
		`create table ZABCDEMAILADDRESS (
			Z_PK integer primary key,
			ZOWNER integer,
			Z22_OWNER integer,
			ZADDRESS varchar,
			ZLABEL varchar,
			ZISPRIMARY integer,
			ZORDERINGINDEX integer
		)`,
		`create table ZABCDPOSTALADDRESS (
			Z_PK integer primary key,
			ZOWNER integer,
			Z22_OWNER integer,
			ZLABEL varchar,
			ZSTREET varchar,
			ZCITY varchar,
			ZSTATE varchar,
			ZZIPCODE varchar,
			ZCOUNTRYNAME varchar,
			ZCOUNTRYCODE varchar,
			ZISPRIMARY integer,
			ZORDERINGINDEX integer
		)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	for _, contact := range contacts {
		if _, err := db.Exec(`insert into ZABCDRECORD (Z_PK, Z_ENT, ZFIRSTNAME, ZMIDDLENAME, ZLASTNAME, ZORGANIZATION, ZUNIQUEID, ZTHUMBNAILIMAGEDATA) values (?, 22, ?, ?, ?, ?, ?, ?)`,
			contact.PK, contact.FirstName, contact.MiddleName, contact.LastName, contact.Organisation, contact.Identifier, contact.Avatar); err != nil {
			t.Fatal(err)
		}
		for i, email := range contact.Emails {
			if _, err := db.Exec(`insert into ZABCDEMAILADDRESS (ZOWNER, ZADDRESS, ZLABEL, ZISPRIMARY, ZORDERINGINDEX) values (?, ?, '_$!<Home>!$_', ?, ?)`,
				contact.PK, email, boolInt(i == 0), i); err != nil {
				t.Fatal(err)
			}
		}
		for i, phone := range contact.Phones {
			if _, err := db.Exec(`insert into ZABCDPHONENUMBER (ZOWNER, ZFULLNUMBER, ZLABEL, ZISPRIMARY, ZORDERINGINDEX) values (?, ?, '_$!<Mobile>!$_', ?, ?)`,
				contact.PK, phone, boolInt(i == 0), i); err != nil {
				t.Fatal(err)
			}
		}
		if contact.Address.Street != "" || contact.Address.City != "" {
			if _, err := db.Exec(`insert into ZABCDPOSTALADDRESS (ZOWNER, ZLABEL, ZSTREET, ZCITY, ZSTATE, ZZIPCODE, ZCOUNTRYNAME, ZCOUNTRYCODE, ZISPRIMARY, ZORDERINGINDEX) values (?, ?, ?, ?, ?, ?, ?, ?, 1, 0)`,
				contact.PK, contact.Address.Label, contact.Address.Street, contact.Address.City, contact.Address.State, contact.Address.ZipCode, contact.Address.CountryName, contact.Address.CountryCode); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func ensureParentDir(path string) error {
	return os.MkdirAll(filepath.Dir(path), 0o755)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func setWALModeWithoutSidecars(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`pragma journal_mode=WAL`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`pragma wal_checkpoint(TRUNCATE)`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(path + suffix); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
}

func assertNoAddressBookSidecars(t *testing.T, path string) {
	t.Helper()
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(path + suffix); !os.IsNotExist(err) {
			t.Fatalf("live sidecar %s stat err = %v, want not exist", path+suffix, err)
		}
	}
}
