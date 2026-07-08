package calcrawl

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openclaw/crawlkit"
	ckoutput "github.com/openclaw/crawlkit/output"
	ckstore "github.com/openclaw/crawlkit/store"
	"github.com/opentrawl/opentrawl/calcrawl/internal/archive"

	_ "github.com/mattn/go-sqlite3"
)

const coreDataUnixOffset = 978307200

func TestCrawlerSyncSearchOpenAndContacts(t *testing.T) {
	ctx := context.Background()
	stateRoot := setupCalendarFixture(t)
	paths := crawlkit.Paths{
		Archive: filepath.Join(stateRoot, "calendar", "calendar.db"),
		Config:  filepath.Join(stateRoot, "calendar", "config.toml"),
		Logs:    filepath.Join(stateRoot, "calendar", "logs"),
	}
	source := New()

	writeStore, err := ckstore.Open(ctx, ckstore.Options{Path: paths.Archive})
	if err != nil {
		t.Fatal(err)
	}
	report, err := source.Sync(ctx, &crawlkit.Request{
		Store:    writeStore,
		Paths:    paths,
		Format:   ckoutput.Text,
		Out:      &bytes.Buffer{},
		Progress: func(crawlkit.Progress) {},
	})
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
	search, err := source.Search(ctx, readRequest(readStore, paths), crawlkit.Query{Text: "planning", Limit: 20})
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
	if hit.Who != "Alice Example" || hit.Where != "Room 1" {
		t.Fatalf("search hit who/where = %q/%q", hit.Who, hit.Where)
	}
	if hit.Calendar != "Work" {
		t.Fatalf("search hit calendar = %q, want Work", hit.Calendar)
	}
	if hit.Availability == nil || *hit.Availability != 2 {
		t.Fatalf("search hit availability = %#v, want raw 2", hit.Availability)
	}

	readStore = openReadStore(t, ctx, paths.Archive)
	var openOut bytes.Buffer
	err = source.Open(ctx, &crawlkit.Request{Store: readStore, Paths: paths, Format: ckoutput.JSON, Out: &openOut}, hit.Ref)
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
	contacts, err := source.ContactExport(ctx, readRequest(readStore, paths))
	_ = readStore.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(contacts.Contacts) != 2 || contacts.Contacts[0].PhoneNumbers[0] != "+15550100" {
		t.Fatalf("contacts = %#v", contacts)
	}
}

