package archive

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	locationwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/location"
	"github.com/opentrawl/opentrawl/trawlkit/store"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func KnownPlaceConfigurationSHA256(ctx context.Context, openedStore *store.Store) ([]byte, error) {
	if err := validateReadStore(ctx, openedStore); err != nil {
		return nil, err
	}
	rows, err := openedStore.DB().QueryContext(ctx, `select id, label_kind, display_name, latitude, longitude, radius_meters, valid_from, valid_until from known_place order by id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	digest := sha256.New()
	for rows.Next() {
		var id, labelKind, displayName, validFrom, validUntil string
		var latitude, longitude, radiusMetres float64
		if err := rows.Scan(&id, &labelKind, &displayName, &latitude, &longitude, &radiusMetres, &validFrom, &validUntil); err != nil {
			return nil, err
		}
		_, _ = fmt.Fprintf(digest, "%q\x00%q\x00%q\x00%.17g\x00%.17g\x00%.17g\x00%q\x00%q\n", id, labelKind, displayName, latitude, longitude, radiusMetres, validFrom, validUntil)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return digest.Sum(nil), nil
}

func LoadMatchConfiguredKnownPlaceOutcome(ctx context.Context, openedStore *store.Store, assetID string) (*locationwire.MatchConfiguredKnownPlaceOutcome, bool, error) {
	outcome := new(locationwire.MatchConfiguredKnownPlaceOutcome)
	found, err := loadLocationOutcome(ctx, openedStore, "configured_known_place_match_outcome", assetID, outcome)
	return outcome, found, err
}

func LoadAppleReverseGeocodingEvidenceOutcome(ctx context.Context, openedStore *store.Store, assetID string) (*locationwire.AcquireAppleReverseGeocodingEvidenceOutcome, bool, error) {
	outcome := new(locationwire.AcquireAppleReverseGeocodingEvidenceOutcome)
	found, err := loadLocationOutcome(ctx, openedStore, "apple_reverse_geocoding_evidence_outcome", assetID, outcome)
	return outcome, found, err
}

func LoadAppleNearbyPlaceEvidenceOutcome(ctx context.Context, openedStore *store.Store, assetID string) (*locationwire.AcquireAppleNearbyPlaceEvidenceOutcome, bool, error) {
	outcome := new(locationwire.AcquireAppleNearbyPlaceEvidenceOutcome)
	found, err := loadLocationOutcome(ctx, openedStore, "apple_nearby_place_evidence_outcome", assetID, outcome)
	return outcome, found, err
}

func LoadGeoapifyReverseGeocodingEvidenceOutcome(ctx context.Context, openedStore *store.Store, assetID string) (*locationwire.AcquireGeoapifyReverseGeocodingEvidenceOutcome, bool, error) {
	outcome := new(locationwire.AcquireGeoapifyReverseGeocodingEvidenceOutcome)
	found, err := loadLocationOutcome(ctx, openedStore, "geoapify_reverse_geocoding_evidence_outcome", assetID, outcome)
	return outcome, found, err
}

func LoadGeoapifyNearbyPlaceEvidenceOutcome(ctx context.Context, openedStore *store.Store, assetID string) (*locationwire.AcquireGeoapifyNearbyPlaceEvidenceOutcome, bool, error) {
	outcome := new(locationwire.AcquireGeoapifyNearbyPlaceEvidenceOutcome)
	found, err := loadLocationOutcome(ctx, openedStore, "geoapify_nearby_place_evidence_outcome", assetID, outcome)
	return outcome, found, err
}

func loadLocationOutcome(ctx context.Context, openedStore *store.Store, tableName, assetID string, destination proto.Message) (bool, error) {
	if err := validateReadStore(ctx, openedStore); err != nil {
		return false, err
	}
	var encoded []byte
	err := openedStore.DB().QueryRowContext(ctx, "select outcome_proto from "+tableName+" where asset_id=?", assetID).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := proto.Unmarshal(encoded, destination); err != nil {
		return false, err
	}
	return true, nil
}

func MatchConfiguredKnownPlace(ctx context.Context, openedStore *store.Store, request *locationwire.MatchConfiguredKnownPlaceRequest) (*locationwire.MatchConfiguredKnownPlaceOutcome, error) {
	if err := validateReadStore(ctx, openedStore); err != nil {
		return nil, err
	}
	if request == nil || request.Input == nil || request.Input.Coordinate == nil || strings.TrimSpace(request.Input.AssetId) == "" {
		return nil, errors.New("configured known-place request is incomplete")
	}
	if !finiteCoordinate(request.Input.Coordinate.Latitude, request.Input.Coordinate.Longitude) {
		return nil, errors.New("configured known-place coordinate is invalid")
	}
	if request.Input.CaptureTime == nil {
		return nil, errors.New("configured known-place capture time is missing")
	}
	if err := request.Input.CaptureTime.CheckValid(); err != nil {
		return nil, fmt.Errorf("validate capture time: %w", err)
	}
	captureTime := request.Input.CaptureTime.AsTime()
	outcome := &locationwire.MatchConfiguredKnownPlaceOutcome{Request: request, State: locationwire.OperationState_OPERATION_STATE_NO_RESULT}
	rows, err := openedStore.DB().QueryContext(ctx, `
select id, label_kind, display_name, latitude, longitude, radius_meters, valid_from, valid_until
from known_place
order by label_kind, display_name`)
	if err != nil {
		return nil, fmt.Errorf("load configured known places: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, labelKind, displayName, validFromText, validUntilText string
		var latitude, longitude, radiusMeters float64
		if err := rows.Scan(&id, &labelKind, &displayName, &latitude, &longitude, &radiusMeters, &validFromText, &validUntilText); err != nil {
			return nil, err
		}
		matchesKnownPlace, relationshipAtCapture := captureRelationshipToKnownPlace(captureTime, validFromText, validUntilText)
		if !matchesKnownPlace {
			continue
		}
		distanceMeters := metersBetweenCoordinates(request.Input.Coordinate.Latitude, request.Input.Coordinate.Longitude, latitude, longitude)
		if distanceMeters <= radiusMeters {
			knownPlaceKind, err := configuredKnownPlaceKind(labelKind)
			if err != nil {
				return nil, err
			}
			validFrom, err := optionalLocationTimestamp(validFromText)
			if err != nil {
				return nil, fmt.Errorf("parse known place valid-from time: %w", err)
			}
			validUntil, err := optionalLocationTimestamp(validUntilText)
			if err != nil {
				return nil, fmt.Errorf("parse known place valid-until time: %w", err)
			}
			outcome.Matches = append(outcome.Matches, &locationwire.ConfiguredKnownPlaceMatch{
				KnownPlaceId: id, Kind: knownPlaceKind, DisplayName: displayName, DistanceMeters: distanceMeters,
				ValidFrom: validFrom, ValidUntil: validUntil, RelationshipAtCapture: relationshipAtCapture,
			})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(outcome.Matches) > 0 {
		outcome.State = locationwire.OperationState_OPERATION_STATE_SUCCEEDED
	}
	outcome.CompletedAt = timestamppb.Now()
	return outcome, nil
}

func configuredKnownPlaceKind(value string) (locationwire.ConfiguredKnownPlaceKind, error) {
	switch value {
	case KnownPlaceKindHome:
		return locationwire.ConfiguredKnownPlaceKind_CONFIGURED_KNOWN_PLACE_KIND_HOME, nil
	case KnownPlaceKindFormerHome:
		return locationwire.ConfiguredKnownPlaceKind_CONFIGURED_KNOWN_PLACE_KIND_FORMER_HOME, nil
	case KnownPlaceKindWork:
		return locationwire.ConfiguredKnownPlaceKind_CONFIGURED_KNOWN_PLACE_KIND_WORK, nil
	default:
		return locationwire.ConfiguredKnownPlaceKind_CONFIGURED_KNOWN_PLACE_KIND_UNSPECIFIED, fmt.Errorf("unknown configured known-place kind %q", value)
	}
}

func optionalLocationTimestamp(value string) (*timestamppb.Timestamp, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, err
	}
	return timestamppb.New(parsed), nil
}

func captureRelationshipToKnownPlace(captureTime time.Time, validFromText, validUntilText string) (bool, locationwire.ConfiguredKnownPlaceRelationshipAtCapture) {
	if validFromText != "" {
		validFrom, err := time.Parse(time.RFC3339, validFromText)
		if err != nil || captureTime.Before(validFrom) {
			return false, locationwire.ConfiguredKnownPlaceRelationshipAtCapture_CONFIGURED_KNOWN_PLACE_RELATIONSHIP_AT_CAPTURE_UNSPECIFIED
		}
	}
	if validUntilText != "" {
		validUntil, err := time.Parse(time.RFC3339, validUntilText)
		if err != nil {
			return false, locationwire.ConfiguredKnownPlaceRelationshipAtCapture_CONFIGURED_KNOWN_PLACE_RELATIONSHIP_AT_CAPTURE_UNSPECIFIED
		}
		if captureTime.After(validUntil) {
			return true, locationwire.ConfiguredKnownPlaceRelationshipAtCapture_CONFIGURED_KNOWN_PLACE_RELATIONSHIP_AT_CAPTURE_VISITED_AFTER_KNOWN_PERIOD
		}
	}
	return true, locationwire.ConfiguredKnownPlaceRelationshipAtCapture_CONFIGURED_KNOWN_PLACE_RELATIONSHIP_AT_CAPTURE_ACTIVE_DURING_KNOWN_PERIOD
}

func StoreMatchConfiguredKnownPlaceOutcome(ctx context.Context, openedStore *store.Store, outcome *locationwire.MatchConfiguredKnownPlaceOutcome) error {
	if err := prepareLocationOutcomeStore(ctx, openedStore); err != nil {
		return err
	}
	assetID, encoded, err := marshalLocationOutcome(outcome.GetRequest().GetInput(), outcome)
	if err != nil {
		return err
	}
	_, err = openedStore.DB().ExecContext(ctx, `insert into configured_known_place_match_outcome(asset_id, outcome_proto) values (?, ?) on conflict(asset_id) do update set outcome_proto=excluded.outcome_proto`, assetID, encoded)
	return err
}

func StoreAppleReverseGeocodingEvidenceOutcome(ctx context.Context, openedStore *store.Store, outcome *locationwire.AcquireAppleReverseGeocodingEvidenceOutcome) error {
	if err := prepareLocationOutcomeStore(ctx, openedStore); err != nil {
		return err
	}
	assetID, encoded, err := marshalLocationOutcome(outcome.GetRequest().GetInput(), outcome)
	if err != nil {
		return err
	}
	_, err = openedStore.DB().ExecContext(ctx, `insert into apple_reverse_geocoding_evidence_outcome(asset_id, outcome_proto) values (?, ?) on conflict(asset_id) do update set outcome_proto=excluded.outcome_proto`, assetID, encoded)
	return err
}

func StoreAppleNearbyPlaceEvidenceOutcome(ctx context.Context, openedStore *store.Store, outcome *locationwire.AcquireAppleNearbyPlaceEvidenceOutcome) error {
	if err := prepareLocationOutcomeStore(ctx, openedStore); err != nil {
		return err
	}
	assetID, encoded, err := marshalLocationOutcome(outcome.GetRequest().GetInput(), outcome)
	if err != nil {
		return err
	}
	_, err = openedStore.DB().ExecContext(ctx, `insert into apple_nearby_place_evidence_outcome(asset_id, outcome_proto) values (?, ?) on conflict(asset_id) do update set outcome_proto=excluded.outcome_proto`, assetID, encoded)
	return err
}

func StoreGeoapifyReverseGeocodingEvidenceOutcome(ctx context.Context, openedStore *store.Store, outcome *locationwire.AcquireGeoapifyReverseGeocodingEvidenceOutcome) error {
	if err := prepareLocationOutcomeStore(ctx, openedStore); err != nil {
		return err
	}
	assetID, encoded, err := marshalLocationOutcome(outcome.GetRequest().GetInput(), outcome)
	if err != nil {
		return err
	}
	_, err = openedStore.DB().ExecContext(ctx, `insert into geoapify_reverse_geocoding_evidence_outcome(asset_id, outcome_proto) values (?, ?) on conflict(asset_id) do update set outcome_proto=excluded.outcome_proto`, assetID, encoded)
	return err
}

func StoreGeoapifyNearbyPlaceEvidenceOutcome(ctx context.Context, openedStore *store.Store, outcome *locationwire.AcquireGeoapifyNearbyPlaceEvidenceOutcome) error {
	if err := prepareLocationOutcomeStore(ctx, openedStore); err != nil {
		return err
	}
	assetID, encoded, err := marshalLocationOutcome(outcome.GetRequest().GetInput(), outcome)
	if err != nil {
		return err
	}
	_, err = openedStore.DB().ExecContext(ctx, `insert into geoapify_nearby_place_evidence_outcome(asset_id, outcome_proto) values (?, ?) on conflict(asset_id) do update set outcome_proto=excluded.outcome_proto`, assetID, encoded)
	return err
}

func StoreComposedPhotoLocationEvidenceOutcome(ctx context.Context, openedStore *store.Store, outcome *locationwire.ComposePhotoLocationEvidenceOutcome) error {
	if err := prepareLocationOutcomeStore(ctx, openedStore); err != nil {
		return err
	}
	if outcome == nil || outcome.Request == nil || strings.TrimSpace(outcome.Request.AssetId) == "" {
		return errors.New("composed photo location evidence outcome is incomplete")
	}
	encoded, err := proto.Marshal(outcome)
	if err != nil {
		return err
	}
	_, err = openedStore.DB().ExecContext(ctx, `insert into composed_photo_location_evidence_outcome(asset_id, outcome_proto) values (?, ?) on conflict(asset_id) do update set outcome_proto=excluded.outcome_proto`, outcome.Request.AssetId, encoded)
	return err
}

func prepareLocationOutcomeStore(ctx context.Context, openedStore *store.Store) error {
	if err := validateReadStore(ctx, openedStore); err != nil {
		return err
	}
	return prepareStore(ctx, openedStore)
}

func marshalLocationOutcome(input *locationwire.CaptureLocationInput, outcome proto.Message) (string, []byte, error) {
	if input == nil || strings.TrimSpace(input.AssetId) == "" || outcome == nil {
		return "", nil, errors.New("location operation outcome is incomplete")
	}
	encoded, err := proto.Marshal(outcome)
	return input.AssetId, encoded, err
}

func metersBetweenCoordinates(fromLatitude, fromLongitude, toLatitude, toLongitude float64) float64 {
	const earthRadiusMeters = 6371000
	fromLatRadians := fromLatitude * 3.141592653589793 / 180
	toLatRadians := toLatitude * 3.141592653589793 / 180
	deltaLat := (toLatitude - fromLatitude) * 3.141592653589793 / 180
	deltaLon := (toLongitude - fromLongitude) * 3.141592653589793 / 180
	a := sinSquared(deltaLat/2) + math.Cos(fromLatRadians)*math.Cos(toLatRadians)*sinSquared(deltaLon/2)
	return earthRadiusMeters * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

func sinSquared(value float64) float64 { sine := math.Sin(value); return sine * sine }
