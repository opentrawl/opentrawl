package archive

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
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
	ProviderLocationOperationGeoapifyReverseGeocoding                   ProviderLocationOperation = 4
)

func CountGeoapifyProviderTransmissionAttemptsSince(ctx context.Context, openedStore *store.Store, since time.Time) (int, error) {
	if err := validateReadStore(ctx, openedStore); err != nil {
		return 0, err
	}
	var count int
	err := openedStore.DB().QueryRowContext(ctx, `
select count(*)
from location_provider_transmission_attempt
where provider_operation in (?, ?) and transmission_started_at>=?`, ProviderLocationOperationGeoapifyPhotographedPlaceCandidateEvidence, ProviderLocationOperationGeoapifyReverseGeocoding, since.UTC().Format(time.RFC3339Nano)).Scan(&count)
	return count, err
}

func KnownPlaceConfigurationSHA256(ctx context.Context, openedStore *store.Store) ([]byte, error) {
	configuration, err := ListConfiguredKnownPlaces(ctx, openedStore)
	if err != nil {
		return nil, err
	}
	configurationInStableIdentityOrder := proto.Clone(configuration).(*locationwire.KnownPlaceConfiguration)
	sort.Slice(configurationInStableIdentityOrder.Places, func(left, right int) bool {
		return configurationInStableIdentityOrder.Places[left].GetKnownPlaceId() < configurationInStableIdentityOrder.Places[right].GetKnownPlaceId()
	})
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(configurationInStableIdentityOrder)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(encoded)
	return digest[:], nil
}

func LoadMatchConfiguredKnownPlaceOutcome(ctx context.Context, openedStore *store.Store, assetID string) (*locationwire.MatchConfiguredKnownPlaceOutcome, bool, error) {
	outcome := new(locationwire.MatchConfiguredKnownPlaceOutcome)
	found, err := loadLocationOutcome(ctx, openedStore, "configured_known_place_match_outcome", assetID, outcome)
	return outcome, found, err
}

func LoadAppleReverseGeocodingEvidenceOutcome(ctx context.Context, openedStore *store.Store, assetID string) (*locationwire.AcquireAppleReverseGeocodingEvidenceOutcome, bool, error) {
	outcome := new(locationwire.AcquireAppleReverseGeocodingEvidenceOutcome)
	found, err := loadProviderLocationOutcomeForAsset(ctx, openedStore, ProviderLocationOperationAppleReverseGeocoding, assetID, outcome)
	return outcome, found, err
}

func LoadAppleNearbyPlaceEvidenceOutcome(ctx context.Context, openedStore *store.Store, assetID string) (*locationwire.AcquireAppleNearbyPlaceEvidenceOutcome, bool, error) {
	outcome := new(locationwire.AcquireAppleNearbyPlaceEvidenceOutcome)
	found, err := loadProviderLocationOutcomeForAsset(ctx, openedStore, ProviderLocationOperationAppleNearbyPlace, assetID, outcome)
	return outcome, found, err
}

func LoadGeoapifyPhotographedPlaceCandidateEvidenceOutcome(ctx context.Context, openedStore *store.Store, assetID string) (*locationwire.AcquireGeoapifyPhotographedPlaceCandidateEvidenceOutcome, bool, error) {
	outcome := new(locationwire.AcquireGeoapifyPhotographedPlaceCandidateEvidenceOutcome)
	found, err := loadProviderLocationOutcomeForAsset(ctx, openedStore, ProviderLocationOperationGeoapifyPhotographedPlaceCandidateEvidence, assetID, outcome)
	return outcome, found, err
}

func LoadGeoapifyReverseGeocodingEvidenceOutcome(ctx context.Context, openedStore *store.Store, assetID string) (*locationwire.AcquireGeoapifyReverseGeocodingEvidenceOutcome, bool, error) {
	outcome := new(locationwire.AcquireGeoapifyReverseGeocodingEvidenceOutcome)
	found, err := loadProviderLocationOutcomeForAsset(ctx, openedStore, ProviderLocationOperationGeoapifyReverseGeocoding, assetID, outcome)
	return outcome, found, err
}

