package calcrawl

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/calcrawl/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	ckoutput "github.com/opentrawl/opentrawl/trawlkit/output"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
	"google.golang.org/protobuf/proto"

	_ "github.com/mattn/go-sqlite3"
)

func TestOpenRecordCallsItsLoaderOnce(t *testing.T) {
	assertOpenRecordLoaderCall(t, "open_record.go", "loadOpenEvent")
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
	t.Setenv("HOME", t.TempDir())
	request := &trawlkit.Request{Paths: trawlkit.Paths{Archive: filepath.Join(t.TempDir(), "calendar.db")}}
	status, err := New().Status(context.Background(), request)
	if err != nil || status.State != "missing" || len(status.SetupRequirements) != 0 {
		t.Fatalf("archive-only status = %#v, %v", status, err)
	}
}

const coreDataUnixOffset = 978307200

func TestCrawlerSyncSearchOpenAndContacts(t *testing.T) {
	ctx := context.Background()
	stateRoot := setupCalendarFixture(t)
	paths := trawlkit.Paths{
		Archive: filepath.Join(stateRoot, "calendar", "calendar.db"),
		Config:  filepath.Join(stateRoot, "calendar", "config.toml"),
		Logs:    filepath.Join(stateRoot, "calendar", "logs"),
	}
	source := New()

	writeStore, err := ckstore.Open(ctx, ckstore.Options{Path: paths.Archive})
	if err != nil {
		t.Fatal(err)
	}
	syncReq := &trawlkit.Request{
		Store:    writeStore,
		Paths:    paths,
		Format:   ckoutput.Text,
		Out:      &bytes.Buffer{},
		Progress: func(trawlkit.Progress) {},
	}
	report, err := source.Sync(ctx, syncReq)
	if err == nil {
		records, recordsErr := source.ShortRefRecords(ctx, syncReq)
		if recordsErr != nil {
			err = recordsErr
		} else if _, assignErr := syncReq.AssignShortRefs(ctx, records); assignErr != nil {
			err = assignErr
		}
	}
	if closeErr := writeStore.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	if report.Added != 2 || report.Updated != 0 || report.Removed != 0 {
		t.Fatalf("sync report = %#v, want 2 added, 0 updated, 0 removed", report)
	}

	readStore := openReadStore(t, ctx, paths.Archive)
	searchReq := readRequest(readStore, paths)
	search, err := source.Search(ctx, searchReq, trawlkit.Query{Text: "planning", Limit: 20})
	fillTestShortRefs(t, ctx, searchReq, search.Results)
	_ = readStore.Close()
	if err != nil {
		t.Fatal(err)
	}
	if search.TotalMatches != 1 || len(search.Results) != 1 {
		t.Fatalf("search = %#v, want one result", search)
	}
	hit := search.Results[0]
	if hit.Ref != "calendar:event/11111111-1111-1111-1111-111111111111" || hit.ShortRef == "" {
		t.Fatalf("search hit refs = %#v", hit)
	}
	if hit.AnchorID != "summary" {
		t.Fatalf("search hit anchor = %q, want summary", hit.AnchorID)
	}
	if hit.Summary.Title != "Planning meeting - Room 1, 1 Example Street" || hit.Summary.Subtitle != "Work" {
		t.Fatalf("search hit summary = %#v", hit.Summary)
	}
	if len(hit.Evidence) == 0 || hit.Evidence[0].Label != "Title" || hit.Evidence[0].Field == nil || hit.Evidence[0].Field.Name != "summary" || !calendarHasMatchedRun(hit.Evidence[0].Field.Value) {
		t.Fatalf("search hit evidence = %#v", hit.Evidence)
	}
	if hit.Availability == nil || *hit.Availability != 2 {
		t.Fatalf("search hit availability = %#v, want raw 2", hit.Availability)
	}

	readStore = openReadStore(t, ctx, paths.Archive)
	var openOut bytes.Buffer
	err = source.Open(ctx, &trawlkit.Request{Store: readStore, Paths: paths, Format: ckoutput.JSON, Out: &openOut}, hit.Ref)
	_ = readStore.Close()
	if err != nil {
		t.Fatal(err)
	}
	var opened archive.EventDetail
	if err := json.Unmarshal(openOut.Bytes(), &opened); err != nil {
		t.Fatalf("open JSON: %v\n%s", err, openOut.String())
	}
	if opened.Calendar != "Work" || opened.Location == nil || opened.Location.Address != "1 Example Street" {
		t.Fatalf("opened event = %#v", opened)
	}
	if opened.Availability == nil || *opened.Availability != 2 {
		t.Fatalf("opened availability = %#v, want raw 2", opened.Availability)
	}

	readStore = openReadStore(t, ctx, paths.Archive)
	fullRecord, err := source.OpenRecord(ctx, &trawlkit.Request{Store: readStore, Paths: paths}, hit.Ref)
	_ = readStore.Close()
	if err != nil {
		t.Fatal(err)
	}
	readStore = openReadStore(t, ctx, paths.Archive)
	shortRecord, err := source.OpenRecord(ctx, &trawlkit.Request{Store: readStore, Paths: paths}, hit.ShortRef)
	_ = readStore.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(fullRecord, shortRecord) || shortRecord.OpenRef != hit.Ref || shortRecord.Data.GetTypeUrl() != "type.googleapis.com/trawl.source.calendar.open.v1.CalendarRecord" || shortRecord.Presentation == nil {
		t.Fatalf("open records full=%#v short=%#v", fullRecord, shortRecord)
	}
	load := func(ref string) archive.EventDetail {
		readStore = openReadStore(t, ctx, paths.Archive)
		value, loadErr := source.loadOpenEvent(ctx, &trawlkit.Request{Store: readStore, Paths: paths}, ref)
		_ = readStore.Close()
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		return value
	}
	captureLegacy := func(caseName, ref string) {
		goldens := map[string]string{"json": "6c6fab2a519fd4ef531916cdcf3e31d3ed8c3c2e489c3c6c06c87ed39e924a01", "text": "81320b22b77fd67d7fd23189283a0edb1b66ceff978621f46353b87e60543b8b"}
		for _, format := range []struct {
			name  string
			value ckoutput.Format
		}{{"json", ckoutput.JSON}, {"text", ckoutput.Text}} {
			readStore = openReadStore(t, ctx, paths.Archive)
			var stdout bytes.Buffer
			openErr := source.Open(ctx, &trawlkit.Request{Store: readStore, Paths: paths, Format: format.value, Out: &stdout}, ref)
			_ = readStore.Close()
			assertLegacyOpenGolden(t, stdout.Bytes(), openErr, goldens[format.name])
			writeLegacyOpenEvidence(t, "calendar", caseName, format.name, stdout.Bytes(), openErr)
			if openErr != nil {
				t.Fatal(openErr)
			}
		}
	}
	writeRuntimeOpenEvidence(t, "calendar", "full", hit.Ref, load(hit.Ref), fullRecord)
	writeRuntimeOpenEvidence(t, "calendar", "short", hit.ShortRef, load(hit.ShortRef), shortRecord)
	captureLegacy("full", hit.Ref)
	captureLegacy("short", hit.ShortRef)
	assertOpenRecordError := func(ref, want string) {
		readStore = openReadStore(t, ctx, paths.Archive)
		_, err = source.OpenRecord(ctx, &trawlkit.Request{Store: readStore, Paths: paths}, ref)
		_ = readStore.Close()
		var typed commandError
		if !errors.As(err, &typed) || typed.name != want {
			t.Fatalf("open %q error = %#v, want %q", ref, err, want)
		}
	}
	assertOpenRecordError("zzzzz", "unknown_short_ref")
	writeStore, err = ckstore.Open(ctx, ckstore.Options{Path: paths.Archive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writeStore.DB().ExecContext(ctx, `insert into short_refs(alias, full_ref, canonical_ref) values (?, ?, ?), (?, ?, ?)`, "zzzzz", hit.Ref, hit.Ref, "zzzzz", "calendar:event/22222222-2222-2222-2222-222222222222", "calendar:event/22222222-2222-2222-2222-222222222222"); err != nil {
		_ = writeStore.Close()
		t.Fatal(err)
	}
	if err := writeStore.Close(); err != nil {
		t.Fatal(err)
	}
	assertOpenRecordError("zzzzz", "ambiguous_short_ref")
	for ref, want := range map[string]string{
		"photos:asset/example": "invalid calendar event ref \"photos:asset/example\"",
		"calendar:event/":      "invalid calendar event ref \"calendar:event/\"",
	} {
		readStore = openReadStore(t, ctx, paths.Archive)
		_, err = source.OpenRecord(ctx, &trawlkit.Request{Store: readStore, Paths: paths}, ref)
		_ = readStore.Close()
		if err == nil || err.Error() != want {
			t.Fatalf("open %q error = %#v, want %q", ref, err, want)
		}
	}
	_, err = source.OpenRecord(ctx, &trawlkit.Request{Paths: trawlkit.Paths{Archive: paths.Archive + ".missing"}}, hit.Ref)
	var archiveFailure commandError
	if !errors.As(err, &archiveFailure) || archiveFailure.name != "archive" {
		t.Fatalf("missing archive error = %#v", err)
	}

	readStore = openReadStore(t, ctx, paths.Archive)
	contacts, err := source.ContactExport(ctx, readRequest(readStore, paths))
	_ = readStore.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(contacts.Contacts) != 2 || contacts.Contacts[0].PhoneNumbers[0] != "+15550100" {
		t.Fatalf("contacts = %#v", contacts)
	}
}

func calendarHasMatchedRun(runs []trawlkit.TextRun) bool {
	for _, run := range runs {
		if run.Matched {
			return true
		}
	}
	return false
}

func TestCalendarVerbsDeclareReadAndWriteAccess(t *testing.T) {
	manifest, err := trawlkit.Manifest(New())
	if err != nil {
		t.Fatal(err)
	}
	calendars := manifest.Commands["calendars"]
	if calendars.Mutates || calendars.Store != "read" {
		t.Fatalf("calendars command = %#v, want non-mutating read", calendars)
	}
	annotate := manifest.Commands["calendars_annotate"]
	if !annotate.Mutates || annotate.Store != "write" {
		t.Fatalf("calendars annotate command = %#v, want mutating write", annotate)
	}
	if !strings.Contains(annotate.Title, "writes to the local archive") {
		t.Fatalf("annotate help does not say it writes: %q", annotate.Title)
	}
}

func TestCalendarsReadVerbDoesNotMutateArchive(t *testing.T) {
	stateRoot, paths := syncedCalendarFixture(t)
	before := fileHash(t, paths.Archive)

	stdout, stderr, code := runCalcrawlForTest(t, stateRoot, "calendar", "calendars", "--json")
	if code != 0 {
		t.Fatalf("calendars code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	after := fileHash(t, paths.Archive)
	if before != after {
		t.Fatalf("calendars read verb mutated archive: before=%x after=%x", before, after)
	}
}

func TestCalendarsHintCommandAndAnnotationRoundTrip(t *testing.T) {
	stateRoot, _ := syncedCalendarFixture(t)

	stdout, stderr, code := runCalcrawlForTest(t, stateRoot, "calendar", "calendars", "--json")
	if code != 0 {
		t.Fatalf("calendars code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var listing calendarsOutput
	if err := json.Unmarshal([]byte(stdout), &listing); err != nil {
		t.Fatalf("calendars JSON: %v\n%s", err, stdout)
	}
	work := findCalendarRow(t, listing.Calendars, "Work")
	if work.AccountName != "iCloud" || work.AccountType != 1 || work.AccountTypeLabel != "EKSourceTypeExchange" || work.ExternalID != "work-calendar" || work.Disabled || work.EventCount != 1 {
		t.Fatalf("work calendar row = %#v", work)
	}
	if work.Meaning != "" || work.MeaningStatedAt != "" {
		t.Fatalf("new calendar meaning = %q/%q, want empty", work.Meaning, work.MeaningStatedAt)
	}
	hint := findCalendarHint(t, listing.Hints, work.ID)
	if hint.Prompt != `Ask the user what "Work" means to them, set CALENDAR_MEANING to their exact words.` {
		t.Fatalf("hint prompt = %q", hint.Prompt)
	}
	if !strings.Contains(hint.Command, "trawl calendar calendars annotate "+work.ID) {
		t.Fatalf("hint = %#v", hint)
	}

	t.Setenv("CALENDAR_MEANING", "Used for work planning with Alice")
	args := hintedCommandArgs(t, hint.Command)
	stdout, stderr, code = runCalcrawlWireForTest(t, stateRoot, args...)
	if code != 0 {
		t.Fatalf("hinted command code=%d stdout=%s stderr=%s args=%#v", code, stdout, stderr, args)
	}

	stdout, stderr, code = runCalcrawlForTest(t, stateRoot, "calendar", "calendars", "--json")
	if code != 0 {
		t.Fatalf("calendars after annotate code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if err := json.Unmarshal([]byte(stdout), &listing); err != nil {
		t.Fatalf("calendars JSON after annotate: %v\n%s", err, stdout)
	}
	work = findCalendarRow(t, listing.Calendars, "Work")
	if work.Meaning != "Used for work planning with Alice" || work.MeaningStatedAt != time.Now().UTC().Format("2006-01-02") {
		t.Fatalf("annotated work calendar = %#v", work)
	}
}

func TestCalendarsAnnotationPreservesMeaningWhitespace(t *testing.T) {
	stateRoot, _ := syncedCalendarFixture(t)
	stdout, stderr, code := runCalcrawlForTest(t, stateRoot, "calendar", "calendars", "--json")
	if code != 0 {
		t.Fatalf("calendars code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var listing calendarsOutput
	if err := json.Unmarshal([]byte(stdout), &listing); err != nil {
		t.Fatalf("calendars JSON: %v\n%s", err, stdout)
	}
	work := findCalendarRow(t, listing.Calendars, "Work")
	hint := findCalendarHint(t, listing.Hints, work.ID)

	wantMeaning := "  Used for work planning with Alice  "
	t.Setenv("CALENDAR_MEANING", wantMeaning)
	args := hintedCommandArgs(t, hint.Command)
	stdout, stderr, code = runCalcrawlWireForTest(t, stateRoot, args...)
	if code != 0 {
		t.Fatalf("hinted command code=%d stdout=%s stderr=%s args=%#v", code, stdout, stderr, args)
	}

	stdout, stderr, code = runCalcrawlForTest(t, stateRoot, "calendar", "calendars", "--json")
	if code != 0 {
		t.Fatalf("calendars after annotate code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if err := json.Unmarshal([]byte(stdout), &listing); err != nil {
		t.Fatalf("calendars JSON after annotate: %v\n%s", err, stdout)
	}
	work = findCalendarRow(t, listing.Calendars, "Work")
	if work.Meaning != wantMeaning {
		t.Fatalf("annotated work calendar meaning = %q, want %q", work.Meaning, wantMeaning)
	}
}

func readRequest(st *ckstore.Store, paths trawlkit.Paths) *trawlkit.Request {
	return &trawlkit.Request{
		Store:  st,
		Paths:  paths,
		Format: ckoutput.Text,
		Out:    &bytes.Buffer{},
	}
}

func syncedCalendarFixture(t *testing.T) (string, trawlkit.Paths) {
	t.Helper()
	ctx := context.Background()
	stateRoot := setupCalendarFixture(t)
	paths := trawlkit.Paths{
		Archive: filepath.Join(stateRoot, "calendar", "calendar.db"),
		Config:  filepath.Join(stateRoot, "calendar", "config.toml"),
		Logs:    filepath.Join(stateRoot, "calendar", "logs"),
	}
	writeStore, err := ckstore.Open(ctx, ckstore.Options{Path: paths.Archive})
	if err != nil {
		t.Fatal(err)
	}
	_, err = New().Sync(ctx, &trawlkit.Request{
		Store:    writeStore,
		Paths:    paths,
		Format:   ckoutput.Text,
		Out:      &bytes.Buffer{},
		Progress: func(trawlkit.Progress) {},
	})
	if closeErr := writeStore.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	return stateRoot, paths
}

func runCalcrawlForTest(t *testing.T, stateRoot string, args ...string) (string, string, int) {
	t.Helper()
	_ = stateRoot
	return runCalcrawlArgsForTest(t, args...)
}

func runCalcrawlWireForTest(t *testing.T, stateRoot string, args ...string) (string, string, int) {
	t.Helper()
	t.Setenv("TRAWLKIT_STATE_ROOT", stateRoot)
	t.Setenv("TRAWLKIT_RUN_ID", "test")
	wireArgs := append([]string{trawlkit.HiddenWireSubcommand}, args...)
	return runCalcrawlArgsForTest(t, wireArgs...)
}

func runCalcrawlArgsForTest(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	oldStdout := os.Stdout
	oldStderr := os.Stderr
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = stdoutWriter
	os.Stderr = stderrWriter
	code := trawlkit.Run(args, []trawlkit.Crawler{New()})
	_ = stdoutWriter.Close()
	_ = stderrWriter.Close()
	os.Stdout = oldStdout
	os.Stderr = oldStderr
	var stdout, stderr bytes.Buffer
	_, _ = stdout.ReadFrom(stdoutReader)
	_, _ = stderr.ReadFrom(stderrReader)
	_ = stdoutReader.Close()
	_ = stderrReader.Close()
	return stdout.String(), stderr.String(), code
}

func fileHash(t *testing.T, path string) [32]byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(data)
}

func findCalendarRow(t *testing.T, rows []calendarRow, title string) calendarRow {
	t.Helper()
	for _, row := range rows {
		if row.Title == title {
			return row
		}
	}
	t.Fatalf("calendar %q not found in %#v", title, rows)
	return calendarRow{}
}

func findCalendarHint(t *testing.T, hints []calendarHint, calendarID string) calendarHint {
	t.Helper()
	for _, hint := range hints {
		if hint.CalendarID == calendarID {
			return hint
		}
	}
	t.Fatalf("calendar hint %q not found in %#v", calendarID, hints)
	return calendarHint{}
}

func hintedCommandArgs(t *testing.T, command string) []string {
	t.Helper()
	tokens := parseHintCommand(t, command)
	if len(tokens) < 2 || tokens[0] != "trawl" || tokens[1] != "calendar" {
		t.Fatalf("hint command = %#v, want trawl calendar ...", tokens)
	}
	args := make([]string, 0, len(tokens)-1)
	for _, token := range tokens[1:] {
		args = append(args, os.ExpandEnv(token))
	}
	return args
}

func parseHintCommand(t *testing.T, command string) []string {
	t.Helper()
	var tokens []string
	var current strings.Builder
	inQuote := false
	for _, r := range command {
		switch r {
		case '"':
			inQuote = !inQuote
		case ' ', '\t', '\n':
			if inQuote {
				current.WriteRune(r)
				continue
			}
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}
	if inQuote {
		t.Fatalf("unclosed quote in hint command %q", command)
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
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

func setupCalendarFixture(t *testing.T) string {
	t.Helper()
	home, err := os.MkdirTemp("/private/tmp", "trawl-152-calcrawl-home-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)
	t.Setenv("TZ", "UTC")
	dir := filepath.Join(home, "Library", "Group Containers", "group.com.apple.calendar")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "Calendar.sqlitedb")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	createCalendarSchema(t, db)
	insertCalendarRows(t, db)
	if err := os.Chtimes(path, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(home, ".opentrawl")
}

func createCalendarSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, stmt := range calendarSchemaStatements() {
		mustExec(t, db, stmt)
	}
}

func calendarSchemaStatements() []string {
	return []string{
		`create table Store (ROWID integer primary key, name text, type integer, disabled integer)`,
		`create table Calendar (ROWID integer primary key, store_id integer, title text, type integer, external_id text)`,
		`create table CalendarItem (
			ROWID integer primary key, summary text, description text, start_date real, end_date real,
			start_tz text, end_tz text, all_day integer, calendar_id integer, organizer_id integer,
			status integer, url text, has_recurrences integer, has_attendees integer, UUID text,
			unique_identifier text, entity_type integer, location_id integer, availability integer
		)`,
		`create table Participant (
			ROWID integer primary key, entity_type integer, type integer, status integer, role integer,
			identity_id integer, owner_id integer, email text, phone_number text, is_self integer,
			comment text
		)`,
		`create table Identity (ROWID integer primary key, display_name text, address text, first_name text, last_name text)`,
		`create table Location (ROWID integer primary key, title text, address text, item_owner_id integer)`,
	}
}

func insertCalendarRows(t *testing.T, db *sql.DB) {
	t.Helper()
	data := calendarFixtureData()
	for _, row := range data.Stores {
		mustExec(t, db, `insert into Store(ROWID, name, type, disabled) values (?, ?, ?, ?)`, row.RowID, row.Name, row.Type, row.Disabled)
	}
	for _, row := range data.Calendars {
		mustExec(t, db, `insert into Calendar(ROWID, store_id, title, type, external_id) values (?, ?, ?, ?, ?)`, row.RowID, row.StoreID, row.Title, row.Type, row.ExternalID)
	}
	for _, row := range data.Events {
		insertCalendarItem(t, db, row)
	}
	for _, row := range data.Tasks {
		insertCalendarItem(t, db, row)
	}
	for _, row := range data.Identities {
		mustExec(t, db, `insert into Identity(ROWID, display_name, address, first_name, last_name) values (?, ?, ?, ?, ?)`, row.RowID, row.DisplayName, row.Address, row.FirstName, row.LastName)
	}
	for _, row := range data.Participants {
		mustExec(t, db, `insert into Participant(ROWID, entity_type, type, status, role, identity_id, owner_id, email, phone_number, is_self, comment) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, row.RowID, row.EntityType, row.Type, row.Status, row.Role, row.IdentityID, row.OwnerID, row.Email, row.PhoneNumber, row.IsSelf, row.Comment)
	}
	for _, row := range data.Locations {
		mustExec(t, db, `insert into Location(ROWID, title, address, item_owner_id) values (?, ?, ?, ?)`, row.RowID, row.Title, row.Address, row.ItemOwnerID)
	}
}

type calendarFixtureDataSet struct {
	Stores       []calendarStoreFixture
	Calendars    []calendarFixtureCalendar
	Events       []calendarFixtureCalendarItem
	Tasks        []calendarFixtureCalendarItem
	Identities   []calendarFixtureIdentity
	Participants []calendarFixtureParticipant
	Locations    []calendarFixtureLocation
}

type calendarStoreFixture struct {
	RowID    int
	Name     string
	Type     int
	Disabled int
}

type calendarFixtureCalendar struct {
	RowID      int
	StoreID    int
	Title      string
	Type       int
	ExternalID string
}

type calendarFixtureCalendarItem struct {
	RowID            int
	Summary          string
	Description      string
	StartCore        float64
	EndCore          float64
	StartTZ          string
	EndTZ            string
	AllDay           int
	CalendarID       int
	OrganizerID      int
	Status           int
	URL              string
	HasRecurrences   int
	HasAttendees     int
	UUID             string
	UniqueIdentifier string
	EntityType       int
	LocationID       int
	Availability     int
}

type calendarFixtureIdentity struct {
	RowID       int
	DisplayName string
	Address     string
	FirstName   string
	LastName    string
}

type calendarFixtureParticipant struct {
	RowID       int
	EntityType  int
	Type        int
	Status      int
	Role        int
	IdentityID  int
	OwnerID     int
	Email       string
	PhoneNumber string
	IsSelf      int
	Comment     string
}

type calendarFixtureLocation struct {
	RowID       int
	Title       string
	Address     string
	ItemOwnerID int
}

func calendarFixtureData() calendarFixtureDataSet {
	return calendarFixtureDataSet{
		Stores: []calendarStoreFixture{
			{RowID: 1, Name: "iCloud", Type: 1, Disabled: 0},
			{RowID: 2, Name: "Subscribed Calendars", Type: 4, Disabled: 0},
			{RowID: 3, Name: "Reminders", Type: 3, Disabled: 0},
		},
		Calendars: []calendarFixtureCalendar{
			{RowID: 10, StoreID: 1, Title: "Work", Type: 1, ExternalID: "work-calendar"},
			{RowID: 11, StoreID: 2, Title: "Holidays", Type: 3, ExternalID: "holidays-calendar"},
			{RowID: 12, StoreID: 3, Title: "Reminders list", Type: 3, ExternalID: "reminders-calendar"},
		},
		Events: []calendarFixtureCalendarItem{
			{
				RowID: 100, Summary: "Planning meeting", Description: "Discuss launch with Alice.",
				StartCore: coreDate(time.Date(2026, 3, 4, 9, 0, 0, 0, time.UTC)), EndCore: coreDate(time.Date(2026, 3, 4, 9, 30, 0, 0, time.UTC)),
				StartTZ: "Europe/Amsterdam", EndTZ: "Europe/Amsterdam", AllDay: 0, CalendarID: 10, OrganizerID: 1000,
				Status: 1, URL: "https://example.com/event", HasRecurrences: 1, HasAttendees: 1,
				UUID: "11111111-1111-1111-1111-111111111111", UniqueIdentifier: "event-planning", EntityType: 2, LocationID: 900, Availability: 2,
			},
			{
				RowID: 101, Summary: "Public holiday", Description: "Subscribed holiday.",
				StartCore: coreDate(time.Date(2026, 5, 4, 22, 0, 0, 0, time.UTC)), EndCore: coreDate(time.Date(2026, 5, 5, 22, 0, 0, 0, time.UTC)),
				StartTZ: "Europe/Amsterdam", EndTZ: "Europe/Amsterdam", AllDay: 1, CalendarID: 11, OrganizerID: 0,
				Status: 0, URL: "", HasRecurrences: 0, HasAttendees: 1,
				UUID: "22222222-2222-2222-2222-222222222222", UniqueIdentifier: "event-holiday", EntityType: 2, LocationID: 901, Availability: 1,
			},
		},
		Tasks: []calendarFixtureCalendarItem{
			{
				RowID: 103, Summary: "Task row", Description: "", StartCore: 0, EndCore: 0,
				StartTZ: "UTC", EndTZ: "UTC", AllDay: 0, CalendarID: 10, OrganizerID: 0,
				Status: 1, URL: "", HasRecurrences: 0, HasAttendees: 0,
				UUID: "44444444-4444-4444-4444-444444444444", UniqueIdentifier: "task-row", EntityType: 1, LocationID: 0, Availability: 0,
			},
		},
		Identities: []calendarFixtureIdentity{
			{RowID: 500, DisplayName: "Alice Example", Address: "alice@example.com", FirstName: "Alice", LastName: "Example"},
			{RowID: 501, DisplayName: "Bob Example", Address: "bob@example.com", FirstName: "Bob", LastName: "Example"},
			{RowID: 502, DisplayName: "Holiday Bot", Address: "holidays@example.com", FirstName: "Holiday", LastName: "Bot"},
		},
		Participants: []calendarFixtureParticipant{
			{RowID: 1000, EntityType: 2, Type: 1, Status: 2, Role: 3, IdentityID: 500, OwnerID: 100, Email: "alice@example.com", PhoneNumber: "+15550100", IsSelf: 1, Comment: ""},
			{RowID: 1001, EntityType: 2, Type: 1, Status: 4, Role: 1, IdentityID: 501, OwnerID: 100, Email: "bob@example.com", PhoneNumber: "+15550101", IsSelf: 0, Comment: ""},
			{RowID: 1002, EntityType: 2, Type: 1, Status: 2, Role: 1, IdentityID: 502, OwnerID: 101, Email: "holidays@example.com", PhoneNumber: "", IsSelf: 0, Comment: ""},
		},
		Locations: []calendarFixtureLocation{
			{RowID: 900, Title: "Room 1", Address: "1 Example Street", ItemOwnerID: 100},
			{RowID: 901, Title: "Netherlands", Address: "", ItemOwnerID: 101},
		},
	}
}

func insertCalendarItem(t *testing.T, db *sql.DB, row calendarFixtureCalendarItem) {
	t.Helper()
	mustExec(t, db, `insert into CalendarItem(
		ROWID, summary, description, start_date, end_date, start_tz, end_tz, all_day,
		calendar_id, organizer_id, status, url, has_recurrences, has_attendees,
		UUID, unique_identifier, entity_type, location_id, availability
	) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		row.RowID, row.Summary, row.Description, row.StartCore, row.EndCore, row.StartTZ, row.EndTZ, row.AllDay,
		row.CalendarID, row.OrganizerID, row.Status, row.URL, row.HasRecurrences, row.HasAttendees,
		row.UUID, row.UniqueIdentifier, row.EntityType, row.LocationID, row.Availability)
}

func coreDate(t time.Time) float64 {
	return float64(t.Unix() - coreDataUnixOffset)
}

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}
