package clawdex

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/apple"
	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
)

var runMu sync.Mutex

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == trawlkit.HiddenWireSubcommand {
		os.Exit(trawlkit.Run(os.Args[1:], []trawlkit.Crawler{New()}))
	}
	os.Exit(m.Run())
}

func TestSetupRequirementMapping(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	requirement := contactsSetupRequirement(context.Background())
	if requirement.ID != "full_disk_access" || requirement.Kind != control.SetupKindFullDiskAccess || requirement.State != control.SetupStateUnavailable || requirement.Action != control.SetupActionNone || len(requirement.Command) != 0 {
		t.Fatalf("requirement = %#v", requirement)
	}
	ready := contactsSetupRequirementForState(apple.SourceReady)
	if ready.ID != "full_disk_access" || ready.Kind != control.SetupKindFullDiskAccess || ready.State != control.SetupStateReady || ready.Action != control.SetupActionNone || len(ready.Command) != 0 {
		t.Fatalf("ready requirement = %#v", ready)
	}
	needsAction := contactsSetupRequirementForState(apple.SourceNeedsFullDiskAccess)
	if needsAction.ID != "full_disk_access" || needsAction.Kind != control.SetupKindFullDiskAccess || needsAction.State != control.SetupStateNeedsAction || needsAction.Action != control.SetupActionOpenFullDiskAccess || len(needsAction.Command) != 0 {
		t.Fatalf("needs-action requirement = %#v", needsAction)
	}
}

