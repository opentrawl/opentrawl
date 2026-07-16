package photoscrawl

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	sourcephotos "github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	"github.com/opentrawl/opentrawl/trawlkit/output"
	"github.com/opentrawl/opentrawl/trawlkit/store"
	"google.golang.org/protobuf/proto"
)

const photosTestRunSubcommand = "photos-test-run"

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == photosTestRunSubcommand {
		source := New()
		if provider := testSnapshotProviderFromEnv(); provider != nil {
			source.snapshotProvider = provider
		}
		os.Exit(trawlkit.Run(os.Args[2:], []trawlkit.Crawler{source}))
	}
	if len(os.Args) > 1 && os.Args[1] == trawlkit.HiddenWireSubcommand {
		source := New()
		if provider := testSnapshotProviderFromEnv(); provider != nil {
			source.snapshotProvider = provider
		}
		os.Exit(trawlkit.Run(os.Args[1:], []trawlkit.Crawler{source}))
	}
	os.Exit(m.Run())
}

func TestOpenRecordCallsItsLoaderOnce(t *testing.T) {
	assertOpenRecordLoaderCall(t, "open_record.go", "loadOpenAsset")
}

func assertOpenRecordLoaderCall(t *testing.T, path, loader string) {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Recv == nil || function.Name.Name != "OpenRecord" {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if ok && selector.Sel.Name == loader {
				calls++
			}
			return true
		})
	}
	if calls != 1 {
		t.Fatalf("OpenRecord %s calls = %d, want 1", loader, calls)
	}
}

func TestStatusUsesOnlyArchiveState(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	crawler := New()
	crawler.cfg.LibraryPath = filepath.Join(t.TempDir(), "synthetic-missing.photoslibrary")
	status, err := crawler.Status(context.Background(), &trawlkit.Request{Paths: trawlkit.Paths{Archive: filepath.Join(t.TempDir(), "photos.db")}})
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "missing" || len(status.SetupRequirements) != 0 {
		t.Fatalf("status = %#v, want missing archive without source setup", status)
	}
}

