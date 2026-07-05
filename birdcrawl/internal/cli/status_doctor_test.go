package cli

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/birdcrawl/internal/store"
)

func TestStatusReportsPausedLiveSyncAndShortHumanTimes(t *testing.T) {
	env := newSyncTestEnv(t)
	ctx := context.Background()
	st, err := store.Open(ctx, env.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.Local)
	liveAt := now.Add(-time.Hour)
	coverageThrough := now.Add(-24 * time.Hour)
	tweet := store.Tweet{
		ID:           "20",
		CreatedAt:    now.Add(-2 * time.Hour),
		AuthorID:     "42",
		AuthorHandle: "example_alex",
		AuthorName:   "Alex Example",
		Text:         "synthetic status tweet",
		FirstSource:  "archive",
	}
	if _, err := st.ImportArchive(ctx, store.ImportBatch{
		Tweets:          []store.Tweet{tweet},
		Roles:           []store.Role{{TweetID: "20", Role: "authored", FirstSeenAt: now, LastSeenAt: now}},
		CoverageThrough: coverageThrough,
		ImportedAt:      now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.CommitLivePage(ctx, store.LivePage{SyncedAt: liveAt, States: []store.SyncStateUpdate{{
		Kind:       "live_sync",
		LastSyncAt: liveAt,
		LastResult: "ok",
	}}}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddSpend(ctx, "2026-07", 10_000_000, now); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	var stdout, stderr bytes.Buffer
	if err := Run(ctx, []string{"--db", env.dbPath, "--config", env.configPath, "status"}, &stdout, &stderr); err != nil {
		t.Fatalf("status error: %v stderr=%s", err, stderr.String())
	}
	for _, want := range []string{
		"archive dump imported through " + formatHumanLocalTime(coverageThrough),
		"live synced at " + formatHumanLocalTime(liveAt),
		"Live sync is paused: the monthly X API budget is spent; it resumes 1 August 2026.",
		"Last sync: " + formatHumanLocalTime(liveAt),
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("status output missing %q:\n%s", want, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), liveAt.Format(time.RFC3339)) || strings.Contains(stdout.String(), coverageThrough.Format(time.RFC3339)) {
		t.Fatalf("status human output used RFC3339:\n%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if err := Run(ctx, []string{"--db", env.dbPath, "--config", env.configPath, "--json", "status"}, &stdout, &stderr); err != nil {
		t.Fatalf("status json error: %v stderr=%s", err, stderr.String())
	}
	var envelope statusEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.Spend.LiveSyncPaused {
		t.Fatalf("spend = %#v, want live_sync_paused", envelope.Spend)
	}
	if envelope.Freshness.LastSync != formatLocalTime(liveAt) {
		t.Fatalf("last_sync = %q, want %q", envelope.Freshness.LastSync, formatLocalTime(liveAt))
	}
}

func TestDoctorReportsSpentBudgetWarnAndSkippedProbe(t *testing.T) {
	env := newSyncTestEnv(t)
	ctx := context.Background()
	st, err := store.Open(ctx, env.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddSpend(ctx, "2026-07", 10_000_000, timeDate2026()); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	var stdout, stderr bytes.Buffer
	err = Run(ctx, []string{"--db", env.dbPath, "--config", env.configPath, "--json", "doctor"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("doctor error: %v stderr=%s", err, stderr.String())
	}
	var output doctorOutput
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	byID := map[string]doctorCheck{}
	for _, check := range output.Checks {
		byID[check.ID] = check
		if check.Message != "" && !strings.HasSuffix(check.Message, ".") {
			t.Fatalf("message for %s lacks full stop: %q", check.ID, check.Message)
		}
	}
	if got := byID["monthly_budget"]; got.State != "warn" || got.Message != "monthly X API budget is spent; live sync resumes 1 August 2026." {
		t.Fatalf("monthly budget check = %#v", got)
	}
	if got := byID["x_account_reachable"]; got.State != "skipped" || got.Message != "skipped: monthly X API budget is spent." {
		t.Fatalf("x account check = %#v", got)
	}
}

// TestStatusAndDoctorReportOutdatedSchemaHonestly pins the fix for a real
// regression the TRAWL-82 sync_state migration introduced: status and
// doctor open the archive read-only, and migrate() (which writes DDL)
// only runs on a writable open, so a not-yet-migrated archive used to
// fail with a generic "cannot be read" error instead of naming the
// actual, actionable cause — and doctor duplicated its integrity checks
// while doing so.
func TestStatusAndDoctorReportOutdatedSchemaHonestly(t *testing.T) {
	env := newSyncTestEnv(t)
	ctx := context.Background()

	// Create the current (canonical) schema, then rewrite sync_state back
	// into the pre-migration shape, so this archive looks exactly like a
	// real one that predates the TRAWL-82 migration.
	seed, err := store.Open(ctx, env.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", env.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`drop table sync_state`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`create table sync_state (
		kind text primary key,
		cursor text,
		last_sync_at text,
		last_result text,
		coverage_note text
	)`); err != nil {
		t.Fatal(err)
	}
	month := time.Now().UTC().Format("2006-01")
	if _, err := db.Exec(`insert into sync_state(kind,cursor,last_sync_at,last_result,coverage_note) values (?,?,?,?,?)`,
		"spend:"+month, "1250000", time.Now().UTC().Format(time.RFC3339), "ok", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`pragma user_version = 1`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := Run(ctx, []string{"--db", env.dbPath, "--config", env.configPath, "status"}, &stdout, &stderr); err != nil {
		t.Fatalf("status error: %v stderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "archive schema needs one sync to finish upgrading") {
		t.Fatalf("status output missing the honest outdated-schema message:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "run: birdcrawl sync") {
		t.Fatalf("status output missing the remedy:\n%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if err := Run(ctx, []string{"--db", env.dbPath, "--config", env.configPath, "doctor"}, &stdout, &stderr); err != nil {
		t.Fatalf("doctor error: %v stderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "archive schema needs one sync to finish upgrading") {
		t.Fatalf("doctor output missing the honest outdated-schema message:\n%s", stdout.String())
	}
	if strings.Count(stdout.String(), "database integrity:") != 1 {
		t.Fatalf("doctor output repeated the database_integrity check instead of reporting it once:\n%s", stdout.String())
	}

	// A writable open (any command using withStore) migrates the archive;
	// status must then read it normally, with no trace of the old message.
	if err := Run(ctx, []string{"--db", env.dbPath, "--config", env.configPath, "tweets", "--limit", "1"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("tweets (writable open, triggers migration): %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	if err := Run(ctx, []string{"--db", env.dbPath, "--config", env.configPath, "--json", "status"}, &stdout, &stderr); err != nil {
		t.Fatalf("status after migration: %v stderr=%s", err, stderr.String())
	}
	if strings.Contains(stdout.String(), "needs one sync") {
		t.Fatalf("status still reports outdated schema after a writable open migrated it:\n%s", stdout.String())
	}
	var envelope statusEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Spend.SpentUSD != "1.25" {
		t.Fatalf("spent_usd after migration = %q, want %q — the migrated spend ledger value must reach status", envelope.Spend.SpentUSD, "1.25")
	}
}
