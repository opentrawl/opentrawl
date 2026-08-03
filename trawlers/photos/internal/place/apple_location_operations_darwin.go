//go:build darwin

package place

/*
#cgo darwin LDFLAGS: -framework Foundation -framework CoreLocation -framework MapKit
#include <stdlib.h>

char *photoscrawl_apple_reverse_geocoding_json(const char *requestJSON, char **errorOut);
char *photoscrawl_apple_nearby_places_json(const char *requestJSON, char **errorOut);
int photoscrawl_current_thread_is_main(void);
*/
import "C"

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"unsafe"

	locationwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/location"
)

func init() {
	runtime.LockOSThread()
}

func AcquireAppleReverseGeocodingEvidence(ctx context.Context, request *locationwire.AcquireAppleReverseGeocodingEvidenceRequest, retain RetainAppleReverseGeocodingStage) (*locationwire.AcquireAppleReverseGeocodingEvidenceOutcome, error) {
	if request == nil || validateCaptureLocationInput(request.GetInput()) != nil || validateProviderCoordinate(request.GetProviderRequest().GetCoordinate()) != nil ||
		!providerCoordinateMatchesCaptureLocation(request.GetProviderRequest().GetCoordinate(), request.GetInput()) {
		return nil, errors.New("Apple reverse-geocoding request is incomplete")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	outcome := &locationwire.AcquireAppleReverseGeocodingEvidenceOutcome{
		Request: request, Exchange: &locationwire.ProviderExchange{State: locationwire.OperationState_OPERATION_STATE_REQUEST_RETAINED},
		Provider:    locationwire.LocationEvidenceProvider_LOCATION_EVIDENCE_PROVIDER_APPLE_CORE_LOCATION,
		EvidenceUse: locationwire.ProviderEvidenceUse_PROVIDER_EVIDENCE_USE_ACQUIRED,
	}
	if err := retainAppleReverseGeocodingStage(retain, outcome); err != nil {
		return nil, err
	}
	outcome.Exchange = &locationwire.ProviderExchange{State: locationwire.OperationState_OPERATION_STATE_TRANSMISSION_STARTED, TransmissionStarted: true}
	if err := retainAppleReverseGeocodingStage(retain, outcome); err != nil {
		return nil, err
	}
	rawResponse, bridgeFailure, err := callAppleReverseGeocoding(ctx, request.GetProviderRequest().GetCoordinate())
	if err != nil {
		return nil, err
	}
	if bridgeFailure != "" {
		outcome.Exchange = failedExchange(locationwire.OperationFailureClass_OPERATION_FAILURE_CLASS_APPLE_PROVIDER, bridgeFailure, true)
		outcome.ObservedAt = completedAt()
		outcome.CompletedAt = outcome.ObservedAt
		return outcome, retainAppleReverseGeocodingStage(retain, outcome)
	}
	outcome.Exchange = &locationwire.ProviderExchange{State: locationwire.OperationState_OPERATION_STATE_RESPONSE_RETAINED, TransmissionStarted: true, ExactResponse: rawResponse}
	outcome.ObservedAt = completedAt()
	if err := retainAppleReverseGeocodingStage(retain, outcome); err != nil {
		return nil, err
	}
	if len(rawResponse) > maxRawEvidenceBytes {
		outcome.Exchange.State = locationwire.OperationState_OPERATION_STATE_FAILED
		outcome.Exchange.Failure = &locationwire.OperationFailure{Class: locationwire.OperationFailureClass_OPERATION_FAILURE_CLASS_RESPONSE_TOO_LARGE, Detail: "Apple reverse-geocoding response exceeded the retained evidence limit"}
		outcome.CompletedAt = completedAt()
		return outcome, retainAppleReverseGeocodingStage(retain, outcome)
	}
	completeAppleReverseGeocodingEvidence(outcome)
	return outcome, retainAppleReverseGeocodingStage(retain, outcome)
}

func ResumeAppleReverseGeocodingEvidence(outcome *locationwire.AcquireAppleReverseGeocodingEvidenceOutcome, retain RetainAppleReverseGeocodingStage) (*locationwire.AcquireAppleReverseGeocodingEvidenceOutcome, error) {
	if outcome == nil || outcome.GetExchange().GetState() != locationwire.OperationState_OPERATION_STATE_RESPONSE_RETAINED || len(outcome.GetExchange().GetExactResponse()) == 0 {
		return nil, errors.New("Apple reverse-geocoding response is not retained")
	}
	completeAppleReverseGeocodingEvidence(outcome)
	return outcome, retainAppleReverseGeocodingStage(retain, outcome)
}

func completeAppleReverseGeocodingEvidence(outcome *locationwire.AcquireAppleReverseGeocodingEvidenceOutcome) {
	var response struct {
		Address *Address `json:"address"`
	}
	if err := json.Unmarshal(outcome.GetExchange().GetExactResponse(), &response); err != nil {
		outcome.Exchange.State = locationwire.OperationState_OPERATION_STATE_FAILED
		outcome.Exchange.Failure = &locationwire.OperationFailure{Class: locationwire.OperationFailureClass_OPERATION_FAILURE_CLASS_DECODE_RESPONSE, Detail: err.Error()}
	} else if response.Address == nil {
		outcome.Exchange.State = locationwire.OperationState_OPERATION_STATE_NO_RESULT
	} else {
		outcome.Address = addressHierarchy(response.Address)
		outcome.Exchange.State = locationwire.OperationState_OPERATION_STATE_SUCCEEDED
	}
	outcome.CompletedAt = completedAt()
}

func AcquireAppleNearbyPlaceEvidence(ctx context.Context, request *locationwire.AcquireAppleNearbyPlaceEvidenceRequest, retain RetainAppleNearbyPlaceStage) (*locationwire.AcquireAppleNearbyPlaceEvidenceOutcome, error) {
	providerRequest := request.GetProviderRequest()
	if request == nil || validateCaptureLocationInput(request.GetInput()) != nil || validateProviderCoordinate(providerRequest.GetCoordinate()) != nil ||
		!providerCoordinateMatchesCaptureLocation(providerRequest.GetCoordinate(), request.GetInput()) {
		return nil, errors.New("Apple nearby-place request is incomplete")
	}
	if providerRequest.GetMaximumCandidates() <= 0 || providerRequest.GetMaximumCandidates() > MaximumNearbyPlaceCandidates || providerRequest.GetRadiusMeters() <= 0 {
		return nil, errors.New("Apple nearby-place candidate limit is invalid")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	outcome := &locationwire.AcquireAppleNearbyPlaceEvidenceOutcome{
		Request: request, Exchange: &locationwire.ProviderExchange{State: locationwire.OperationState_OPERATION_STATE_REQUEST_RETAINED},
		Provider:    locationwire.LocationEvidenceProvider_LOCATION_EVIDENCE_PROVIDER_APPLE_MAP_KIT,
		EvidenceUse: locationwire.ProviderEvidenceUse_PROVIDER_EVIDENCE_USE_ACQUIRED,
	}
	if err := retainAppleNearbyPlaceStage(retain, outcome); err != nil {
		return nil, err
	}
	outcome.Exchange = &locationwire.ProviderExchange{State: locationwire.OperationState_OPERATION_STATE_TRANSMISSION_STARTED, TransmissionStarted: true}
	if err := retainAppleNearbyPlaceStage(retain, outcome); err != nil {
		return nil, err
	}
	rawResponse, bridgeFailure, err := callAppleNearbyPlaces(ctx, providerRequest.GetCoordinate(), providerRequest.GetRadiusMeters(), providerRequest.GetMaximumCandidates())
	if err != nil {
		return nil, err
	}
	if bridgeFailure != "" {
		outcome.Exchange = failedExchange(locationwire.OperationFailureClass_OPERATION_FAILURE_CLASS_APPLE_PROVIDER, bridgeFailure, true)
		outcome.ObservedAt = completedAt()
		outcome.CompletedAt = outcome.ObservedAt
		return outcome, retainAppleNearbyPlaceStage(retain, outcome)
	}
	outcome.Exchange = &locationwire.ProviderExchange{State: locationwire.OperationState_OPERATION_STATE_RESPONSE_RETAINED, TransmissionStarted: true, ExactResponse: rawResponse}
	outcome.ObservedAt = completedAt()
	if err := retainAppleNearbyPlaceStage(retain, outcome); err != nil {
		return nil, err
	}
	if len(rawResponse) > maxRawEvidenceBytes {
		outcome.Exchange.State = locationwire.OperationState_OPERATION_STATE_FAILED
		outcome.Exchange.Failure = &locationwire.OperationFailure{Class: locationwire.OperationFailureClass_OPERATION_FAILURE_CLASS_RESPONSE_TOO_LARGE, Detail: "Apple nearby-place response exceeded the retained evidence limit"}
		outcome.CompletedAt = completedAt()
		return outcome, retainAppleNearbyPlaceStage(retain, outcome)
	}
	completeAppleNearbyPlaceEvidence(outcome)
	return outcome, retainAppleNearbyPlaceStage(retain, outcome)
}

func ResumeAppleNearbyPlaceEvidence(outcome *locationwire.AcquireAppleNearbyPlaceEvidenceOutcome, retain RetainAppleNearbyPlaceStage) (*locationwire.AcquireAppleNearbyPlaceEvidenceOutcome, error) {
	if outcome == nil || outcome.GetExchange().GetState() != locationwire.OperationState_OPERATION_STATE_RESPONSE_RETAINED || len(outcome.GetExchange().GetExactResponse()) == 0 {
		return nil, errors.New("Apple nearby-place response is not retained")
	}
	completeAppleNearbyPlaceEvidence(outcome)
	return outcome, retainAppleNearbyPlaceStage(retain, outcome)
}

func completeAppleNearbyPlaceEvidence(outcome *locationwire.AcquireAppleNearbyPlaceEvidenceOutcome) {
	providerRequest := outcome.GetRequest().GetProviderRequest()
	rawResponse := outcome.GetExchange().GetExactResponse()
	var response struct {
		Candidates []struct {
			Name           string      `json:"name"`
			Category       string      `json:"category"`
			Coordinate     *Coordinate `json:"coordinate"`
			Address        *Address    `json:"address"`
			DistanceMeters float64     `json:"distance_m"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(rawResponse, &response); err != nil {
		outcome.Exchange.State = locationwire.OperationState_OPERATION_STATE_FAILED
		outcome.Exchange.Failure = &locationwire.OperationFailure{Class: locationwire.OperationFailureClass_OPERATION_FAILURE_CLASS_DECODE_RESPONSE, Detail: err.Error()}
	} else if len(response.Candidates) == 0 {
		outcome.Exchange.State = locationwire.OperationState_OPERATION_STATE_NO_RESULT
	} else if len(response.Candidates) > int(providerRequest.GetMaximumCandidates()) {
		outcome.Exchange.State = locationwire.OperationState_OPERATION_STATE_FAILED
		outcome.Exchange.Failure = &locationwire.OperationFailure{Class: locationwire.OperationFailureClass_OPERATION_FAILURE_CLASS_CANDIDATE_LIMIT, Detail: "Apple returned more candidates than requested"}
	} else {
		for providerPosition, source := range response.Candidates {
			candidate := &locationwire.PlaceCandidate{ProviderPosition: int32(providerPosition), Name: source.Name, DistanceMeters: source.DistanceMeters, Address: addressHierarchy(source.Address)}
			if source.Category != "" {
				candidate.Categories = []string{source.Category}
			}
			if source.Coordinate != nil {
				candidate.Coordinate = &locationwire.Coordinate{Latitude: source.Coordinate.Latitude, Longitude: source.Coordinate.Longitude}
			}
			outcome.Candidates = append(outcome.Candidates, candidate)
		}
		outcome.Exchange.State = locationwire.OperationState_OPERATION_STATE_SUCCEEDED
	}
	outcome.CompletedAt = completedAt()
}

func callAppleReverseGeocoding(ctx context.Context, coordinate *locationwire.Coordinate) ([]byte, string, error) {
	requestBytes, err := json.Marshal(struct {
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
	}{coordinate.Latitude, coordinate.Longitude})
	if err != nil {
		return nil, "", err
	}
	return callAppleBridge(ctx, requestBytes, true)
}

func callAppleNearbyPlaces(ctx context.Context, coordinate *locationwire.Coordinate, radiusMeters float64, maximumCandidates int32) ([]byte, string, error) {
	requestBytes, err := json.Marshal(struct {
		Latitude          float64 `json:"latitude"`
		Longitude         float64 `json:"longitude"`
		RadiusMeters      float64 `json:"radius_meters"`
		MaximumCandidates int32   `json:"maximum_candidates"`
	}{coordinate.Latitude, coordinate.Longitude, radiusMeters, maximumCandidates})
	if err != nil {
		return nil, "", err
	}
	return callAppleBridge(ctx, requestBytes, false)
}

func callAppleBridge(ctx context.Context, requestBytes []byte, reverse bool) ([]byte, string, error) {
	select {
	case <-ctx.Done():
		return nil, "", ctx.Err()
	default:
	}
	if C.photoscrawl_current_thread_is_main() == 0 {
		return nil, "", errors.New("Apple location operation must execute on the process main thread")
	}
	cRequest := C.CString(string(requestBytes))
	defer C.free(unsafe.Pointer(cRequest))
	var cError *C.char
	var cResponse *C.char
	if reverse {
		cResponse = C.photoscrawl_apple_reverse_geocoding_json(cRequest, &cError)
	} else {
		cResponse = C.photoscrawl_apple_nearby_places_json(cRequest, &cError)
	}
	if cError != nil {
		defer C.free(unsafe.Pointer(cError))
		return nil, C.GoString(cError), nil
	}
	if cResponse == nil {
		return nil, "Apple location operation returned no response", nil
	}
	defer C.free(unsafe.Pointer(cResponse))
	return []byte(C.GoString(cResponse)), "", nil
}
