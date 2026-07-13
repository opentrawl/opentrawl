package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/place"
)

func TestUsageMentionsLabVerbs(t *testing.T) {
	err := run(context.Background(), nil)
	if err == nil {
		t.Fatal("expected usage error")
	}
	if !strings.Contains(err.Error(), "usage: photoscrawl-lab <place-evidence|place-evidence-inventory|place-evidence-campaign|place-context|eval-card|audit-card-input|approved-card|known-places>") {
		t.Fatalf("unexpected usage error: %v", err)
	}
}

func TestCardInputAuditSelectionAndOutputBoundary(t *testing.T) {
	root := t.TempDir()
	outDir := filepath.Join(root, "audit")
	if err := os.Mkdir(outDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := validateCardInputAuditOutput(outDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := validateCardInputAuditOutput(root); err == nil {
		t.Fatal("non-owner-only output directory passed")
	}
	selectionPath := filepath.Join(root, "selection.json")
	if err := os.WriteFile(selectionPath, []byte(`{"asset_ids":["asset:ready","asset:stopped"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	selection, err := readCardInputAuditSelection(selectionPath)
	if err != nil || len(selection.AssetIDs) != 2 || selection.AssetIDs[0] != "asset:ready" {
		t.Fatalf("selection=%+v err=%v", selection, err)
	}
	if _, err := readCardInputAuditSelection(""); err == nil {
		t.Fatal("empty selection path passed")
	}
	path, err := writeCardInputAuditOutput(outDir, "inventory", map[string]string{"asset": "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("private output=%q info=%v err=%v", path, info, err)
	}
	if _, err := writeCardInputAuditOutput(outDir, "inventory", map[string]string{"asset": "changed"}); err == nil {
		t.Fatal("audit output overwrite passed")
	}
}

func TestCardInputAuditPrepareRequiresItsPrivateBoundary(t *testing.T) {
	outDir := filepath.Join(t.TempDir(), "audit")
	if err := os.Mkdir(outDir, 0o700); err != nil {
		t.Fatal(err)
	}
	err := runAuditCardInput(context.Background(), archive.Paths{}, []string{"prepare", "--archive", "archive.sqlite", "--source-library", "source:synthetic", "--out", outDir, "--json"})
	if err == nil || !strings.Contains(err.Error(), "requires --asset and --cache") {
		t.Fatalf("prepare validation error = %v", err)
	}
	err = runAuditCardInput(context.Background(), archive.Paths{}, []string{"prepare", "--archive", "archive.sqlite", "--source-library", "source:synthetic", "--asset", "asset:synthetic", "--cache", filepath.Join(t.TempDir(), "cache"), "--out", outDir})
	if err == nil || !strings.Contains(err.Error(), "requires --json") {
		t.Fatalf("prepare JSON error = %v", err)
	}
}

func TestCardInputAuditInspectRequiresItsPreparedCache(t *testing.T) {
	outDir := filepath.Join(t.TempDir(), "audit")
	if err := os.Mkdir(outDir, 0o700); err != nil {
		t.Fatal(err)
	}
	err := runAuditCardInput(context.Background(), archive.Paths{}, []string{"inspect", "--archive", "archive.sqlite", "--source-library", "source:synthetic", "--out", outDir, "--json"})
	if err == nil || !strings.Contains(err.Error(), "requires --cache") {
		t.Fatalf("inspect cache validation error = %v", err)
	}
}

func TestCardInputAuditSnapshotsWALArchiveWithoutChangingSource(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.sqlite")
	db, err := sql.Open("sqlite3", source)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`pragma journal_mode=WAL; pragma wal_autocheckpoint=0; create table fixture(value text not null); insert into fixture(value) values('synthetic');`); err != nil {
		t.Fatal(err)
	}
	before := cardInputAuditSQLiteState(t, source)
	outDir := filepath.Join(root, "out")
	if err := os.Mkdir(outDir, 0o700); err != nil {
		t.Fatal(err)
	}
	snapshotPath, cleanup, err := snapshotCardInputAuditArchive(context.Background(), source, outDir)
	if err != nil {
		t.Fatal(err)
	}
	copyDB, err := sql.Open("sqlite3", snapshotPath)
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	var count int
	if err := copyDB.QueryRow(`select count(*) from fixture`).Scan(&count); err != nil {
		_ = copyDB.Close()
		cleanup()
		t.Fatal(err)
	}
	if err := copyDB.Close(); err != nil {
		cleanup()
		t.Fatal(err)
	}
	after := cardInputAuditSQLiteState(t, source)
	if count != 1 || !sameCardInputAuditSQLiteState(before, after) {
		cleanup()
		t.Fatalf("snapshot count=%d source changed=%v", count, !sameCardInputAuditSQLiteState(before, after))
	}
	cleanup()
	entries, err := os.ReadDir(outDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary snapshot remains: entries=%v err=%v", entries, err)
	}
}

type cardInputAuditSQLiteFileState struct {
	Size    int64
	Mode    os.FileMode
	ModTime time.Time
	SHA256  [sha256.Size]byte
}

func cardInputAuditSQLiteState(t *testing.T, path string) map[string]cardInputAuditSQLiteFileState {
	t.Helper()
	state := map[string]cardInputAuditSQLiteFileState{}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		data, err := os.ReadFile(path + suffix)
		if err != nil {
			if suffix != "" && os.IsNotExist(err) {
				continue
			}
			t.Fatal(err)
		}
		info, err := os.Stat(path + suffix)
		if err != nil {
			t.Fatal(err)
		}
		state[suffix] = cardInputAuditSQLiteFileState{Size: info.Size(), Mode: info.Mode(), ModTime: info.ModTime(), SHA256: sha256.Sum256(data)}
	}
	return state
}

func sameCardInputAuditSQLiteState(left, right map[string]cardInputAuditSQLiteFileState) bool {
	if len(left) != len(right) {
		return false
	}
	for suffix, want := range left {
		if got, ok := right[suffix]; !ok || got != want {
			return false
		}
	}
	return true
}

func TestPlaceEvidencePassesExactCheckedConfiguration(t *testing.T) {
	root := t.TempDir()
	paths := archive.Paths{
		ConfigPath: filepath.Join(root, "config.toml"),
		DataDir:    filepath.Join(root, "data"),
		CacheDir:   filepath.Join(root, "cache"),
	}
	config := `library_path = "/tmp/Synthetic.photoslibrary"

[place_evidence.geoapify]
provider_identity = "synthetic-osm"
reverse_endpoint = "https://geo.example.com/configured/reverse"
nearby_endpoint = "https://geo.example.com/configured/nearby"
credential_env = "SYNTHETIC_OSM_KEY"
credential_parameter = "syntheticKey"
nearby_categories = ["natural", "tourism.museum"]
reverse_limit = 2
nearby_limit = 50
`
	if err := os.WriteFile(paths.ConfigPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	inputPath := filepath.Join(root, "input.json")
	input := `{"asset_id":"synthetic-asset","location":{"latitude":52.36,"longitude":4.89},"accuracy_meters":8}`
	if err := os.WriteFile(inputPath, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SYNTHETIC_OSM_KEY", "synthetic-secret")
	outDir := filepath.Join(root, "evidence")
	var got place.EvidenceOptions
	var stdout bytes.Buffer
	err := runPlaceEvidenceWith(context.Background(), paths, []string{
		"--input", inputPath,
		"--coordinate-variant", "source-coordinate",
		"--radius", "175",
		"--out", outDir,
		"--json",
	}, &stdout, func(_ context.Context, opts place.EvidenceOptions) (place.EvidenceResult, error) {
		got = opts
		return place.EvidenceResult{State: "complete", CoordinateVariant: opts.CoordinateVariant}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Input.Location.Latitude != 52.36 || got.Input.Location.Longitude != 4.89 || got.RadiusMeters != 175 {
		t.Fatalf("coordinate boundary = %#v", got)
	}
	if got.CoordinateVariant != "source-coordinate" || got.OutputDir != outDir {
		t.Fatalf("run boundary = %#v", got)
	}
	if got.Operation != place.EvidenceOperationAll {
		t.Fatalf("default operation = %q", got.Operation)
	}
	if got.Geoapify.ProviderIdentity != "synthetic-osm" || got.Geoapify.ReverseEndpoint != "https://geo.example.com/configured/reverse" || got.Geoapify.NearbyEndpoint != "https://geo.example.com/configured/nearby" {
		t.Fatalf("provider boundary = %#v", got.Geoapify)
	}
	if got.Geoapify.CredentialReference != "SYNTHETIC_OSM_KEY" || got.Geoapify.CredentialParameter != "syntheticKey" || got.Geoapify.Credential != "synthetic-secret" {
		t.Fatalf("credential boundary = %#v", got.Geoapify)
	}
	if strings.Contains(stdout.String(), "synthetic-secret") {
		t.Fatalf("command output leaked credential: %s", stdout.String())
	}
	t.Logf("RAW CONFIG %q", config)
	t.Logf("RAW COORDINATE INPUT %q", input)
}

func TestPlaceEvidenceOperationStopsBeforeConfigOrRunner(t *testing.T) {
	t.Setenv(placeEvidenceOperationEnv, "unknown")
	runnerCalled := false
	err := runPlaceEvidenceWith(context.Background(), archive.Paths{ConfigPath: filepath.Join(t.TempDir(), "missing.toml")}, []string{
		"--input", "missing.json",
		"--coordinate-variant", "source-coordinate",
		"--radius", "150",
		"--out", t.TempDir(),
		"--json",
	}, &bytes.Buffer{}, func(context.Context, place.EvidenceOptions) (place.EvidenceResult, error) {
		runnerCalled = true
		return place.EvidenceResult{}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "unknown place evidence operation") || runnerCalled {
		t.Fatalf("operation guard error=%v runner=%v", err, runnerCalled)
	}
}

func TestPlaceEvidenceOperationPassesEachAllowedValue(t *testing.T) {
	for _, operation := range []place.EvidenceOperation{
		place.EvidenceOperationAll,
		place.EvidenceOperationApple,
		place.EvidenceOperationGeoapifyReverse,
		place.EvidenceOperationGeoapifyNearby,
	} {
		t.Run(string(operation), func(t *testing.T) {
			root := t.TempDir()
			paths := archive.Paths{ConfigPath: filepath.Join(root, "config.toml"), CacheDir: filepath.Join(root, "cache")}
			config := `[place_evidence.geoapify]
provider_identity = "synthetic-osm"
reverse_endpoint = "https://geo.example.com/reverse"
nearby_endpoint = "https://geo.example.com/nearby"
credential_env = "SYNTHETIC_OSM_KEY"
credential_parameter = "syntheticKey"
nearby_categories = ["natural"]
reverse_limit = 2
nearby_limit = 4
`
			if err := os.WriteFile(paths.ConfigPath, []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}
			inputPath := filepath.Join(root, "input.json")
			if err := os.WriteFile(inputPath, []byte(`{"asset_id":"synthetic-asset","location":{"latitude":52.36,"longitude":4.89}}`), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv(placeEvidenceOperationEnv, string(operation))
			t.Setenv("SYNTHETIC_OSM_KEY", "synthetic-secret")
			var got place.EvidenceOperation
			err := runPlaceEvidenceWith(context.Background(), paths, []string{"--input", inputPath, "--coordinate-variant", "source-coordinate", "--radius", "150", "--out", filepath.Join(root, "out"), "--json"}, &bytes.Buffer{}, func(_ context.Context, opts place.EvidenceOptions) (place.EvidenceResult, error) {
				got = opts.Operation
				return place.EvidenceResult{State: "complete"}, nil
			})
			if err != nil || got != operation {
				t.Fatalf("operation boundary = %q error=%v", got, err)
			}
		})
	}
}

func TestPlaceEvidenceCommandsUsePhotosRunLog(t *testing.T) {
	run, err := newPlaceEvidenceLog(archive.Paths{DataDir: t.TempDir()}, "place-evidence-campaign")
	if err != nil {
		t.Fatal(err)
	}
	if err := run.Info("place_evidence_campaign_case", "phase=canary case=1 outcome=complete duration_ms=1"); err != nil {
		t.Fatal(err)
	}
	if err := run.Finish(nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(run.Path())
	if err != nil || !strings.Contains(string(data), "place_evidence_campaign_case") || !strings.Contains(string(data), "duration_ms=1") {
		t.Fatalf("Photos run log = %q error=%v", data, err)
	}
}

func TestSplitList(t *testing.T) {
	got := splitList("gemma4:31b, gemini-flash-latest,")
	want := []string{"gemma4:31b", "gemini-flash-latest"}
	if len(got) != len(want) {
		t.Fatalf("splitList = %#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("splitList = %#v", got)
		}
	}
}

func TestReadKnownPlacesInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known-places.json")
	data := []byte(`[{
	  "label_kind": "home",
	  "display_name": "Example Residence",
	  "latitude": 52,
	  "longitude": 4,
	  "valid_from": "2026-01-01T00:00:00Z"
	}]`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	places, err := readKnownPlacesInput(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(places) != 1 || places[0].LabelKind != archive.KnownPlaceKindHome || places[0].DisplayName != "Example Residence" {
		t.Fatalf("known places input = %#v", places)
	}
}
