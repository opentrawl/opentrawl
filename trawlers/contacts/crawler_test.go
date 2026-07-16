package clawdex

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	"github.com/opentrawl/opentrawl/trawlkit/output"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
	"google.golang.org/protobuf/proto"
)

const contactsTestRunSubcommand = "contacts-test-run"

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == contactsTestRunSubcommand {
		os.Exit(trawlkit.Run(os.Args[2:], []trawlkit.Crawler{New()}))
	}
	if len(os.Args) > 1 && os.Args[1] == trawlkit.HiddenWireSubcommand {
		os.Exit(trawlkit.Run(os.Args[1:], []trawlkit.Crawler{New()}))
	}
	os.Exit(m.Run())
}

func TestOpenRecordCallsItsLoaderOnce(t *testing.T) {
	assertOpenRecordLoaderCall(t, "open_record.go", "loadOpenPerson")
}

func assertOpenRecordLoaderCall(t *testing.T, path, loader string) {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Recv == nil || function.Name.Name != "OpenRecord" {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if ok && selector.Sel.Name == loader {
				calls++
			}
			return true
		})
	}
	if calls != 1 {
		t.Fatalf("OpenRecord %s calls = %d, want 1", loader, calls)
	}
}

func TestStatusUsesOnlyArchiveState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeContactsSourceFixture(t, filepath.Join(home, "Library", "Application Support", "AddressBook", "AddressBook-v22.abcddb"))
	request := &trawlkit.Request{Paths: trawlkit.Paths{Archive: filepath.Join(t.TempDir(), "contacts.db")}}
	status, err := New().Status(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "missing" || len(status.SetupRequirements) != 0 {
		t.Fatalf("status = %#v, want missing archive without source setup", status)
	}
}

