package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
)

// CardInputCurrentStillOptions names one ready-candidate asset. Acquisition
// reads the archive, then writes only the checked cache.
type CardInputCurrentStillOptions struct {
	CardInputAuditInventoryOptions
	CacheDir     string
	AssetID      string
	AllowNetwork bool
}

// CardInputCurrentStill records one checked current-still acquisition. A
// stopped result contains the exact preflight input and named stop.
type CardInputCurrentStill struct {
	AssetID              string                  `json:"asset_id"`
	StopReason           string                  `json:"stop_reason,omitempty"`
	Preflight            any                     `json:"preflight"`
	ImmutableOriginal    cardInputAuditArtifact  `json:"immutable_original,omitempty"`
	OriginalRequests     int                     `json:"original_requests,omitempty"`
	CurrentStill         photos.CurrentStillFact `json:"current_still,omitempty"`
	CurrentStillProof    string                  `json:"current_still_proof_sha256,omitempty"`
	CurrentStillSource   string                  `json:"current_still_source,omitempty"`
	CurrentStillRequests int                     `json:"current_still_requests,omitempty"`
}

// AcquireCardInputCurrentStill makes at most one request for each missing
// CardInput image role. Cache hits are reopened without PhotoKit.
func AcquireCardInputCurrentStill(ctx context.Context, options CardInputCurrentStillOptions) (CardInputCurrentStill, error) {
	db, err := openCardInputAuditArchive(ctx, options.ArchivePath)
	if err != nil {
		return CardInputCurrentStill{}, err
	}
	defer db.Close()
	_, complete, err := cardInputAuditSnapshot(ctx, db.DB(), options.SourceLibraryID)
	if err != nil {
		return CardInputCurrentStill{}, err
	}
	return acquireCardInputCurrentStill(ctx, db.DB(), complete, options)
}

func acquireCardInputCurrentStill(ctx context.Context, db *sql.DB, complete bool, options CardInputCurrentStillOptions) (CardInputCurrentStill, error) {
	input, err := loadCardInputAuditInput(ctx, db, options.SourceLibraryID, strings.TrimSpace(options.AssetID))
	if err != nil {
		return CardInputCurrentStill{}, err
	}
	acquisition := CardInputCurrentStill{AssetID: input.AssetID, Preflight: input}
	if !complete {
		acquisition.StopReason = cardInputAuditStopSnapshotIncomplete
		return acquisition, nil
	}
	eligibility, err := firstCardEligibilityForAsset(ctx, db, input.AssetID)
	if err != nil {
		return CardInputCurrentStill{}, err
	}
	if eligibility == firstCardProhibitedDeletedBeforeCard {
		acquisition.StopReason = cardInputAuditStopProhibited
		return acquisition, nil
	}
	if input.SourceState != sourceStateCurrent {
		acquisition.StopReason = cardInputAuditStopSourceNotCurrent
		return acquisition, nil
	}
	if input.MediaType != "image" {
		acquisition.StopReason = cardInputAuditStopUnsupportedMedia
		return acquisition, nil
	}
	readiness, err := preflightCardInputMedia(ctx, input)
	if err != nil {
		return CardInputCurrentStill{}, fmt.Errorf("preflight PhotoKit media identity: %w", err)
	}
	originalRequest := input.originalRequest()
	originalRequest.AllowNetwork = options.AllowNetwork
	originalRequest.Query.LocalIdentifier = readiness.LocalIdentifier
	originalResolver, err := photos.NewOriginalResolver(filepath.Join(strings.TrimSpace(options.CacheDir), "originals"), exportOriginalResource)
	if err != nil {
		return CardInputCurrentStill{}, err
	}
	original, err := originalResolver.Resolve(ctx, originalRequest)
	if err != nil {
		return CardInputCurrentStill{}, fmt.Errorf("acquire immutable original: %w", err)
	}
	if original.Lease != nil {
		defer original.Lease.Close()
	}
	acquisition.ImmutableOriginal = cardInputAuditArtifact{Source: original.Source, Size: original.Size, SHA256: original.SHA256}
	if original.Exported {
		acquisition.OriginalRequests = 1
	}
	request, err := input.currentStillRequest()
	if err != nil {
		return CardInputCurrentStill{}, err
	}
	request.AllowNetwork = options.AllowNetwork
	request.PhotoKitLocalIdentifier = readiness.LocalIdentifier
	resolver, err := newCurrentStillResolver(filepath.Join(strings.TrimSpace(options.CacheDir), "originals"), exportCurrentStillResource)
	if err != nil {
		return CardInputCurrentStill{}, err
	}
	resolved, err := resolver.Resolve(ctx, request)
	if err != nil {
		return CardInputCurrentStill{}, fmt.Errorf("acquire full current still: %w", err)
	}
	if resolved.Lease != nil {
		defer resolved.Lease.Close()
	}
	if resolved.PhotoKitCalls > 1 {
		return CardInputCurrentStill{}, fmt.Errorf("acquire current still made %d PhotoKit requests", resolved.PhotoKitCalls)
	}
	_, checkedCurrent, proofSHA256, ok := photos.ReadCachedCurrentStill(filepath.Join(strings.TrimSpace(options.CacheDir), "originals"), request.SourceLibraryID, request.AssetUUID, request.Freshness)
	if !ok {
		return CardInputCurrentStill{}, errors.New("acquire did not reopen the checked current still")
	}
	acquisition.CurrentStill = checkedCurrent
	acquisition.CurrentStillProof = proofSHA256
	acquisition.CurrentStillSource = resolved.Source
	acquisition.CurrentStillRequests = resolved.PhotoKitCalls
	return acquisition, nil
}