func LoadAppleReverseGeocodingEvidenceOutcomeForRequest(ctx context.Context, openedStore *store.Store, request *locationwire.AcquireAppleReverseGeocodingEvidenceRequest) (*locationwire.AcquireAppleReverseGeocodingEvidenceOutcome, bool, error) {
	outcome := new(locationwire.AcquireAppleReverseGeocodingEvidenceOutcome)
	found, err := loadProviderLocationOutcomeForRequest(ctx, openedStore, ProviderLocationOperationAppleReverseGeocoding, request.GetProviderRequest(), outcome)
	if found {
		outcome.Request = request
		outcome.EvidenceUse = locationwire.ProviderEvidenceUse_PROVIDER_EVIDENCE_USE_REUSED
	}
	return outcome, found, err
}

func LoadAppleNearbyPlaceEvidenceOutcomeForRequest(ctx context.Context, openedStore *store.Store, request *locationwire.AcquireAppleNearbyPlaceEvidenceRequest) (*locationwire.AcquireAppleNearbyPlaceEvidenceOutcome, bool, error) {
	outcome := new(locationwire.AcquireAppleNearbyPlaceEvidenceOutcome)
	found, err := loadProviderLocationOutcomeForRequest(ctx, openedStore, ProviderLocationOperationAppleNearbyPlace, request.GetProviderRequest(), outcome)
	if found {
		outcome.Request = request
		outcome.EvidenceUse = locationwire.ProviderEvidenceUse_PROVIDER_EVIDENCE_USE_REUSED
	}
	return outcome, found, err
}

func LoadGeoapifyPhotographedPlaceCandidateEvidenceOutcomeForRequest(ctx context.Context, openedStore *store.Store, request *locationwire.AcquireGeoapifyPhotographedPlaceCandidateEvidenceRequest) (*locationwire.AcquireGeoapifyPhotographedPlaceCandidateEvidenceOutcome, bool, error) {
	outcome := new(locationwire.AcquireGeoapifyPhotographedPlaceCandidateEvidenceOutcome)
	found, err := loadProviderLocationOutcomeForRequest(ctx, openedStore, ProviderLocationOperationGeoapifyPhotographedPlaceCandidateEvidence, request.GetProviderRequest(), outcome)
	if found {
		outcome.Request = request
		outcome.EvidenceUse = locationwire.ProviderEvidenceUse_PROVIDER_EVIDENCE_USE_REUSED
	}
	return outcome, found, err
}

func LoadGeoapifyReverseGeocodingEvidenceOutcomeForRequest(ctx context.Context, openedStore *store.Store, request *locationwire.AcquireGeoapifyReverseGeocodingEvidenceRequest) (*locationwire.AcquireGeoapifyReverseGeocodingEvidenceOutcome, bool, error) {
	outcome := new(locationwire.AcquireGeoapifyReverseGeocodingEvidenceOutcome)
	found, err := loadProviderLocationOutcomeForRequest(ctx, openedStore, ProviderLocationOperationGeoapifyReverseGeocoding, request.GetProviderRequest(), outcome)
	if found {
		outcome.Request = request
		outcome.EvidenceUse = locationwire.ProviderEvidenceUse_PROVIDER_EVIDENCE_USE_REUSED
	}
	return outcome, found, err
}

func PhotoLocationProviderOperationRequestMatches(ctx context.Context, openedStore *store.Store, assetID string, providerOperation ProviderLocationOperation, expectedRequest proto.Message) (bool, error) {
	if err := validateReadStore(ctx, openedStore); err != nil {
		return false, err
	}
	if expectedRequest == nil || !expectedRequest.ProtoReflect().IsValid() {
		return false, errors.New("expected photo location provider request is missing")
	}
	var encodedRequest []byte
	err := openedStore.DB().QueryRowContext(ctx, `select operation_request_proto from photo_location_provider_operation where asset_id=? and provider_operation=?`, assetID, providerOperation).Scan(&encodedRequest)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	retainedRequest := expectedRequest.ProtoReflect().Type().New().Interface()
	if err := proto.Unmarshal(encodedRequest, retainedRequest); err != nil {
		return false, nil
	}
	return proto.Equal(retainedRequest, expectedRequest), nil
}

