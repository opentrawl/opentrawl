package archive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/media/mediawire"
	locationwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/location"
	"github.com/opentrawl/opentrawl/trawlkit/store"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func CurrentRenderedStillRequestForPhotoUpdateAsset(asset PhotoUpdateAsset) *mediawire.AcquireCurrentRenderedStillRequest {
	request := &mediawire.AcquireCurrentRenderedStillRequest{
		PhotoAssetLocalIdentifier: string(asset.LocalIdentifier),
		AllowIcloudNetworkAccess:  true,
	}
	if asset.ModificationTime.Present {
		request.ExpectedPhotoModificationTime = timestamppb.New(asset.ModificationTime.Value)
	}
	return request
}

func ImmutableOriginalImageFactsRequestForPhotoUpdateAsset(asset PhotoUpdateAsset) *mediawire.InspectImmutableOriginalImageFactsRequest {
	request := &mediawire.InspectImmutableOriginalImageFactsRequest{
		PhotoAssetLocalIdentifier: string(asset.LocalIdentifier),
		AllowIcloudNetworkAccess:  true,
	}
	for _, resource := range asset.OriginalResources {
		request.ExpectedIndexedOriginalResources = append(request.ExpectedIndexedOriginalResources, &mediawire.IndexedOriginalResourceIdentity{
			PhotosSqliteResourcePrimaryKey: resource.SourceResourcePrimaryKey,
			PhotosSqliteResourceType:       resource.SourceResourceType,
			SourceStableHash:               resource.SourceStableHash,
			SourceFingerprint:              resource.SourceFingerprint,
			Filename:                       resource.Filename,
			UniformTypeIdentifier:          resource.UniformTypeIdentifier,
			IndexedByteCount:               resource.IndexedByteCount,
		})
	}
	return request
}

func CurrentRenderedPhotoMediaOutcomeMatchesRequest(outcome *mediawire.CurrentRenderedPhotoMediaOutcome, request *mediawire.AcquireCurrentRenderedStillRequest) bool {
	if outcome == nil || request == nil || outcome.GetCompletedAt() == nil {
		return false
	}
	if available := outcome.GetAvailable(); available != nil {
		return available.GetDerivationReceipt() != nil && proto.Equal(available.GetDerivationReceipt().GetRequest(), request)
	}
	if unavailable := outcome.GetUnavailable(); unavailable != nil {
		return unavailable.GetReason() != nil && proto.Equal(unavailable.GetRequest(), request)
	}
	return false
}

func ImmutableOriginalImageFactsOutcomeMatchesRequest(outcome *mediawire.ImmutableOriginalImageFactsOutcome, request *mediawire.InspectImmutableOriginalImageFactsRequest) bool {
	if !immutableOriginalImageFactsOutcomeIsComplete(outcome) || request == nil {
		return false
	}
	switch outcome.GetState() {
	case mediawire.ImmutableOriginalImageFactsState_IMMUTABLE_ORIGINAL_IMAGE_FACTS_STATE_AVAILABLE:
	case mediawire.ImmutableOriginalImageFactsState_IMMUTABLE_ORIGINAL_IMAGE_FACTS_STATE_UNAVAILABLE:
		if outcome.GetUnavailable().GetReason() != mediawire.PhotosMediaUnavailableReason_PHOTOS_MEDIA_UNAVAILABLE_REASON_IMMUTABLE_ORIGINAL_NOT_FOUND {
			return false
		}
	default:
		return false
	}
	return proto.Equal(normalizeImmutableOriginalRequest(outcome.GetRequest()), normalizeImmutableOriginalRequest(request))
}

func normalizeImmutableOriginalRequest(request *mediawire.InspectImmutableOriginalImageFactsRequest) *mediawire.InspectImmutableOriginalImageFactsRequest {
	if request == nil {
		return nil
	}
	return proto.Clone(request).(*mediawire.InspectImmutableOriginalImageFactsRequest)
}

func LoadCurrentRenderedPhotoMediaOutcome(ctx context.Context, openedStore *store.Store, assetID PhotoAssetID) (*mediawire.CurrentRenderedPhotoMediaOutcome, bool, error) {
	var encoded []byte
	err := openedStore.DB().QueryRowContext(ctx, `select outcome_proto from current_rendered_photo_media_outcome where asset_id=?`, assetID).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	outcome := new(mediawire.CurrentRenderedPhotoMediaOutcome)
	if err := proto.Unmarshal(encoded, outcome); err != nil {
		return nil, false, err
	}
	return outcome, true, nil
}

