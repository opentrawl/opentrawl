package wacrawl

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	"github.com/opentrawl/opentrawl/trawlkit/output"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"

	_ "github.com/mattn/go-sqlite3"
)

func TestSetupRequirementMapping(t *testing.T) {
	requirements := whatsappSetupRequirements(context.Background(), filepath.Join(t.TempDir(), "missing-whatsapp"))
	if len(requirements) != 2 {
		t.Fatalf("requirements = %#v", requirements)
	}
	pairing, access := requirements[0], requirements[1]
	if pairing.ID != "pairing" || pairing.Kind != control.SetupKindPairing || pairing.State != control.SetupStateNeedsAction || pairing.Action != control.SetupActionRunCommand {
		t.Fatalf("pairing = %#v", pairing)
	}
	if got := pairing.Command; len(got) != 3 || got[0] != "/usr/bin/open" || got[1] != "-b" || got[2] != "net.whatsapp.WhatsApp" {
		t.Fatalf("pairing command = %#v", got)
	}
	if access.ID != "full_disk_access" || access.Kind != control.SetupKindFullDiskAccess || access.State != control.SetupStateNeedsAction || access.Action != control.SetupActionOpenFullDiskAccess || len(access.Command) != 0 {
		t.Fatalf("full disk access = %#v", access)
	}
	invalidPath := filepath.Join(t.TempDir(), "invalid-whatsapp")
	if err := os.WriteFile(invalidPath, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	invalid := whatsappSetupRequirements(context.Background(), invalidPath)
	for _, requirement := range invalid {
		if requirement.State != control.SetupStateUnavailable || requirement.Action != control.SetupActionNone || len(requirement.Command) != 0 {
			t.Fatalf("invalid source requirement = %#v", requirement)
		}
	}
	readyRoot := t.TempDir()
	createDesktopFixture(t, readyRoot)
	ready := whatsappSetupRequirements(context.Background(), readyRoot)
	for _, requirement := range ready {
		if requirement.State != control.SetupStateReady || requirement.Action != control.SetupActionNone || len(requirement.Command) != 0 {
			t.Fatalf("ready source requirement = %#v", requirement)
		}
	}
	if os.Geteuid() == 0 {
		t.Skip("root can read a mode-zero fixture")
	}
	deniedRoot := t.TempDir()
	createDesktopFixture(t, deniedRoot)
	chatPath := filepath.Join(deniedRoot, "ChatStorage.sqlite")
	if err := os.Chmod(chatPath, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(chatPath, 0o600) })
	denied := whatsappSetupRequirements(context.Background(), deniedRoot)
	if denied[0].ID != "pairing" || denied[0].Kind != control.SetupKindPairing || denied[0].State != control.SetupStateReady || denied[0].Action != control.SetupActionNone || len(denied[0].Command) != 0 {
		t.Fatalf("permission-denied pairing requirement = %#v", denied[0])
	}
	if denied[1].ID != "full_disk_access" || denied[1].Kind != control.SetupKindFullDiskAccess || denied[1].State != control.SetupStateNeedsAction || denied[1].Action != control.SetupActionOpenFullDiskAccess || len(denied[1].Command) != 0 {
		t.Fatalf("permission-denied access requirement = %#v", denied[1])
	}
}