func loadProviderLocationOutcomeForAsset(ctx context.Context, openedStore *store.Store, providerOperation ProviderLocationOperation, assetID string, destination proto.Message) (bool, error) {
	if err := validateReadStore(ctx, openedStore); err != nil {
		return false, err
	}
	var encoded, skippedEncoded, operationRequestEncoded []byte
	err := openedStore.DB().QueryRowContext(ctx, `
select evidence.outcome_proto, photo.skipped_outcome_proto, photo.operation_request_proto
from photo_location_provider_operation as photo
left join location_provider_evidence as evidence
  on evidence.provider_operation=photo.provider_operation
 and evidence.provider_request_sha256=photo.provider_request_sha256
where photo.asset_id=? and photo.provider_operation=?`, assetID, providerOperation).Scan(&encoded, &skippedEncoded, &operationRequestEncoded)
	if len(skippedEncoded) > 0 {
		encoded = skippedEncoded
	}
	found, err := unmarshalOptionalLocationOutcome(encoded, err, destination)
	if err != nil || !found {
		return found, err
	}
	if err := setProviderLocationOutcomeOperationRequest(destination, operationRequestEncoded); err != nil {
		return false, err
	}
	markProviderLocationEvidenceReused(destination)
	return true, nil
}

func loadProviderLocationOutcomeForRequest(ctx context.Context, openedStore *store.Store, providerOperation ProviderLocationOperation, providerRequest proto.Message, destination proto.Message) (bool, error) {
	if err := validateReadStore(ctx, openedStore); err != nil {
		return false, err
	}
	_, requestSHA256, err := marshalProviderRequest(providerRequest)
	if err != nil {
		return false, err
	}
	var encoded []byte
	err = openedStore.DB().QueryRowContext(ctx, `select outcome_proto from location_provider_evidence where provider_operation=? and provider_request_sha256=?`, providerOperation, requestSHA256).Scan(&encoded)
	return unmarshalOptionalLocationOutcome(encoded, err, destination)
}

