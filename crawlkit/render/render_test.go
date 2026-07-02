package render

import (
	"bytes"
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
		"  source_store: ok",
		"  archive: missing - archive has not been synced",
		"    Remedy: run: examplecrawl sync",
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
				Event:   "backup_failed",
				Message: `error="backup fetch exited early" remedy="try again when the network is available"`,
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
		"  Last run: sync success at 2026-07-02T14:03:11+02:00",
		"  Most recent error: sync backup_failed: backup fetch exited early",
		"    Remedy: try again when the network is available",
		"",
	}, "\n")
	if buf.String() != want {
		t.Fatalf("status output:\n%s\nwant:\n%s", buf.String(), want)
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
