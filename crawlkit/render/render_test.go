package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	cklog "github.com/openclaw/crawlkit/log"
)

func TestWriteDoctor(t *testing.T) {
	var buf bytes.Buffer
	err := WriteDoctor(&buf, []Check{
		{Name: "source_store", State: CheckOK},
		{Name: "archive", State: CheckMissing, Message: "archive has not been synced", Remedy: "run: examplecrawl sync"},
	}, LogTail{})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"Doctor checks:",
		"  source store: ok",
		"  archive: missing - archive has not been synced",
		"    Remedy: run: examplecrawl sync",
		"",
	}, "\n")
	if buf.String() != want {
		t.Fatalf("doctor output:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func TestWriteSearchSummaryTruncatesLongDisplayQuery(t *testing.T) {
	t.Setenv("COLUMNS", "80")
	var buf bytes.Buffer
	if err := WriteSearchSummary(&buf, strings.Repeat("q", 200), 0, 0); err != nil {
		t.Fatal(err)
	}
	got := strings.TrimRight(buf.String(), "\n")
	if !strings.Contains(got, "…") {
		t.Fatalf("summary did not truncate query:\n%s", got)
	}
	for lineNo, line := range strings.Split(got, "\n") {
		if width := DisplayWidth(line); width > 80 {
			t.Fatalf("line %d width = %d, want <= 80:\n%s", lineNo+1, width, got)
		}
	}
}

func TestWriteWrappedFieldUsesContinuationIndent(t *testing.T) {
	t.Setenv("COLUMNS", "32")
	var buf bytes.Buffer
	if err := WriteWrappedField(&buf, "Text", "alpha beta gamma delta epsilon zeta"); err != nil {
		t.Fatal(err)
	}
	got := strings.TrimRight(buf.String(), "\n")
	if !strings.Contains(got, "\n      epsilon") {
		t.Fatalf("wrapped field did not align continuation:\n%s", got)
	}
	for lineNo, line := range strings.Split(got, "\n") {
		if width := DisplayWidth(line); width > 32 {
			t.Fatalf("line %d width = %d, want <= 32:\n%s", lineNo+1, width, got)
		}
	}
}

func TestWriteDoctorFiltersUsageRecentError(t *testing.T) {
	var buf bytes.Buffer
	err := WriteDoctor(&buf, []Check{
		{Name: "source_store", State: CheckOK},
	}, LogTail{
		MostRecentError: &cklog.Line{
			Level:   cklog.LevelError,
			Command: "search",
			Event:   "usage_error",
			Message: `error="search --who requires an identity" remedy="run search with --who NAME"`,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"Doctor checks:",
		"  source store: ok",
		"",
	}, "\n")
	if buf.String() != want {
		t.Fatalf("doctor output:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func TestWriteDoctorShowsWorldChangeRecentError(t *testing.T) {
	var buf bytes.Buffer
	err := WriteDoctor(&buf, []Check{
		{Name: "auth", State: CheckFail, Message: "calendar permission denied"},
	}, LogTail{
		MostRecentError: &cklog.Line{
			Level:   cklog.LevelError,
			Command: "sync",
			Event:   "permission_denied",
			Message: `error="calendar permission denied" remedy="grant Calendar access" visibility=user`,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"Doctor checks:",
		"  auth: fail - calendar permission denied",
		"",
		"Recent log:",
		"  Most recent error: sync permission denied: calendar permission denied",
		"    Remedy: grant Calendar access",
		"",
	}, "\n")
	if buf.String() != want {
		t.Fatalf("doctor output:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func TestDoctorLogTailOutputHumanizesLogJSON(t *testing.T) {
	when := time.Date(2026, 7, 3, 10, 45, 0, 0, time.UTC)
	out := DoctorLogTailOutput(LogTail{
		LastRun: &cklog.RunSummary{
			RunID:      "internal-run-id",
			Command:    "sync",
			Outcome:    "error",
			LastEvent:  "run_failed",
			FinishedAt: when,
		},
		MostRecentError: &cklog.Line{
			Level:      cklog.LevelError,
			RunID:      "internal-run-id",
			Command:    "sync",
			Event:      "run_failed",
			Message:    `error="archive.db has not been synced" remedy="run trawl imsgcrawl sync" visibility=user`,
			Timestamp:  when,
			Visibility: cklog.VisibilityUserFacing,
		},
	})
	if out == nil || out.LastRun == nil || out.MostRecentError == nil {
		t.Fatalf("doctor log output missing fields: %#v", out)
	}
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{
		`"what_happened":"sync ended with error"`,
		`"what_happened":"archive.db has not been synced"`,
		`"when":"2026-07-03T10:45:00Z"`,
		`"remedy":"run trawl imsgcrawl sync"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("doctor log json = %s, missing %s", got, want)
		}
	}
	for _, forbidden := range []string{"run_id", "last_event", "run_failed", "event=", "visibility"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("doctor log json leaked %q: %s", forbidden, got)
		}
	}
}

func TestDoctorLogTailOutputFiltersInternalErrors(t *testing.T) {
	out := DoctorLogTailOutput(LogTail{
		MostRecentError: &cklog.Line{
			Level:      cklog.LevelError,
			Command:    "search",
			Event:      "usage_error",
			Message:    `error="search --who requires an identity" remedy="run search with --who NAME" visibility=internal`,
			Visibility: cklog.VisibilityInternal,
		},
	})
	if out != nil {
		t.Fatalf("internal doctor log output = %#v, want nil", out)
	}
}

func TestWriteDoctorHumanizesSnakeCaseCheckNames(t *testing.T) {
	var buf bytes.Buffer
	err := WriteDoctor(&buf, []Check{
		{Name: "source_store", State: CheckOK},
		{Name: "gog_binary", State: CheckMissing},
		{Name: "full disk access", State: CheckOK},
	}, LogTail{})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"Doctor checks:",
		"  source store: ok",
		"  gog binary: missing",
		"  full disk access: ok",
		"",
	}, "\n")
	if buf.String() != want {
		t.Fatalf("doctor output:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func TestWriteStatus(t *testing.T) {
	when := time.Date(2026, 7, 2, 14, 3, 11, 0, time.FixedZone("CEST", 2*60*60))
	var buf bytes.Buffer
	err := WriteStatus(&buf, Status{
		State:   StatusOK,
		Summary: "Archive is fresh.",
		Sections: []Section{
			{Title: "Local archive", Fields: []Field{
				{Label: "Database", Value: "/tmp/example.db"},
				{Label: "Last sync", Value: "2026-07-02T14:03:11+02:00"},
				{Label: "Messages", Value: "12"},
			}},
			{Title: "Auth", Fields: []Field{{Label: "Authorised", Value: "true"}}},
		},
		Freshness: &Freshness{LastSync: "2026-07-02T14:03:11+02:00", State: "fresh"},
		Log: LogTail{
			LastRun: &cklog.RunSummary{
				Command:    "sync",
				Outcome:    "success",
				FinishedAt: when,
			},
			MostRecentError: &cklog.Line{
				Command: "sync",
				Event:   "source_failed",
				Message: `error="source fetch exited early" remedy="try again when the source is available"`,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"Status: ok",
		"Archive is fresh.",
		"",
		"Local archive:",
		"  Database: /tmp/example.db",
		"  Last sync: 2026-07-02T14:03:11+02:00",
		"  Messages: 12",
		"",
		"Auth:",
		"  Authorised: true",
		"",
		"Freshness:",
		"  Last sync: 2026-07-02T14:03:11+02:00",
		"  State: fresh",
		"",
		"Recent log:",
		"  Last run: sync succeeded at 2026-07-02 14:03",
		"  Most recent error: sync source failed: source fetch exited early",
		"    Remedy: try again when the source is available",
		"",
	}, "\n")
	if buf.String() != want {
		t.Fatalf("status output:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func TestWriteStatusShowsRecentUsageError(t *testing.T) {
	var buf bytes.Buffer
	err := WriteStatus(&buf, Status{
		State:   StatusError,
		Summary: "Command failed.",
		Log: LogTail{
			MostRecentError: &cklog.Line{
				Level:   cklog.LevelError,
				Command: "search",
				Event:   "usage_error",
				Message: `error="search --who requires an identity" remedy="run search with --who NAME"`,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"Status: error",
		"Command failed.",
		"",
		"Recent log:",
		"  Most recent error: search usage error: search --who requires an identity",
		"    Remedy: run search with --who NAME",
		"",
	}, "\n")
	if buf.String() != want {
		t.Fatalf("status output:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func TestWriteLogTailHumanisesRunStatus(t *testing.T) {
	when := time.Date(2026, 7, 4, 21, 31, 45, 0, time.FixedZone("CEST", 2*60*60))
	var buf bytes.Buffer
	err := WriteLogTail(&buf, LogTail{
		LastRun: &cklog.RunSummary{
			Command:    "stats",
			Outcome:    "success",
			FinishedAt: when,
		},
		MostRecentError: &cklog.Line{
			Command: "search",
			Event:   "run_failed",
			Message: `error="search takes exactly one query"`,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"",
		"Recent log:",
		"  Last run: stats succeeded at 2026-07-04 21:31",
		"  Most recent error: search failed: search takes exactly one query",
		"",
	}, "\n")
	if buf.String() != want {
		t.Fatalf("log tail output:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func TestWriteLogTailHumanisesFailureOutcome(t *testing.T) {
	var buf bytes.Buffer
	err := WriteLogTail(&buf, LogTail{
		LastRun: &cklog.RunSummary{
			Command: "sync",
			Outcome: "failure",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"",
		"Recent log:",
		"  Last run: sync failed",
		"",
	}, "\n")
	if buf.String() != want {
		t.Fatalf("log tail output:\n%s\nwant:\n%s", buf.String(), want)
	}
}

// TestWriteLogTailHumanisesErrorOutcome is the TRAWL-17 tripwire: the log
// reader canonicalizes a failed run to the "error" outcome, and the human
// run line must read "failed", never the raw enum "error" — otherwise a
// failed sync prints "sync error" next to another run's "sync succeeded".
func TestWriteLogTailHumanisesErrorOutcome(t *testing.T) {
	var buf bytes.Buffer
	err := WriteLogTail(&buf, LogTail{
		LastRun: &cklog.RunSummary{
			Command: "sync",
			Outcome: "error",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"",
		"Recent log:",
		"  Last run: sync failed",
		"",
	}, "\n")
	if buf.String() != want {
		t.Fatalf("log tail output:\n%s\nwant:\n%s", buf.String(), want)
	}
}

// TestRejectedRunRendersRejected is the TRAWL-101 tripwire: a run
// refused before any work ran (usage error) must read as rejected on
// every surface that shows run outcomes, never as a success.
func TestRejectedRunRendersRejected(t *testing.T) {
	run := cklog.RunSummary{
		Command:    "search",
		Outcome:    "rejected",
		FinishedAt: time.Date(2026, 7, 5, 1, 13, 0, 0, time.Local),
	}

	var buf bytes.Buffer
	if err := WriteLogTail(&buf, LogTail{LastRun: &run}); err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"",
		"Recent log:",
		"  Last run: search rejected at 2026-07-05 01:13",
		"",
	}, "\n")
	if buf.String() != want {
		t.Fatalf("log tail output:\n%s\nwant:\n%s", buf.String(), want)
	}

	doctor := DoctorLogTailOutput(LogTail{LastRun: &run})
	if doctor == nil || doctor.LastRun == nil {
		t.Fatalf("doctor output = %+v, want last run", doctor)
	}
	if got := doctor.LastRun.WhatHappened; got != "search ended with rejected" {
		t.Fatalf("doctor last run = %q, want %q", got, "search ended with rejected")
	}
}

func TestWriteStatusDefaultsBlankFields(t *testing.T) {
	var buf bytes.Buffer
	err := WriteStatus(&buf, Status{
		Summary: "Archive has not been synced.",
		Sections: []Section{{Title: "Local archive", Fields: []Field{
			{Label: "Database"},
			{Label: "Messages", Value: "0"},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"Status: unknown",
		"Archive has not been synced.",
		"",
		"Local archive:",
		"  Database: -",
		"  Messages: 0",
		"",
	}, "\n")
	if buf.String() != want {
		t.Fatalf("status output:\n%s\nwant:\n%s", buf.String(), want)
	}
}