func StoreAvailableCurrentRenderedPhotoMediaOutcome(ctx context.Context, openedStore *store.Store, assetID PhotoAssetID, currentRenderedStill *mediawire.CurrentRenderedStillLease) error {
	if currentRenderedStill == nil || len(currentRenderedStill.GetSha256()) != sha256.Size {
		return errors.New("current rendered photo is incomplete")
	}
	receipt := currentRenderedStill.GetDerivationReceipt()
	if receipt == nil || len(receipt.GetFinalJpegSha256()) != sha256.Size || !bytes.Equal(receipt.GetFinalJpegSha256(), currentRenderedStill.GetSha256()) {
		return errors.New("current rendered photo receipt is incomplete")
	}
	outcome := &mediawire.CurrentRenderedPhotoMediaOutcome{
		Outcome: &mediawire.CurrentRenderedPhotoMediaOutcome_Available{Available: &mediawire.AvailableCurrentRenderedPhotoMedia{
			ByteCount: currentRenderedStill.GetByteCount(), Sha256: currentRenderedStill.GetSha256(),
			UniformTypeIdentifier: currentRenderedStill.GetUniformTypeIdentifier(), ImageOrientation: currentRenderedStill.GetImageOrientation(),
			PixelWidth: currentRenderedStill.GetPixelWidth(), PixelHeight: currentRenderedStill.GetPixelHeight(), DerivationReceipt: receipt,
		}},
		CompletedAt: timestamppb.Now(),
	}
	return storeCurrentRenderedPhotoMediaOutcome(ctx, openedStore, assetID, outcome)
}

func StoreUnavailableCurrentRenderedPhotoMediaOutcome(ctx context.Context, openedStore *store.Store, assetID PhotoAssetID, request *mediawire.AcquireCurrentRenderedStillRequest, unavailable *mediawire.PhotosMediaUnavailable) error {
	if request == nil || unavailable == nil || unavailable.GetReason() == mediawire.PhotosMediaUnavailableReason_PHOTOS_MEDIA_UNAVAILABLE_REASON_UNSPECIFIED {
		return errors.New("unavailable current rendered photo outcome is incomplete")
	}
	outcome := &mediawire.CurrentRenderedPhotoMediaOutcome{
		Outcome: &mediawire.CurrentRenderedPhotoMediaOutcome_Unavailable{Unavailable: &mediawire.UnavailableCurrentRenderedPhotoMedia{
			Request: proto.Clone(request).(*mediawire.AcquireCurrentRenderedStillRequest),
			Reason:  proto.Clone(unavailable).(*mediawire.PhotosMediaUnavailable),
		}},
		CompletedAt: timestamppb.Now(),
	}
	return storeCurrentRenderedPhotoMediaOutcome(ctx, openedStore, assetID, outcome)
}

func storeCurrentRenderedPhotoMediaOutcome(ctx context.Context, openedStore *store.Store, assetID PhotoAssetID, outcome *mediawire.CurrentRenderedPhotoMediaOutcome) error {
	if assetID == "" || outcome == nil || outcome.GetCompletedAt() == nil || outcome.GetOutcome() == nil {
		return errors.New("current rendered photo media outcome is incomplete")
	}
	encoded, err := proto.Marshal(outcome)
	if err != nil {
		return err
	}
	_, err = openedStore.DB().ExecContext(ctx, `insert into current_rendered_photo_media_outcome(asset_id, outcome_proto) values (?, ?) on conflict(asset_id) do update set outcome_proto=excluded.outcome_proto`, assetID, encoded)
	return err
}

func LoadCurrentImmutableOriginalImageFactsOutcomeForRequest(ctx context.Context, openedStore *store.Store, assetID PhotoAssetID, request *mediawire.InspectImmutableOriginalImageFactsRequest) (*mediawire.ImmutableOriginalImageFactsOutcome, bool, error) {
	outcome, found, err := LoadRetainedImmutableOriginalImageFactsOutcome(ctx, openedStore, assetID)
	if err != nil || !found {
		return outcome, false, err
	}
	return outcome, ImmutableOriginalImageFactsOutcomeMatchesRequest(outcome, request), nil
}

func LoadRetainedImmutableOriginalImageFactsOutcome(ctx context.Context, openedStore *store.Store, assetID PhotoAssetID) (*mediawire.ImmutableOriginalImageFactsOutcome, bool, error) {
	var encoded []byte
	err := openedStore.DB().QueryRowContext(ctx, `select outcome_proto from current_immutable_original_image_facts where asset_id=?`, assetID).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	outcome := new(mediawire.ImmutableOriginalImageFactsOutcome)
	if err := proto.Unmarshal(encoded, outcome); err != nil {
		return nil, false, err
	}
	return outcome, true, nil
}