func TestRunnerPreparesOldArchiveForStatusSearchAndOpen(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("HOME", home)
	archivePath := filepath.Join(home, ".opentrawl", "photos", "photos.db")
	oldSchema := strings.ReplaceAll(archive.Schema, ",\n  stale_since text,\n  stale_reason text,\n  superseded_at text", "")
	oldSchema = strings.ReplaceAll(oldSchema, ",\n  generation_id text references model_generation(id)", "")
	db, err := store.Open(ctx, store.Options{Path: archivePath, Schema: oldSchema, SchemaVersion: archive.SchemaVersion - 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().ExecContext(ctx, `
insert into source_library(id, library_path, snapshot_path, snapshot_created_at, photos_version, metadata_json)
values ('source:runner-fixture', '/tmp/runner-fixture.photoslibrary', 'sqlite:crawl_snapshot/runner-fixture', '2026-07-14T10:00:00Z', 'fixture', '{}');
insert into asset(id, local_identifier, media_type, media_subtypes, creation_date, modification_date, added_date, timezone_name,
  width, height, duration_seconds, favorite, hidden, burst_identifier, represents_burst,
  camera_make, camera_model, lens_model, source_library_id, metadata_json)
values ('asset:runner-fixture', 'runner-fixture', 'image', '0', '2026-07-14T10:00:00Z', '2026-07-14T10:00:00Z',
  '2026-07-14T10:00:00Z', 'UTC', 100, 80, 0, 0, 0, '', 0, '', '', '', 'source:runner-fixture', '{}');
insert into model_observation(id, asset_id, observation_type, value_text, value_json, confidence, source, model_id, prompt_version, evidence_id)
values ('observation:runner-fixture', 'asset:runner-fixture', 'card_summary', 'Synthetic runner migrationterm card.', '{}', 1.0, 'fixture', 'fixture-model', 'v1', '');
`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := captureRun(t, []string{"status", "--json"})
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"state": "ok"`) {
		t.Fatalf("status code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	stdout, stderr, code = captureRun(t, []string{"search", "migrationterm", "--json"})
	if code != 0 || stderr != "" || !strings.Contains(stdout, "photos:asset/runner-fixture") {
		t.Fatalf("search code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	stdout, stderr, code = captureRun(t, []string{"open", "photos:asset/runner-fixture", "--json"})
	if code != 0 || stderr != "" || !strings.Contains(stdout, "Synthetic runner migrationterm card.") {
		t.Fatalf("open code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}

func TestRunnerKeepsIncompatibleArchiveError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	archivePath := filepath.Join(home, ".opentrawl", "photos", "photos.db")
	db, err := store.Open(context.Background(), store.Options{Path: archivePath, Schema: archive.Schema, SchemaVersion: archive.SchemaVersion + 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := captureRun(t, []string{"search", "fixture", "--json"})
	if code == 0 || stderr != "" {
		t.Fatalf("search code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var envelope output.ErrorEnvelope
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("search error JSON: %v\n%s", err, stdout)
	}
	want := output.ErrorBody{Code: "archive_incompatible", Message: "The Photos archive needs to be updated.", Remedy: "run trawl sync photos, then retry"}
	if envelope.Error.Code != want.Code || envelope.Error.Message != want.Message || envelope.Error.Remedy != want.Remedy {
		t.Fatalf("search error = %#v, want %#v", envelope.Error, want)
	}
}

func TestPhotosAccessRequirementMapsAllStates(t *testing.T) {
	for _, test := range []struct {
		status string
		id     string
		state  control.SetupState
		action control.SetupActionKind
	}{
		{status: "not_determined", id: "photos_access_not_determined", state: control.SetupStateNeedsAction, action: control.SetupActionRequestPhotos},
		{status: "restricted", id: "photos_access_restricted", state: control.SetupStateUnavailable, action: control.SetupActionNone},
		{status: "denied", id: "photos_access_denied", state: control.SetupStateNeedsAction, action: control.SetupActionNone},
		{status: "authorized", id: "photos_access_authorized", state: control.SetupStateReady, action: control.SetupActionNone},
		{status: "limited", id: "photos_access_limited", state: control.SetupStateNeedsAction, action: control.SetupActionNone},
	} {
		t.Run(test.status, func(t *testing.T) {
			requirement := photosAccessSetupRequirement(test.status)
			if requirement.ID != test.id || requirement.State != test.state || requirement.Action != test.action {
				t.Fatalf("requirement = %#v", requirement)
			}
		})
	}
}

func TestRequestPhotosAccessUsesTheSharedHelperPath(t *testing.T) {
	oldStatus := photosAccessStatus
	t.Cleanup(func() { photosAccessStatus = oldStatus })
	requested := false
	photosAccessStatus = func(_ context.Context, request bool) (string, error) {
		requested = request
		return "authorized", nil
	}
	requirement, err := New().RequestPhotosAccess(context.Background())
	if err != nil || !requested || requirement.ID != "photos_access_authorized" {
		t.Fatalf("requirement=%#v err=%v requested=%t", requirement, err, requested)
	}
}

func TestPhotosPermissionStatusIsInternalToTheAppRequest(t *testing.T) {
	oldStatus := photosAccessStatus
	t.Cleanup(func() { photosAccessStatus = oldStatus })
	calls := 0
	photosAccessStatus = func(context.Context, bool) (string, error) {
		calls++
		return "authorized", nil
	}
	crawler := New()
	if requirements := crawler.photosSetupRequirements(context.Background()); len(requirements) != 0 || calls != 0 {
		t.Fatalf("public requirements=%#v calls=%d", requirements, calls)
	}
	requirements := crawler.photosSetupRequirements(trawlkit.WithInternalAppRequest(context.Background()))
	if len(requirements) != 1 || requirements[0].Kind != control.SetupKindPhotosPermission || calls != 1 {
		t.Fatalf("app requirements=%#v calls=%d", requirements, calls)
	}
}

func TestRunnerManifestListsCapabilitiesAndClassify(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	t.Setenv("HOME", home)
	stdout, stderr, code := captureRun(t, []string{"metadata", "--json"})
	if code != 0 || stderr != "" {
		t.Fatalf("metadata code=%d stderr=%q stdout=%s", code, stderr, stdout)
	}
	var manifest control.Manifest
	if err := json.Unmarshal([]byte(stdout), &manifest); err != nil {
		t.Fatalf("manifest JSON: %v\n%s", err, stdout)
	}
	if manifest.SchemaVersion != control.RunnerManifestVersion {
		t.Fatalf("schema_version = %d", manifest.SchemaVersion)
	}
	for _, capability := range []string{"metadata", "status", "sync", "classify", "acquire_current_still", "search", "short_refs", "open"} {
		if !slices.Contains(manifest.Capabilities, capability) {
			t.Fatalf("missing capability %q in %#v", capability, manifest.Capabilities)
		}
	}
	for _, capability := range []string{"who"} {
		if slices.Contains(manifest.Capabilities, capability) {
			t.Fatalf("unexpected capability %q in %#v", capability, manifest.Capabilities)
		}
	}
	if got := manifest.Paths.DefaultDatabase; got != filepath.Join(home, ".opentrawl", "photos", "photos.db") {
		t.Fatalf("default database = %q", got)
	}
	classify := manifest.Commands["classify"]
	if !classify.Mutates || !classify.JSON {
		t.Fatalf("classify command = %#v", classify)
	}
	for _, name := range []string{"limit", "model"} {
		if !commandHasFlag(classify, name) {
			t.Fatalf("classify flags missing %q: %#v", name, classify.Flags)
		}
	}
	acquire := manifest.Commands["acquire_current_still"]
	if !acquire.Mutates || acquire.Store != "none" || !acquire.JSON {
		t.Fatalf("acquire-current-still command = %#v", acquire)
	}
	for _, name := range []string{"asset", "source-library"} {
		if !commandHasFlag(acquire, name) {
			t.Fatalf("acquire-current-still flags missing %q: %#v", name, acquire.Flags)
		}
	}
	readiness := manifest.Commands["select_card_input_ready"]
	if readiness.Mutates || readiness.Store != "none" || !readiness.JSON {
		t.Fatalf("select-card-input-ready command = %#v", readiness)
	}
	for _, name := range []string{"source-library", "exclude-asset"} {
		if !commandHasFlag(readiness, name) {
			t.Fatalf("select-card-input-ready flags missing %q: %#v", name, readiness.Flags)
		}
	}
	if !manifest.Commands["sync"].Mutates {
		t.Fatalf("sync command = %#v", manifest.Commands["sync"])
	}
}

func TestCrawlerSyncSearchOpenAndClassify(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	stateRoot := filepath.Join(root, "state")
	paths := trawlkit.Paths{
		Archive: filepath.Join(stateRoot, "photos", "photos.db"),
		Config:  filepath.Join(stateRoot, "photos", "config.toml"),
		Logs:    filepath.Join(stateRoot, "photos", "logs"),
	}
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	libraryPath := filepath.Join(home, "Pictures", "Photos Library.photoslibrary")
	createSyntheticLibrary(t, libraryPath)
	snapshot, err := (sourcephotos.SQLiteSnapshotProvider{}).Snapshot(ctx, libraryPath)
	if err != nil {
		t.Fatal(err)
	}

	source := New()
	source.cfg.LibraryPath = "/synthetic/Photos Library.photoslibrary"
	source.snapshotProvider = staticSnapshotProvider{snapshot: snapshot}
	writeStore, err := store.Open(ctx, store.Options{Path: paths.Archive})
	if err != nil {
		t.Fatal(err)
	}
	syncReq := &trawlkit.Request{Store: writeStore, Paths: paths, Progress: func(trawlkit.Progress) {}}
	report, err := source.Sync(ctx, syncReq)
	if err == nil {
		var records []trawlkit.ShortRefRecord
		records, err = source.ShortRefRecords(ctx, syncReq)
		if err == nil {
			_, err = syncReq.AssignShortRefs(ctx, records)
		}
	}
	if closeErr := writeStore.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	if report.Added != 1 || report.Updated != 0 || report.Removed != 0 {
		t.Fatalf("sync report = %#v", report)
	}

	readStore := openReadStore(t, ctx, paths.Archive)
	searchReq := readRequest(readStore, paths)
	search, err := source.Search(ctx, searchReq, trawlkit.Query{Text: "synthetic", Limit: 20})
	fillTestShortRefs(t, ctx, searchReq, search.Results)
	_ = readStore.Close()
	if err != nil {
		t.Fatal(err)
	}
	if search.TotalMatches != 1 || len(search.Results) != 1 {
		t.Fatalf("search = %#v", search)
	}
	hit := search.Results[0]
	if hit.ShortRef == "" || hit.Ref == "" || hit.AnchorID != "filename" || hit.Summary.Title == "" || len(hit.Evidence) != 1 {
		t.Fatalf("search hit = %#v", hit)
	}
	filename := hit.Evidence[0]
	filenameMatched := false
	if filename.Field != nil {
		for _, run := range filename.Field.Value {
			filenameMatched = filenameMatched || run.Matched && strings.Contains(run.Text, "synthetic")
		}
	}
	if filename.Label != "Original filename" || filename.Field == nil || filename.Field.Name != "filename" || !filenameMatched {
		t.Fatalf("filename evidence = %#v", filename)
	}

	readStore = openReadStore(t, ctx, paths.Archive)
	var openOut bytes.Buffer
	err = source.Open(ctx, &trawlkit.Request{Store: readStore, Paths: paths, Format: output.JSON, Out: &openOut}, hit.ShortRef)
	_ = readStore.Close()
	if err != nil {
		t.Fatal(err)
	}
	var opened archive.OpenResult
	if err := json.Unmarshal(openOut.Bytes(), &opened); err != nil {
		t.Fatalf("open JSON: %v\n%s", err, openOut.String())
	}
	if opened.Ref != hit.Ref || opened.Mechanical.Media == nil || opened.Mechanical.Media.Width != 4032 {
		t.Fatalf("opened = %#v", opened)
	}
	if strings.Contains(openOut.String(), "short_ref") {
		t.Fatalf("open JSON leaked short ref:\n%s", openOut.String())
	}

	readStore = openReadStore(t, ctx, paths.Archive)
	fullRecord, err := source.OpenRecord(ctx, &trawlkit.Request{Store: readStore, Paths: paths}, hit.Ref)
	_ = readStore.Close()
	if err != nil {
		t.Fatal(err)
	}
	readStore = openReadStore(t, ctx, paths.Archive)
	shortRecord, err := source.OpenRecord(ctx, &trawlkit.Request{Store: readStore, Paths: paths}, hit.ShortRef)
	_ = readStore.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(fullRecord, shortRecord) || shortRecord.OpenRef != hit.Ref || shortRecord.Data.GetTypeUrl() != "type.googleapis.com/trawl.source.photos.open.v1.PhotosRecord" || shortRecord.Presentation == nil {
		t.Fatalf("open records full=%#v short=%#v", fullRecord, shortRecord)
	}
	load := func(ref string) archive.OpenResult {
		readStore = openReadStore(t, ctx, paths.Archive)
		value, loadErr := source.loadOpenAsset(ctx, &trawlkit.Request{Store: readStore, Paths: paths}, ref)
		_ = readStore.Close()
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		return value
	}
	captureLegacy := func(caseName, ref string) {
		goldens := map[string]string{"json": "fae78acb0b146d7865bd805c18b63f0d66caa386a13792ca93d5cd833ab79544", "text": "b31d653ef54ec3d5c2892473c6730c78688c02b2601608c9b22c0037f0a1f03b"}
		for _, format := range []struct {
			name  string
			value output.Format
		}{{"json", output.JSON}, {"text", output.Text}} {
			readStore = openReadStore(t, ctx, paths.Archive)
			var stdout bytes.Buffer
			openErr := source.Open(ctx, &trawlkit.Request{Store: readStore, Paths: paths, Format: format.value, Out: &stdout}, ref)
			_ = readStore.Close()
			writeLegacyOpenEvidence(t, "photos", caseName, format.name, stdout.Bytes(), openErr)
			assertLegacyOpenGolden(t, stdout.Bytes(), openErr, goldens[format.name])
			if openErr != nil {
				t.Fatal(openErr)
			}
		}
	}
	writeRuntimeOpenEvidence(t, "photos", "full", hit.Ref, load(hit.Ref), fullRecord)
	writeRuntimeOpenEvidence(t, "photos", "short", hit.ShortRef, load(hit.ShortRef), shortRecord)
	captureLegacy("full", hit.Ref)
	captureLegacy("short", hit.ShortRef)
	readStore = openReadStore(t, ctx, paths.Archive)
	_, err = source.OpenRecord(ctx, &trawlkit.Request{Store: readStore, Paths: paths}, "zzzzz")
	_ = readStore.Close()
	if typed, ok := err.(commandError); !ok || typed.Code != "unknown_short_ref" {
		t.Fatalf("unknown short ref error = %#v", err)
	}
	writeStore, err = store.Open(ctx, store.Options{Path: paths.Archive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writeStore.DB().ExecContext(ctx, `insert into short_refs(alias, full_ref, canonical_ref) values (?, ?, ?), (?, ?, ?)`, "zzzzz", hit.Ref, hit.Ref, "zzzzz", "photos:asset/missing", "photos:asset/missing"); err != nil {
		_ = writeStore.Close()
		t.Fatal(err)
	}
	if err := writeStore.Close(); err != nil {
		t.Fatal(err)
	}
	readStore = openReadStore(t, ctx, paths.Archive)
	_, err = source.OpenRecord(ctx, &trawlkit.Request{Store: readStore, Paths: paths}, "zzzzz")
	_ = readStore.Close()
	if typed, ok := err.(commandError); !ok || typed.Code != "ambiguous_short_ref" {
		t.Fatalf("ambiguous short ref error = %#v", err)
	}
	readStore = openReadStore(t, ctx, paths.Archive)
	_, err = source.OpenRecord(ctx, &trawlkit.Request{Store: readStore, Paths: paths}, "photos:asset/missing")
	_ = readStore.Close()
	if err == nil || err.Error() != "asset not found: asset:missing" {
		t.Fatalf("missing asset ref error = %#v", err)
	}
	for ref, want := range map[string]string{
		"calendar:event/example": "asset not found: calendar:event:example",
		"photos:asset/":          "asset not found: asset:",
	} {
		readStore = openReadStore(t, ctx, paths.Archive)
		_, err = source.OpenRecord(ctx, &trawlkit.Request{Store: readStore, Paths: paths}, ref)
		_ = readStore.Close()
		if err == nil || err.Error() != want {
			t.Fatalf("open %q error = %#v, want %q", ref, err, want)
		}
	}
	_, err = source.OpenRecord(ctx, &trawlkit.Request{Paths: trawlkit.Paths{Archive: paths.Archive + ".missing"}}, hit.Ref)
	if err == nil || err.Error() != "photos read store is required" {
		t.Fatalf("missing runner store error = %#v", err)
	}

	classifyStore, err := store.Open(ctx, store.Options{Path: paths.Archive})
	if err != nil {
		t.Fatal(err)
	}
	var classifyOut bytes.Buffer
	classify := source.Verbs()[0]
	fs := flag.NewFlagSet("classify", flag.ContinueOnError)
	classify.Flags(fs)
	if err := fs.Parse([]string{"--limit", "1"}); err != nil {
		t.Fatal(err)
	}
	err = classify.Run(ctx, &trawlkit.Request{Store: classifyStore, Paths: paths, Format: output.JSON, Out: &classifyOut, Progress: func(trawlkit.Progress) {}})
	if closeErr := classifyStore.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	var classified archive.ClassifyResult
	if err := json.Unmarshal(classifyOut.Bytes(), &classified); err != nil {
		t.Fatalf("classify JSON: %v\n%s", err, classifyOut.String())
	}
	if classified.Processed != 1 || classified.MetadataClassified != 1 {
		t.Fatalf("classify = %#v", classified)
	}
}

func TestStatusSearchAndOpenAgreeOnIncompatibleArchive(t *testing.T) {
	ctx := context.Background()
	paths := trawlkit.Paths{Archive: filepath.Join(t.TempDir(), "photos.db")}
	writeStore, err := store.Open(ctx, store.Options{
		Path:          paths.Archive,
		Schema:        archive.Schema,
		SchemaVersion: archive.SchemaVersion - 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := writeStore.Close(); err != nil {
		t.Fatal(err)
	}

	source := New()
	status, err := source.Status(ctx, &trawlkit.Request{Paths: paths})
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "error" || status.Summary != "Photos archive status cannot be read." || len(status.Counts) != 0 || len(status.Errors) != 1 || status.Errors[0] != "photos archive is incompatible" {
		t.Fatalf("status = %#v", status)
	}
	assertIncompatible := func(t *testing.T, err error) {
		t.Helper()
		got, ok := err.(commandError)
		want := commandError{
			Code:    "archive_incompatible",
			Message: "The Photos archive needs to be updated.",
			Remedy:  "run trawl sync photos, then retry",
		}
		if !ok || got != want {
			t.Fatalf("error = %#v, want %#v", err, want)
		}
	}

	readStore := openReadStore(t, ctx, paths.Archive)
	_, err = source.Search(ctx, readRequest(readStore, paths), trawlkit.Query{Text: "fixture", Limit: 1})
	_ = readStore.Close()
	assertIncompatible(t, err)

	readStore = openReadStore(t, ctx, paths.Archive)
	err = source.Open(ctx, readRequest(readStore, paths), "photos:asset/fixture")
	_ = readStore.Close()
	assertIncompatible(t, err)
}

func TestSearchAndOpenUseRunnerProvidedReadStore(t *testing.T) {
	ctx := context.Background()
	paths := trawlkit.Paths{Archive: filepath.Join(t.TempDir(), "photos.db")}
	writeStore, err := store.Open(ctx, store.Options{
		Path:          paths.Archive,
		Schema:        archive.Schema,
		SchemaVersion: archive.SchemaVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := writeStore.Close(); err != nil {
		t.Fatal(err)
	}
	readStore := openReadStore(t, ctx, paths.Archive)
	defer func() { _ = readStore.Close() }()
	if err := os.Rename(paths.Archive, paths.Archive+".moved"); err != nil {
		t.Fatal(err)
	}

	source := New()
	search, err := source.Search(ctx, readRequest(readStore, paths), trawlkit.Query{Text: "fixture", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if search.TotalMatches != 0 || len(search.Results) != 0 {
		t.Fatalf("search = %#v", search)
	}
	err = source.Open(ctx, readRequest(readStore, paths), "photos:asset/fixture")
	if err == nil || err.Error() != "asset not found: asset:fixture" {
		t.Fatalf("open error = %v, want missing fixture asset", err)
	}
}

func TestSearchKeepsDatelessAssets(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	stateRoot := filepath.Join(home, ".opentrawl")
	paths := trawlkit.Paths{
		Archive: filepath.Join(stateRoot, "photos", "photos.db"),
		Config:  filepath.Join(stateRoot, "photos", "config.toml"),
		Logs:    filepath.Join(stateRoot, "photos", "logs"),
	}
	t.Setenv("HOME", home)
	libraryPath := filepath.Join(home, "Pictures", "Photos Library.photoslibrary")
	createSyntheticLibrary(t, libraryPath)
	insertSyntheticDatelessAsset(t, libraryPath)

	source := New()
	writeStore, err := store.Open(ctx, store.Options{Path: paths.Archive})
	if err != nil {
		t.Fatal(err)
	}
	_, err = source.Sync(ctx, &trawlkit.Request{Store: writeStore, Paths: paths, Progress: func(trawlkit.Progress) {}})
	if closeErr := writeStore.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}

	readStore := openReadStore(t, ctx, paths.Archive)
	search, err := source.Search(ctx, readRequest(readStore, paths), trawlkit.Query{Text: "dateless", Limit: 20})
	_ = readStore.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(search.Results) != 1 {
		t.Fatalf("search results = %#v", search.Results)
	}
	if !search.Results[0].Time.IsZero() {
		t.Fatalf("dateless search time = %s, want zero time", search.Results[0].Time)
	}
}

func TestRunSyncThroughTrawlkitChildCreatesArchive(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	t.Setenv("HOME", home)
	t.Setenv("TRAWLKIT_STATE_ROOT", "")
	t.Setenv("TRAWLKIT_RUN_ID", "")
	createSyntheticLibrary(t, filepath.Join(home, "Pictures", "Photos Library.photoslibrary"))

	stdout, stderr, code := captureRun(t, []string{"sync", "--json"})
	if code != 0 {
		t.Fatalf("sync code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	archivePath := filepath.Join(home, ".opentrawl", "photos", "photos.db")
	if _, err := os.Stat(archivePath); err != nil {
		t.Fatalf("archive was not created at %s: %v", archivePath, err)
	}
}

func TestSyncWarningsAndStatusQueueCounts(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	t.Setenv("HOME", home)
	t.Setenv("TRAWLKIT_STATE_ROOT", "")
	t.Setenv("TRAWLKIT_RUN_ID", "")
	libraryPath := filepath.Join(home, "Pictures", "Photos Library.photoslibrary")
	createSyntheticLibrary(t, libraryPath)
	archivePath := filepath.Join(home, ".opentrawl", "photos", "photos.db")

	stdout, stderr, code := captureRun(t, []string{"sync", "--json"})
	if code != 0 {
		t.Fatalf("first sync code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	seedStaleObservationRows(t, archivePath, "json")
	setSyntheticFavorite(t, libraryPath, 0)

	stdout, stderr, code = captureRun(t, []string{"sync", "--json"})
	if code != 0 {
		t.Fatalf("json sync code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var report trawlkit.SyncReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("sync JSON: %v\n%s", err, stdout)
	}
	for _, warning := range []string{
		"marked_stale_model_assets=1",
		"marked_stale_model_rows=2",
		"marked_stale_place_assets=1",
		"marked_stale_place_rows=1",
	} {
		if !slices.Contains(report.Warnings, warning) {
			t.Fatalf("sync warnings missing %q in %#v", warning, report.Warnings)
		}
	}

	assertSyncWrittenLog(t, filepath.Join(home, ".opentrawl", "photos", "logs", "current.log"),
		"provider=photos_sqlite_snapshot completeness=complete assets=1 new=0 changed=1 unchanged=0 missing=0 "+
			"queued_for_classify=1 queued_needs_download=1 classification_queue_pending=1 "+
			"marked_stale_model_assets=1 marked_stale_model_rows=2 "+
			"marked_stale_place_assets=1 marked_stale_place_rows=1")

	seedStaleObservationRows(t, archivePath, "human")
	setSyntheticFavorite(t, libraryPath, 1)
	stdout, stderr, code = captureRun(t, []string{"sync"})
	if code != 0 {
		t.Fatalf("human sync code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	for _, warning := range []string{
		"marked_stale_model_assets=1",
		"marked_stale_model_rows=2",
		"marked_stale_place_assets=1",
		"marked_stale_place_rows=1",
	} {
		if !strings.Contains(stdout, warning) {
			t.Fatalf("human sync output missing %q:\n%s", warning, stdout)
		}
	}

	stdout, stderr, code = captureRun(t, []string{"status", "--json"})
	if code != 0 {
		t.Fatalf("status code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var status control.Status
	if err := json.Unmarshal([]byte(stdout), &status); err != nil {
		t.Fatalf("status JSON: %v\n%s", err, stdout)
	}
	for _, id := range []string{"queued_for_classify", "queued_needs_download", "classification_queue_pending"} {
		if !statusHasCount(status.Counts, id) {
			t.Fatalf("status counts missing %q in %#v", id, status.Counts)
		}
	}
}

func TestClassifyLimitContractIsUsageError(t *testing.T) {
	for _, args := range [][]string{
		{"--limit", "0"},
	} {
		source := New()
		classify := source.Verbs()[0]
		fs := flag.NewFlagSet("classify", flag.ContinueOnError)
		classify.Flags(fs)
		if err := fs.Parse(args); err != nil {
			t.Fatal(err)
		}
		err := classify.Run(context.Background(), &trawlkit.Request{Out: io.Discard})
		if !output.IsUsage(err) {
			t.Fatalf("classify %v error = %v, want usage", args, err)
		}
	}
}

func captureRun(t *testing.T, args []string) (string, string, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	command := exec.Command(os.Args[0], append([]string{photosTestRunSubcommand}, args...)...)
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if err == nil {
		return stdout.String(), stderr.String(), 0
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatal(err)
	}
	return stdout.String(), stderr.String(), exitErr.ExitCode()
}

func commandHasFlag(command control.Command, name string) bool {
	for _, flag := range command.Flags {
		if flag.Name == name {
			return true
		}
	}
	return false
}

func readRequest(st *store.Store, paths trawlkit.Paths) *trawlkit.Request {
	return &trawlkit.Request{
		Store: st,
		Paths: paths,
		Out:   io.Discard,
	}
}

func fillTestShortRefs(t *testing.T, ctx context.Context, req *trawlkit.Request, hits []trawlkit.Hit) {
	t.Helper()
	refs := make([]string, 0, len(hits))
	for _, hit := range hits {
		refs = append(refs, hit.Ref)
	}
	aliases, err := req.ShortRefAliases(ctx, refs)
	if err != nil {
		t.Fatal(err)
	}
	for i := range hits {
		hits[i].ShortRef = aliases[hits[i].Ref]
	}
}

func openReadStore(t *testing.T, ctx context.Context, path string) *store.Store {
	t.Helper()
	st, err := store.OpenReadOnly(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func createSyntheticLibrary(t *testing.T, libraryPath string) {
	t.Helper()
	dbPath := filepath.Join(libraryPath, "database", "Photos.sqlite")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(context.Background(), store.Options{Path: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := createSyntheticPhotosDB(db.DB()); err != nil {
		t.Fatal(err)
	}
}

func createSyntheticPhotosDB(db *sql.DB) error {
	statements := []string{
		`create table ZASSET (
			Z_PK integer primary key,
			ZUUID varchar,
			ZKIND integer,
			ZKINDSUBTYPE integer,
			ZDATECREATED timestamp,
			ZMODIFICATIONDATE timestamp,
			ZADDEDDATE timestamp,
			ZWIDTH integer,
			ZHEIGHT integer,
			ZDURATION float,
			ZFAVORITE integer,
			ZHIDDEN integer,
			ZAVALANCHEUUID varchar,
			ZLATITUDE float,
			ZLONGITUDE float,
			ZUNIFORMTYPEIDENTIFIER varchar,
			ZFILENAME varchar,
			ZTRASHEDSTATE integer
		)`,
		`create table ZADDITIONALASSETATTRIBUTES (
			ZASSET integer,
			ZTIMEZONENAME varchar,
			ZGPSHORIZONTALACCURACY float,
			ZORIGINALFILENAME varchar
		)`,
		`create table ZEXTENDEDATTRIBUTES (
			ZASSET integer,
			ZTIMEZONENAME varchar,
			ZCAMERAMAKE varchar,
			ZCAMERAMODEL varchar,
			ZLENSMODEL varchar,
			ZFOCALLENGTH float,
			ZFOCALLENGTHIN35MM float,
			ZAPERTURE float,
			ZSHUTTERSPEED float,
			ZISO float
		)`,
		`create table ZINTERNALRESOURCE (
			ZASSET integer,
			ZRESOURCETYPE integer,
			ZCOMPACTUTI varchar,
			ZDATALENGTH integer,
			ZSTABLEHASH varchar,
			ZFINGERPRINT varchar,
			ZLOCALAVAILABILITY integer,
			ZREMOTEAVAILABILITY integer,
			ZVERSION integer
		)`,
		`create table ZGENERICALBUM (
			Z_PK integer primary key,
			ZUUID varchar,
			ZTITLE varchar,
			ZKIND integer,
			ZCLOUDALBUMSUBTYPE integer,
			ZTRASHEDSTATE integer
		)`,
		`create table Z_34ASSETS (
			Z_34ALBUMS integer,
			Z_3ASSETS integer
		)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			return err
		}
	}
	created := coreDataSeconds("2026-05-28T10:00:00Z")
	if _, err := db.Exec(`
insert into ZASSET(Z_PK, ZUUID, ZKIND, ZKINDSUBTYPE, ZDATECREATED, ZMODIFICATIONDATE, ZADDEDDATE, ZWIDTH, ZHEIGHT, ZDURATION, ZFAVORITE, ZHIDDEN, ZAVALANCHEUUID, ZLATITUDE, ZLONGITUDE, ZUNIFORMTYPEIDENTIFIER, ZFILENAME, ZTRASHEDSTATE)
values (1, 'fixture-uuid-1', 0, 0, ?, ?, ?, 4032, 3024, 0, 1, 0, '', 52.3676, 4.9041, 'public.heic', 'synthetic.heic', 0)
`, created, created, created); err != nil {
		return err
	}
	inserts := []string{
		`insert into ZADDITIONALASSETATTRIBUTES(ZASSET, ZTIMEZONENAME, ZGPSHORIZONTALACCURACY, ZORIGINALFILENAME) values (1, 'Europe/Amsterdam', 8.25, 'synthetic.heic')`,
		`insert into ZEXTENDEDATTRIBUTES(ZASSET, ZCAMERAMAKE, ZCAMERAMODEL, ZLENSMODEL, ZFOCALLENGTH, ZFOCALLENGTHIN35MM, ZAPERTURE, ZSHUTTERSPEED, ZISO) values (1, 'Apple', 'iPhone 15 Pro', 'back camera', 6.86, 24, 1.8, 0.008333333333333333, 64)`,
		`insert into ZINTERNALRESOURCE(ZASSET, ZRESOURCETYPE, ZCOMPACTUTI, ZDATALENGTH, ZSTABLEHASH, ZFINGERPRINT, ZLOCALAVAILABILITY, ZREMOTEAVAILABILITY, ZVERSION) values (1, 0, 'public.heic', 12345, 'stable-hash', '', 0, 1, 1)`,
		`insert into ZGENERICALBUM(Z_PK, ZUUID, ZTITLE, ZKIND, ZCLOUDALBUMSUBTYPE, ZTRASHEDSTATE) values (10, 'album-uuid-1', 'Synthetic Album', 2, 0, 0)`,
		`insert into Z_34ASSETS(Z_34ALBUMS, Z_3ASSETS) values (10, 1)`,
	}
	for _, insert := range inserts {
		if _, err := db.Exec(insert); err != nil {
			return err
		}
	}
	return nil
}

func insertSyntheticDatelessAsset(t *testing.T, libraryPath string) {
	t.Helper()
	dbPath := filepath.Join(libraryPath, "database", "Photos.sqlite")
	db := openSyntheticPhotosDB(t, dbPath)
	defer func() { _ = db.Close() }()
	created := coreDataSeconds("2026-05-29T10:00:00Z")
	if _, err := db.DB().Exec(`
insert into ZASSET(Z_PK, ZUUID, ZKIND, ZKINDSUBTYPE, ZDATECREATED, ZMODIFICATIONDATE, ZADDEDDATE, ZWIDTH, ZHEIGHT, ZDURATION, ZFAVORITE, ZHIDDEN, ZAVALANCHEUUID, ZLATITUDE, ZLONGITUDE, ZUNIFORMTYPEIDENTIFIER, ZFILENAME, ZTRASHEDSTATE)
values (2, 'fixture-uuid-dateless', 0, 0, null, ?, ?, 3024, 4032, 0, 0, 0, '', 0, 0, 'public.heic', 'dateless.heic', 0)
`, created, created); err != nil {
		t.Fatal(err)
	}
	inserts := []string{
		`insert into ZADDITIONALASSETATTRIBUTES(ZASSET, ZTIMEZONENAME, ZGPSHORIZONTALACCURACY, ZORIGINALFILENAME) values (2, '', 0, 'dateless.heic')`,
		`insert into ZEXTENDEDATTRIBUTES(ZASSET, ZCAMERAMAKE, ZCAMERAMODEL, ZLENSMODEL, ZFOCALLENGTH, ZFOCALLENGTHIN35MM, ZAPERTURE, ZSHUTTERSPEED, ZISO) values (2, '', '', '', 0, 0, 0, 0, 0)`,
		`insert into ZINTERNALRESOURCE(ZASSET, ZRESOURCETYPE, ZCOMPACTUTI, ZDATALENGTH, ZSTABLEHASH, ZFINGERPRINT, ZLOCALAVAILABILITY, ZREMOTEAVAILABILITY, ZVERSION) values (2, 0, 'public.heic', 222, 'dateless-hash', '', 1, 0, 1)`,
	}
	for _, insert := range inserts {
		if _, err := db.DB().Exec(insert); err != nil {
			t.Fatal(err)
		}
	}
}

func setSyntheticFavorite(t *testing.T, libraryPath string, favorite int) {
	t.Helper()
	db := openSyntheticPhotosDB(t, filepath.Join(libraryPath, "database", "Photos.sqlite"))
	defer func() { _ = db.Close() }()
	if _, err := db.DB().Exec(`update ZASSET set ZFAVORITE = ? where ZUUID = 'fixture-uuid-1'`, favorite); err != nil {
		t.Fatal(err)
	}
}

func openSyntheticPhotosDB(t *testing.T, dbPath string) *store.Store {
	t.Helper()
	db, err := store.Open(context.Background(), store.Options{Path: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func seedStaleObservationRows(t *testing.T, archivePath, suffix string) {
	t.Helper()
	db, err := store.Open(context.Background(), store.Options{
		Path:          archivePath,
		Schema:        archive.Schema,
		SchemaVersion: archive.SchemaVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var assetID string
	if err := db.DB().QueryRow(`select id from asset where local_identifier = 'fixture-uuid-1'`).Scan(&assetID); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ id, observationType, text string }{
		{"fixture-card-summary-" + suffix, "card_summary", "Synthetic beach scene."},
		{"fixture-card-description-" + suffix, "card_description", ""},
	} {
		if _, err := db.DB().Exec(`
insert into model_observation(id, asset_id, observation_type, value_text, value_json, confidence, source, model_id, prompt_version, evidence_id)
values (?, ?, ?, ?, '{}', 1.0, 'model_multimodal', 'fixture-model', 'v1', '')
`, row.id, assetID, row.observationType, row.text); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.DB().Exec(`
insert into place_observation(id, asset_id, observation_type, value_text, value_json, source, provider, cache_status, tier, distance_meters, evidence_id)
values (?, ?, 'venue', 'Synthetic Pier', '{"name":"Synthetic Pier"}', 'place_context', 'apple', 'hit', 'venue_candidate', 12, '')
`, "fixture-place-"+suffix, assetID); err != nil {
		t.Fatal(err)
	}
}

func statusHasCount(counts []control.Count, id string) bool {
	for _, count := range counts {
		if count.ID == id {
			return true
		}
	}
	return false
}

func assertSyncWrittenLog(t *testing.T, path, want string) {
	t.Helper()
	log, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), "sync_written: "+want) {
		t.Fatalf("sync_written log missing %q:\n%s", want, log)
	}
}

func coreDataSeconds(value string) int64 {
	const coreDataUnixOffset = 978307200
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return parsed.Unix() - coreDataUnixOffset
}
