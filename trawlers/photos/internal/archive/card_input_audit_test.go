package archive

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/cardinput"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/imagemetadata"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
)

func TestCardInputAuditInventoryIsStructuralAndNamesStops(t *testing.T) {
	ctx, db := cardInputAuditTestDB(t)
	defer db.Close()
	seedCardInputAuditSnapshot(t, ctx, db, "complete")
	seedCardInputAuditAsset(t, ctx, db, "asset:ready", "current", "image", "{}")
	seedCardInputAuditAsset(t, ctx, db, "asset:video", "current", "video", `{"source":"present"}`)
	seedCardInputAuditAsset(t, ctx, db, "asset:gone", "deleted_upstream", "image", `{"source":"present"}`)
	if _, err := db.ExecContext(ctx, `update asset set first_card_blocked_at='2026-07-13T00:00:00Z', first_card_blocked_snapshot_id='snapshot:complete' where id='asset:gone'`); err != nil {
		t.Fatal(err)
	}
	for _, assetID := range []string{"asset:ready", "asset:video", "asset:gone"} {
		if _, err := db.ExecContext(ctx, `insert into asset_resource(id,asset_id,resource_type,uti,original_filename,local_path,file_size,sha256,available_locally,needs_download) values(?,?,'photo','public.jpeg','synthetic.jpg','',3,?,1,0)`, "resource:"+assetID, assetID, digestText(assetID)); err != nil {
			t.Fatal(err)
		}
	}
	inventory, err := readCardInputAuditInventory(ctx, db, "source:synthetic")
	if err != nil {
		t.Fatal(err)
	}
	if !inventory.Complete || len(inventory.Assets) != 3 {
		t.Fatalf("inventory=%+v", inventory)
	}
	byID := map[string]CardInputAuditInventoryRow{}
	for _, row := range inventory.Assets {
		byID[row.AssetID] = row
	}
	if row := byID["asset:ready"]; !reflect.DeepEqual(row.ResourceRoles, []string{"photo"}) || row.HasMetadata || len(row.StopReasons) != 0 {
		t.Fatalf("ready structural row=%+v", row)
	}
	if row := byID["asset:video"]; !containsAuditStop(row.StopReasons, cardInputAuditStopUnsupportedMedia) {
		t.Fatalf("video stops=%+v", row.StopReasons)
	}
	if row := byID["asset:gone"]; !containsAuditStop(row.StopReasons, cardInputAuditStopProhibited) || !containsAuditStop(row.StopReasons, cardInputAuditStopSourceNotCurrent) {
		t.Fatalf("gone stops=%+v", row.StopReasons)
	}
}