func TestStatusSetupRequirementBoundary(t *testing.T) {
	cases := []struct {
		name  string
		state control.SetupState
		setup func(*testing.T, string)
	}{
		{name: "ready", state: control.SetupStateReady},
		{name: "needs action", state: control.SetupStateNeedsAction, setup: func(t *testing.T, sourcePath string) {
			if os.Geteuid() == 0 {
				t.Skip("root can read a mode-zero fixture")
			}
			if err := os.Chmod(sourcePath, 0); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(sourcePath, 0o600) })
		}},
		{name: "unavailable", state: control.SetupStateUnavailable},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			sourcePath := filepath.Join(home, "Library", "Application Support", "AddressBook", "AddressBook-v22.abcddb")
			if test.name != "unavailable" {
				writeContactsSourceFixture(t, sourcePath)
			}
			if test.setup != nil {
				test.setup(t, sourcePath)
			}
			request := &trawlkit.Request{Paths: trawlkit.Paths{Archive: filepath.Join(t.TempDir(), "contacts.db")}}
			status, err := New().Status(context.Background(), request)
			t.Logf("synthetic status boundary request=%#v status=%#v error=%v", request, status, err)
			if err != nil {
				t.Fatal(err)
			}
			if status.State != "missing" || len(status.SetupRequirements) != 1 {
				t.Fatalf("status = %#v, want missing with one setup requirement", status)
			}
			requirement := status.SetupRequirements[0]
			if requirement.ID != "full_disk_access" || requirement.Kind != control.SetupKindFullDiskAccess || requirement.State != test.state {
				t.Fatalf("requirement = %#v, want state %q", requirement, test.state)
			}
			wantAction := control.SetupActionNone
			if test.state == control.SetupStateNeedsAction {
				wantAction = control.SetupActionOpenFullDiskAccess
			}
			if requirement.Action != wantAction || len(requirement.Command) != 0 {
				t.Fatalf("requirement action/command = %q/%#v, want %q/empty", requirement.Action, requirement.Command, wantAction)
			}
		})
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
	wantCommands := []string{"contacts_export", "doctor", "export_vcard", "import", "import_legacy", "metadata", "open", "person_annotate", "person_list", "person_show", "search", "status", "sync_apple", "sync_google", "who"}
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
	if got := manifest.Commands["sync_apple"]; got.Mutates || got.Store != "none" {
		t.Fatalf("sync_apple command = %#v", got)
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
	if len(search.Results) != 1 || search.Results[0].Who != "Ada Example" || search.Results[0].ShortRef == "" {
		t.Fatalf("search = %#v", search)
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
	code, stdout, stderr = runContacts(t, home, "doctor", "--json")
	if code != 0 {
		t.Fatalf("doctor code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, `"id": "archive"`) || !strings.Contains(stdout, `"state": "ok"`) {
		t.Fatalf("doctor stdout = %s", stdout)
	}
}

func TestDoctorChecksAppleSourceBeforeArchive(t *testing.T) {
	home := testHome(t)
	if runtime.GOOS == "darwin" {
		dir := filepath.Join(home, "Library", "Application Support", "AddressBook")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "AddressBook-v22.abcddb"), []byte("not sqlite"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	code, stdout, stderr := runContacts(t, home, "doctor", "--json")
	if code != 0 {
		t.Fatalf("doctor code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var doctor trawlkit.Doctor
	if err := json.Unmarshal([]byte(stdout), &doctor); err != nil {
		t.Fatalf("doctor JSON: %v\n%s", err, stdout)
	}
	t.Logf("raw doctor JSON: %s", stdout)
	t.Logf("doctor boundary: argv=%q exit=%d stderr=%q", []string{"doctor", "--json"}, code, stderr)
	if len(doctor.Checks) != 3 {
		t.Fatalf("doctor checks = %#v", doctor.Checks)
	}
	if doctor.Checks[0].ID != "apple_source" || doctor.Checks[1].ID != "archive" || doctor.Checks[2].ID != "schema" {
		t.Fatalf("doctor check order = %#v", doctor.Checks)
	}
	if doctor.Checks[1].Remedy != "" || doctor.Checks[2].Remedy != "" {
		t.Fatalf("archive remedies = %q, %q", doctor.Checks[1].Remedy, doctor.Checks[2].Remedy)
	}
	if runtime.GOOS == "darwin" && (doctor.Checks[0].State != "invalid" || doctor.Checks[0].Message != "Apple Contacts source is invalid") {
		t.Fatalf("invalid Apple source check = %#v", doctor.Checks[0])
	}
	if strings.Contains(stdout, "sync apple") || strings.Contains(stdout, "contacts_export") {
		t.Fatalf("doctor exposed the wrong remedy or unrelated source data: %s", stdout)
	}
}

func TestArchiveRemediesFollowAppleSource(t *testing.T) {
	for _, state := range []apple.SourceState{
		apple.SourceNeedsFullDiskAccess,
		apple.SourceUnavailable,
		apple.SourceInvalid,
	} {
		present := checkArchivePresent(&trawlkit.Request{}, state)
		schema := checkArchiveSchema(t.Context(), &trawlkit.Request{}, state)
		if present.Remedy != "" || schema.Remedy != "" {
			t.Fatalf("state %q remedies = %q, %q", state, present.Remedy, schema.Remedy)
		}
	}
	readyPresent := checkArchivePresent(&trawlkit.Request{}, apple.SourceReady)
	readySchema := checkArchiveSchema(t.Context(), &trawlkit.Request{}, apple.SourceReady)
	if readyPresent.Remedy != "trawl contacts import apple" || readySchema.Remedy != "trawl contacts import apple" {
		t.Fatalf("ready remedies = %q, %q", readyPresent.Remedy, readySchema.Remedy)
	}
}

func TestAppleSourceCheckStates(t *testing.T) {
	tests := []struct {
		name           string
		state          apple.SourceState
		archiveMissing bool
		wantState      string
		wantMessage    string
		wantRemedy     string
	}{
		{
			name:           "ready for first import",
			state:          apple.SourceReady,
			archiveMissing: true,
			wantState:      "ok",
			wantMessage:    "Apple Contacts source is ready for first import",
			wantRemedy:     "trawl contacts import apple",
		},
		{
			name:        "ready with archive",
			state:       apple.SourceReady,
			wantState:   "ok",
			wantMessage: "Apple Contacts source is readable",
		},
		{
			name:        "needs Full Disk Access",
			state:       apple.SourceNeedsFullDiskAccess,
			wantState:   "fail",
			wantMessage: "Apple Contacts needs Full Disk Access",
			wantRemedy:  "grant Full Disk Access to Trawl or the terminal running it in System Settings > Privacy & Security > Full Disk Access",
		},
		{
			name:        "unavailable",
			state:       apple.SourceUnavailable,
			wantState:   "missing",
			wantMessage: "Apple Contacts source is unavailable",
		},
		{
			name:        "invalid",
			state:       apple.SourceInvalid,
			wantState:   "invalid",
			wantMessage: "Apple Contacts source is invalid",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			check := appleSourceCheck(tt.state, tt.archiveMissing)
			if check.State != tt.wantState || check.Message != tt.wantMessage || check.Remedy != tt.wantRemedy {
				t.Fatalf("check = %#v", check)
			}
		})
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
	st, err := archive.Open(t.Context(), filepath.Join(home, ".opentrawl", "contacts", "contacts.db"))
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
}

func TestSyncPreviewVerbsPreserveOutput(t *testing.T) {
	home := testHome(t)
	if code, stdout, stderr := runContacts(t, home, "sync", "apple", "--json"); code != 0 {
		t.Fatalf("sync apple code=%d stdout=%s stderr=%s", code, stdout, stderr)
	} else if !strings.Contains(stdout, `"dry_run": true`) || !strings.Contains(stdout, "use import apple") {
		t.Fatalf("sync apple stdout = %s", stdout)
	}
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
	runMu.Lock()
	defer runMu.Unlock()
	t.Setenv("HOME", home)
	oldStdout := os.Stdout
	oldStderr := os.Stderr
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(&stdout, stdoutR)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(&stderr, stderrR)
	}()
	os.Stdout = stdoutW
	os.Stderr = stderrW
	code := trawlkit.Run(args, []trawlkit.Crawler{New()})
	_ = stdoutW.Close()
	_ = stderrW.Close()
	wg.Wait()
	os.Stdout = oldStdout
	os.Stderr = oldStderr
	_ = stdoutR.Close()
	_ = stderrR.Close()
	return code, stdout.String(), stderr.String()
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
