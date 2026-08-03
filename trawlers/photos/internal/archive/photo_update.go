package archive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/media/mediawire"
	locationwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/location"
	"github.com/opentrawl/opentrawl/trawlkit/store"
	"google.golang.org/protobuf/proto"
)

type PhotoUpdateAsset struct {
	AssetID           PhotoAssetID
	SourceLibraryID   string
	SourceFingerprint PhotoSourceFingerprint
	LocalIdentifier   PhotosLocalIdentifier
	MediaType         PhotoMediaKind
	MediaSubtypes     string
	CreationTime      OptionalPhotosTimestamp
	ModificationTime  OptionalPhotosTimestamp
	PixelWidth        int64
	PixelHeight       int64
	CameraMake        string
	CameraModel       string
	LensModel         string
	FocalLengthMM     sql.NullFloat64
	Aperture          sql.NullFloat64
	ExposureSeconds   sql.NullFloat64
	ISO               sql.NullInt64
	OriginalResources []PhotoUpdateOriginalResource
}

type PhotoAssetID string
type PhotosLocalIdentifier string
type PhotoSourceFingerprint string
type PhotoMediaKind string

type OptionalPhotosTimestamp struct {
	Value   time.Time
	Present bool
}

const PhotoMediaKindImage PhotoMediaKind = "image"

type PhotoUpdateOriginalResource struct {
	SourceResourcePrimaryKey int64
	SourceResourceType       int32
	SourceStableHash         string
	SourceFingerprint        string
	Filename                 string
	UniformTypeIdentifier    string
	IndexedByteCount         int64
}

type RetainedCurrentPhotoMediaEvidence struct {
	SourceFingerprint                     PhotoSourceFingerprint
	ImmutableOriginalImageFactsOutcome    *mediawire.ImmutableOriginalImageFactsOutcome
	ImmutableOriginalImageFacts           *mediawire.ImmutableOriginalImageFacts
	CurrentRenderedStillDerivationReceipt *mediawire.CurrentRenderedStillDerivationReceipt
	CurrentRenderedStillSHA256            []byte
	CurrentRenderedStillMediaType         string
	CurrentRenderedStillByteCount         uint64
	CurrentRenderedStillPixelWidth        uint64
	CurrentRenderedStillPixelHeight       uint64
	CurrentRenderedStillOrientation       mediawire.ImageOrientation
}

type photoUpdateOriginalResourceProjection struct {
	SourceResourcePrimaryKey sql.NullInt64
	SourceResourceType       sql.NullInt64
	SourceStableHash         sql.NullString
	SourceFingerprint        sql.NullString
	Filename                 sql.NullString
	UniformTypeIdentifier    sql.NullString
	ByteCount                sql.NullInt64
}

type photoUpdateAssetRowScanner interface {
	Scan(destinations ...any) error
}

func scanPhotoUpdateAssetProjection(scanner photoUpdateAssetRowScanner, trailingDestinations ...any) (PhotoUpdateAsset, photoUpdateOriginalResourceProjection, error) {
	var asset PhotoUpdateAsset
	var creationTimeText, modificationTimeText string
	var original photoUpdateOriginalResourceProjection
	destinations := []any{
		&asset.AssetID, &asset.SourceLibraryID, &asset.SourceFingerprint, &asset.LocalIdentifier,
		&asset.MediaType, &asset.MediaSubtypes, &creationTimeText, &modificationTimeText,
		&asset.PixelWidth, &asset.PixelHeight, &asset.CameraMake, &asset.CameraModel, &asset.LensModel,
		&asset.FocalLengthMM, &asset.Aperture, &asset.ExposureSeconds, &asset.ISO,
		&original.SourceResourcePrimaryKey, &original.SourceResourceType,
		&original.SourceStableHash, &original.SourceFingerprint,
		&original.Filename, &original.UniformTypeIdentifier, &original.ByteCount,
	}
	destinations = append(destinations, trailingDestinations...)
	if err := scanner.Scan(destinations...); err != nil {
		return PhotoUpdateAsset{}, photoUpdateOriginalResourceProjection{}, err
	}
	var err error
	asset.CreationTime, err = parseOptionalPhotosTimestamp(creationTimeText)
	if err != nil {
		return PhotoUpdateAsset{}, photoUpdateOriginalResourceProjection{}, fmt.Errorf("parse Photos creation time for asset %q: %w", asset.AssetID, err)
	}
	asset.ModificationTime, err = parseOptionalPhotosTimestamp(modificationTimeText)
	if err != nil {
		return PhotoUpdateAsset{}, photoUpdateOriginalResourceProjection{}, fmt.Errorf("parse Photos modification time for asset %q: %w", asset.AssetID, err)
	}
	return asset, original, nil
}

