package archive

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	photosmedia "github.com/opentrawl/opentrawl/trawlers/photos/internal/media"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/media/mediawire"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// CardInputCurrentStillOptions names one ready-candidate asset for a live
// installed-app media audit.
type CardInputCurrentStillOptions struct {
	CardInputAuditInventoryOptions
	AssetID      string
	AllowNetwork bool
}

// CardInputCurrentStill records facts from one checked current-still lease. It
// never owns or returns the leased image bytes. A stopped result contains the
// exact preflight input and named stop.
type CardInputCurrentStill struct {
	AssetID              string                  `json:"asset_id"`
	StopReason           string                  `json:"stop_reason,omitempty"`
	Preflight            classifyInput           `json:"preflight"`
	ImmutableOriginal    cardInputAuditArtifact  `json:"immutable_original,omitempty"`
	OriginalRequests     int                     `json:"original_requests,omitempty"`
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

// AcquireCardInputCurrentStill asks the installed OpenTrawl application for
// immutable-original facts and one current-rendered-still lease, verifies the
// lease, records its facts, then releases it.
func AcquireCardInputCurrentStill(ctx context.Context, options CardInputCurrentStillOptions) (CardInputCurrentStill, error) {
	db, err := openCardInputAuditArchive(ctx, options.ArchivePath)
	if err != nil {
		return CardInputCurrentStill{}, err
	}
	defer func() { _ = db.Close() }()
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
	originalRequest := input.immutableOriginalIdentity()
	originalByteCount := immutableOriginalByteCount(input, originalRequest.OriginalFilename, originalRequest.OriginalUTI)
	client := photosmedia.NewInstalledOpenTrawlClient()
	original, err := client.InspectImmutableOriginalImageFacts(ctx, &mediawire.InspectImmutableOriginalImageFactsRequest{
		PhotoAssetLocalIdentifier:                      readiness.GetPhotoAssetLocalIdentifier(),
		ExpectedImmutableOriginalFilename:              originalRequest.OriginalFilename,
		ExpectedImmutableOriginalUniformTypeIdentifier: originalRequest.OriginalUTI,
		ExpectedImmutableOriginalByteCount:             uint64(originalByteCount),
		AllowIcloudNetworkAccess:                       options.AllowNetwork,
	})
	if err != nil {
		return CardInputCurrentStill{}, fmt.Errorf("acquire immutable original: %w", err)
	}
	acquisition.ImmutableOriginal = cardInputAuditArtifact{Source: "installed_opentrawl_immutable_original_facts", Size: int64(original.GetByteCount()), SHA256: hex.EncodeToString(original.GetSha256())}
	acquisition.OriginalRequests = 1
	request, err := input.currentStillRequest()
	if err != nil {
		return CardInputCurrentStill{}, err
	}
	mediaRequest, err := currentRenderedStillMediaRequest(request, readiness.GetPhotoAssetLocalIdentifier(), options.AllowNetwork)
	if err != nil {
		return CardInputCurrentStill{}, err
	}
	lease, err := client.AcquireCurrentRenderedStill(ctx, mediaRequest)
	if err != nil {
		return CardInputCurrentStill{}, fmt.Errorf("acquire full current still: %w", err)
	}
	if err := lease.Verify(); err != nil {
		if releaseErr := lease.Close(); releaseErr != nil {
			return CardInputCurrentStill{}, fmt.Errorf("verify current rendered still: %v; release current rendered still: %w", err, releaseErr)
		}
		return CardInputCurrentStill{}, err
	}
	outcome := lease.Outcome
	acquisition.CurrentStill = photos.CurrentStillFact{
		MediaType:     outcome.GetUniformTypeIdentifier(),
		Orientation:   int32(outcome.GetImageOrientation()),
		PixelWidth:    int64(outcome.GetPixelWidth()),
		PixelHeight:   int64(outcome.GetPixelHeight()),
		Size:          int64(outcome.GetByteCount()),
		SHA256:        hex.EncodeToString(outcome.GetSha256()),
		PhotoKitCalls: 1,
	}
	acquisition.CurrentStillProof = hex.EncodeToString(outcome.GetSha256())
	acquisition.CurrentStillSource = "installed_opentrawl_current_rendered_still"
	acquisition.CurrentStillRequests = 1
	if err := lease.Close(); err != nil {
		return CardInputCurrentStill{}, fmt.Errorf("release current rendered still: %w", err)
	}
	return acquisition, nil
}

func immutableOriginalByteCount(input classifyInput, filename, uniformTypeIdentifier string) int64 {
	for _, resource := range input.Resources {
		if resource.ResourceType == "photo" && resource.OriginalFilename == filename && resource.UTI == uniformTypeIdentifier {
			return resource.FileSize
		}
	}
	return 0
}

func currentRenderedStillMediaRequest(request photos.CurrentStillRequest, localIdentifier string, allowNetwork bool) (*mediawire.AcquireCurrentRenderedStillRequest, error) {
	modification, ok := request.Freshness.ExpectedModification()
	if !ok {
		return nil, errors.New("current rendered still requires the indexed Photos modification instant")
	}
	return &mediawire.AcquireCurrentRenderedStillRequest{
		PhotoAssetLocalIdentifier:     localIdentifier,
		ExpectedPhotoModificationTime: timestamppb.New(time.Unix(modification.UnixSeconds, int64(modification.Microseconds)*int64(time.Microsecond))),
		AllowIcloudNetworkAccess:      allowNetwork,
	}, nil
}
