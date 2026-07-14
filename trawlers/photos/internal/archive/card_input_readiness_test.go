package archive

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
)

func TestSelectCardInputArchiveCandidateSkipsUnavailablePackageOriginal(t *testing.T) {
	ctx, db := cardInputAuditTestDB(t)
	defer db.Close()
	db.SetMaxOpenConns(1)
	seedCardInputAuditAsset(t, ctx, db, "asset:a-stopped", sourceStateCurrent, "image", `{}`)
	seedCardInputAuditAsset(t, ctx, db, "asset:b-ready", sourceStateCurrent, "image", `{}`)
	missingPath := filepath.Join(t.TempDir(), "missing.jpg")
	readyPath := filepath.Join(t.TempDir(), "ready.jpg")
	readyBytes := []byte("synthetic package original")
	if err := os.WriteFile(readyPath, readyBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	insertCardInputReadinessResource(t, ctx, db, "asset:a-stopped", missingPath, 1)
	insertCardInputReadinessResource(t, ctx, db, "asset:b-ready", readyPath, int64(len(readyBytes)))

	input, err := selectCardInputArchiveCandidate(ctx, db, "source:synthetic")
	if err != nil {
		t.Fatal(err)
	}
	if input.AssetID != "asset:b-ready" {
		t.Fatalf("selected asset = %q, want asset:b-ready", input.AssetID)
	}
}

func insertCardInputReadinessResource(t *testing.T, ctx context.Context, db *sql.DB, assetID, path string, size int64) {
	t.Helper()
	_, err := db.ExecContext(ctx, `insert into asset_resource(id,asset_id,resource_type,uti,original_filename,local_path,file_size,sha256,available_locally,needs_download) values(?,?,'local_original','public.jpeg','synthetic.jpg',?,?,'',1,0)`, "resource:"+assetID, assetID, path, size)
	if err != nil {
		t.Fatal(err)
	}
}

func TestValidateCardInputLiveReadinessBindsBothMediaBoundaries(t *testing.T) {
	input := classifyInput{
		AssetID:          "asset:synthetic",
		SourceLibraryID:  "source:synthetic",
		LocalIdentifier:  "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE",
		SourceState:      sourceStateCurrent,
		MediaType:        "image",
		CreationDate:     "2026-07-14T12:00:00Z",
		ModificationDate: "2026-07-14T12:00:01Z",
		Width:            4032,
		Height:           3024,
		Resources: []classifyResource{{
			ResourceType: "photo", OriginalFilename: "synthetic.heic", UTI: "public.heic",
		}},
	}
	readiness := photos.AssetReadiness{
		LocalIdentifier:  "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE/L0/001",
		AssetUUID:        "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE",
		MediaType:        "image",
		CreationDate:     "2026-07-14T12:00:00Z",
		ModificationDate: "2026-07-14T12:00:01Z",
		PixelWidth:       4032,
		PixelHeight:      3024,
		OriginalFilename: "synthetic.heic",
		OriginalUTI:      "public.heic",
	}
	if err := validateCardInputLiveReadiness(input, readiness); err != nil {
		t.Fatal(err)
	}
	readiness.ModificationDate = "2026-07-14T12:00:02Z"
	if err := validateCardInputLiveReadiness(input, readiness); err == nil {
		t.Fatal("changed current-still freshness passed readiness")
	}
}
