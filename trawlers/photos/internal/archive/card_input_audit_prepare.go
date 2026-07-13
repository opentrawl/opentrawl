package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/imagemetadata"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
)

const cardInputAuditStopPackageOriginal = "package_original_not_checked"

// CardInputAuditPrepareOptions names one asset and the private checked-artifact
// root that prepare may write. The archive is always opened read-only.
type CardInputAuditPrepareOptions struct {
	CardInputAuditInventoryOptions
	CacheDir string
	AssetID  string
}

// CardInputAuditPreparation records the checked artifacts that inspect reopens.
// A stopped preparation retains every checked artifact prepared before its
// named stop.
type CardInputAuditPreparation struct {
	AssetID              string                  `json:"asset_id"`
	StopReason           string                  `json:"stop_reason,omitempty"`
	Preflight            any                     `json:"preflight"`
	ImmutableOriginal    cardInputAuditArtifact  `json:"immutable_original,omitempty"`
	Metadata             imagemetadata.Artifacts `json:"metadata,omitempty"`
	CurrentStill         photos.CurrentStillFact `json:"current_still,omitempty"`
	CurrentStillProof    string                  `json:"current_still_proof_sha256,omitempty"`
	CurrentStillSource   string                  `json:"current_still_source,omitempty"`
	CurrentStillRequests int                     `json:"current_still_requests,omitempty"`
}

type cardInputAuditArtifact struct {
	Source string `json:"source"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func PrepareCardInputAudit(ctx context.Context, options CardInputAuditPrepareOptions) (CardInputAuditPreparation, error) {
	db, err := openCardInputAuditArchive(ctx, options.ArchivePath)
	if err != nil {
		return CardInputAuditPreparation{}, err
	}
	defer db.Close()
	_, complete, err := cardInputAuditSnapshot(ctx, db.DB(), options.SourceLibraryID)
	if err != nil {
		return CardInputAuditPreparation{}, err
	}
	return prepareCardInputAudit(ctx, db.DB(), complete, options)
}

func prepareCardInputAudit(ctx context.Context, db *sql.DB, complete bool, options CardInputAuditPrepareOptions) (CardInputAuditPreparation, error) {
	input, err := loadCardInputAuditInput(ctx, db, options.SourceLibraryID, strings.TrimSpace(options.AssetID))
	if err != nil {
		return CardInputAuditPreparation{}, err
	}
	prepared := CardInputAuditPreparation{AssetID: input.AssetID, Preflight: input}
	if !complete {
		prepared.StopReason = cardInputAuditStopSnapshotIncomplete
		return prepared, nil
	}
	eligibility, err := firstCardEligibilityForAsset(ctx, db, input.AssetID)
	if err != nil {
		return CardInputAuditPreparation{}, err
	}
	if eligibility == firstCardProhibitedDeletedBeforeCard {
		prepared.StopReason = cardInputAuditStopProhibited
		return prepared, nil
	}
	if input.SourceState != sourceStateCurrent {
		prepared.StopReason = cardInputAuditStopSourceNotCurrent
		return prepared, nil
	}
	if input.MediaType != "image" {
		prepared.StopReason = cardInputAuditStopUnsupportedMedia
		return prepared, nil
	}
	request := input.originalRequest()
	if _, ok := photos.UniquePackageOriginal(request.PackageCandidates); !ok {
		prepared.StopReason = cardInputAuditStopPackageOriginal
		return prepared, nil
	}
	request.AllowNetwork = false
	originalResolver, err := photos.NewOriginalResolver(filepath.Join(strings.TrimSpace(options.CacheDir), "originals"), rejectCardInputAuditOriginalExport)
	if err != nil {
		return CardInputAuditPreparation{}, err
	}
	original, err := originalResolver.Resolve(ctx, request)
	if err != nil {
		return CardInputAuditPreparation{}, fmt.Errorf("prepare package-local immutable original: %w", err)
	}
	if original.Lease != nil {
		defer original.Lease.Close()
	}
	if original.Source != photos.OriginalSourcePackage {
		return CardInputAuditPreparation{}, errors.New("prepare did not resolve a package-local immutable original")
	}
	prepared.ImmutableOriginal = cardInputAuditArtifact{Source: original.Source, Size: original.Size, SHA256: original.SHA256}
	metadataStore, err := imagemetadata.NewStore(filepath.Join(strings.TrimSpace(options.CacheDir), "image-metadata"), extractImageMetadata)
	if err != nil {
		return CardInputAuditPreparation{}, err
	}
	metadata, err := metadataStore.Load(ctx, original.Path, original.SHA256)
	if err != nil {
		return CardInputAuditPreparation{}, fmt.Errorf("prepare exact-original metadata: %w", err)
	}
	prepared.Metadata = metadata
	currentRequest, err := input.currentStillRequest()
	if err != nil {
		return CardInputAuditPreparation{}, err
	}
	if currentRequest.AllowNetwork {
		return CardInputAuditPreparation{}, errors.New("prepare current-still request enabled network access")
	}
	_, checkedCurrent, proofSHA256, ok := photos.ReadCachedCurrentStill(filepath.Join(strings.TrimSpace(options.CacheDir), "originals"), currentRequest.SourceLibraryID, currentRequest.AssetUUID, currentRequest.Freshness)
	if !ok {
		prepared.StopReason = cardInputAuditStopMissingCurrentStill
		return prepared, nil
	}
	prepared.CurrentStill = checkedCurrent
	prepared.CurrentStillProof = proofSHA256
	prepared.CurrentStillSource = photos.CurrentStillSourceCache
	prepared.CurrentStillRequests = 0
	return prepared, nil
}

func rejectCardInputAuditOriginalExport(context.Context, photos.OriginalExportQuery, string, bool) error {
	return errors.New("card-input audit prepare requires a package-local immutable original")
}