func appendPhotoUpdateOriginalResource(asset *PhotoUpdateAsset, original photoUpdateOriginalResourceProjection) {
	if asset == nil || !original.Filename.Valid {
		return
	}
	asset.OriginalResources = append(asset.OriginalResources, PhotoUpdateOriginalResource{
		SourceResourcePrimaryKey: original.SourceResourcePrimaryKey.Int64,
		SourceResourceType:       int32(original.SourceResourceType.Int64),
		SourceStableHash:         original.SourceStableHash.String,
		SourceFingerprint:        original.SourceFingerprint.String,
		Filename:                 original.Filename.String,
		UniformTypeIdentifier:    original.UniformTypeIdentifier.String,
		IndexedByteCount:         original.ByteCount.Int64,
	})
}

func immutableOriginalImageFactsOutcomeIsComplete(outcome *mediawire.ImmutableOriginalImageFactsOutcome) bool {
	if outcome == nil || outcome.GetRequest() == nil || outcome.GetCompletedAt() == nil {
		return false
	}
	switch outcome.GetState() {
	case mediawire.ImmutableOriginalImageFactsState_IMMUTABLE_ORIGINAL_IMAGE_FACTS_STATE_AVAILABLE:
		return outcome.GetFacts() != nil && len(outcome.GetFacts().GetSha256()) == sha256.Size
	case mediawire.ImmutableOriginalImageFactsState_IMMUTABLE_ORIGINAL_IMAGE_FACTS_STATE_UNAVAILABLE:
		return outcome.GetUnavailable() != nil
	case mediawire.ImmutableOriginalImageFactsState_IMMUTABLE_ORIGINAL_IMAGE_FACTS_STATE_FAILED:
		return outcome.GetFailure() != nil || outcome.GetAdmissionDeferred() != nil
	default:
		return false
	}
}

