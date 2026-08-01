package archive

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	locationwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/location"
	"github.com/opentrawl/opentrawl/trawlkit/store"
	"google.golang.org/protobuf/proto"
)

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
	captureTime, err := time.Parse(time.RFC3339, request.Input.CaptureTime)
	if err != nil {
		return nil, fmt.Errorf("parse capture time: %w", err)
	}
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
		if !captureWithinKnownPlaceWindow(captureTime, validFromText, validUntilText) {
			continue
		}
		distanceMeters := metersBetweenCoordinates(request.Input.Coordinate.Latitude, request.Input.Coordinate.Longitude, latitude, longitude)
		if distanceMeters <= radiusMeters {
			outcome.Matches = append(outcome.Matches, &locationwire.ConfiguredKnownPlaceMatch{
				KnownPlaceId: id, LabelKind: labelKind, DisplayName: displayName, DistanceMeters: distanceMeters,
				ValidFrom: validFromText, ValidUntil: validUntilText,
			})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(outcome.Matches) > 0 {
		outcome.State = locationwire.OperationState_OPERATION_STATE_SUCCEEDED
	}
	outcome.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return outcome, nil
}

func captureWithinKnownPlaceWindow(captureTime time.Time, validFromText, validUntilText string) bool {
	if validFromText != "" {
		validFrom, err := time.Parse(time.RFC3339, validFromText)
		if err != nil || captureTime.Before(validFrom) {
			return false
		}
	}
	if validUntilText != "" {
		validUntil, err := time.Parse(time.RFC3339, validUntilText)
		if err != nil || captureTime.After(validUntil) {
			return false
		}
	}
	return true
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

func StoreGeoapifyBriefingProjectionOutcome(ctx context.Context, openedStore *store.Store, outcome *locationwire.ProjectGeoapifyEvidenceForBriefingOutcome) error {
	if err := prepareLocationOutcomeStore(ctx, openedStore); err != nil {
		return err
	}
	if outcome == nil || outcome.Request == nil || strings.TrimSpace(outcome.Request.AssetId) == "" {
		return errors.New("Geoapify briefing projection outcome is incomplete")
	}
	encoded, err := proto.Marshal(outcome)
	if err != nil {
		return err
	}
	_, err = openedStore.DB().ExecContext(ctx, `insert into geoapify_briefing_projection_outcome(asset_id, outcome_proto) values (?, ?) on conflict(asset_id) do update set outcome_proto=excluded.outcome_proto`, outcome.Request.AssetId, encoded)
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
