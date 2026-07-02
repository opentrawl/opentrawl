package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	if _, err := parseRefCommand("evidence", []string{"photoscrawl:asset/fixture", "--limit", "5"}, false); err == nil {
		t.Fatal("expected unknown --limit error when limit is not allowed")
	}
	neighbors, err := parseRefCommand("neighbors", []string{"photoscrawl:asset/fixture", "--limit", "5"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if neighbors.Limit != 5 {
		t.Fatalf("parsed neighbors = %#v", neighbors)
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
		"none",
		"Paths:",
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
		"Capabilities: status, sync, search, open, doctor",
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
		"Evidence: 20 resources, 3 album memberships, 7 locations",
		"Classification queue: 0 queued, 0 need download",
	)
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
		"source_store:",
		"archive:",
		"Remedy:",
	)
}

func TestOpenHumanOutputIsProse(t *testing.T) {
	var out strings.Builder
	err := printOpenText(&out, archive.OpenResult{
		Ref:       "photoscrawl:asset/fixture",
		Time:      "2026-05-28T12:00:00+02:00",
		MediaType: "image",
		Dimensions: &archive.OpenDimensions{
			Width:  4032,
			Height: 3024,
		},
		Where:         "Synthetic Pier",
		Who:           []string{"Synthetic Person"},
		LocationCount: 1,
		Albums: []archive.OpenAlbum{{
			Title: "Synthetic album",
			Kind:  "user",
		}},
		Resources: []archive.OpenResource{{
			Type:             "photo",
			Filename:         "IMG_0001.JPG",
			AvailableLocally: true,
		}},
		Observations: []archive.OpenObservation{{
			Kind: "scene_summary",
			Text: "Synthetic beach scene",
		}},
		Evidence: archive.OpenEvidence{
			Refs: []archive.EvidenceReference{{Ref: "photoscrawl:fixture-evidence", Kind: "asset metadata"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertHumanProseOutput(t, out.String(),
		"Asset: photoscrawl:asset/fixture",
		"Where: Synthetic Pier",
		"Who: Synthetic Person",
		"Evidence refs: 1",
		"scene_summary: Synthetic beach scene",
	)
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
