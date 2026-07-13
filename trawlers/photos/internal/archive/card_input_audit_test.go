package archive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/cardinput"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/imagemetadata"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
	"github.com/opentrawl/opentrawl/trawlkit/store"
)

func TestCardInputAuditInventoryIsStructuralAndNamesStops(t *testing.T) {
	ctx, db := cardInputAuditTestDB(t)
	defer db.Close()
	seedCardInputAuditSnapshot(t, ctx, db, "complete")
	seedCardInputAuditAsset(t, ctx, db, "asset:ready", "current", "image", "{}")
	seedCardInputAuditAsset(t, ctx, db, "asset:video", "current", "video", `{"source":"present"}`)
	seedCardInputAuditAsset(t, ctx, db, "asset:gone", "deleted_upstream", "image", `{"source":"present"}`)
	seedCardInputAuditAsset(t, ctx, db, "asset:carded", "current", "image", `{"source":"present"}`)
	if _, err := db.ExecContext(ctx, `update asset set first_card_blocked_at='2026-07-13T00:00:00Z', first_card_blocked_snapshot_id='snapshot:complete' where id='asset:gone'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `update asset set first_card_blocked_at='2026-07-13T00:00:00Z', first_card_blocked_snapshot_id='snapshot:complete' where id='asset:carded'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `insert into model_observation(asset_id,observation_type) values('asset:carded', ?)`, modelObservationCardSummary); err != nil {
		t.Fatal(err)
	}
	for _, assetID := range []string{"asset:ready", "asset:video", "asset:gone", "asset:carded"} {
		if _, err := db.ExecContext(ctx, `insert into asset_resource(id,asset_id,resource_type,uti,original_filename,local_path,file_size,sha256,available_locally,needs_download) values(?,?,'photo','public.jpeg','synthetic.jpg','',3,?,1,0)`, "resource:"+assetID, assetID, digestText(assetID)); err != nil {
			t.Fatal(err)
		}
	}
	inventory, err := readCardInputAuditInventory(ctx, db, "source:synthetic")
	if err != nil {
		t.Fatal(err)
	}
	if !inventory.Complete || len(inventory.Assets) != 4 {
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
	if row := byID["asset:carded"]; row.Eligibility != string(firstCardEligible) || containsAuditStop(row.StopReasons, cardInputAuditStopProhibited) {
		t.Fatalf("carded row=%+v", row)
	}
}

func TestCardInputAuditOpensTheCurrentSchema13SnapshotReadOnly(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "photos.sqlite")
	fixture, err := store.Open(ctx, store.Options{Path: path, Schema: Schema, SchemaVersion: cardInputAuditSchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.Close(); err != nil {
		t.Fatal(err)
	}
	opened, err := openCardInputAuditArchive(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := opened.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCardInputAuditRejectsSchema12Snapshot(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "photos.sqlite")
	fixture, err := store.Open(ctx, store.Options{Path: path, Schema: Schema, SchemaVersion: cardInputAuditSchemaVersion - 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = openCardInputAuditArchive(ctx, path)
	if !errors.Is(err, ArchiveIncompatibleError{}) {
		t.Fatalf("open schema 12 error = %v, want incompatible archive", err)
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

func TestCardInputAuditPrepareReopensOnePackageOriginalAndExistingCurrentStillWithoutPhotoKit(t *testing.T) {
	ctx, db := cardInputAuditTestDB(t)
	defer db.Close()
	seedCardInputAuditSnapshot(t, ctx, db, "complete")
	seedCardInputAuditAsset(t, ctx, db, "asset:prepare", "current", "image", `{"present":true}`)
	root := t.TempDir()
	originalPath := filepath.Join(root, "original.jpg")
	originalBytes := []byte("synthetic package original")
	if err := os.WriteFile(originalPath, originalBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	originalSHA := digestText(string(originalBytes))
	if _, err := db.ExecContext(ctx, `insert into asset_resource(id,asset_id,resource_type,uti,original_filename,local_path,file_size,sha256,available_locally,needs_download) values('resource:prepare','asset:prepare','local_original','public.jpeg','original.jpg',?,?,?,1,0)`, originalPath, len(originalBytes), originalSHA); err != nil {
		t.Fatal(err)
	}
	currentBytes := []byte("synthetic current still")
	oldExtract := extractImageMetadata
	t.Cleanup(func() {
		extractImageMetadata = oldExtract
	})
	extractCalls := 0
	extractImageMetadata = func(_ context.Context, path string) ([]byte, error) {
		extractCalls++
		if path != originalPath {
			t.Fatalf("ImageIO path = %q, want %q", path, originalPath)
		}
		return []byte(fmt.Sprintf(`{"extractor_version":"imageio-v1","original_sha256":%q,"container":{"type":"dictionary","dictionary":{"Make":{"type":"string","string":"Example"}}},"images":[{"index":0,"properties":{"type":"dictionary","dictionary":{}}}]}`, originalSHA)), nil
	}
	cacheDir := filepath.Join(root, "checked-cache")
	input, err := loadCardInputAuditInput(ctx, db, "source:synthetic", "asset:prepare")
	if err != nil {
		t.Fatal(err)
	}
	currentRequest, err := input.currentStillRequest()
	if err != nil {
		t.Fatal(err)
	}
	cacheSeedCalls := 0
	currentResolver, err := photos.NewCurrentStillResolver(filepath.Join(cacheDir, "originals"), func(_ context.Context, request photos.CurrentStillRequest, destination string) (photos.CurrentStillFact, error) {
		cacheSeedCalls++
		if request.AllowNetwork {
			t.Fatal("cache seed enabled current-still network access")
		}
		if err := os.WriteFile(destination, currentBytes, 0o600); err != nil {
			return photos.CurrentStillFact{}, err
		}
		return photos.CurrentStillFact{MediaType: "public.jpeg", Orientation: 1, PixelWidth: 2, PixelHeight: 2, Size: int64(len(currentBytes)), SHA256: digestText(string(currentBytes)), PhotoKitCalls: 1}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	seeded, err := currentResolver.Resolve(ctx, currentRequest)
	if err != nil {
		t.Fatal(err)
	}
	if seeded.Lease != nil {
		defer seeded.Lease.Close()
	}
	prepared, err := prepareCardInputAudit(ctx, db, true, CardInputAuditPrepareOptions{CardInputAuditInventoryOptions: CardInputAuditInventoryOptions{SourceLibraryID: "source:synthetic"}, CacheDir: cacheDir, AssetID: "asset:prepare"})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.StopReason != "" || prepared.ImmutableOriginal.Source != photos.OriginalSourcePackage || prepared.ImmutableOriginal.SHA256 != originalSHA || prepared.CurrentStillRequests != 0 || prepared.CurrentStillSource != photos.CurrentStillSourceCache || prepared.CurrentStillProof == "" || cacheSeedCalls != 1 || extractCalls != 1 {
		t.Fatalf("prepared=%+v cache seed calls=%d ImageIO calls=%d", prepared, cacheSeedCalls, extractCalls)
	}
	if _, ok := imagemetadata.ReadCheckedArtifacts(filepath.Join(cacheDir, "image-metadata"), originalSHA); !ok {
		t.Fatal("prepare did not write checked metadata")
	}
	if _, err := os.Stat(filepath.Join(cacheDir, "originals")); err != nil {
		t.Fatalf("prepare did not create the named checked cache root: %v", err)
	}
}

func TestCardInputCurrentStillAcquiresOnceThenPrepareReopensWithoutPhotoKit(t *testing.T) {
	ctx, db := cardInputAuditTestDB(t)
	defer db.Close()
	seedCardInputAuditSnapshot(t, ctx, db, "complete")
	seedCardInputAuditAsset(t, ctx, db, "asset:acquire", "current", "image", `{"present":true}`)
	if _, err := db.ExecContext(ctx, `update asset set local_identifier='AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE' where id='asset:acquire'`); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	originalPath := filepath.Join(root, "original.jpg")
	originalBytes := []byte("synthetic package original")
	if err := os.WriteFile(originalPath, originalBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	originalSHA := digestText(string(originalBytes))
	if _, err := db.ExecContext(ctx, `insert into asset_resource(id,asset_id,resource_type,uti,original_filename,local_path,file_size,sha256,available_locally,needs_download) values('resource:acquire','asset:acquire','local_original','public.jpeg','original.jpg',?,?,?,1,0)`, originalPath, len(originalBytes), originalSHA); err != nil {
		t.Fatal(err)
	}
	oldCurrentExporter := exportCurrentStillResource
	oldExtract := extractImageMetadata
	oldReadiness := preflightCardInputMedia
	t.Cleanup(func() {
		exportCurrentStillResource = oldCurrentExporter
		extractImageMetadata = oldExtract
		preflightCardInputMedia = oldReadiness
	})
	preflightCardInputMedia = syntheticCardInputReadiness
	currentBytes := []byte("synthetic current still")
	exporterCalls := 0
	exportCurrentStillResource = func(_ context.Context, request photos.CurrentStillRequest, destination string) (photos.CurrentStillFact, error) {
		exporterCalls++
		if request.AllowNetwork {
			t.Fatal("acquire enabled current-still network access")
		}
		if request.PhotoKitLocalIdentifier != "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE/L0/001" {
			t.Fatalf("current-still PhotoKit identifier = %q", request.PhotoKitLocalIdentifier)
		}
		if err := os.WriteFile(destination, currentBytes, 0o600); err != nil {
			return photos.CurrentStillFact{}, err
		}
		return photos.CurrentStillFact{MediaType: "public.jpeg", Orientation: 1, PixelWidth: 2, PixelHeight: 2, Size: int64(len(currentBytes)), SHA256: digestText(string(currentBytes)), PhotoKitCalls: 1}, nil
	}
	cacheDir := filepath.Join(root, "checked-cache")
	acquired, err := acquireCardInputCurrentStill(ctx, db, true, CardInputCurrentStillOptions{CardInputAuditInventoryOptions: CardInputAuditInventoryOptions{SourceLibraryID: "source:synthetic"}, CacheDir: cacheDir, AssetID: "asset:acquire"})
	if err != nil {
		t.Fatal(err)
	}
	if acquired.StopReason != "" || acquired.ImmutableOriginal.Source != photos.OriginalSourcePackage || acquired.CurrentStillSource != photos.CurrentStillSourcePhotoKit || acquired.CurrentStillRequests != 1 || acquired.CurrentStillProof == "" || exporterCalls != 1 {
		t.Fatalf("acquired=%+v exporter calls=%d", acquired, exporterCalls)
	}
	exportCurrentStillResource = func(context.Context, photos.CurrentStillRequest, string) (photos.CurrentStillFact, error) {
		t.Fatal("prepare invoked the current-still exporter after acquisition")
		return photos.CurrentStillFact{}, nil
	}
	extractImageMetadata = func(context.Context, string) ([]byte, error) {
		return []byte(fmt.Sprintf(`{"extractor_version":"imageio-v1","original_sha256":%q,"container":{"type":"dictionary","dictionary":{"Make":{"type":"string","string":"Example"}}},"images":[{"index":0,"properties":{"type":"dictionary","dictionary":{}}}]}`, originalSHA)), nil
	}
	prepared, err := prepareCardInputAudit(ctx, db, true, CardInputAuditPrepareOptions{CardInputAuditInventoryOptions: CardInputAuditInventoryOptions{SourceLibraryID: "source:synthetic"}, CacheDir: cacheDir, AssetID: "asset:acquire"})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.StopReason != "" || prepared.CurrentStillSource != photos.CurrentStillSourceCache || prepared.CurrentStillRequests != 0 || prepared.CurrentStillProof != acquired.CurrentStillProof || exporterCalls != 1 {
		t.Fatalf("prepared=%+v exporter calls=%d", prepared, exporterCalls)
	}
}

func TestCardInputAuditPrepareProhibitedStopsBeforeCacheOrArtifactAccess(t *testing.T) {
	ctx, db := cardInputAuditTestDB(t)
	defer db.Close()
	seedCardInputAuditSnapshot(t, ctx, db, "complete")
	seedCardInputAuditAsset(t, ctx, db, "asset:prohibited-prepare", "deleted_upstream", "image", `{"present":true}`)
	if _, err := db.ExecContext(ctx, `update asset set first_card_blocked_at='2026-07-13T00:00:00Z', first_card_blocked_snapshot_id='snapshot:complete' where id='asset:prohibited-prepare'`); err != nil {
		t.Fatal(err)
	}
	cacheDir := filepath.Join(t.TempDir(), "must-not-exist")
	prepared, err := prepareCardInputAudit(ctx, db, true, CardInputAuditPrepareOptions{CardInputAuditInventoryOptions: CardInputAuditInventoryOptions{SourceLibraryID: "source:synthetic"}, CacheDir: cacheDir, AssetID: "asset:prohibited-prepare"})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.StopReason != cardInputAuditStopProhibited {
		t.Fatalf("prepared=%+v", prepared)
	}
	if _, err := os.Stat(cacheDir); !os.IsNotExist(err) {
		t.Fatalf("prohibited prepare touched cache: %v", err)
	}
}

func TestCardInputAuditPrepareStopsWithoutCheckedImmutableOriginal(t *testing.T) {
	ctx, db := cardInputAuditTestDB(t)
	defer db.Close()
	seedCardInputAuditSnapshot(t, ctx, db, "complete")
	seedCardInputAuditAsset(t, ctx, db, "asset:no-package", "current", "image", `{"present":true}`)
	cacheDir := filepath.Join(t.TempDir(), "must-not-exist")
	prepared, err := prepareCardInputAudit(ctx, db, true, CardInputAuditPrepareOptions{CardInputAuditInventoryOptions: CardInputAuditInventoryOptions{SourceLibraryID: "source:synthetic"}, CacheDir: cacheDir, AssetID: "asset:no-package"})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.StopReason != cardInputAuditStopImmutableOriginal {
		t.Fatalf("prepared=%+v", prepared)
	}
	if _, err := os.Stat(cacheDir); !os.IsNotExist(err) {
		t.Fatalf("missing package original touched cache: %v", err)
	}
}

func TestCardInputCurrentStillAcquiresAndReopensPhotoKitOriginalWithoutNetwork(t *testing.T) {
	ctx, db := cardInputAuditTestDB(t)
	defer db.Close()
	seedCardInputAuditSnapshot(t, ctx, db, "complete")
	seedCardInputAuditAsset(t, ctx, db, "asset:photokit", "current", "image", `{"present":true}`)
	if _, err := db.ExecContext(ctx, `update asset set local_identifier='AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE' where id='asset:photokit'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `insert into asset_resource(id,asset_id,resource_type,uti,original_filename,local_path,file_size,sha256,available_locally,needs_download) values('resource:photokit','asset:photokit','photo','','photokit.jpg','',0,'',0,1)`); err != nil {
		t.Fatal(err)
	}
	oldOriginalExporter := exportOriginalResource
	oldCurrentExporter := exportCurrentStillResource
	oldExtract := extractImageMetadata
	oldReadiness := preflightCardInputMedia
	t.Cleanup(func() {
		exportOriginalResource = oldOriginalExporter
		exportCurrentStillResource = oldCurrentExporter
		extractImageMetadata = oldExtract
		preflightCardInputMedia = oldReadiness
	})
	preflightCardInputMedia = syntheticCardInputReadiness
	originalBytes := []byte("synthetic PhotoKit original")
	originalCalls := 0
	exportOriginalResource = func(_ context.Context, query photos.OriginalExportQuery, destination string, allowNetwork bool) error {
		originalCalls++
		if allowNetwork {
			t.Fatal("acquire enabled original network access")
		}
		if query.LocalIdentifier != "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE/L0/001" {
			t.Fatalf("original PhotoKit identifier = %q", query.LocalIdentifier)
		}
		return os.WriteFile(destination, originalBytes, 0o600)
	}
	currentBytes := []byte("synthetic PhotoKit current still")
	exportCurrentStillResource = func(_ context.Context, request photos.CurrentStillRequest, destination string) (photos.CurrentStillFact, error) {
		if request.AllowNetwork {
			t.Fatal("acquire enabled current-still network access")
		}
		if request.PhotoKitLocalIdentifier != "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE/L0/001" {
			t.Fatalf("current-still PhotoKit identifier = %q", request.PhotoKitLocalIdentifier)
		}
		if err := os.WriteFile(destination, currentBytes, 0o600); err != nil {
			return photos.CurrentStillFact{}, err
		}
		return photos.CurrentStillFact{MediaType: "public.jpeg", Orientation: 1, PixelWidth: 2, PixelHeight: 2, Size: int64(len(currentBytes)), SHA256: digestText(string(currentBytes)), PhotoKitCalls: 1}, nil
	}
	cacheDir := filepath.Join(t.TempDir(), "checked-cache")
	options := CardInputCurrentStillOptions{CardInputAuditInventoryOptions: CardInputAuditInventoryOptions{SourceLibraryID: "source:synthetic"}, CacheDir: cacheDir, AssetID: "asset:photokit"}
	acquired, err := acquireCardInputCurrentStill(ctx, db, true, options)
	if err != nil {
		t.Fatal(err)
	}
	if acquired.ImmutableOriginal.Source != photos.OriginalSourcePhotoKit || acquired.OriginalRequests != 1 || originalCalls != 1 {
		t.Fatalf("acquired=%+v original calls=%d", acquired, originalCalls)
	}
	exportOriginalResource = func(context.Context, photos.OriginalExportQuery, string, bool) error {
		t.Fatal("cache-only prepare invoked PhotoKit original export")
		return nil
	}
	extractImageMetadata = func(context.Context, string) ([]byte, error) {
		return []byte(fmt.Sprintf(`{"extractor_version":"imageio-v1","original_sha256":%q,"container":{"type":"dictionary","dictionary":{"Make":{"type":"string","string":"Example"}}},"images":[{"index":0,"properties":{"type":"dictionary","dictionary":{}}}]}`, digestText(string(originalBytes)))), nil
	}
	prepared, err := prepareCardInputAudit(ctx, db, true, CardInputAuditPrepareOptions{CardInputAuditInventoryOptions: options.CardInputAuditInventoryOptions, CacheDir: cacheDir, AssetID: options.AssetID})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.StopReason != "" || prepared.ImmutableOriginal.Source != photos.OriginalSourceCache || prepared.ImmutableOriginal.SHA256 != acquired.ImmutableOriginal.SHA256 || originalCalls != 1 {
		t.Fatalf("prepared=%+v original calls=%d", prepared, originalCalls)
	}
}

func TestCardInputAuditPrepareStopsWithoutCheckedCurrentStill(t *testing.T) {
	ctx, db := cardInputAuditTestDB(t)
	defer db.Close()
	seedCardInputAuditSnapshot(t, ctx, db, "complete")
	seedCardInputAuditAsset(t, ctx, db, "asset:no-current", "current", "image", `{"present":true}`)
	root := t.TempDir()
	originalPath := filepath.Join(root, "original.jpg")
	originalBytes := []byte("synthetic package original")
	if err := os.WriteFile(originalPath, originalBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	originalSHA := digestText(string(originalBytes))
	if _, err := db.ExecContext(ctx, `insert into asset_resource(id,asset_id,resource_type,uti,original_filename,local_path,file_size,sha256,available_locally,needs_download) values('resource:no-current','asset:no-current','local_original','public.jpeg','original.jpg',?,?,?,1,0)`, originalPath, len(originalBytes), originalSHA); err != nil {
		t.Fatal(err)
	}
	oldExtract := extractImageMetadata
	oldCurrentExporter := exportCurrentStillResource
	t.Cleanup(func() {
		extractImageMetadata = oldExtract
		exportCurrentStillResource = oldCurrentExporter
	})
	extractImageMetadata = func(context.Context, string) ([]byte, error) {
		return []byte(fmt.Sprintf(`{"extractor_version":"imageio-v1","original_sha256":%q,"container":{"type":"dictionary","dictionary":{"Make":{"type":"string","string":"Example"}}},"images":[{"index":0,"properties":{"type":"dictionary","dictionary":{}}}]}`, originalSHA)), nil
	}
	exportCurrentStillResource = func(context.Context, photos.CurrentStillRequest, string) (photos.CurrentStillFact, error) {
		t.Fatal("prepare invoked the current-still exporter on a checked-cache miss")
		return photos.CurrentStillFact{}, nil
	}
	cacheDir := filepath.Join(root, "checked-cache")
	prepared, err := prepareCardInputAudit(ctx, db, true, CardInputAuditPrepareOptions{CardInputAuditInventoryOptions: CardInputAuditInventoryOptions{SourceLibraryID: "source:synthetic"}, CacheDir: cacheDir, AssetID: "asset:no-current"})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.StopReason != cardInputAuditStopMissingCurrentStill || prepared.CurrentStillRequests != 0 || prepared.CurrentStillProof != "" {
		t.Fatalf("prepared=%+v", prepared)
	}
	if paths, err := filepath.Glob(filepath.Join(cacheDir, "originals", "*.current")); err != nil || len(paths) != 0 {
		t.Fatalf("prepare created a current-still cache entry: paths=%v err=%v", paths, err)
	}
}

func syntheticCardInputReadiness(_ context.Context, input classifyInput) (photos.AssetReadiness, error) {
	return photos.AssetReadiness{
		LocalIdentifier:  input.LocalIdentifier + "/L0/001",
		AssetUUID:        input.LocalIdentifier,
		MediaType:        "image",
		OriginalFilename: "synthetic.heic",
	}, nil
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
	if _, err := db.ExecContext(ctx, `insert into asset_resource(id,asset_id,resource_type,uti,original_filename,local_path,file_size,sha256,available_locally,needs_download) values
		('resource:ready-photo','asset:ready','photo','public.jpeg','synthetic.jpg',?, ?,?,1,0),
		('resource:ready-original','asset:ready','local_original','public.jpeg','synthetic.jpg',?, ?,?,1,0)`, originalPath, len(originalBytes), digestText("stale photo observation"), originalPath, len(originalBytes), digestText("stale package observation")); err != nil {
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

func TestCardInputAuditReadsStoredProductionBoundaryBeforeArchiveDigest(t *testing.T) {
	ctx := context.Background()
	db := fixtureCardStore(t, ctx)
	defer db.Close()
	assetID := "asset:stored-audit"
	seedFixtureCardAsset(t, ctx, db, assetID)
	classifier, err := newModelClassifier("fixture-model", "https://models.example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	prepared := fixtureCardPreparationFor(assetID)
	executionID := fixtureExecutionIdentity(t, prepared, classifier)
	stored, err := executeFixtureCard(ctx, db, executionID, func() (fixtureCardPreparation, error) {
		return prepared, nil
	}, classifier, fixtureWireBytes(t, fixtureProviderResponse(t)), fixedClock("2026-07-13T17:00:00Z")())
	if err != nil {
		t.Fatal(err)
	}
	var changesBefore, changesAfter int
	cacheDir := filepath.Join(t.TempDir(), "must-not-open")
	_ = db.DB().QueryRowContext(ctx, `select total_changes()`).Scan(&changesBefore)
	inspection, err := inspectCardInput(ctx, db.DB(), true, CardInputAuditInspectOptions{CardInputAuditInventoryOptions: CardInputAuditInventoryOptions{SourceLibraryID: "source:fixture"}, CacheDir: cacheDir}, modelClassifier{}, assetID)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.DB().QueryRowContext(ctx, `select total_changes()`).Scan(&changesAfter)
	if inspection.StopReason != "" || inspection.RenderedRoute != stored.Request.Route() || inspection.RenderedModel != stored.Request.Model() || !bytes.Equal(inspection.RenderedRequest, stored.Request.Body()) || inspection.CardInputWire != base64.StdEncoding.EncodeToString(stored.Input.Bytes) {
		t.Fatalf("stored inspection=%+v", inspection)
	}
	if _, ok := inspection.Preflight.(classifyInput); !ok || changesAfter != changesBefore {
		t.Fatalf("stored audit preflight=%T changes=%d/%d", inspection.Preflight, changesBefore, changesAfter)
	}
	if _, err := os.Stat(cacheDir); !os.IsNotExist(err) {
		t.Fatalf("stored audit touched cache: %v", err)
	}
}

func TestStoredCardInputAuditBoundaryRejectsIdentityMismatch(t *testing.T) {
	tests := []struct {
		name    string
		mutate  string
		wantErr string
	}{
		{name: "CardInput", mutate: `update card_execution set card_input_id='card_input:mismatch' where id=?`, wantErr: "stored card-input audit identity does not match its bytes"},
		{name: "request", mutate: `update model_generation set request_sha256='mismatch' where id=(select generation_id from card_execution where id=?)`, wantErr: "stored card-input audit request identity does not match its bytes"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			db := fixtureCardStore(t, ctx)
			defer db.Close()
			assetID := "asset:audit-mismatch:" + test.name
			seedFixtureCardAsset(t, ctx, db, assetID)
			classifier, err := newModelClassifier("fixture-model", "https://models.example.com", "")
			if err != nil {
				t.Fatal(err)
			}
			prepared := fixtureCardPreparationFor(assetID)
			executionID := fixtureExecutionIdentity(t, prepared, classifier)
			if _, err := executeFixtureCard(ctx, db, executionID, func() (fixtureCardPreparation, error) {
				return prepared, nil
			}, classifier, fixtureWireBytes(t, fixtureProviderResponse(t)), fixedClock("2026-07-13T17:00:00Z")()); err != nil {
				t.Fatal(err)
			}
			if _, err := db.DB().ExecContext(ctx, test.mutate, executionID); err != nil {
				t.Fatal(err)
			}
			_, _, err = readStoredCardInputAuditBoundary(ctx, db.DB(), assetID)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("identity mismatch error = %v", err)
			}
		})
	}
}

func TestStoredCardInputAuditBoundaryIgnoresInactiveExecution(t *testing.T) {
	for _, column := range []string{"stale_since", "superseded_at"} {
		t.Run(column, func(t *testing.T) {
			ctx := context.Background()
			db := fixtureCardStore(t, ctx)
			defer db.Close()
			assetID := "asset:audit-inactive:" + column
			seedFixtureCardAsset(t, ctx, db, assetID)
			classifier, err := newModelClassifier("fixture-model", "https://models.example.com", "")
			if err != nil {
				t.Fatal(err)
			}
			prepared := fixtureCardPreparationFor(assetID)
			executionID := fixtureExecutionIdentity(t, prepared, classifier)
			if _, err := executeFixtureCard(ctx, db, executionID, func() (fixtureCardPreparation, error) {
				return prepared, nil
			}, classifier, fixtureWireBytes(t, fixtureProviderResponse(t)), fixedClock("2026-07-13T17:00:00Z")()); err != nil {
				t.Fatal(err)
			}
			query := `update model_observation set ` + column + `='2026-07-13T18:00:00Z' where generation_id=(select generation_id from card_execution where id=?) and observation_type=?`
			if _, err := db.DB().ExecContext(ctx, query, executionID, modelObservationCardSummary); err != nil {
				t.Fatal(err)
			}
			_, found, err := readStoredCardInputAuditBoundary(ctx, db.DB(), assetID)
			if err != nil || found {
				t.Fatalf("inactive execution found=%v err=%v", found, err)
			}
		})
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
create table model_observation(asset_id text, observation_type text, generation_id text, stale_since text, superseded_at text);
create table model_generation(id text primary key, request_sha256 text, request_route text, model_id text, request_body blob);
create table card_execution(id text primary key, asset_id text, card_input_id text, card_input blob, generation_id text, completed_at text);
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