func unmarshalOptionalLocationOutcome(encoded []byte, queryError error, destination proto.Message) (bool, error) {
	if errors.Is(queryError, sql.ErrNoRows) {
		return false, nil
	}
	if queryError != nil {
		return false, queryError
	}
	if err := proto.Unmarshal(encoded, destination); err != nil {
		return false, err
	}
	return true, nil
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
	if !validConfiguredKnownPlaceCoordinate(request.Input.Coordinate) {
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
	configuration, err := ListConfiguredKnownPlaces(ctx, openedStore)
	if err != nil {
		return nil, err
	}
	for _, place := range configuration.GetPlaces() {
		matchesKnownPlace, relationshipAtCapture := captureRelationshipToKnownPlace(captureTime, place.GetValidFrom(), place.GetValidUntil())
		if !matchesKnownPlace {
			continue
		}
		distanceMeters := metersBetweenCoordinates(request.Input.Coordinate.Latitude, request.Input.Coordinate.Longitude, place.GetCoordinate().GetLatitude(), place.GetCoordinate().GetLongitude())
		if distanceMeters <= place.GetRadiusMeters() {
			outcome.Matches = append(outcome.Matches, &locationwire.ConfiguredKnownPlaceMatch{
				KnownPlaceId: place.GetKnownPlaceId(), Kind: place.GetKind(), DisplayName: place.GetDisplayName(), DistanceMeters: distanceMeters,
				ValidFrom: place.GetValidFrom(), ValidUntil: place.GetValidUntil(), RelationshipAtCapture: relationshipAtCapture,
			})
		}
	}
	if len(outcome.Matches) > 0 {
		outcome.State = locationwire.OperationState_OPERATION_STATE_SUCCEEDED
	}
	outcome.CompletedAt = timestamppb.Now()
	return outcome, nil
}

func configuredKnownPlaceKind(value string) (locationwire.ConfiguredKnownPlaceKind, error) {
	switch value {
	case "home":
		return locationwire.ConfiguredKnownPlaceKind_CONFIGURED_KNOWN_PLACE_KIND_HOME, nil
	case "former_home":
		return locationwire.ConfiguredKnownPlaceKind_CONFIGURED_KNOWN_PLACE_KIND_FORMER_HOME, nil
	case "work":
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

func captureRelationshipToKnownPlace(captureTime time.Time, validFrom, validUntil *timestamppb.Timestamp) (bool, locationwire.ConfiguredKnownPlaceRelationshipAtCapture) {
	if validFrom != nil {
		if !validFrom.IsValid() || captureTime.Before(validFrom.AsTime()) {
			return false, locationwire.ConfiguredKnownPlaceRelationshipAtCapture_CONFIGURED_KNOWN_PLACE_RELATIONSHIP_AT_CAPTURE_UNSPECIFIED
		}
	}
	if validUntil != nil {
		if !validUntil.IsValid() {
			return false, locationwire.ConfiguredKnownPlaceRelationshipAtCapture_CONFIGURED_KNOWN_PLACE_RELATIONSHIP_AT_CAPTURE_UNSPECIFIED
		}
		if captureTime.After(validUntil.AsTime()) {
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
	assetID, encoded, err := marshalProviderLocationOutcome(outcome.GetRequest().GetInput(), outcome)
	if err != nil {
		return err
	}
	return storeProviderLocationOutcome(ctx, openedStore, ProviderLocationOperationAppleReverseGeocoding, assetID, outcome.GetRequest(), outcome.GetRequest().GetProviderRequest(), outcome.GetExchange(), encoded)
}

func StoreAppleNearbyPlaceEvidenceOutcome(ctx context.Context, openedStore *store.Store, outcome *locationwire.AcquireAppleNearbyPlaceEvidenceOutcome) error {
	if err := prepareLocationOutcomeStore(ctx, openedStore); err != nil {
		return err
	}
	assetID, encoded, err := marshalProviderLocationOutcome(outcome.GetRequest().GetInput(), outcome)
	if err != nil {
		return err
	}
	return storeProviderLocationOutcome(ctx, openedStore, ProviderLocationOperationAppleNearbyPlace, assetID, outcome.GetRequest(), outcome.GetRequest().GetProviderRequest(), outcome.GetExchange(), encoded)
}

func StoreGeoapifyPhotographedPlaceCandidateEvidenceOutcome(ctx context.Context, openedStore *store.Store, outcome *locationwire.AcquireGeoapifyPhotographedPlaceCandidateEvidenceOutcome) error {
	if err := prepareLocationOutcomeStore(ctx, openedStore); err != nil {
		return err
	}
	assetID, encoded, err := marshalProviderLocationOutcome(outcome.GetRequest().GetInput(), outcome)
	if err != nil {
		return err
	}
	return storeProviderLocationOutcome(ctx, openedStore, ProviderLocationOperationGeoapifyPhotographedPlaceCandidateEvidence, assetID, outcome.GetRequest(), outcome.GetRequest().GetProviderRequest(), outcome.GetExchange(), encoded)
}

func StoreGeoapifyReverseGeocodingEvidenceOutcome(ctx context.Context, openedStore *store.Store, outcome *locationwire.AcquireGeoapifyReverseGeocodingEvidenceOutcome) error {
	if err := prepareLocationOutcomeStore(ctx, openedStore); err != nil {
		return err
	}
	assetID, encoded, err := marshalProviderLocationOutcome(outcome.GetRequest().GetInput(), outcome)
	if err != nil {
		return err
	}
	return storeProviderLocationOutcome(ctx, openedStore, ProviderLocationOperationGeoapifyReverseGeocoding, assetID, outcome.GetRequest(), outcome.GetRequest().GetProviderRequest(), outcome.GetExchange(), encoded)
}

func storeProviderLocationOutcome(ctx context.Context, openedStore *store.Store, providerOperation ProviderLocationOperation, assetID string, operationRequest, providerRequest proto.Message, exchange *locationwire.ProviderExchange, encoded []byte) error {
	providerRequestBytes, providerRequestSHA256, err := marshalProviderRequest(providerRequest)
	if err != nil {
		return err
	}
	operationRequestBytes, err := proto.Marshal(operationRequest)
	if err != nil {
		return err
	}
	return openedStore.WithTx(ctx, func(tx *sql.Tx) error {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if exchange.GetState() == locationwire.OperationState_OPERATION_STATE_SKIPPED_KNOWN_PLACE {
			_, err := tx.ExecContext(ctx, `
insert into photo_location_provider_operation(asset_id, provider_operation, provider_request_sha256, operation_request_proto, operation_state, skipped_outcome_proto)
values (?, ?, null, ?, ?, ?)
on conflict(asset_id, provider_operation) do update set
  provider_request_sha256=null,
  operation_request_proto=excluded.operation_request_proto,
  operation_state=excluded.operation_state,
  skipped_outcome_proto=excluded.skipped_outcome_proto`, assetID, providerOperation, operationRequestBytes, exchange.GetState(), encoded)
			return err
		}
		switch exchange.GetState() {
		case locationwire.OperationState_OPERATION_STATE_TRANSMISSION_STARTED:
			if _, err := tx.ExecContext(ctx, `insert into location_provider_transmission_attempt(provider_operation, provider_request_sha256, provider_request_proto, operation_state, transmission_started_at) values (?, ?, ?, ?, ?)`, providerOperation, providerRequestSHA256, providerRequestBytes, exchange.GetState(), now); err != nil {
				return err
			}
		case locationwire.OperationState_OPERATION_STATE_RESPONSE_RETAINED:
			if _, err := tx.ExecContext(ctx, `update location_provider_transmission_attempt set operation_state=?, response_retained_at=? where attempt_id=(select attempt_id from location_provider_transmission_attempt where provider_operation=? and provider_request_sha256=? and completed_at is null order by attempt_id desc limit 1)`, exchange.GetState(), now, providerOperation, providerRequestSHA256); err != nil {
				return err
			}
		case locationwire.OperationState_OPERATION_STATE_SUCCEEDED, locationwire.OperationState_OPERATION_STATE_NO_RESULT, locationwire.OperationState_OPERATION_STATE_FAILED:
			if exchange.GetTransmissionStarted() {
				if _, err := tx.ExecContext(ctx, `update location_provider_transmission_attempt set operation_state=?, completed_at=? where attempt_id=(select attempt_id from location_provider_transmission_attempt where provider_operation=? and provider_request_sha256=? and completed_at is null order by attempt_id desc limit 1)`, exchange.GetState(), now, providerOperation, providerRequestSHA256); err != nil {
					return err
				}
			}
		}
		if exchange.GetState() == locationwire.OperationState_OPERATION_STATE_REQUEST_RETAINED || exchange.GetState() == locationwire.OperationState_OPERATION_STATE_TRANSMISSION_STARTED {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `
insert into location_provider_evidence(provider_operation, provider_request_sha256, provider_request_proto, operation_state, outcome_proto)
values (?, ?, ?, ?, ?)
on conflict(provider_operation, provider_request_sha256) do update set
  provider_request_proto=excluded.provider_request_proto,
  operation_state=excluded.operation_state,
  outcome_proto=excluded.outcome_proto
where location_provider_evidence.operation_state not in (?, ?)
   or excluded.operation_state in (?, ?)`,
			providerOperation, providerRequestSHA256, providerRequestBytes, exchange.GetState(), encoded,
			locationwire.OperationState_OPERATION_STATE_SUCCEEDED, locationwire.OperationState_OPERATION_STATE_NO_RESULT,
			locationwire.OperationState_OPERATION_STATE_SUCCEEDED, locationwire.OperationState_OPERATION_STATE_NO_RESULT); err != nil {
			return err
		}
		var retainedOperationState locationwire.OperationState
		if err := tx.QueryRowContext(ctx, `select operation_state from location_provider_evidence where provider_operation=? and provider_request_sha256=?`, providerOperation, providerRequestSHA256).Scan(&retainedOperationState); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `
insert into photo_location_provider_operation(asset_id, provider_operation, provider_request_sha256, operation_request_proto, operation_state, skipped_outcome_proto)
values (?, ?, ?, ?, ?, null)
on conflict(asset_id, provider_operation) do update set
  provider_request_sha256=excluded.provider_request_sha256,
  operation_request_proto=excluded.operation_request_proto,
  operation_state=excluded.operation_state,
  skipped_outcome_proto=null`, assetID, providerOperation, providerRequestSHA256, operationRequestBytes, retainedOperationState)
		return err
	})
}

func marshalProviderRequest(providerRequest proto.Message) ([]byte, []byte, error) {
	if providerRequest == nil || !providerRequest.ProtoReflect().IsValid() {
		return nil, nil, errors.New("location provider request is missing")
	}
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(providerRequest)
	if err != nil {
		return nil, nil, err
	}
	digest := sha256.Sum256(encoded)
	return encoded, digest[:], nil
}

func marshalProviderLocationOutcome(input *locationwire.CaptureLocationInput, outcome proto.Message) (string, []byte, error) {
	if input == nil || strings.TrimSpace(input.AssetId) == "" || outcome == nil {
		return "", nil, errors.New("location provider outcome is incomplete")
	}
	storedOutcome := proto.Clone(outcome)
	switch typedOutcome := storedOutcome.(type) {
	case *locationwire.AcquireAppleReverseGeocodingEvidenceOutcome:
		typedOutcome.Request = nil
		if typedOutcome.GetExchange().GetState() != locationwire.OperationState_OPERATION_STATE_SKIPPED_KNOWN_PLACE {
			typedOutcome.EvidenceUse = locationwire.ProviderEvidenceUse_PROVIDER_EVIDENCE_USE_ACQUIRED
		}
	case *locationwire.AcquireAppleNearbyPlaceEvidenceOutcome:
		typedOutcome.Request = nil
		if typedOutcome.GetExchange().GetState() != locationwire.OperationState_OPERATION_STATE_SKIPPED_KNOWN_PLACE {
			typedOutcome.EvidenceUse = locationwire.ProviderEvidenceUse_PROVIDER_EVIDENCE_USE_ACQUIRED
		}
	case *locationwire.AcquireGeoapifyPhotographedPlaceCandidateEvidenceOutcome:
		typedOutcome.Request = nil
		if typedOutcome.GetExchange().GetState() != locationwire.OperationState_OPERATION_STATE_SKIPPED_KNOWN_PLACE {
			typedOutcome.EvidenceUse = locationwire.ProviderEvidenceUse_PROVIDER_EVIDENCE_USE_ACQUIRED
		}
	case *locationwire.AcquireGeoapifyReverseGeocodingEvidenceOutcome:
		typedOutcome.Request = nil
		if typedOutcome.GetExchange().GetState() != locationwire.OperationState_OPERATION_STATE_SKIPPED_KNOWN_PLACE {
			typedOutcome.EvidenceUse = locationwire.ProviderEvidenceUse_PROVIDER_EVIDENCE_USE_ACQUIRED
		}
	default:
		return "", nil, errors.New("location provider outcome type is unsupported")
	}
	encoded, err := proto.Marshal(storedOutcome)
	return input.AssetId, encoded, err
}

func setProviderLocationOutcomeOperationRequest(outcome proto.Message, encodedRequest []byte) error {
	switch typedOutcome := outcome.(type) {
	case *locationwire.AcquireAppleReverseGeocodingEvidenceOutcome:
		typedOutcome.Request = new(locationwire.AcquireAppleReverseGeocodingEvidenceRequest)
		return proto.Unmarshal(encodedRequest, typedOutcome.Request)
	case *locationwire.AcquireAppleNearbyPlaceEvidenceOutcome:
		typedOutcome.Request = new(locationwire.AcquireAppleNearbyPlaceEvidenceRequest)
		return proto.Unmarshal(encodedRequest, typedOutcome.Request)
	case *locationwire.AcquireGeoapifyPhotographedPlaceCandidateEvidenceOutcome:
		typedOutcome.Request = new(locationwire.AcquireGeoapifyPhotographedPlaceCandidateEvidenceRequest)
		return proto.Unmarshal(encodedRequest, typedOutcome.Request)
	case *locationwire.AcquireGeoapifyReverseGeocodingEvidenceOutcome:
		typedOutcome.Request = new(locationwire.AcquireGeoapifyReverseGeocodingEvidenceRequest)
		return proto.Unmarshal(encodedRequest, typedOutcome.Request)
	default:
		return errors.New("location provider outcome type is unsupported")
	}
}

func markProviderLocationEvidenceReused(outcome proto.Message) {
	switch typedOutcome := outcome.(type) {
	case *locationwire.AcquireAppleReverseGeocodingEvidenceOutcome:
		if typedOutcome.GetExchange().GetState() != locationwire.OperationState_OPERATION_STATE_SKIPPED_KNOWN_PLACE {
			typedOutcome.EvidenceUse = locationwire.ProviderEvidenceUse_PROVIDER_EVIDENCE_USE_REUSED
		}
	case *locationwire.AcquireAppleNearbyPlaceEvidenceOutcome:
		if typedOutcome.GetExchange().GetState() != locationwire.OperationState_OPERATION_STATE_SKIPPED_KNOWN_PLACE {
			typedOutcome.EvidenceUse = locationwire.ProviderEvidenceUse_PROVIDER_EVIDENCE_USE_REUSED
		}
	case *locationwire.AcquireGeoapifyPhotographedPlaceCandidateEvidenceOutcome:
		if typedOutcome.GetExchange().GetState() != locationwire.OperationState_OPERATION_STATE_SKIPPED_KNOWN_PLACE {
			typedOutcome.EvidenceUse = locationwire.ProviderEvidenceUse_PROVIDER_EVIDENCE_USE_REUSED
		}
	case *locationwire.AcquireGeoapifyReverseGeocodingEvidenceOutcome:
		if typedOutcome.GetExchange().GetState() != locationwire.OperationState_OPERATION_STATE_SKIPPED_KNOWN_PLACE {
			typedOutcome.EvidenceUse = locationwire.ProviderEvidenceUse_PROVIDER_EVIDENCE_USE_REUSED
		}
	}
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