func TestCardInputAuditProhibitedStopsBeforeArtifactRead(t *testing.T) {
	ctx, db := cardInputAuditTestDB(t)
	defer db.Close()
	seedCardInputAuditSnapshot(t, ctx, db, "complete")
	seedCardInputAuditAsset(t, ctx, db, "asset:prohibited", "deleted_upstream", "image", `{"present":true}`)
	if _, err := db.ExecContext(ctx, `update asset set first_card_blocked_at='2026-07-13T00:00:00Z', first_card_blocked_snapshot_id='snapshot:complete' where id='asset:prohibited'`); err != nil {
		t.Fatal(err)
	}
	input, err := loadCardInputAuditInput(ctx, db, "source:synthetic", "asset:prohibited")
	if err != nil {
		t.Fatal(err)
	}
	classifier, err := newModelClassifier("synthetic-model", "https://models.example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := inspectCardInput(ctx, db, true, CardInputAuditInspectOptions{CardInputAuditInventoryOptions: CardInputAuditInventoryOptions{SourceLibraryID: "source:synthetic"}, CacheDir: t.TempDir()}, classifier, "asset:prohibited")
	if err != nil {
		t.Fatal(err)
	}
	if inspection.StopReason != cardInputAuditStopProhibited || inspection.CardInput != nil || inspection.RenderedRequest != nil {
		t.Fatalf("inspection=%+v", inspection)
	}
	preflight, ok := inspection.Preflight.(classifyInput)
	if !ok || !reflect.DeepEqual(preflight, input) {
		t.Fatalf("preflight=%#v want=%#v", inspection.Preflight, input)
	}
}

func TestCardInputAuditFactsKeepNumericCameraValues(t *testing.T) {
	input := classifyInput{AssetID: "asset:camera", SourceLibraryID: "source:synthetic", CreationDate: "2026-07-13T10:00:00Z", TimezoneName: "UTC", MediaType: "image", Width: 2, Height: 2, CameraMake: "Example", CameraModel: "Camera", FocalLengthMM: 6.5, FocalLength35MM: 28, Aperture: 1.8, ShutterSpeed: 0.01, ISO: 100}
	original := classifyResource{ID: "resource:camera", ResourceType: "photo", UTI: "public.jpeg", OriginalFilename: "synthetic.jpg", FileSize: 3, SHA256: digestText("original"), AvailableLocally: true}
	metadata := imagemetadata.Artifacts{Projection: imagemetadata.Projection{Lines: []string{"Camera: Example"}}, Proof: imagemetadata.Proof{RecordSHA256: digestText("record"), ProjectionSHA256: digestText("projection")}}
	source, artifacts := cardInputAuditFacts(input, original, metadata, photos.CurrentStillFact{MediaType: "public.jpeg", Orientation: 1, PixelWidth: 2, PixelHeight: 2, Size: 3, SHA256: digestText("current")}, digestText("proof"))
	card, err := cardinput.Build(source, artifacts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if card.Input.Camera.GetFocalLengthMm() != 6.5 || card.Input.Camera.GetFocalLength_35Mm() != 28 || card.Input.Camera.GetAperture() != 1.8 || card.Input.Camera.GetShutterSpeed() != 0.01 || card.Input.Camera.GetIso() != 100 {
		t.Fatalf("camera=%+v", card.Input.Camera)
	}
}

func TestCardInputAuditBackstopIsStableAndBounded(t *testing.T) {
	assets := []string{"asset:c", "asset:a", "asset:b"}
	first := StableCardInputAuditBackstop("snapshot:complete", assets, 2)
	second := StableCardInputAuditBackstop("snapshot:complete", []string{"asset:b", "asset:c", "asset:a"}, 2)
	if !reflect.DeepEqual(first, second) || len(first) != 2 {
		t.Fatalf("stable backstop first=%q second=%q", first, second)
	}
	if got := StableCardInputAuditBackstop("snapshot:complete", assets, 9); len(got) != len(assets) {
		t.Fatalf("unbounded backstop=%q", got)
	}
}

func TestCardInputAuditReadyInspectionReadsOnlyCheckedArtifacts(t *testing.T) {
	ctx, db := cardInputAuditTestDB(t)
	defer db.Close()
	seedCardInputAuditSnapshot(t, ctx, db, "complete")
	seedCardInputAuditAsset(t, ctx, db, "asset:ready", "current", "image", `{"present":true}`)
	root := t.TempDir()
	cacheDir := filepath.Join(root, "cache")
	originalPath := filepath.Join(root, "original.jpg")
	originalBytes := []byte("synthetic original")
	if err := os.WriteFile(originalPath, originalBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	originalSHA := digestText(string(originalBytes))
	if _, err := db.ExecContext(ctx, `insert into asset_resource(id,asset_id,resource_type,uti,original_filename,local_path,file_size,sha256,available_locally,needs_download) values('resource:ready','asset:ready','photo','public.jpeg','synthetic.jpg',?, ?,?,1,0)`, originalPath, len(originalBytes), originalSHA); err != nil {
		t.Fatal(err)
	}
	metadataStore, err := imagemetadata.NewStore(filepath.Join(cacheDir, "image-metadata"), func(context.Context, string) ([]byte, error) {
		return []byte(fmt.Sprintf(`{"extractor_version":"imageio-v1","original_sha256":%q,"container":{"type":"dictionary","dictionary":{"Make":{"type":"string","string":"Example"}}},"images":[{"index":0,"properties":{"type":"dictionary","dictionary":{}}}]}`, originalSHA)), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := metadataStore.Load(ctx, originalPath, originalSHA); err != nil {
		t.Fatal(err)
	}
	currentBytes := []byte("synthetic current still")
	modification, err := photos.ParseCurrentStillModification("2026-07-13T10:01:00Z")
	if err != nil {
		t.Fatal(err)
	}
	freshness, err := photos.CurrentStillFreshnessForModification(modification)
	if err != nil {
		t.Fatal(err)
	}
	currentRoot := filepath.Join(cacheDir, "originals")
	if err := os.MkdirAll(currentRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	currentPath := photos.CurrentStillCachePath(currentRoot, "source:synthetic", "asset:ready", freshness)
	if err := os.WriteFile(currentPath, currentBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	currentSHA := digestText(string(currentBytes))
	proof := fmt.Sprintf(`{"version":1,"role":"current_still","media_type":"public.jpeg","orientation":1,"pixel_width":2,"pixel_height":2,"size":%d,"sha256":%q}`, len(currentBytes), currentSHA) + "\n"
	if err := os.WriteFile(currentPath+".proof.json", []byte(proof), 0o600); err != nil {
		t.Fatal(err)
	}
	beforeChanges := sqliteChanges(t, ctx, db)
	beforeCache := cardInputAuditDirectory(t, cacheDir)
	classifier, err := newModelClassifier("synthetic-model", "https://models.example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := inspectCardInput(ctx, db, true, CardInputAuditInspectOptions{CardInputAuditInventoryOptions: CardInputAuditInventoryOptions{SourceLibraryID: "source:synthetic"}, CacheDir: cacheDir}, classifier, "asset:ready")
	if err != nil {
		t.Fatal(err)
	}
	if inspection.StopReason != "" || len(inspection.CardInput) == 0 || len(inspection.RenderedRequest) == 0 || inspection.RenderedRoute == "" || inspection.CurrentStillPath != currentPath {
		t.Fatalf("inspection=%+v", inspection)
	}
	var request map[string]any
	if err := json.Unmarshal(inspection.RenderedRequest, &request); err != nil {
		t.Fatal(err)
	}
	if request["model"] != "synthetic-model" {
		t.Fatalf("rendered request=%s", inspection.RenderedRequest)
	}
	if sqliteChanges(t, ctx, db) != beforeChanges || !reflect.DeepEqual(cardInputAuditDirectory(t, cacheDir), beforeCache) {
		t.Fatalf("audit mutated archive or cache")
	}
}

func cardInputAuditTestDB(t *testing.T) (context.Context, *sql.DB) {
	t.Helper()
	ctx := context.Background()
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	schema := `
create table crawl_snapshot(id text, source_library_id text, completed_at text, completeness_state text);
create table asset(id text primary key, source_library_id text, source_state text, local_identifier text, media_type text, media_subtypes text, creation_date text, modification_date text, timezone_name text, width integer, height integer, duration_seconds real, favorite integer, hidden integer, burst_identifier text, metadata_json text, camera_make text, camera_model text, lens_model text, focal_length_mm real, focal_length_35mm real, aperture real, shutter_speed real, iso integer, first_card_blocked_at text, first_card_blocked_snapshot_id text);
create table classification_queue(id text, asset_id text, source_library_id text, state text, needs_download integer);
create table asset_resource(id text, asset_id text, resource_type text, uti text, original_filename text, local_path text, file_size integer, sha256 text, available_locally integer, needs_download integer);
create table album_membership(asset_id text, album_title text, album_kind text);
create table location_observation(asset_id text, latitude real, longitude real, horizontal_accuracy real);
create table model_observation(asset_id text, observation_type text);
create table crawl_seen_asset(asset_id text, source_library_id text, source_fingerprint text, last_seen_snapshot_id text);
	`
	for _, statement := range strings.Split(schema, ";\n") {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if _, err = db.ExecContext(ctx, statement); err != nil {
			break
		}
	}
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	return ctx, db
}

func seedCardInputAuditSnapshot(t *testing.T, ctx context.Context, db *sql.DB, state string) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `insert into crawl_snapshot values('snapshot:complete','source:synthetic','2026-07-13T00:00:00Z',?)`, state); err != nil {
		t.Fatal(err)
	}
}
func seedCardInputAuditAsset(t *testing.T, ctx context.Context, db *sql.DB, id, state, media, metadata string) {
	t.Helper()
	_, err := db.ExecContext(ctx, `insert into asset values(?,?,?,? ,?,'','2026-07-13T10:00:00Z','2026-07-13T10:01:00Z','UTC',2,2,0,0,0,'',?,'Example','Camera','Lens',6.5,28,1.8,0.01,100,null,null)`, id, "source:synthetic", state, id, media, metadata)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `insert into classification_queue values(?,?,?,'pending',0)`, "queue:"+id, id, "source:synthetic"); err != nil {
		t.Fatal(err)
	}
}
func containsAuditStop(stops []string, want string) bool {
	for _, stop := range stops {
		if stop == want {
			return true
		}
	}
	return false
}

func sqliteChanges(t *testing.T, ctx context.Context, db *sql.DB) int {
	t.Helper()
	var changes int
	if err := db.QueryRowContext(ctx, `select total_changes()`).Scan(&changes); err != nil {
		t.Fatal(err)
	}
	return changes
}

type cardInputAuditCacheFile struct {
	Mode   os.FileMode
	SHA256 string
}

func cardInputAuditDirectory(t *testing.T, root string) map[string]cardInputAuditCacheFile {
	t.Helper()
	paths := map[string]cardInputAuditCacheFile{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		paths[strings.TrimPrefix(path, root+string(filepath.Separator))] = cardInputAuditCacheFile{Mode: info.Mode().Perm(), SHA256: hex.EncodeToString(sum[:])}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

func digestText(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
