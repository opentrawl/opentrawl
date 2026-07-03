package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openclaw/crawlkit/conformance"
	cklog "github.com/openclaw/crawlkit/log"
	"github.com/openclaw/crawlkit/store"
	"github.com/openclaw/photoscrawl/internal/archive"
	"github.com/openclaw/photoscrawl/internal/photos"
)

func TestJoinedQueryPreservesLauncherArguments(t *testing.T) {
	if got := joinedQuery("hello", []string{"world", "photos"}); got != "hello world photos" {
		t.Fatalf("joined query = %q", got)
	}
	if got := joinedQuery("", []string{"hello", "world"}); got != "hello world" {
		t.Fatalf("positional query = %q", got)
	}
}

func TestParseSearchCommandAllowsLauncherFlagOrder(t *testing.T) {
	parsed, err := parseSearchCommand([]string{"boat", "trip", "--limit", "5", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query != "boat trip" || parsed.Limit != 5 || parsed.Format != "json" {
		t.Fatalf("parsed search = %#v", parsed)
	}

	parsed, err = parseSearchCommand([]string{"--json", "--limit", "3", "boat", "trip"})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query != "boat trip" || parsed.Limit != 3 || parsed.Format != "json" {
		t.Fatalf("launcher-style parsed search = %#v", parsed)
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
		"Capabilities: metadata, status, doctor, sync, classify, search, short_refs, open, neighbors",
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

func TestCommandWritesCrawlkitLog(t *testing.T) {
	_, _, err, root := captureRunOutputWithRuntime(t, []string{"metadata"})
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(root, "state", "photoscrawl", "logs", "current.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 4 {
		t.Fatalf("log lines = %d, want at least 4:\n%s", len(lines), data)
	}
	if line, ok := cklog.ParseLine(lines[0]); !ok || line.Event != "grammar" || line.Message != cklog.Grammar {
		t.Fatalf("log header does not use crawlkit grammar: %q", lines[0])
	}
	if !strings.Contains(string(data), " metadata start: ") || !strings.Contains(string(data), " metadata_written: ") || !strings.Contains(string(data), " metadata finish: outcome=success") {
		t.Fatalf("metadata command was not logged with start, event and finish:\n%s", data)
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

func TestSearchHumanOutputUsesShortRefWhenAvailable(t *testing.T) {
	var out strings.Builder
	err := printSearchText(&out, archive.SearchResult{
		Query:        "receipt",
		TotalMatches: 1,
		Results: []archive.SearchHit{{
			Ref:      "photoscrawl:asset/fixture",
			ShortRef: "7abc9",
			Time:     "2026-05-28T12:00:00+02:00",
			Snippet:  "receipt candidate from local metadata",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := out.String()
	assertHumanProseOutput(t, got,
		"2026-05-28T12:00:00+02:00 | 7abc9",
	)
	if strings.Contains(got, "photoscrawl:asset/fixture") {
		t.Fatalf("human search output used full ref despite short ref:\n%s", got)
	}
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

func TestOpenAndNeighborsAcceptShortRef(t *testing.T) {
	dbPath, shortRef, fullRef := buildCLIShortRefArchive(t)

	human, errOut, err := captureRunOutput(t, []string{"open", shortRef, "--db", dbPath})
	if err != nil {
		t.Fatalf("open short ref text: %v stderr=%s stdout=%s", err, errOut, human)
	}
	assertHumanProseOutput(t, human, fullRef, "Captured:", "Media:")

	jsonOut, errOut, err := captureRunOutput(t, []string{"open", shortRef, "--db", dbPath, "--json"})
	if err != nil {
		t.Fatalf("open short ref json: %v stderr=%s stdout=%s", err, errOut, jsonOut)
	}
	var opened archive.OpenResult
	if err := json.Unmarshal([]byte(jsonOut), &opened); err != nil {
		t.Fatal(err)
	}
	if opened.Ref != fullRef {
		t.Fatalf("opened ref = %q, want %q", opened.Ref, fullRef)
	}
	if strings.Contains(jsonOut, "short_ref") {
		t.Fatalf("open JSON leaked short ref as identity:\n%s", jsonOut)
	}

	neighborsOut, errOut, err := captureRunOutput(t, []string{"neighbors", shortRef, "--db", dbPath, "--json"})
	if err != nil {
		t.Fatalf("neighbors short ref json: %v stderr=%s stdout=%s", err, errOut, neighborsOut)
	}
	var neighbors archive.NeighborResult
	if err := json.Unmarshal([]byte(neighborsOut), &neighbors); err != nil {
		t.Fatal(err)
	}
	if neighbors.Ref != fullRef {
		t.Fatalf("neighbors ref = %q, want %q", neighbors.Ref, fullRef)
	}
}

func TestShortRefErrorsAreStructured(t *testing.T) {
	dbPath, _, _ := buildCLIShortRefArchive(t)

	unknown := unusedShortAlias(t, dbPath)
	out, _, err := captureRunOutput(t, []string{"open", unknown, "--db", dbPath, "--json"})
	if err == nil {
		t.Fatal("expected unknown short ref error")
	}
	assertWrittenErrorCode(t, err, out, "unknown_short_ref")

	makeAmbiguousShortRef(t, dbPath, "22222")
	out, _, err = captureRunOutput(t, []string{"open", "22222", "--db", dbPath, "--json"})
	if err == nil {
		t.Fatal("expected ambiguous short ref error")
	}
	assertWrittenErrorCode(t, err, out, "ambiguous_short_ref")
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
	stdout, stderr, err, _ := captureRunOutputWithRuntime(t, args)
	return stdout, stderr, err
}

func captureRunOutputWithRuntime(t *testing.T, args []string) (string, string, error, string) {
	t.Helper()
	root := setTestRuntimeEnv(t)
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
	return string(stdout), string(stderr), runErr, root
}

func setTestRuntimeEnv(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	return root
}

func buildCLIShortRefArchive(t *testing.T) (string, string, string) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	dbPath := filepath.Join(root, "photos.sqlite")
	libraryPath := filepath.Join(root, "Fixture Photos Library.photoslibrary")
	if err := os.MkdirAll(libraryPath, 0o755); err != nil {
		t.Fatal(err)
	}
	paths := archive.Paths{
		DataDir:  filepath.Join(root, "data"),
		Database: dbPath,
		CacheDir: filepath.Join(root, "cache"),
		LogDir:   filepath.Join(root, "state", "photoscrawl", "logs"),
	}
	_, err := archive.Sync(ctx, paths, archive.SyncOptions{
		LibraryPath: libraryPath,
		Provider: cliFixtureProvider{snapshot: photos.LibrarySnapshot{
			Provider:      "fixture",
			PhotosVersion: "fixture",
			Assets: []photos.Asset{
				{
					LocalIdentifier: "fixture-asset-1",
					MediaType:       "image",
					MediaSubtypes:   "0",
					CreationDate:    "2026-05-28T10:00:00Z",
					Width:           100,
					Height:          80,
					Resources: []photos.Resource{
						{Type: "photo", UTI: "public.heic", OriginalFilename: "Synthetic Beach.heic", Availability: "local", AvailableLocally: true},
					},
					Albums: []photos.AlbumMembership{
						{AlbumID: "album-1", AlbumTitle: "Beach", AlbumKind: "album"},
					},
				},
				{
					LocalIdentifier: "fixture-asset-2",
					MediaType:       "image",
					MediaSubtypes:   "0",
					CreationDate:    "2026-05-28T10:03:00Z",
					Width:           100,
					Height:          80,
					Resources: []photos.Resource{
						{Type: "photo", UTI: "public.heic", OriginalFilename: "Synthetic Kitchen.heic", Availability: "local", AvailableLocally: true},
					},
				},
			},
		}},
		Now: func() time.Time {
			parsed, _ := time.Parse(time.RFC3339, "2026-05-28T11:00:00Z")
			return parsed
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := archive.Search(ctx, paths, archive.SearchOptions{Query: "beach", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Results) != 1 || result.Results[0].ShortRef == "" {
		t.Fatalf("search result did not include short ref: %#v", result.Results)
	}
	return dbPath, result.Results[0].ShortRef, result.Results[0].Ref
}

type cliFixtureProvider struct {
	snapshot photos.LibrarySnapshot
}

func (p cliFixtureProvider) Snapshot(context.Context, string) (photos.LibrarySnapshot, error) {
	return p.snapshot, nil
}

func unusedShortAlias(t *testing.T, dbPath string) string {
	t.Helper()
	db, err := store.Open(context.Background(), store.Options{Path: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, alias := range []string{"22222", "33333", "44444", "55555", "66666"} {
		var count int
		if err := db.DB().QueryRow(`select count(*) from short_refs where alias = ?`, alias).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == 0 {
			return alias
		}
	}
	t.Fatal("could not find unused short alias")
	return ""
}

func makeAmbiguousShortRef(t *testing.T, dbPath, alias string) {
	t.Helper()
	db, err := store.Open(context.Background(), store.Options{Path: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.DB().Exec(`delete from short_refs`); err != nil {
		t.Fatal(err)
	}
	for _, fullRef := range []string{"photoscrawl:asset/synthetic-a", "photoscrawl:asset/synthetic-b"} {
		if _, err := db.DB().Exec(`insert into short_refs(alias, full_ref) values(?, ?)`, alias, fullRef); err != nil {
			t.Fatal(err)
		}
	}
}

func assertErrorCode(t *testing.T, output, code string) {
	t.Helper()
	var payload struct {
		Error commandError `json:"error"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("error JSON = %s err=%v", output, err)
	}
	if payload.Error.Code != code {
		t.Fatalf("error code = %q, want %q in %s", payload.Error.Code, code, output)
	}
}

func assertWrittenErrorCode(t *testing.T, err error, output, code string) {
	t.Helper()
	if strings.TrimSpace(output) == "" {
		var written strings.Builder
		if writeErr := writeError(&written, err); writeErr != nil {
			t.Fatal(writeErr)
		}
		output = written.String()
	}
	assertErrorCode(t, output, code)
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