func TestCalendarVerbsDeclareReadAndWriteAccess(t *testing.T) {
	manifest, err := crawlkit.Manifest(New())
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

func readRequest(st *ckstore.Store, paths crawlkit.Paths) *crawlkit.Request {
	return &crawlkit.Request{
		Store:  st,
		Paths:  paths,
		Format: ckoutput.Text,
		Out:    &bytes.Buffer{},
	}
}

func syncedCalendarFixture(t *testing.T) (string, crawlkit.Paths) {
	t.Helper()
	ctx := context.Background()
	stateRoot := setupCalendarFixture(t)
	paths := crawlkit.Paths{
		Archive: filepath.Join(stateRoot, "calendar", "calendar.db"),
		Config:  filepath.Join(stateRoot, "calendar", "config.toml"),
		Logs:    filepath.Join(stateRoot, "calendar", "logs"),
	}
	writeStore, err := ckstore.Open(ctx, ckstore.Options{Path: paths.Archive})
	if err != nil {
		t.Fatal(err)
	}
	_, err = New().Sync(ctx, &crawlkit.Request{
		Store:    writeStore,
		Paths:    paths,
		Format:   ckoutput.Text,
		Out:      &bytes.Buffer{},
		Progress: func(crawlkit.Progress) {},
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
	t.Setenv("CRAWLKIT_STATE_ROOT", stateRoot)
	t.Setenv("CRAWLKIT_RUN_ID", "test")
	wireArgs := append([]string{crawlkit.HiddenWireSubcommand}, args...)
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
	code := crawlkit.Run(args, []crawlkit.Crawler{New()})
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
	for _, stmt := range []string{
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
	} {
		mustExec(t, db, stmt)
	}
}

func insertCalendarRows(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `insert into Store(ROWID, name, type, disabled) values
		(1, 'iCloud', 1, 0),
		(2, 'Subscribed Calendars', 4, 0),
		(3, 'Reminders', 3, 0)`)
	mustExec(t, db, `insert into Calendar(ROWID, store_id, title, type, external_id) values
		(10, 1, 'Work', 1, 'work-calendar'),
		(11, 2, 'Holidays', 3, 'holidays-calendar'),
		(12, 3, 'Reminders list', 3, 'reminders-calendar')`)
	insertEvent(t, db, 100, "11111111-1111-1111-1111-111111111111", "event-planning", "Planning meeting", "Discuss launch with Alice.", time.Date(2026, 3, 4, 9, 0, 0, 0, time.UTC), time.Date(2026, 3, 4, 9, 30, 0, 0, time.UTC), false, 10, 1000, 1, "https://example.com/event", true, 900, 2)
	insertEvent(t, db, 101, "22222222-2222-2222-2222-222222222222", "event-holiday", "Public holiday", "Subscribed holiday.", time.Date(2026, 5, 4, 22, 0, 0, 0, time.UTC), time.Date(2026, 5, 5, 22, 0, 0, 0, time.UTC), true, 11, 0, 0, "", false, 901, 1)
	mustExec(t, db, `insert into CalendarItem(
		ROWID, summary, description, start_date, end_date, start_tz, end_tz, all_day,
		calendar_id, organizer_id, status, url, has_recurrences, has_attendees,
		UUID, unique_identifier, entity_type, location_id, availability
	) values (103, 'Task row', '', 0, 0, 'UTC', 'UTC', 0, 10, 0, 1, '', 0, 0,
		'44444444-4444-4444-4444-444444444444', 'task-row', 1, 0, 0)`)
	mustExec(t, db, `insert into Identity(ROWID, display_name, address, first_name, last_name) values
		(500, 'Alice Example', 'alice@example.com', 'Alice', 'Example'),
		(501, 'Bob Example', 'bob@example.com', 'Bob', 'Example'),
		(502, 'Holiday Bot', 'holidays@example.com', 'Holiday', 'Bot')`)
	mustExec(t, db, `insert into Participant(
		ROWID, entity_type, type, status, role, identity_id, owner_id, email, phone_number, is_self, comment
	) values
		(1000, 2, 1, 2, 3, 500, 100, 'alice@example.com', '+15550100', 1, ''),
		(1001, 2, 1, 4, 1, 501, 100, 'bob@example.com', '+15550101', 0, ''),
		(1002, 2, 1, 2, 1, 502, 101, 'holidays@example.com', '', 0, '')`)
	mustExec(t, db, `insert into Location(ROWID, title, address, item_owner_id) values
		(900, 'Room 1', '1 Example Street', 100),
		(901, 'Netherlands', '', 101)`)
}

func insertEvent(t *testing.T, db *sql.DB, rowID int, uuid, uniqueID, summary, description string, start, end time.Time, allDay bool, calendarID, organizerID, status int, url string, recurs bool, locationID int, availability int) {
	t.Helper()
	mustExec(t, db, `insert into CalendarItem(
		ROWID, summary, description, start_date, end_date, start_tz, end_tz, all_day,
		calendar_id, organizer_id, status, url, has_recurrences, has_attendees,
		UUID, unique_identifier, entity_type, location_id, availability
	) values (?, ?, ?, ?, ?, 'Europe/Amsterdam', 'Europe/Amsterdam', ?, ?, ?, ?, ?, ?, 1, ?, ?, 2, ?, ?)`,
		rowID, summary, description, coreDate(start), coreDate(end), boolInt(allDay), calendarID, organizerID, status, url, boolInt(recurs), uuid, uniqueID, locationID, availability)
}

func coreDate(t time.Time) float64 {
	return float64(t.Unix() - coreDataUnixOffset)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}
