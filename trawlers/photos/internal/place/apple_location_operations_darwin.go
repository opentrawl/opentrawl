//go:build darwin

package place

/*
#cgo darwin LDFLAGS: -framework Foundation -framework CoreLocation -framework MapKit
#include <stdlib.h>

char *photoscrawl_apple_reverse_geocoding_json(double latitude, double longitude, char **errorDescriptionOut, char **errorDomainOut, long long *errorCodeOut, int *loadingThrottledOut);
char *photoscrawl_apple_nearby_places_json(double latitude, double longitude, double radiusMeters, int maximumCandidates, char **errorDescriptionOut, char **errorDomainOut, long long *errorCodeOut, int *loadingThrottledOut);
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
		!providerCoordinateMatchesCaptureLocation(request.GetProviderRequest().GetCoordinate(), request.GetInput()) ||
		request.GetProviderRequest().GetMethod() != locationwire.AppleReverseGeocodingMethod_APPLE_REVERSE_GEOCODING_METHOD_MAP_KIT_REVERSE_GEOCODING_REQUEST {
		return nil, errors.New("Apple reverse-geocoding request is incomplete")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	outcome := &locationwire.AcquireAppleReverseGeocodingEvidenceOutcome{
		Request: request, Exchange: &locationwire.ProviderExchange{State: locationwire.OperationState_OPERATION_STATE_REQUEST_RETAINED},
		Provider:     locationwire.LocationEvidenceProvider_LOCATION_EVIDENCE_PROVIDER_APPLE_REVERSE_GEOCODING,
		EvidenceUse:  locationwire.ProviderEvidenceUse_PROVIDER_EVIDENCE_USE_ACQUIRED,
		Attributions: []*locationwire.LocationEvidenceAttribution{{ProviderName: "Apple Maps", DataSourceName: "Apple Maps"}},
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
	if bridgeFailure != nil {
		outcome.Exchange = &locationwire.ProviderExchange{
			State:               locationwire.OperationState_OPERATION_STATE_FAILED,
			TransmissionStarted: true,
			Failure:             appleOperationFailure(bridgeFailure),
		}
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
	outcome.Provider = locationwire.LocationEvidenceProvider_LOCATION_EVIDENCE_PROVIDER_APPLE_REVERSE_GEOCODING
	outcome.Attributions = []*locationwire.LocationEvidenceAttribution{{ProviderName: "Apple Maps", DataSourceName: "Apple Maps"}}
	outcome.CompletedAt = completedAt()
}

func AcquireAppleNearbyPlaceEvidence(ctx context.Context, request *locationwire.AcquireAppleNearbyPlaceEvidenceRequest, retain RetainAppleNearbyPlaceStage) (*locationwire.AcquireAppleNearbyPlaceEvidenceOutcome, error) {
	providerRequest := request.GetProviderRequest()
	if request == nil || validateCaptureLocationInput(request.GetInput()) != nil || validateProviderCoordinate(providerRequest.GetCoordinate()) != nil ||
		!providerCoordinateMatchesCaptureLocation(providerRequest.GetCoordinate(), request.GetInput()) ||
		providerRequest.GetMethod() != locationwire.AppleNearbyPlaceSearchMethod_APPLE_NEARBY_PLACE_SEARCH_METHOD_MAP_KIT_LOCAL_SEARCH {
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
		Provider:     locationwire.LocationEvidenceProvider_LOCATION_EVIDENCE_PROVIDER_APPLE_NEARBY_PLACES,
		EvidenceUse:  locationwire.ProviderEvidenceUse_PROVIDER_EVIDENCE_USE_ACQUIRED,
		Attributions: []*locationwire.LocationEvidenceAttribution{{ProviderName: "Apple Maps", DataSourceName: "Apple Maps"}},
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
	if bridgeFailure != nil {
		outcome.Exchange = &locationwire.ProviderExchange{
			State:               locationwire.OperationState_OPERATION_STATE_FAILED,
			TransmissionStarted: true,
			Failure:             appleOperationFailure(bridgeFailure),
		}
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
	outcome.Provider = locationwire.LocationEvidenceProvider_LOCATION_EVIDENCE_PROVIDER_APPLE_NEARBY_PLACES
	outcome.Attributions = []*locationwire.LocationEvidenceAttribution{{ProviderName: "Apple Maps", DataSourceName: "Apple Maps"}}
}

type appleProviderFailure struct {
	detail              string
	providerErrorDomain string
	providerErrorCode   int64
	loadingThrottled    bool
}

func appleOperationFailure(providerFailure *appleProviderFailure) *locationwire.OperationFailure {
	failureClass := locationwire.OperationFailureClass_OPERATION_FAILURE_CLASS_APPLE_PROVIDER
	if providerFailure.loadingThrottled {
		failureClass = locationwire.OperationFailureClass_OPERATION_FAILURE_CLASS_APPLE_MAPKIT_LOADING_THROTTLED
	}
	return &locationwire.OperationFailure{
		Class:               failureClass,
		Detail:              providerFailure.detail,
		ProviderErrorDomain: providerFailure.providerErrorDomain,
		ProviderErrorCode:   providerFailure.providerErrorCode,
	}
}

func callAppleReverseGeocoding(ctx context.Context, coordinate *locationwire.Coordinate) ([]byte, *appleProviderFailure, error) {
	if err := prepareAppleBridgeCall(ctx); err != nil {
		return nil, nil, err
	}
	var cErrorDescription *C.char
	var cErrorDomain *C.char
	var cErrorCode C.longlong
	var cLoadingThrottled C.int
	cResponse := C.photoscrawl_apple_reverse_geocoding_json(
		C.double(coordinate.GetLatitude()),
		C.double(coordinate.GetLongitude()),
		&cErrorDescription,
		&cErrorDomain,
		&cErrorCode,
		&cLoadingThrottled,
	)
	return consumeAppleBridgeResponse(cResponse, cErrorDescription, cErrorDomain, cErrorCode, cLoadingThrottled, "Apple reverse geocoding returned no response")
}

func callAppleNearbyPlaces(ctx context.Context, coordinate *locationwire.Coordinate, radiusMeters float64, maximumCandidates int32) ([]byte, *appleProviderFailure, error) {
	if err := prepareAppleBridgeCall(ctx); err != nil {
		return nil, nil, err
	}
	var cErrorDescription *C.char
	var cErrorDomain *C.char
	var cErrorCode C.longlong
	var cLoadingThrottled C.int
	cResponse := C.photoscrawl_apple_nearby_places_json(
		C.double(coordinate.GetLatitude()),
		C.double(coordinate.GetLongitude()),
		C.double(radiusMeters),
		C.int(maximumCandidates),
		&cErrorDescription,
		&cErrorDomain,
		&cErrorCode,
		&cLoadingThrottled,
	)
	return consumeAppleBridgeResponse(cResponse, cErrorDescription, cErrorDomain, cErrorCode, cLoadingThrottled, "Apple nearby place search returned no response")
}

func prepareAppleBridgeCall(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if C.photoscrawl_current_thread_is_main() == 0 {
		return errors.New("Apple location operation must execute on the process main thread")
	}
	return nil
}

func consumeAppleBridgeResponse(cResponse *C.char, cErrorDescription *C.char, cErrorDomain *C.char, cErrorCode C.longlong, cLoadingThrottled C.int, emptyResponseDescription string) ([]byte, *appleProviderFailure, error) {
	if cErrorDescription != nil {
		defer C.free(unsafe.Pointer(cErrorDescription))
		failure := &appleProviderFailure{detail: C.GoString(cErrorDescription), providerErrorCode: int64(cErrorCode), loadingThrottled: cLoadingThrottled != 0}
		if cErrorDomain != nil {
			failure.providerErrorDomain = C.GoString(cErrorDomain)
		}
		if cErrorDomain != nil {
			C.free(unsafe.Pointer(cErrorDomain))
		}
		return nil, failure, nil
	}
	if cResponse == nil {
		return nil, &appleProviderFailure{detail: emptyResponseDescription}, nil
	}
	defer C.free(unsafe.Pointer(cResponse))
	return []byte(C.GoString(cResponse)), nil, nil
}
