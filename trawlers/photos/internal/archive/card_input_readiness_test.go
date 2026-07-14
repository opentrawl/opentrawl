package archive

import (
	"context"
	"database/sql"
	"testing"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
)

func TestSelectCardInputArchiveCandidateExcludesStoppedAsset(t *testing.T) {
	ctx, db := cardInputAuditTestDB(t)
	defer db.Close()
	db.SetMaxOpenConns(1)
	seedCardInputAuditAsset(t, ctx, db, "asset:a-stopped", sourceStateCurrent, "image", `{}`)
	seedCardInputAuditAsset(t, ctx, db, "asset:b-ready", sourceStateCurrent, "image", `{}`)
	insertCardInputReadinessResource(t, ctx, db, "asset:a-stopped")
	insertCardInputReadinessResource(t, ctx, db, "asset:b-ready")
	input, err := selectCardInputArchiveCandidate(ctx, db, "source:synthetic", []string{"asset:a-stopped"})
	if err != nil {
		t.Fatal(err)
	}
	if input.AssetID != "asset:b-ready" {
		t.Fatalf("selected asset = %q, want asset:b-ready", input.AssetID)
	}
	fresh, err := selectCardInputArchiveCandidate(ctx, db, "source:synthetic", nil)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.AssetID != "asset:a-stopped" {
		t.Fatalf("fresh selected asset = %q, want asset:a-stopped", fresh.AssetID)
	}
}

func insertCardInputReadinessResource(t *testing.T, ctx context.Context, db *sql.DB, assetID string) {
	t.Helper()
	_, err := db.ExecContext(ctx, `insert into asset_resource(id,asset_id,resource_type,uti,original_filename,local_path,file_size,sha256,available_locally,needs_download) values(?,?,'photo','public.jpeg','synthetic.jpg','',0,'',0,1)`, "resource:"+assetID, assetID)
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
