package cli

import (
	"bytes"
	"context"
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