func StoreCurrentImmutableOriginalImageFactsOutcome(ctx context.Context, openedStore *store.Store, assetID PhotoAssetID, outcome *mediawire.ImmutableOriginalImageFactsOutcome) error {
	if !immutableOriginalImageFactsOutcomeIsComplete(outcome) {
		return errors.New("immutable original image facts outcome is incomplete")
	}
	encoded, err := proto.Marshal(outcome)
	if err != nil {
		return err
	}
	_, err = openedStore.DB().ExecContext(ctx, `insert into current_immutable_original_image_facts(asset_id, outcome_proto) values (?, ?) on conflict(asset_id) do update set outcome_proto=excluded.outcome_proto`, assetID, encoded)
	return err
}

func SelectPhotosWithPendingProductionNodes(
	ctx context.Context,
	openedStore *store.Store,
	knownPlaceConfigurationSHA256 []byte,
	currentLocationEvidenceMatchesDependencies func(context.Context, PhotoUpdateAsset, *locationwire.CaptureLocationInput, *locationwire.ComposePhotoLocationEvidenceOutcome) (bool, error),
) ([]PhotoUpdateAsset, error) {
	if err := prepareStore(ctx, openedStore); err != nil {
		return nil, err
	}
	rows, err := openedStore.DB().QueryContext(ctx, `
select asset.id, asset.source_library_id, seen.source_fingerprint, asset.local_identifier,
       asset.media_type, printf('kind_subtype:%d', asset.photos_sqlite_kind_subtype), asset.creation_date, asset.modification_date,
       asset.width, asset.height, asset.camera_make, asset.camera_model, asset.lens_model,
       asset.focal_length_mm, asset.aperture, asset.shutter_speed, asset.iso,
       resource.photos_sqlite_resource_primary_key, resource.photos_sqlite_resource_type,
       resource.photos_sqlite_stable_hash, resource.photos_sqlite_fingerprint,
       resource.original_filename, resource.uti_projection, resource.file_size
from asset
join crawl_seen_asset seen on seen.asset_id=asset.id and seen.source_library_id=asset.source_library_id
left join asset_resource resource on resource.asset_id=asset.id and resource.resource_type_projection='photo'
where asset.source_state='current'
order by asset.creation_date, asset.id, resource.photos_sqlite_resource_primary_key`)
	if err != nil {
		return nil, fmt.Errorf("select current Photos assets: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var allAssets []PhotoUpdateAsset
	for rows.Next() {
		asset, original, scanErr := scanPhotoUpdateAssetProjection(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		if len(allAssets) == 0 || allAssets[len(allAssets)-1].AssetID != asset.AssetID {
			allAssets = append(allAssets, asset)
		}
		appendPhotoUpdateOriginalResource(&allAssets[len(allAssets)-1], original)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	pending := make([]PhotoUpdateAsset, 0, len(allAssets))
	for _, asset := range allAssets {
		if asset.MediaType != PhotoMediaKindImage {
			continue
		}
		captureInput, hasCapture, err := LoadOptionalCaptureLocationInput(ctx, openedStore, string(asset.AssetID))
		if err != nil {
			return nil, err
		}
		mediaRequest := CurrentRenderedStillRequestForPhotoUpdateAsset(asset)
		media, mediaFound, err := LoadCurrentRenderedPhotoMediaOutcome(ctx, openedStore, asset.AssetID)
		if err != nil {
			return nil, err
		}
		mediaReady := mediaFound && CurrentRenderedPhotoMediaOutcomeMatchesRequest(media, mediaRequest)
		originalRequest := ImmutableOriginalImageFactsRequestForPhotoUpdateAsset(asset)
		_, originalReady, err := LoadCurrentImmutableOriginalImageFactsOutcomeForRequest(ctx, openedStore, asset.AssetID, originalRequest)
		if err != nil {
			return nil, err
		}
		locationReady := !hasCapture
		if hasCapture {
			locationOutcome, found, loadErr := LoadCurrentPhotoLocationEvidence(ctx, openedStore, asset.AssetID)
			if loadErr != nil {
				return nil, loadErr
			}
			if found && currentLocationEvidenceMatchesDependencies != nil {
				locationReady, loadErr = currentLocationEvidenceMatchesDependencies(ctx, asset, captureInput, locationOutcome)
				if loadErr != nil {
					return nil, loadErr
				}
			}
		}
		if !mediaReady || !originalReady || !locationReady {
			pending = append(pending, asset)
		}
	}
	return pending, nil
}