func TestMetadataManifestGeneratedByRunner(t *testing.T) {
	home := testHome(t)
	code, stdout, stderr := runContacts(t, home, "metadata", "--json")
	if code != 0 {
		t.Fatalf("metadata code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var manifest control.Manifest
	if err := json.Unmarshal([]byte(stdout), &manifest); err != nil {
		t.Fatalf("manifest JSON: %v\n%s", err, stdout)
	}
	wantCommands := []string{"contacts_export", "export_vcard", "import", "import_legacy", "metadata", "open", "person_annotate", "person_list", "person_show", "search", "status", "sync", "sync_google", "who"}
	if got := sortedKeys(manifest.Commands); !equalStrings(got, wantCommands) {
		t.Fatalf("commands = %v, want %v", got, wantCommands)
	}
	if got := manifest.Paths.DefaultDatabase; !strings.HasSuffix(got, filepath.Join(".opentrawl", "contacts", "contacts.db")) {
		t.Fatalf("default database = %q", got)
	}
	if got := manifest.Commands["contacts_export"].Store; got != "read" {
		t.Fatalf("contacts_export store = %q", got)
	}
	if got := manifest.Commands["import"]; !got.Mutates || got.Store != "write" {
		t.Fatalf("import command = %#v", got)
	}
	if got := manifest.Commands["sync"]; !got.Mutates || got.Store != "write" {
		t.Fatalf("sync command = %#v", got)
	}
}

func TestRunnerCommandsAgainstSyntheticArchive(t *testing.T) {
	home := testHome(t)
	input := filepath.Join(home, "apple.ndjson")
	avatar := base64.StdEncoding.EncodeToString([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
	if err := os.WriteFile(input, []byte(`{"identifier":"a1","full_name":"Ada Example","emails":["ada@example.com"],"phones":["+15550100"],"avatar_data":"`+avatar+`"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, stdout, stderr := runContacts(t, home, "import", "apple", "--input", input, "--avatars", "--json"); code != 0 {
		t.Fatalf("import code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	archivePath := filepath.Join(home, ".opentrawl", "contacts", "contacts.db")
	if _, err := os.Stat(archivePath); err != nil {
		t.Fatalf("archive was not created at %s: %v", archivePath, err)
	}
	code, stdout, stderr := runContacts(t, home, "status", "--json")
	if code != 0 {
		t.Fatalf("status code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, `"state": "ok"`) || !strings.Contains(stdout, `"database_path": "`+archivePath+`"`) {
		t.Fatalf("status stdout = %s", stdout)
	}
	code, stdout, stderr = runContacts(t, home, "search", "Ada", "--json")
	if code != 0 {
		t.Fatalf("search code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var search struct {
		Results []trawlkit.Hit `json:"results"`
	}
	if err := json.Unmarshal([]byte(stdout), &search); err != nil {
		t.Fatalf("search JSON: %v\n%s", err, stdout)
	}
	if len(search.Results) != 1 || search.Results[0].ShortRef == "" {
		t.Fatalf("search = %#v", search)
	}
	match := search.Results[0]
	if match.AnchorID != "name" || match.Summary.Title != "Ada Example" || match.Summary.Subtitle != "" {
		t.Fatalf("search match = %#v", match)
	}
	if len(match.Archive) != 1 || match.Archive[0].Label != "In Contacts" {
		t.Fatalf("search archive context = %#v", match.Archive)
	}
	nameEvidence := match.Evidence[0]
	matched := false
	if nameEvidence.Field != nil {
		for _, run := range nameEvidence.Field.Value {
			matched = matched || run.Matched
		}
	}
	if nameEvidence.Label != "Name" || nameEvidence.Field == nil || nameEvidence.Field.Name != "name" || !matched {
		t.Fatalf("search evidence = %#v", match.Evidence)
	}
	readStore, err := ckstore.OpenReadOnly(context.Background(), archivePath)
	if err != nil {
		t.Fatal(err)
	}
	record, err := New().OpenRecord(context.Background(), &trawlkit.Request{Store: readStore, Paths: trawlkit.Paths{Archive: archivePath}}, match.Ref)
	_ = readStore.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := openrecord.ValidateRequestedAnchor(record, match.AnchorID); err != nil {
		t.Fatalf("search anchor %q does not open: %v", match.AnchorID, err)
	}
	code, stdout, stderr = runContacts(t, home, "open", search.Results[0].ShortRef, "--json")
	if code != 0 {
		t.Fatalf("open code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var opened struct {
		Ref    string       `json:"ref"`
		Person model.Person `json:"person"`
	}
	if err := json.Unmarshal([]byte(stdout), &opened); err != nil {
		t.Fatalf("open JSON: %v\n%s", err, stdout)
	}
	person := opened.Person
	if opened.Ref != archive.PersonRef(person.ID) {
		t.Fatalf("open ref = %q person=%#v", opened.Ref, person)
	}
	if person.Name != "Ada Example" {
		t.Fatalf("person = %#v", person)
	}
	code, stdout, stderr = runContacts(t, home, "who", "Ada", "--json")
	if code != 0 {
		t.Fatalf("who code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, `"who": "Ada Example"`) {
		t.Fatalf("who stdout = %s", stdout)
	}
	code, stdout, stderr = runContacts(t, home, "person", "annotate", person.ID, "Ada owns billing", "--json")
	if code != 0 {
		t.Fatalf("annotate code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, `"annotation": "Ada owns billing"`) {
		t.Fatalf("annotate stdout = %s", stdout)
	}
	code, stdout, stderr = runContacts(t, home, "export", "vcard", "--person", person.ID, "--include-avatars", "--out", "-")
	if code != 0 {
		t.Fatalf("export vcard code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "PHOTO:data:image/png;base64,iVBORw0KGgo=") {
		t.Fatalf("vcard stdout = %s", stdout)
	}
	code, stdout, stderr = runContacts(t, home, "contacts", "contacts", "export", "--json")
	if code != 0 {
		t.Fatalf("contacts export code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var export control.ContactExport
	if err := json.Unmarshal([]byte(stdout), &export); err != nil {
		t.Fatalf("contacts JSON: %v\n%s", err, stdout)
	}
	if len(export.Contacts) != 1 || export.Contacts[0].PhoneNumbers[0] != "+15550100" {
		t.Fatalf("contacts = %#v", export)
	}
}

func TestImportLegacyUsesSyntheticShareReadOnlyAndIsIdempotent(t *testing.T) {
	home := testHome(t)
	legacy := filepath.Join(home, "legacy-share")
	writeLegacyFixture(t, legacy)
	code, stdout, stderr := runContacts(t, home, "import-legacy", "--from", legacy, "--json")
	if code != 0 {
		t.Fatalf("import-legacy code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var first legacyImportEnvelope
	if err := json.Unmarshal([]byte(stdout), &first); err != nil {
		t.Fatalf("legacy JSON: %v\n%s", err, stdout)
	}
	if first.Summary.People != 2 || first.Summary.Notes != 1 || first.Summary.Created != 2 {
		t.Fatalf("first summary = %#v", first.Summary)
	}
	if _, err := os.Stat(filepath.Join(legacy, ".git")); !os.IsNotExist(err) {
		t.Fatalf("legacy importer created or touched .git: %v", err)
	}
	code, stdout, stderr = runContacts(t, home, "import-legacy", "--from", legacy, "--json")
	if code != 0 {
		t.Fatalf("rerun import-legacy code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var second legacyImportEnvelope
	if err := json.Unmarshal([]byte(stdout), &second); err != nil {
		t.Fatalf("legacy rerun JSON: %v\n%s", err, stdout)
	}
	if second.Summary.People != 2 || second.Summary.Unchanged != 2 {
		t.Fatalf("second summary = %#v", second.Summary)
	}
	archivePath := filepath.Join(home, ".opentrawl", "contacts", "contacts.db")
	st, err := archive.Open(t.Context(), archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	people, err := st.People(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(people) != 2 {
		t.Fatalf("people = %#v", people)
	}
	readStore, err := ckstore.Open(t.Context(), ckstore.Options{Path: archivePath})
	if err != nil {
		t.Fatal(err)
	}
	req := &trawlkit.Request{Store: readStore, Paths: trawlkit.Paths{Archive: archivePath}}
	app := New()
	records, err := app.ShortRefRecords(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := req.AssignShortRefs(t.Context(), records); err != nil {
		t.Fatal(err)
	}
	fullRef := archive.PersonRef(people[0].ID)
	aliases, err := req.ShortRefAliases(t.Context(), []string{fullRef})
	if err != nil {
		t.Fatal(err)
	}
	fullRecord, err := app.OpenRecord(t.Context(), req, fullRef)
	if err != nil {
		t.Fatal(err)
	}
	shortRecord, err := app.OpenRecord(t.Context(), req, aliases[fullRef])
	if err != nil {
		_ = readStore.Close()
		t.Fatal(err)
	}
	if !proto.Equal(fullRecord, shortRecord) || shortRecord.OpenRef != fullRef || shortRecord.Data.GetTypeUrl() != "type.googleapis.com/trawl.source.contacts.open.v1.ContactsRecord" || shortRecord.Presentation == nil {
		_ = readStore.Close()
		t.Fatalf("open records full=%#v short=%#v", fullRecord, shortRecord)
	}
	fullValue, err := app.loadOpenPerson(t.Context(), req, fullRef)
	if err != nil {
		_ = readStore.Close()
		t.Fatal(err)
	}
	shortValue, err := app.loadOpenPerson(t.Context(), req, aliases[fullRef])
	if err != nil {
		_ = readStore.Close()
		t.Fatal(err)
	}
	captureLegacy := func(caseName, ref string) {
		goldens := map[string]string{"json": "f3e70150b4077553248159eeed11c1f34519c92b2cdd7560e9c7fc7f34e4fcfe", "text": "219b5fa7efa35288edd82aff50a9a9e2277dc22a85ba81b5c22dc533f9304676"}
		for _, format := range []struct {
			name  string
			value output.Format
		}{{"json", output.JSON}, {"text", output.Text}} {
			var stdout bytes.Buffer
			legacyReq := *req
			legacyReq.Format, legacyReq.Out = format.value, &stdout
			openErr := app.Open(t.Context(), &legacyReq, ref)
			assertLegacyOpenGolden(t, stdout.Bytes(), openErr, goldens[format.name])
			writeLegacyOpenEvidence(t, "contacts", caseName, format.name, stdout.Bytes(), openErr)
			if openErr != nil {
				_ = readStore.Close()
				t.Fatal(openErr)
			}
		}
	}
	writeRuntimeOpenEvidence(t, "contacts", "full", fullRef, map[string]any{"ref": fullValue.ref, "person": fullValue.person}, fullRecord)
	writeRuntimeOpenEvidence(t, "contacts", "short", aliases[fullRef], map[string]any{"ref": shortValue.ref, "person": shortValue.person}, shortRecord)
	captureLegacy("full", fullRef)
	captureLegacy("short", aliases[fullRef])
	if _, err := app.OpenRecord(t.Context(), req, "zzzzz"); err == nil || err.Error() != `no person matched "zzzzz"` {
		_ = readStore.Close()
		t.Fatalf("unknown short-like contact query error = %#v", err)
	}
	secondRef := archive.PersonRef(people[1].ID)
	if _, err := readStore.DB().ExecContext(t.Context(), `insert into short_refs(alias, full_ref, canonical_ref) values (?, ?, ?), (?, ?, ?)`, "zzzzz", fullRef, fullRef, "zzzzz", secondRef, secondRef); err != nil {
		_ = readStore.Close()
		t.Fatal(err)
	}
	_, err = app.OpenRecord(t.Context(), req, "zzzzz")
	var ambiguous output.UsageError
	if !errors.As(err, &ambiguous) {
		_ = readStore.Close()
		t.Fatalf("ambiguous short ref error = %#v", err)
	}
	if _, err := app.OpenRecord(t.Context(), req, "contacts:person/missing"); err == nil || err.Error() != `no person matched "missing"` {
		_ = readStore.Close()
		t.Fatalf("missing contact ref error = %#v", err)
	}
	if _, err := app.OpenRecord(t.Context(), req, "photos:asset/example"); err == nil || err.Error() != `no person matched "photos:asset/example"` {
		_ = readStore.Close()
		t.Fatalf("foreign contact ref error = %#v", err)
	}
	_, err = app.OpenRecord(t.Context(), &trawlkit.Request{Paths: trawlkit.Paths{Archive: archivePath + ".missing"}}, fullRef)
	if err == nil {
		_ = readStore.Close()
		t.Fatalf("missing archive error = %#v", err)
	}
	_ = readStore.Close()
}

func TestSyncPreviewVerbsPreserveOutput(t *testing.T) {
	home := testHome(t)
	if code, stdout, stderr := runContacts(t, home, "sync", "google", "--account", "ada@example.com", "--json"); code != 0 {
		t.Fatalf("sync google code=%d stdout=%s stderr=%s", code, stdout, stderr)
	} else if !strings.Contains(stdout, `"account": "ada@example.com"`) || !strings.Contains(stdout, "use import google") {
		t.Fatalf("sync google stdout = %s", stdout)
	}
}

func TestImportContactsFromCrawlerIsRetired(t *testing.T) {
	home := testHome(t)
	code, stdout, stderr := runContacts(t, home, "import", "contacts", "--json")
	if code != 2 {
		t.Fatalf("import contacts code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "import contacts from crawler binaries has been removed") {
		t.Fatalf("stdout = %s", stdout)
	}
}

func testHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func runContacts(t *testing.T, home string, args ...string) (int, string, string) {
	t.Helper()
	t.Setenv("HOME", home)
	var stdout, stderr bytes.Buffer
	command := exec.Command(os.Args[0], append([]string{contactsTestRunSubcommand}, args...)...)
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if err == nil {
		return 0, stdout.String(), stderr.String()
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatal(err)
	}
	return exitErr.ExitCode(), stdout.String(), stderr.String()
}

func sortedKeys(commands map[string]control.Command) []string {
	keys := make([]string, 0, len(commands))
	for key := range commands {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func writeLegacyFixture(t *testing.T, root string) {
	t.Helper()
	writePersonFile(t, root, "ada", `---
id: person_ada
name: Ada Legacy
tags: [vip]
emails:
  - value: ada@example.com
phones:
  - value: "+15550100"
accounts:
  telegram: [ada_legacy]
created_at: 2026-07-01T10:00:00Z
updated_at: 2026-07-02T10:00:00Z
---
# Ada Legacy

Legacy body.
`)
	writeNoteFile(t, root, "ada", `---
id: note_ada
person_id: person_ada
occurred_at: 2026-07-08T09:00:00Z
captured_at: 2026-07-08T10:00:00Z
kind: dm
source: telegram
topics: [handoff]
privacy: normal
---
Discuss the handoff.
`)
	writePersonFile(t, root, "grace", `---
id: person_grace
name: Grace Legacy
emails:
  - value: grace@example.com
phones:
  - value: "+15550101"
created_at: 2026-07-01T10:00:00Z
updated_at: 2026-07-02T10:00:00Z
---
# Grace Legacy
`)
}

func writeContactsSourceFixture(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, statement := range []string{
		`create table Z_PRIMARYKEY (Z_ENT integer, Z_NAME varchar, Z_SUPER integer)`,
		`insert into Z_PRIMARYKEY (Z_ENT, Z_NAME, Z_SUPER) values (22, 'ABCDContact', 17)`,
		`create table ZABCDRECORD (Z_PK integer primary key, Z_ENT integer, ZFIRSTNAME varchar, ZMIDDLENAME varchar, ZLASTNAME varchar, ZORGANIZATION varchar, ZUNIQUEID varchar, ZEXTERNALUUID varchar, ZTHUMBNAILIMAGEDATA blob)`,
		`create table ZABCDPHONENUMBER (Z_PK integer primary key, ZOWNER integer, Z22_OWNER integer, ZFULLNUMBER varchar, ZLABEL varchar, ZISPRIMARY integer, ZORDERINGINDEX integer)`,
		`create table ZABCDEMAILADDRESS (Z_PK integer primary key, ZOWNER integer, Z22_OWNER integer, ZADDRESS varchar, ZLABEL varchar, ZISPRIMARY integer, ZORDERINGINDEX integer)`,
		`create table ZABCDPOSTALADDRESS (Z_PK integer primary key, ZOWNER integer, Z22_OWNER integer, ZLABEL varchar, ZSTREET varchar, ZCITY varchar, ZSTATE varchar, ZZIPCODE varchar, ZCOUNTRYNAME varchar, ZCOUNTRYCODE varchar, ZISPRIMARY integer, ZORDERINGINDEX integer)`,
		`insert into ZABCDRECORD (Z_PK, Z_ENT, ZFIRSTNAME, ZLASTNAME, ZUNIQUEID) values (1, 22, 'Ada', 'Example', 'synthetic-contact')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
}

func writePersonFile(t *testing.T, root, slug, data string) {
	t.Helper()
	path := filepath.Join(root, "people", slug, "person.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeNoteFile(t *testing.T, root, slug, data string) {
	t.Helper()
	path := filepath.Join(root, "people", slug, "notes", "note.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}
