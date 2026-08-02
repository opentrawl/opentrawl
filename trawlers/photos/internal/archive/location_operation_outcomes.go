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

type ProviderLocationOperation int

const (
	ProviderLocationOperationAppleReverseGeocoding                      ProviderLocationOperation = 1
	ProviderLocationOperationAppleNearbyPlace                           ProviderLocationOperation = 2
	ProviderLocationOperationGeoapifyPhotographedPlaceCandidateEvidence ProviderLocationOperation = 3
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

func LoadGeoapifyPhotographedPlaceCandidateEvidenceOutcome(ctx context.Context, openedStore *store.Store, assetID string) (*locationwire.AcquireGeoapifyPhotographedPlaceCandidateEvidenceOutcome, bool, error) {
	outcome := new(locationwire.AcquireGeoapifyPhotographedPlaceCandidateEvidenceOutcome)
	found, err := loadLocationOutcome(ctx, openedStore, "geoapify_photographed_place_candidate_evidence_outcome", assetID, outcome)
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
	if request == nil || request.Input == nil || request.Input.Coordinate == nil || strings.TrimSpace(request.Input.AssetId) == "" || len(request.KnownPlaceConfigurationSha256) != sha256.Size {
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
	return storeProviderLocationOutcome(ctx, openedStore, "apple_reverse_geocoding_evidence_outcome", ProviderLocationOperationAppleReverseGeocoding, assetID, outcome.GetRequest(), outcome.GetExchange(), encoded)
}

func StoreAppleNearbyPlaceEvidenceOutcome(ctx context.Context, openedStore *store.Store, outcome *locationwire.AcquireAppleNearbyPlaceEvidenceOutcome) error {
	if err := prepareLocationOutcomeStore(ctx, openedStore); err != nil {
		return err
	}
	assetID, encoded, err := marshalLocationOutcome(outcome.GetRequest().GetInput(), outcome)
	if err != nil {
		return err
	}
	return storeProviderLocationOutcome(ctx, openedStore, "apple_nearby_place_evidence_outcome", ProviderLocationOperationAppleNearbyPlace, assetID, outcome.GetRequest(), outcome.GetExchange(), encoded)
}

func StoreGeoapifyPhotographedPlaceCandidateEvidenceOutcome(ctx context.Context, openedStore *store.Store, outcome *locationwire.AcquireGeoapifyPhotographedPlaceCandidateEvidenceOutcome) error {
	if err := prepareLocationOutcomeStore(ctx, openedStore); err != nil {
		return err
	}
	assetID, encoded, err := marshalLocationOutcome(outcome.GetRequest().GetInput(), outcome)
	if err != nil {
		return err
	}
	return storeProviderLocationOutcome(ctx, openedStore, "geoapify_photographed_place_candidate_evidence_outcome", ProviderLocationOperationGeoapifyPhotographedPlaceCandidateEvidence, assetID, outcome.GetRequest(), outcome.GetExchange(), encoded)
}

func storeProviderLocationOutcome(ctx context.Context, openedStore *store.Store, tableName string, providerOperation ProviderLocationOperation, assetID string, request proto.Message, exchange *locationwire.ProviderExchange, encoded []byte) error {
	requestBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(request)
	if err != nil {
		return err
	}
	requestDigest := sha256.Sum256(requestBytes)
	return openedStore.WithTx(ctx, func(tx *sql.Tx) error {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		switch exchange.GetState() {
		case locationwire.OperationState_OPERATION_STATE_TRANSMISSION_STARTED:
			if _, err := tx.ExecContext(ctx, `insert into provider_location_transmission_attempt(asset_id, provider_operation, request_sha256, operation_state, transmission_started_at) values (?, ?, ?, ?, ?)`, assetID, providerOperation, requestDigest[:], exchange.GetState(), now); err != nil {
				return err
			}
		case locationwire.OperationState_OPERATION_STATE_RESPONSE_RETAINED:
			if _, err := tx.ExecContext(ctx, `update provider_location_transmission_attempt set operation_state=? where attempt_id=(select attempt_id from provider_location_transmission_attempt where asset_id=? and provider_operation=? and request_sha256=? and completed_at is null order by attempt_id desc limit 1)`, exchange.GetState(), assetID, providerOperation, requestDigest[:]); err != nil {
				return err
			}
		case locationwire.OperationState_OPERATION_STATE_SUCCEEDED, locationwire.OperationState_OPERATION_STATE_NO_RESULT, locationwire.OperationState_OPERATION_STATE_FAILED:
			if exchange.GetTransmissionStarted() {
				if _, err := tx.ExecContext(ctx, `update provider_location_transmission_attempt set operation_state=?, completed_at=? where attempt_id=(select attempt_id from provider_location_transmission_attempt where asset_id=? and provider_operation=? and request_sha256=? and completed_at is null order by attempt_id desc limit 1)`, exchange.GetState(), now, assetID, providerOperation, requestDigest[:]); err != nil {
					return err
				}
			}
		}
		if exchange.GetState() == locationwire.OperationState_OPERATION_STATE_FAILED {
			digest := sha256.Sum256(encoded)
			if _, err := tx.ExecContext(ctx, `insert or ignore into failed_location_operation_history(outcome_sha256, asset_id, provider_operation, outcome_proto, retained_at) values (?, ?, ?, ?, ?)`, digest[:], assetID, providerOperation, encoded, now); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, "insert into "+store.QuoteIdent(tableName)+"(asset_id, outcome_proto) values (?, ?) on conflict(asset_id) do update set outcome_proto=excluded.outcome_proto", assetID, encoded)
		return err
	})
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
