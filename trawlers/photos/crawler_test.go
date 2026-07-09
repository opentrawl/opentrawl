package photoscrawl

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	"github.com/opentrawl/opentrawl/trawlkit/output"
	"github.com/opentrawl/opentrawl/trawlkit/store"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == trawlkit.HiddenWireSubcommand {
		os.Exit(trawlkit.Run(os.Args[1:], []trawlkit.Crawler{New()}))
	}
	os.Exit(m.Run())
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
	for _, capability := range []string{"metadata", "status", "doctor", "sync", "classify", "search", "short_refs", "open"} {
		if !slices.Contains(manifest.Capabilities, capability) {
			t.Fatalf("missing capability %q in %#v", capability, manifest.Capabilities)
		}
	}
	for _, capability := range []string{"who", "contacts_export"} {
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
	t.Setenv("HOME", filepath.Join(root, "home"))
	createSyntheticLibrary(t, filepath.Join(root, "home", "Pictures", "Photos Library.photoslibrary"))

	source := New()
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
			_, err = syncReq.RebuildShortRefs(ctx, records)
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
	if hit.ShortRef == "" || hit.Ref == "" || !strings.Contains(hit.Snippet, "synthetic.heic") {
		t.Fatalf("search hit = %#v", hit)
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
		"provider=photos_sqlite_snapshot assets=1 new=0 changed=1 unchanged=0 missing=0 "+
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
	code := trawlkit.Run(args, []trawlkit.Crawler{New()})
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
	return string(stdout), string(stderr), code
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
		{"fixture-card-description-" + suffix, "card_description", "A synthetic beach fixture with visible album context."},
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
