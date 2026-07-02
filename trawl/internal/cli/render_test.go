package cli

import (
	"bytes"
	"testing"
	"time"
)

func TestRenderStatusTable(t *testing.T) {
	now := time.Date(2026, 7, 2, 14, 5, 0, 0, time.UTC)
	results := []StatusResult{
		{
			Source: Source{ID: "imessage"},
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
			Source: Source{ID: "telegram"},
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
			Source: Source{ID: "gmail"},
			Status: StatusEnvelope{
				AppID:   "gmail",
				State:   "error",
				Summary: "auth expired — run: trawl doctor gmail",
			},
		},
	}
	var out bytes.Buffer
	if err := renderStatusTable(&out, results, now); err != nil {
		t.Fatal(err)
	}
	want := "SOURCE    STATE  FRESH   HEADLINE\n" +
		"imessage  ok     2m ago  12,345 messages · 87 chats · since 2014\n" +
		"telegram  stale  3d ago  23,456 messages\n" +
		"gmail     error  —       auth expired — run: trawl doctor gmail\n"
	if out.String() != want {
		t.Fatalf("status table:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestRenderDoctor(t *testing.T) {
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
	want := "SOURCE    CHECK                 STATE  MESSAGE\n" +
		"imessage  source_store          ok\n" +
		"imessage  tcc_full_disk_access  fail   cannot read the source database\n" +
		"  remedy: grant Full Disk Access to Trawl in System Settings > Privacy\n"
	if out.String() != want {
		t.Fatalf("doctor output:\n%s\nwant:\n%s", out.String(), want)
	}
}
