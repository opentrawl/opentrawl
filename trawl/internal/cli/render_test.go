package cli

import (
	"bytes"
	"testing"
	"time"
)

func TestRenderStatusTable(t *testing.T) {
	t.Setenv("COLUMNS", "120")
	now := time.Date(2026, 7, 2, 14, 5, 0, 0, time.UTC)
	results := []StatusResult{
		{
			Source: Source{ID: "imessage", DisplayName: "iMessage"},
			Status: StatusEnvelope{
				AppID:     "imessage",
				State:     "ok",
				Freshness: &Freshness{LastSync: "2026-07-02T14:03:00Z"},
				Counts: []Count{
					{ID: "messages", Label: "messages", Value: countValue(int64(12345))},
					{ID: "chats", Label: "chats", Value: countValue(int64(87))},
					{ID: "since", Label: "since", Value: countValue(int64(2014))},
				},
			},
		},
		{
			Source: Source{ID: "telegram", DisplayName: "Telegram"},
			Status: StatusEnvelope{
				AppID:     "telegram",
				State:     "stale",
				Freshness: &Freshness{LastSync: "2026-06-29T14:05:00Z"},
				Counts: []Count{
					{ID: "messages", Label: "messages", Value: countValue(int64(23456))},
				},
			},
		},
		{
			Source: Source{ID: "gmail", DisplayName: "Gmail"},
			Status: StatusEnvelope{
				AppID:   "gmail",
				State:   "error",
				Summary: "auth expired",
			},
		},
	}
	var out bytes.Buffer
	if err := renderStatusTable(&out, results, now); err != nil {
		t.Fatal(err)
	}
	want := "source    surface   state  fresh   headline\n" +
		"imessage  iMessage  ok     2m ago  12,345 messages · 87 chats · since 2014\n" +
		"telegram  Telegram  stale  3d ago  23,456 messages\n" +
		"gmail     Gmail     error  —       auth expired\n"
	if out.String() != want {
		t.Fatalf("status table:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestStatusHeadlineDropsZeroSinceAndYearCounts(t *testing.T) {
	headline := statusHeadline(StatusEnvelope{Counts: []Count{
		{ID: "messages", Label: "messages", Value: countValue(int64(0))},
		{ID: "since", Label: "since", Value: countValue(int64(0))},
		{ID: "oldest_year", Label: "oldest year", Value: countValue(int64(0))},
		{ID: "senders", Label: "senders", Value: countValue(int64(2))},
	}})

	want := "0 messages · 2 senders"
	if headline != want {
		t.Fatalf("headline = %q, want %q", headline, want)
	}
}

// TestRenderDoctor pins the doctor design: raw check ids never reach a
// reader, and a remedy sits on its own labelled line below the table
// instead of riding a data row.
func TestRenderDoctor(t *testing.T) {
	t.Setenv("COLUMNS", "120")
	results := []DoctorResult{{
		Source: "imessage",
		Checks: []DoctorCheck{
			{ID: "source_store", State: "ok"},
			{
				ID:      "tcc_full_disk_access",
				State:   "fail",
				Message: "cannot read the source database",
				Remedy:  "grant Full Disk Access to Trawl in System Settings > Privacy",
			},
		},
	}}
	var out bytes.Buffer
	if err := renderDoctor(&out, results); err != nil {
		t.Fatal(err)
	}
	want := "source    state  checks\n" +
		"imessage  FAIL   tcc full disk access failed · 1 of 2 ok\n" +
		"\n" +
		"imessage tcc full disk access failed: cannot read the source database\n" +
		"  Remedy: grant Full Disk Access to Trawl in System Settings > Privacy\n"
	if out.String() != want {
		t.Fatalf("doctor output:\n%s\nwant:\n%s", out.String(), want)
	}
}
