package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openclaw/crawlkit/conformance"
	"github.com/openclaw/photoscrawl/internal/archive"
)

func TestJoinedQueryPreservesLauncherArguments(t *testing.T) {
	if got := joinedQuery("hello", []string{"world", "photos"}); got != "hello world photos" {
		t.Fatalf("joined query = %q", got)
	}
	if got := joinedQuery("", []string{"hello", "world"}); got != "hello world" {
		t.Fatalf("positional query = %q", got)
	}
}

func TestParseSearchCommandRequiresFlagsAfterQuery(t *testing.T) {
	parsed, err := parseSearchCommand([]string{"boat", "trip", "--limit", "5", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query != "boat trip" || parsed.Limit != 5 || parsed.Format != "json" {
		t.Fatalf("parsed search = %#v", parsed)
	}
	if _, err := parseSearchCommand([]string{"--json", "boat"}); err == nil {
		t.Fatal("expected flags-before-query error")
	}
}

func TestParseRefCommandRequiresRefBeforeFlags(t *testing.T) {
	parsed, err := parseRefCommand("open", []string{"photoscrawl:asset/fixture", "--json"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Ref != "photoscrawl:asset/fixture" || parsed.Format != "json" {
		t.Fatalf("parsed open = %#v", parsed)
	}
	if _, err := parseRefCommand("open", []string{"--json", "photoscrawl:asset/fixture"}, false); err == nil {
		t.Fatal("expected flags-before-ref error")
	}
	neighbors, err := parseRefCommand("neighbors", []string{"photoscrawl:asset/fixture", "--limit", "5"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if neighbors.Limit != 5 {
		t.Fatalf("parsed neighbors = %#v", neighbors)
	}
}

func TestExportAlreadyRunningErrorIsStructured(t *testing.T) {
	got := normaliseError(errors.New("photokit export already running"))
	if got.Code != "export_already_running" || !strings.Contains(got.Remedy, "wait for the other export run") {
		t.Fatalf("normaliseError = %#v", got)
	}
}

func TestMovedResearchVerbsReturnUsage(t *testing.T) {
	for _, verb := range []string{"place-context", "place-card", "place-backfill", "eval-card"} {
		t.Run(verb, func(t *testing.T) {
			_, _, err := captureRunOutput(t, []string{verb})
			if err == nil {
				t.Fatal("expected usage error")
			}
			if !strings.Contains(err.Error(), "usage: photoscrawl <metadata|status|doctor|sync|classify|search|open|neighbors>") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestStatusHumanOutputIsProse(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "photos.sqlite")
	out, errOut, err := captureRunOutput(t, []string{"status", "--db", dbPath})
	if err != nil {
		t.Fatalf("status: %v stderr=%s stdout=%s", err, errOut, out)
	}
	assertHumanProseOutput(t, out,
		"Status: missing",
		"photos.sqlite has not been initialized",
		"Counts:",
		"Archived photos: none",
		"Archive:",
		"Database:",
	)
}

func TestMetadataHumanOutputIsProse(t *testing.T) {
	out, errOut, err := captureRunOutput(t, []string{"metadata"})
	if err != nil {
		t.Fatalf("metadata: %v stderr=%s stdout=%s", err, errOut, out)
	}
	assertHumanProseOutput(t, out,
		"Photos (photoscrawl)",
		"Contract version: 1",
		"Capabilities: metadata, status, doctor, sync, classify, search, open, neighbors",
		"sync: photoscrawl sync --library <path> --json",
		"open: photoscrawl open <ref> --json",
	)
	if strings.Contains(out, "photoscrawl crawl") {
		t.Fatalf("metadata still advertises crawl:\n%s", out)
	}
	if strings.Contains(out, "photoscrawl init") {
		t.Fatalf("metadata advertises non-contract init command:\n%s", out)
	}
}

func TestSyncHumanOutputIsProse(t *testing.T) {
	var out strings.Builder
	err := printSyncText(&out, archive.SyncResult{
		Database:              filepath.Join("tmp", "photos.sqlite"),
		Provider:              "photos_sqlite_snapshot",
		AssetsSeen:            10,
		AssetsNew:             0,
		AssetsChanged:         0,
		AssetsUnchanged:       10,
		ResourcesSeen:         20,
		AlbumMembershipsSeen:  3,
		LocationsSeen:         7,
		QueuedForClassify:     0,
		QueuedNeedsDownload:   0,
		PreviouslySeenMissing: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertHumanProseOutput(t, out.String(),
		"Sync complete",
		"Provider: photos_sqlite_snapshot",
		"Assets: 10 seen, 0 new, 0 changed, 10 unchanged, 0 missing",
		"Imported: 20 resources, 3 album memberships, 7 locations",
		"Classification queue: 0 queued, 0 need download",
	)
}

func TestSearchHumanOutputIsProse(t *testing.T) {
	var out strings.Builder
	err := printSearchText(&out, archive.SearchResult{
		Query:        "receipt",
		TotalMatches: 1,
		Results: []archive.SearchHit{{
			Ref:     "photoscrawl:asset/fixture",
			Time:    "2026-05-28T12:00:00+02:00",
			Where:   "Synthetic Pier",
			Snippet: "receipt candidate from local metadata",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertHumanProseOutput(t, out.String(),
		"Search: \"receipt\"",
		"Showing 1 of 1 matches",
		"photoscrawl:asset/fixture",
		"receipt candidate from local metadata",
	)
}

func TestSearchHumanOutputOmitsEmptyWhoWhere(t *testing.T) {
	var out strings.Builder
	err := printSearchText(&out, archive.SearchResult{
		Query:        "image",
		TotalMatches: 1,
		Results: []archive.SearchHit{{
			Ref:     "photoscrawl:asset/fixture",
			Time:    "2026-05-28T12:00:00+02:00",
			Snippet: "image",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := out.String()
	assertHumanProseOutput(t, got,
		"2026-05-28T12:00:00+02:00 | photoscrawl:asset/fixture",
	)
	if strings.Contains(got, " |  | ") {
		t.Fatalf("human search output kept empty who/where fields:\n%s", got)
	}
}

func TestDoctorHumanOutputIsProse(t *testing.T) {
	dir := t.TempDir()
	libraryPath := filepath.Join(dir, "Fixture Photos Library.photoslibrary")
	if err := os.MkdirAll(libraryPath, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "photos.sqlite")
	out, errOut, err := captureRunOutput(t, []string{"doctor", "--db", dbPath, "--library", libraryPath})
	if err != nil {
		t.Fatalf("doctor: %v stderr=%s stdout=%s", err, errOut, out)
	}
	assertHumanProseOutput(t, out,
		"Doctor checks:",
		"source store:",
		"archive:",
		"Remedy:",
	)
}

func TestOpenHumanOutputIsProse(t *testing.T) {
	var out strings.Builder
	err := printOpenText(&out, archive.OpenResult{
		Ref: "photoscrawl:asset/fixture",
		Mechanical: archive.OpenMechanical{
			Captured: &archive.OpenCaptured{Local: "2026-05-28T12:00:00+02:00", Timezone: "Europe/Amsterdam"},
			Media:    &archive.OpenMedia{Kind: "photo", Width: 100, Height: 80},
			GPS:      &archive.OpenGPS{Latitude: 52, Longitude: 4, HorizontalAccuracyMeters: 8},
			Address:  "Synthetic Street, Synthetic City",
			Venue:    &archive.OpenVenue{Name: "Synthetic Pier", Tier: "venue_candidate", DistanceMeters: 12},
			Camera:   &archive.OpenCamera{Display: "Apple iPhone 15 Pro, 24mm equiv, f/1.8, 1/120s, ISO 64"},
			Original: &archive.OpenOriginal{
				Filename:     "IMG_0001.JPG",
				Bytes:        5_700_000,
				Availability: "on this Mac",
			},
			Flags: []string{"favourite"},
		},
		Model: archive.OpenModel{
			Summary:     "Synthetic beach scene.",
			Description: "A synthetic fixture photo shows a beach scene with a pier in the background.",
			Uncertainties: []string{
				"exact venue",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertHumanProseOutput(t, out.String(),
		"photoscrawl:asset/fixture",
		"Captured: 2026-05-28 12:00 local (Europe/Amsterdam)",
		"Media: photo, 100 x 80",
		"GPS: 52.00000, 4.00000, +/-8m",
		"Venue: Synthetic Pier, candidate, 12m from GPS",
		"Camera: Apple iPhone 15 Pro, 24mm equiv, f/1.8, 1/120s, ISO 64",
		"Summary: Synthetic beach scene.",
		"Uncertainty: exact venue.",
		"Original: IMG_0001.JPG",
	)
}

func TestOpenHumanOutputUsesKnownPlaceInsteadOfVenue(t *testing.T) {
	var out strings.Builder
	err := printOpenText(&out, archive.OpenResult{
		Ref: "photoscrawl:asset/fixture",
		Mechanical: archive.OpenMechanical{
			Address:    "23 Example Street, Example City",
			KnownPlace: &archive.OpenKnownPlace{Kind: archive.KnownPlaceKindWork, Name: "Example Studio"},
			Venue:      &archive.OpenVenue{Name: "Synthetic Consultancy", Tier: "venue_candidate", DistanceMeters: 12},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := out.String()
	assertHumanProseOutput(t, got,
		"Address: 23 Example Street, Example City",
		"At work (Example Studio)",
	)
	if strings.Contains(got, "Venue:") || strings.Contains(got, "Synthetic Consultancy") {
		t.Fatalf("known place output leaked venue:\n%s", got)
	}
}

func captureRunOutput(t *testing.T, args []string) (string, string, error) {
	t.Helper()
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
	os.Stdout = stdoutW
	os.Stderr = stderrW
	defer func() {
		os.Stdout = oldStdout
		os.Stderr = oldStderr
	}()

	runErr := run(context.Background(), args)
	if err := stdoutW.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stderrW.Close(); err != nil {
		t.Fatal(err)
	}
	stdout, err := io.ReadAll(stdoutR)
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := io.ReadAll(stderrR)
	if err != nil {
		t.Fatal(err)
	}
	if err := stdoutR.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stderrR.Close(); err != nil {
		t.Fatal(err)
	}
	return string(stdout), string(stderr), runErr
}

func assertHumanProseOutput(t *testing.T, got string, wants ...string) {
	t.Helper()
	conformance.AssertHumanOutput(t, got)
	if strings.HasPrefix(strings.TrimSpace(got), "{") {
		t.Fatalf("human output starts like JSON or a Go struct: %q", got)
	}
	if strings.Contains(got, "{[{") {
		t.Fatalf("human output contains Go struct debris: %q", got)
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Fatalf("human output missing %q:\n%s", want, got)
		}
	}
}