func LoadPhotoUpdateAsset(ctx context.Context, openedStore *store.Store, assetID PhotoAssetID) (PhotoUpdateAsset, error) {
	rows, err := openedStore.DB().QueryContext(ctx, `
select asset.id, asset.source_library_id, seen.source_fingerprint, asset.local_identifier,
       asset.media_type, printf('kind_subtype:%d', asset.photos_sqlite_kind_subtype), asset.creation_date, asset.modification_date,
       asset.width, asset.height, asset.camera_make, asset.camera_model, asset.lens_model,
       asset.focal_length_mm, asset.aperture, asset.shutter_speed, asset.iso,
       resource.photos_sqlite_resource_primary_key, resource.photos_sqlite_resource_type,
       resource.photos_sqlite_stable_hash, resource.photos_sqlite_fingerprint,
       resource.original_filename, resource.uti_projection, resource.file_size
from asset
join crawl_seen_asset seen on seen.asset_id = asset.id and seen.source_library_id = asset.source_library_id
left join asset_resource resource on resource.asset_id = asset.id and resource.resource_type_projection = 'photo'
where asset.id = ? and asset.source_state = 'current'
order by resource.photos_sqlite_resource_primary_key`, assetID)
	if err != nil {
		return PhotoUpdateAsset{}, fmt.Errorf("load Photos update asset: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var loaded PhotoUpdateAsset
	for rows.Next() {
		asset, originalResource, scanErr := scanPhotoUpdateAssetProjection(rows)
		if scanErr != nil {
			return PhotoUpdateAsset{}, fmt.Errorf("read Photos update asset: %w", scanErr)
		}
		if loaded.AssetID == "" {
			loaded = asset
		}
		appendPhotoUpdateOriginalResource(&loaded, originalResource)
	}
	if err := rows.Err(); err != nil {
		return PhotoUpdateAsset{}, fmt.Errorf("read Photos update asset: %w", err)
	}
	if loaded.AssetID == "" {
		return PhotoUpdateAsset{}, fmt.Errorf("Photos asset %q is not indexed as current", assetID)
	}
	return loaded, nil
}

func parseOptionalPhotosTimestamp(value string) (OptionalPhotosTimestamp, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return OptionalPhotosTimestamp{}, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return OptionalPhotosTimestamp{}, err
	}
	return OptionalPhotosTimestamp{Value: parsed, Present: true}, nil
}

func LoadCurrentPhotoLocationEvidence(ctx context.Context, openedStore *store.Store, assetID PhotoAssetID) (*locationwire.ComposePhotoLocationEvidenceOutcome, bool, error) {
	captureLocationInput, found, err := LoadOptionalCaptureLocationInput(ctx, openedStore, string(assetID))
	if err != nil || !found {
		return nil, false, err
	}
	knownPlaceConfigurationSHA256, err := KnownPlaceConfigurationSHA256(ctx, openedStore)
	if err != nil {
		return nil, false, err
	}
	var encoded []byte
	err = openedStore.DB().QueryRowContext(ctx, `select outcome_proto from current_photo_location_evidence where asset_id=? and known_place_configuration_sha256=?`, assetID, knownPlaceConfigurationSHA256).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	outcome := new(locationwire.ComposePhotoLocationEvidenceOutcome)
	if err := proto.Unmarshal(encoded, outcome); err != nil {
		return nil, false, nil
	}
	knownPlaceOutcome, knownPlaceFound, err := LoadMatchConfiguredKnownPlaceOutcome(ctx, openedStore, string(assetID))
	if err != nil || !knownPlaceFound {
		return nil, false, err
	}
	appleReverseOutcome, appleReverseFound, err := LoadAppleReverseGeocodingEvidenceOutcome(ctx, openedStore, string(assetID))
	if err != nil || !appleReverseFound {
		return nil, false, err
	}
	appleNearbyOutcome, appleNearbyFound, err := LoadAppleNearbyPlaceEvidenceOutcome(ctx, openedStore, string(assetID))
	if err != nil || !appleNearbyFound {
		return nil, false, err
	}
	geoapifyReverseOutcome, geoapifyReverseFound, err := LoadGeoapifyReverseGeocodingEvidenceOutcome(ctx, openedStore, string(assetID))
	if err != nil || !geoapifyReverseFound {
		return nil, false, err
	}
	geoapifyPlacesOutcome, geoapifyPlacesFound, err := LoadGeoapifyPhotographedPlaceCandidateEvidenceOutcome(ctx, openedStore, string(assetID))
	if err != nil || !geoapifyPlacesFound {
		return nil, false, err
	}
	if !photoLocationDependenciesMatchCaptureLocation(captureLocationInput, knownPlaceConfigurationSHA256, knownPlaceOutcome, appleReverseOutcome, appleNearbyOutcome, geoapifyReverseOutcome, geoapifyPlacesOutcome) ||
		!PhotoLocationEvidenceCompositionMatchesDependencies(outcome, knownPlaceOutcome, appleReverseOutcome, appleNearbyOutcome, geoapifyReverseOutcome, geoapifyPlacesOutcome, outcome.GetRequest().GetMaximumDistinctCandidateCategoriesPerProvider()) ||
		!proto.Equal(outcome.GetBriefing().GetCaptureLocation(), captureLocationInput) {
		return nil, false, nil
	}
	return outcome, true, nil
}

func photoLocationDependenciesMatchCaptureLocation(captureLocationInput *locationwire.CaptureLocationInput, knownPlaceConfigurationSHA256 []byte, knownPlaceOutcome *locationwire.MatchConfiguredKnownPlaceOutcome, appleReverseOutcome *locationwire.AcquireAppleReverseGeocodingEvidenceOutcome, appleNearbyOutcome *locationwire.AcquireAppleNearbyPlaceEvidenceOutcome, geoapifyReverseOutcome *locationwire.AcquireGeoapifyReverseGeocodingEvidenceOutcome, geoapifyPlacesOutcome *locationwire.AcquireGeoapifyPhotographedPlaceCandidateEvidenceOutcome) bool {
	return proto.Equal(knownPlaceOutcome.GetRequest().GetInput(), captureLocationInput) &&
		bytes.Equal(knownPlaceOutcome.GetRequest().GetKnownPlaceConfigurationSha256(), knownPlaceConfigurationSHA256) &&
		proto.Equal(appleReverseOutcome.GetRequest().GetInput(), captureLocationInput) &&
		proto.Equal(appleNearbyOutcome.GetRequest().GetInput(), captureLocationInput) &&
		proto.Equal(geoapifyReverseOutcome.GetRequest().GetInput(), captureLocationInput) &&
		proto.Equal(geoapifyPlacesOutcome.GetRequest().GetInput(), captureLocationInput)
}

func PhotoLocationEvidenceCompositionMatchesDependencies(retained *locationwire.ComposePhotoLocationEvidenceOutcome, knownPlaceOutcome *locationwire.MatchConfiguredKnownPlaceOutcome, appleReverseOutcome *locationwire.AcquireAppleReverseGeocodingEvidenceOutcome, appleNearbyOutcome *locationwire.AcquireAppleNearbyPlaceEvidenceOutcome, geoapifyReverseOutcome *locationwire.AcquireGeoapifyReverseGeocodingEvidenceOutcome, geoapifyPlacesOutcome *locationwire.AcquireGeoapifyPhotographedPlaceCandidateEvidenceOutcome, maximumDistinctCandidateCategoriesPerProvider uint32) bool {
	request := retained.GetRequest()
	return composedPhotoLocationEvidenceIsCurrent(retained) &&
		request.GetAssetId() == knownPlaceOutcome.GetRequest().GetInput().GetAssetId() &&
		request.GetMaximumDistinctCandidateCategoriesPerProvider() == maximumDistinctCandidateCategoriesPerProvider &&
		protoSHA256Matches(request.GetKnownPlaceOutcomeSha256(), knownPlaceOutcome) &&
		protoSHA256Matches(request.GetAppleReverseOutcomeSha256(), appleReverseOutcome) &&
		protoSHA256Matches(request.GetAppleNearbyOutcomeSha256(), appleNearbyOutcome) &&
		protoSHA256Matches(request.GetGeoapifyReverseGeocodingOutcomeSha256(), geoapifyReverseOutcome) &&
		protoSHA256Matches(request.GetGeoapifyPhotographedPlaceCandidateEvidenceOutcomeSha256(), geoapifyPlacesOutcome)
}

func protoSHA256Matches(expected []byte, message proto.Message) bool {
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	if err != nil {
		return false
	}
	digest := sha256.Sum256(encoded)
	return bytes.Equal(expected, digest[:])
}

func composedPhotoLocationEvidenceIsCurrent(outcome *locationwire.ComposePhotoLocationEvidenceOutcome) bool {
	if outcome.GetState() != locationwire.OperationState_OPERATION_STATE_SUCCEEDED {
		return false
	}
	briefing := outcome.GetBriefing()
	if briefing == nil {
		return false
	}
	reusable := func(status *locationwire.LocationOperationTerminalStatus, allowKnownPlaceSkip bool) bool {
		switch status.GetState() {
		case locationwire.OperationState_OPERATION_STATE_SUCCEEDED, locationwire.OperationState_OPERATION_STATE_NO_RESULT:
			return status.GetFailure() == nil
		case locationwire.OperationState_OPERATION_STATE_SKIPPED_KNOWN_PLACE:
			return allowKnownPlaceSkip && status.GetFailure() == nil
		default:
			return false
		}
	}
	providers := map[locationwire.LocationEvidenceProvider]bool{}
	for _, evidence := range briefing.GetProviderEvidence() {
		if evidence == nil || providers[evidence.GetProvider()] {
			return false
		}
		allowKnownPlaceSkip := evidence.GetProvider() == locationwire.LocationEvidenceProvider_LOCATION_EVIDENCE_PROVIDER_APPLE_NEARBY_PLACES ||
			evidence.GetProvider() == locationwire.LocationEvidenceProvider_LOCATION_EVIDENCE_PROVIDER_GEOAPIFY_PLACES
		if !reusable(evidence.GetTerminalStatus(), allowKnownPlaceSkip) {
			return false
		}
		providers[evidence.GetProvider()] = true
	}
	return providers[locationwire.LocationEvidenceProvider_LOCATION_EVIDENCE_PROVIDER_APPLE_REVERSE_GEOCODING] &&
		providers[locationwire.LocationEvidenceProvider_LOCATION_EVIDENCE_PROVIDER_APPLE_NEARBY_PLACES] &&
		providers[locationwire.LocationEvidenceProvider_LOCATION_EVIDENCE_PROVIDER_GEOAPIFY_REVERSE_GEOCODING] &&
		providers[locationwire.LocationEvidenceProvider_LOCATION_EVIDENCE_PROVIDER_GEOAPIFY_PLACES]
}

func StoreCurrentPhotoLocationEvidence(ctx context.Context, openedStore *store.Store, asset PhotoUpdateAsset, knownPlaceConfigurationSHA256 []byte, outcome *locationwire.ComposePhotoLocationEvidenceOutcome) error {
	if outcome == nil || outcome.Request == nil || outcome.Request.AssetId != string(asset.AssetID) || len(knownPlaceConfigurationSHA256) != sha256.Size {
		return errors.New("current photo location evidence is incomplete")
	}
	encoded, err := proto.Marshal(outcome)
	if err != nil {
		return err
	}
	_, err = openedStore.DB().ExecContext(ctx, `
insert into current_photo_location_evidence(asset_id, known_place_configuration_sha256, outcome_proto)
values (?, ?, ?)
on conflict(asset_id) do update set known_place_configuration_sha256=excluded.known_place_configuration_sha256, outcome_proto=excluded.outcome_proto`, asset.AssetID, knownPlaceConfigurationSHA256, encoded)
	return err
}
