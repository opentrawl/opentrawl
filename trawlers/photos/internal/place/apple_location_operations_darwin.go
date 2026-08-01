//go:build darwin

package place

/*
#cgo darwin LDFLAGS: -framework Foundation -framework CoreLocation -framework MapKit
#include <stdlib.h>

char *photoscrawl_apple_reverse_geocoding_json(const char *requestJSON, char **errorOut);
char *photoscrawl_apple_nearby_places_json(const char *requestJSON, char **errorOut);
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

func AcquireAppleReverseGeocodingEvidence(ctx context.Context, request *locationwire.AcquireAppleReverseGeocodingEvidenceRequest) (*locationwire.AcquireAppleReverseGeocodingEvidenceOutcome, error) {
	if request == nil || validateCaptureLocationInput(request.Input) != nil {
		return nil, errors.New("Apple reverse-geocoding request is incomplete")
	}
	outcome := &locationwire.AcquireAppleReverseGeocodingEvidenceOutcome{Request: request, Exchange: &locationwire.ProviderExchange{State: locationwire.OperationState_OPERATION_STATE_REQUEST_RETAINED}}
	rawResponse, bridgeFailure, err := callAppleReverseGeocoding(ctx, request.Input.Coordinate)
	if err != nil {
		return nil, err
	}
	if bridgeFailure != "" {
		outcome.Exchange = failedExchange(locationwire.OperationFailureClass_OPERATION_FAILURE_CLASS_APPLE_PROVIDER, bridgeFailure, true)
		outcome.CompletedAt = completedAt()
		return outcome, nil
	}
	if len(rawResponse) > maxRawEvidenceBytes {
		outcome.Exchange = failedExchange(locationwire.OperationFailureClass_OPERATION_FAILURE_CLASS_RESPONSE_TOO_LARGE, "Apple reverse-geocoding response exceeded the retained evidence limit", true)
		outcome.CompletedAt = completedAt()
		return outcome, nil
	}
	outcome.Exchange = &locationwire.ProviderExchange{State: locationwire.OperationState_OPERATION_STATE_RESPONSE_RETAINED, TransmissionStarted: true, ExactResponse: rawResponse}
	var response struct {
		Address *Address `json:"address"`
	}
	if err := json.Unmarshal(rawResponse, &response); err != nil {
		outcome.Exchange.State = locationwire.OperationState_OPERATION_STATE_FAILED
		outcome.Exchange.Failure = &locationwire.OperationFailure{Class: locationwire.OperationFailureClass_OPERATION_FAILURE_CLASS_DECODE_RESPONSE, Detail: err.Error()}
	} else if response.Address == nil {
		outcome.Exchange.State = locationwire.OperationState_OPERATION_STATE_NO_RESULT
	} else {
		outcome.Address = addressHierarchy(response.Address)
		outcome.Exchange.State = locationwire.OperationState_OPERATION_STATE_SUCCEEDED
	}
	outcome.CompletedAt = completedAt()
	return outcome, nil
}

func AcquireAppleNearbyPlaceEvidence(ctx context.Context, request *locationwire.AcquireAppleNearbyPlaceEvidenceRequest) (*locationwire.AcquireAppleNearbyPlaceEvidenceOutcome, error) {
	if request == nil || validateCaptureLocationInput(request.Input) != nil {
		return nil, errors.New("Apple nearby-place request is incomplete")
	}
	if request.MaximumCandidates <= 0 || request.MaximumCandidates > MaximumNearbyPlaceCandidates {
		return nil, errors.New("Apple nearby-place candidate limit is invalid")
	}
	outcome := &locationwire.AcquireAppleNearbyPlaceEvidenceOutcome{Request: request, Exchange: &locationwire.ProviderExchange{State: locationwire.OperationState_OPERATION_STATE_REQUEST_RETAINED}}
	if len(request.GetKnownPlaceOutcome().GetMatches()) > 0 {
		outcome.Exchange.State = locationwire.OperationState_OPERATION_STATE_SKIPPED_KNOWN_PLACE
		outcome.CompletedAt = completedAt()
		return outcome, nil
	}
	rawResponse, bridgeFailure, err := callAppleNearbyPlaces(ctx, request.Input.Coordinate, request.RadiusMeters, request.MaximumCandidates)
	if err != nil {
		return nil, err
	}
	if bridgeFailure != "" {
		outcome.Exchange = failedExchange(locationwire.OperationFailureClass_OPERATION_FAILURE_CLASS_APPLE_PROVIDER, bridgeFailure, true)
		outcome.CompletedAt = completedAt()
		return outcome, nil
	}
	if len(rawResponse) > maxRawEvidenceBytes {
		outcome.Exchange = failedExchange(locationwire.OperationFailureClass_OPERATION_FAILURE_CLASS_RESPONSE_TOO_LARGE, "Apple nearby-place response exceeded the retained evidence limit", true)
		outcome.CompletedAt = completedAt()
		return outcome, nil
	}
	outcome.Exchange = &locationwire.ProviderExchange{State: locationwire.OperationState_OPERATION_STATE_RESPONSE_RETAINED, TransmissionStarted: true, ExactResponse: rawResponse}
	var response struct {
		Candidates []struct {
			ProviderReference string      `json:"provider_reference"`
			Name              string      `json:"name"`
			Category          string      `json:"category"`
			Coordinate        *Coordinate `json:"coordinate"`
			Address           *Address    `json:"address"`
			DistanceMeters    float64     `json:"distance_m"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(rawResponse, &response); err != nil {
		outcome.Exchange.State = locationwire.OperationState_OPERATION_STATE_FAILED
		outcome.Exchange.Failure = &locationwire.OperationFailure{Class: locationwire.OperationFailureClass_OPERATION_FAILURE_CLASS_DECODE_RESPONSE, Detail: err.Error()}
	} else if len(response.Candidates) == 0 {
		outcome.Exchange.State = locationwire.OperationState_OPERATION_STATE_NO_RESULT
	} else if len(response.Candidates) > int(request.MaximumCandidates) {
		outcome.Exchange.State = locationwire.OperationState_OPERATION_STATE_FAILED
		outcome.Exchange.Failure = &locationwire.OperationFailure{Class: locationwire.OperationFailureClass_OPERATION_FAILURE_CLASS_CANDIDATE_LIMIT, Detail: "Apple returned more candidates than requested"}
	} else {
		seenProviderReferences := make(map[string]struct{}, len(response.Candidates))
		for providerPosition, source := range response.Candidates {
			if _, seen := seenProviderReferences[source.ProviderReference]; seen {
				continue
			}
			seenProviderReferences[source.ProviderReference] = struct{}{}
			candidate := &locationwire.PlaceCandidate{ProviderPosition: int32(providerPosition), ProviderReference: source.ProviderReference, Name: source.Name, DistanceMeters: source.DistanceMeters, Address: addressHierarchy(source.Address)}
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
	return outcome, nil
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
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
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