func TestCrawlerCoreMethods(t *testing.T) {
	ctx := context.Background()
	sourceRoot := t.TempDir()
	createDesktopFixture(t, sourceRoot)
	stateRoot := t.TempDir()
	paths := trawlkit.Paths{
		Archive: filepath.Join(stateRoot, "whatsapp", "whatsapp.db"),
		Config:  filepath.Join(stateRoot, "whatsapp", "config.toml"),
		Logs:    filepath.Join(stateRoot, "whatsapp", "logs"),
	}
	crawler := New()
	crawler.cfg.Source = sourceRoot
	crawler.cfg.CopyMedia = true

	writeStore, err := ckstore.Open(ctx, ckstore.Options{Path: paths.Archive})
	if err != nil {
		t.Fatal(err)
	}
	syncReq := &trawlkit.Request{
		Store:    writeStore,
		Paths:    paths,
		Format:   output.Text,
		Out:      &bytes.Buffer{},
		Progress: func(trawlkit.Progress) {},
	}
	report, err := crawler.Sync(ctx, syncReq)
	if err == nil {
		var records []trawlkit.ShortRefRecord
		records, err = crawler.ShortRefRecords(ctx, syncReq)
		if err == nil {
			_, err = syncReq.AssignShortRefs(ctx, records)
		}
	}
	if closeErr := writeStore.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	if report.Added != 3 || report.Updated != 0 || report.Removed != 0 {
		t.Fatalf("sync report = %#v, want 3 added and zero updates/removals", report)
	}

	readStore := openReadStore(t, ctx, paths.Archive)
	status, err := crawler.Status(ctx, readRequest(readStore, paths))
	_ = readStore.Close()
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "ok" || !countPresent(status.Counts, "messages", 3) || !countPresent(status.Counts, "participants", 1) {
		t.Fatalf("status = %#v", status)
	}

	readStore = openReadStore(t, ctx, paths.Archive)
	searchReq := readRequest(readStore, paths)
	search, err := crawler.Search(ctx, searchReq, trawlkit.Query{Text: "launch", Limit: 20})
	fillTestShortRefs(t, ctx, searchReq, search.Results)
	_ = readStore.Close()
	if err != nil {
		t.Fatal(err)
	}
	if search.TotalMatches != 1 || len(search.Results) != 1 {
		t.Fatalf("search = %#v, want one result", search)
	}
	hit := search.Results[0]
	if hit.Ref != "whatsapp:msg/group-image" || hit.ShortRef == "" || hit.Who != "Alice Example" || hit.Where != "Launch Group" {
		t.Fatalf("search hit = %#v", hit)
	}

	readStore = openReadStore(t, ctx, paths.Archive)
	who, err := crawler.Who(ctx, readRequest(readStore, paths), "Alice")
	_ = readStore.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(who) != 1 || who[0].Who != "Alice Example" || who[0].Messages != 1 {
		t.Fatalf("who = %#v", who)
	}

	readStore = openReadStore(t, ctx, paths.Archive)
	contacts, err := crawler.ContactExport(ctx, readRequest(readStore, paths))
	_ = readStore.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(contacts.Contacts) != 2 || !contactPresent(contacts.Contacts, "Alice Example", "+15550222") || !contactPresent(contacts.Contacts, "Bob Example", "+15550111") {
		t.Fatalf("contacts = %#v", contacts)
	}

	readStore = openReadStore(t, ctx, paths.Archive)
	var openOut bytes.Buffer
	err = crawler.Open(ctx, &trawlkit.Request{Store: readStore, Paths: paths, Format: output.JSON, Out: &openOut}, hit.ShortRef)
	_ = readStore.Close()
	if err != nil {
		t.Fatal(err)
	}
	var opened openEnvelope
	if err := json.Unmarshal(openOut.Bytes(), &opened); err != nil {
		t.Fatalf("open JSON: %v\n%s", err, openOut.String())
	}
	if opened.Ref != "whatsapp:msg/group-image" || opened.Message.Text != "launch now" || opened.Message.Media == nil {
		t.Fatalf("opened = %#v", opened)
	}
	if len(opened.Participants) != 1 || opened.Participants[0] != "Alice Example" {
		t.Fatalf("participants = %#v, want Alice Example", opened.Participants)
	}

	readStore = openReadStore(t, ctx, paths.Archive)
	openOut.Reset()
	err = crawler.Open(ctx, &trawlkit.Request{Store: readStore, Paths: paths, Format: output.Text, Out: &openOut}, hit.Ref)
	_ = readStore.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Participants: Alice Example",
		"Context: 1 messages around this one.",
	} {
		if !strings.Contains(openOut.String(), want) {
			t.Fatalf("open text missing %q:\n%s", want, openOut.String())
		}
	}
}

func TestMetadataManifestListsRegisteredVerbs(t *testing.T) {
	stateRootForRun(t)
	code, stdout, stderr := captureRun(t, []string{"metadata", "--json"}, New())
	if code != 0 {
		t.Fatalf("metadata code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var manifest control.Manifest
	if err := json.Unmarshal([]byte(stdout), &manifest); err != nil {
		t.Fatalf("metadata JSON: %v\n%s", err, stdout)
	}
	// chats is now the shared trawlkit capability; the old bespoke unread verb
	// collapsed into chats --unread.
	for _, capability := range []string{"sync", "search", "who", "open", "contacts_export", "short_refs", "chats", "messages"} {
		if !stringSliceContains(manifest.Capabilities, capability) {
			t.Fatalf("capabilities = %#v, missing %s", manifest.Capabilities, capability)
		}
	}
	for _, command := range []string{"metadata", "status", "doctor", "sync", "search", "who", "open", "contacts_export", "chats", "messages"} {
		if _, ok := manifest.Commands[command]; !ok {
			t.Fatalf("commands = %#v, missing %s", manifest.Commands, command)
		}
	}
	if manifest.SchemaVersion != control.RunnerManifestVersion {
		t.Fatalf("schema version = %d, want %d", manifest.SchemaVersion, control.RunnerManifestVersion)
	}
}

func readRequest(st *ckstore.Store, paths trawlkit.Paths) *trawlkit.Request {
	return &trawlkit.Request{Store: st, Paths: paths, Format: output.Text, Out: &bytes.Buffer{}}
}

func fillTestShortRefs(t *testing.T, ctx context.Context, req *trawlkit.Request, hits []trawlkit.Hit) {
	t.Helper()
	refs := make([]string, 0, len(hits))
	for _, hit := range hits {
		refs = append(refs, hit.Ref)
	}
	aliases, err := req.ShortRefAliases(ctx, refs)
	if err != nil {
		t.Fatal(err)
	}
	for i := range hits {
		hits[i].ShortRef = aliases[hits[i].Ref]
	}
}

func openReadStore(t *testing.T, ctx context.Context, path string) *ckstore.Store {
	t.Helper()
	st, err := ckstore.OpenReadOnly(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func countPresent(counts []control.Count, id string, value int64) bool {
	for _, count := range counts {
		if count.ID == id && count.Value == value {
			return true
		}
	}
	return false
}

func contactPresent(contacts []control.Contact, name, phone string) bool {
	for _, contact := range contacts {
		if contact.DisplayName == name && len(contact.PhoneNumbers) == 1 && contact.PhoneNumbers[0] == phone {
			return true
		}
	}
	return false
}

func captureRun(t *testing.T, args []string, crawler *Crawler) (int, string, string) {
	t.Helper()
	origStdout := os.Stdout
	origStderr := os.Stderr
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = outW
	os.Stderr = errW
	code := trawlkit.Run(args, []trawlkit.Crawler{crawler})
	_ = outW.Close()
	_ = errW.Close()
	os.Stdout = origStdout
	os.Stderr = origStderr
	stdout, _ := io.ReadAll(outR)
	stderr, _ := io.ReadAll(errR)
	_ = outR.Close()
	_ = errR.Close()
	return code, string(stdout), string(stderr)
}

func stateRootForRun(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return filepath.Join(home, ".opentrawl")
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func createDesktopFixture(t *testing.T, dir string) {
	t.Helper()
	chat, err := sql.Open("sqlite3", filepath.Join(dir, "ChatStorage.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = chat.Close() }()
	mustExec(t, chat, `
create table ZWACHATSESSION (Z_PK integer primary key, ZCONTACTJID varchar, ZPARTNERNAME varchar, ZLASTMESSAGEDATE timestamp, ZUNREADCOUNT integer, ZARCHIVED integer, ZREMOVED integer, ZHIDDEN integer, ZSESSIONTYPE integer);
create table ZWAGROUPINFO (Z_PK integer primary key, ZCHATSESSION integer, ZOWNERJID varchar, ZCREATIONDATE timestamp);
create table ZWAGROUPMEMBER (Z_PK integer primary key, ZCHATSESSION integer, ZMEMBERJID varchar, ZCONTACTNAME varchar, ZFIRSTNAME varchar, ZISADMIN integer, ZISACTIVE integer);
create table ZWAMEDIAITEM (Z_PK integer primary key, ZMESSAGE integer, ZMEDIALOCALPATH varchar, ZMEDIAURL varchar, ZTITLE varchar, ZVCARDNAME varchar, ZFILESIZE integer);
create table ZWAMESSAGE (Z_PK integer primary key, ZCHATSESSION integer, ZGROUPMEMBER integer, ZMEDIAITEM integer, ZSTANZAID varchar, ZISFROMME integer, ZMESSAGEDATE timestamp, ZTEXT varchar, ZMESSAGETYPE integer, ZSTARRED integer, ZFROMJID varchar, ZTOJID varchar, ZPUSHNAME varchar);
insert into ZWACHATSESSION values (1, '15550111@s.whatsapp.net', 'Bob Example', 700000020, 0, 0, 0, 0, 0);
insert into ZWACHATSESSION values (2, '123@g.us', 'Launch Group', 700000010, 2, 0, 0, 0, 1);
insert into ZWAGROUPINFO values (1, 2, 'owner@s.whatsapp.net', 699999000);
insert into ZWAGROUPMEMBER values (1, 2, '222@lid', 'Alice Example', 'Alice', 1, 1);
insert into ZWAMEDIAITEM values (1, 3, 'Media/123@g.us/a/test.jpg', 'https://example.invalid/media.enc', 'launch image', '', 42);
insert into ZWAMESSAGE values (1, 1, null, null, 'dm-in', 0, 700000000, 'hello from bob', 0, 0, '15550111@s.whatsapp.net', '', 'Bob Example');
insert into ZWAMESSAGE values (2, 1, null, null, 'dm-out', 1, 700000001, 'roger that', 0, 0, '', '15550111@s.whatsapp.net', '');
insert into ZWAMESSAGE values (3, 2, 1, 1, 'group-image', 0, 700000002, 'launch now', 1, 1, '123@g.us', '', 'Alice Example');
`)
	contacts, err := sql.Open("sqlite3", filepath.Join(dir, "ContactsV2.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = contacts.Close() }()
	mustExec(t, contacts, `
create table ZWAADDRESSBOOKCONTACT (ZWHATSAPPID varchar, ZPHONENUMBER varchar, ZFULLNAME varchar, ZGIVENNAME varchar, ZLASTNAME varchar, ZBUSINESSNAME varchar, ZUSERNAME varchar, ZLID varchar, ZABOUTTEXT varchar, ZLASTUPDATED timestamp);
insert into ZWAADDRESSBOOKCONTACT values ('15550111@s.whatsapp.net', '+15550111', 'Bob Example', 'Bob', 'Example', '', '', '', '', 700000000);
insert into ZWAADDRESSBOOKCONTACT values ('222@s.whatsapp.net', '+15550222', 'Alice Example', 'Alice', 'Example', '', '', '222', '', 700000000);
`)
	mediaPath := filepath.Join(dir, "Media", "123@g.us", "a", "test.jpg")
	if err := os.MkdirAll(filepath.Dir(mediaPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mediaPath, []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustExec(t *testing.T, db *sql.DB, query string) {
	t.Helper()
	if _, err := db.Exec(query); err != nil {
		t.Fatal(err)
	}
}

func TestParseTimeDateOnlyUsesLocalMidnight(t *testing.T) {
	fixed := time.FixedZone("UTC+2", 2*60*60)
	previous := time.Local
	time.Local = fixed
	t.Cleanup(func() { time.Local = previous })

	got, err := parseTime("2026-07-04")
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 7, 3, 22, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("parseTime = %v, want %v", got, want)
	}
}
