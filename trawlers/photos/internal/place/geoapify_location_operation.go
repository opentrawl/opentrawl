package place

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	locationwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/location"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	geoapifyReverseGeocodingEndpoint = "https://api.geoapify.com/v1/geocode/reverse"
	geoapifyPlacesEndpoint           = "https://api.geoapify.com/v2/places"
)

func AcquireGeoapifyReverseGeocodingEvidence(ctx context.Context, request *locationwire.AcquireGeoapifyReverseGeocodingEvidenceRequest, apiKeyFilePath string, client *http.Client, retain RetainGeoapifyReverseGeocodingStage) (*locationwire.AcquireGeoapifyReverseGeocodingEvidenceOutcome, error) {
	providerRequest := request.GetProviderRequest()
	if request == nil || validateCaptureLocationInput(request.GetInput()) != nil || validateProviderCoordinate(providerRequest.GetCoordinate()) != nil ||
		!providerCoordinateMatchesCaptureLocation(providerRequest.GetCoordinate(), request.GetInput()) {
		return nil, errors.New("Geoapify reverse-geocoding request is incomplete")
	}
	if providerRequest.GetResponseFormat() != locationwire.GeoapifyReverseGeocodingResponseFormat_GEOAPIFY_REVERSE_GEOCODING_RESPONSE_FORMAT_GEOJSON {
		return nil, errors.New("Geoapify reverse-geocoding response format is unsupported")
	}
	if providerRequest.GetMaximumResults() != 1 {
		return nil, errors.New("Geoapify reverse geocoding requires exactly one maximum result")
	}
	outcome := &locationwire.AcquireGeoapifyReverseGeocodingEvidenceOutcome{
		Request: request, Exchange: &locationwire.ProviderExchange{State: locationwire.OperationState_OPERATION_STATE_REQUEST_RETAINED},
		Provider:    locationwire.LocationEvidenceProvider_LOCATION_EVIDENCE_PROVIDER_GEOAPIFY_REVERSE_GEOCODING,
		EvidenceUse: locationwire.ProviderEvidenceUse_PROVIDER_EVIDENCE_USE_ACQUIRED,
	}
	if err := retainGeoapifyReverseGeocodingStage(retain, outcome); err != nil {
		return nil, err
	}
	apiKey, err := readGeoapifyAPIKey(apiKeyFilePath)
	if err != nil {
		return nil, err
	}
	if client == nil {
		client = http.DefaultClient
	}
	coordinate := providerRequest.GetCoordinate()
	reverseGeocodingValues := url.Values{
		"lat":    {geoapifyCoordinateText(coordinate.GetLatitude())},
		"lon":    {geoapifyCoordinateText(coordinate.GetLongitude())},
		"format": {"geojson"},
		"limit":  {strconv.Itoa(int(providerRequest.GetMaximumResults()))},
		"apiKey": {apiKey},
	}
	outcome.Exchange, err = transmitGeoapify(ctx, client, geoapifyReverseGeocodingEndpoint, reverseGeocodingValues, func(exchange *locationwire.ProviderExchange) error {
		outcome.Exchange = exchange
		if exchange.GetState() == locationwire.OperationState_OPERATION_STATE_RESPONSE_RETAINED && outcome.GetObservedAt() == nil {
			outcome.ObservedAt = completedAt()
		}
		return retainGeoapifyReverseGeocodingStage(retain, outcome)
	})
	if err != nil {
		return nil, err
	}
	if outcome.Exchange.GetState() == locationwire.OperationState_OPERATION_STATE_RESPONSE_RETAINED {
		completeGeoapifyReverseGeocodingEvidence(outcome)
	} else {
		outcome.CompletedAt = completedAt()
	}
	return outcome, retainGeoapifyReverseGeocodingStage(retain, outcome)
}

func ResumeGeoapifyReverseGeocodingEvidence(outcome *locationwire.AcquireGeoapifyReverseGeocodingEvidenceOutcome, retain RetainGeoapifyReverseGeocodingStage) (*locationwire.AcquireGeoapifyReverseGeocodingEvidenceOutcome, error) {
	if outcome == nil || outcome.GetExchange().GetState() != locationwire.OperationState_OPERATION_STATE_RESPONSE_RETAINED || len(outcome.GetExchange().GetExactResponse()) == 0 {
		return nil, errors.New("Geoapify reverse-geocoding response is not retained")
	}
	completeGeoapifyReverseGeocodingEvidence(outcome)
	return outcome, retainGeoapifyReverseGeocodingStage(retain, outcome)
}

func completeGeoapifyReverseGeocodingEvidence(outcome *locationwire.AcquireGeoapifyReverseGeocodingEvidenceOutcome) {
	address, attributions, parseErr := parseGeoapifyReverseGeocodingResponse(outcome.GetExchange().GetExactResponse(), outcome.GetRequest().GetProviderRequest().GetMaximumResults())
	outcome.Attributions = attributions
	if parseErr != nil {
		outcome.Exchange.State = locationwire.OperationState_OPERATION_STATE_FAILED
		outcome.Exchange.Failure = &locationwire.OperationFailure{Class: locationwire.OperationFailureClass_OPERATION_FAILURE_CLASS_DECODE_RESPONSE, Detail: parseErr.Error()}
	} else if address == nil {
		outcome.Exchange.State = locationwire.OperationState_OPERATION_STATE_NO_RESULT
	} else {
		outcome.Address = address
		outcome.Exchange.State = locationwire.OperationState_OPERATION_STATE_SUCCEEDED
	}
	outcome.CompletedAt = completedAt()
}

func AcquireGeoapifyNearbyPlaceEvidence(ctx context.Context, request *locationwire.AcquireGeoapifyNearbyPlaceEvidenceRequest, apiKeyFilePath string, client *http.Client, retain RetainGeoapifyNearbyPlaceEvidenceStage) (*locationwire.AcquireGeoapifyNearbyPlaceEvidenceOutcome, error) {
	providerRequest := request.GetProviderRequest()
	if request == nil || validateCaptureLocationInput(request.GetInput()) != nil || validateProviderCoordinate(providerRequest.GetCoordinate()) != nil ||
		!providerCoordinateMatchesCaptureLocation(providerRequest.GetCoordinate(), request.GetInput()) {
		return nil, errors.New("Geoapify nearby-place request is incomplete")
	}
	if providerRequest.GetMaximumCandidates() <= 0 || providerRequest.GetMaximumCandidates() > MaximumNearbyPlaceCandidates {
		return nil, fmt.Errorf("Geoapify maximum nearby places must be between 1 and %d", MaximumNearbyPlaceCandidates)
	}
	if providerRequest.GetRadiusMeters() <= 0 {
		return nil, errors.New("Geoapify nearby-place search radius must be positive")
	}
	if err := validateGeoapifyProviderCategories(providerRequest.GetProviderCategories()); err != nil {
		return nil, err
	}
	outcome := &locationwire.AcquireGeoapifyNearbyPlaceEvidenceOutcome{
		Request: request, Exchange: &locationwire.ProviderExchange{State: locationwire.OperationState_OPERATION_STATE_REQUEST_RETAINED},
		Provider:    locationwire.LocationEvidenceProvider_LOCATION_EVIDENCE_PROVIDER_GEOAPIFY_PLACES,
		EvidenceUse: locationwire.ProviderEvidenceUse_PROVIDER_EVIDENCE_USE_ACQUIRED,
	}
	if err := retainGeoapifyNearbyPlaceEvidenceStage(retain, outcome); err != nil {
		return nil, err
	}
	apiKey, err := readGeoapifyAPIKey(apiKeyFilePath)
	if err != nil {
		return nil, err
	}
	if client == nil {
		client = http.DefaultClient
	}
	coordinate := providerRequest.GetCoordinate()
	placesValues := url.Values{
		"categories": {strings.Join(providerRequest.GetProviderCategories(), ",")},
		"filter":     {geoapifyCircleFilter(coordinate, providerRequest.GetRadiusMeters())},
		"bias":       {geoapifyProximityBias(coordinate)},
		"limit":      {strconv.Itoa(int(providerRequest.GetMaximumCandidates()))}, "apiKey": {apiKey},
	}
	if providerRequest.GetRequireNamedCandidates() {
		placesValues.Set("conditions", "named")
	}
	outcome.Exchange, err = transmitGeoapify(ctx, client, geoapifyPlacesEndpoint, placesValues, func(exchange *locationwire.ProviderExchange) error {
		outcome.Exchange = exchange
		if exchange.GetState() == locationwire.OperationState_OPERATION_STATE_RESPONSE_RETAINED && outcome.GetObservedAt() == nil {
			outcome.ObservedAt = completedAt()
		}
		return retainGeoapifyNearbyPlaceEvidenceStage(retain, outcome)
	})
	if err != nil {
		return nil, err
	}
	if outcome.Exchange.State == locationwire.OperationState_OPERATION_STATE_RESPONSE_RETAINED {
		completeGeoapifyNearbyPlaceEvidence(outcome)
	} else {
		outcome.CompletedAt = completedAt()
	}
	return outcome, retainGeoapifyNearbyPlaceEvidenceStage(retain, outcome)
}

func validateGeoapifyProviderCategories(providerCategories []string) error {
	if len(providerCategories) == 0 {
		return errors.New("Geoapify nearby-place categories are required")
	}
	seen := make(map[string]struct{}, len(providerCategories))
	for _, providerCategory := range providerCategories {
		if strings.TrimSpace(providerCategory) != providerCategory || providerCategory == "" || strings.Contains(providerCategory, ",") {
			return errors.New("Geoapify nearby-place category is invalid")
		}
		if _, duplicate := seen[providerCategory]; duplicate {
			return errors.New("Geoapify nearby-place category is duplicated")
		}
		seen[providerCategory] = struct{}{}
	}
	return nil
}

func ResumeGeoapifyNearbyPlaceEvidence(outcome *locationwire.AcquireGeoapifyNearbyPlaceEvidenceOutcome, retain RetainGeoapifyNearbyPlaceEvidenceStage) (*locationwire.AcquireGeoapifyNearbyPlaceEvidenceOutcome, error) {
	if outcome == nil || outcome.GetExchange().GetState() != locationwire.OperationState_OPERATION_STATE_RESPONSE_RETAINED || len(outcome.GetExchange().GetExactResponse()) == 0 {
		return nil, errors.New("Geoapify nearby-place response is not retained")
	}
	completeGeoapifyNearbyPlaceEvidence(outcome)
	return outcome, retainGeoapifyNearbyPlaceEvidenceStage(retain, outcome)
}

func completeGeoapifyNearbyPlaceEvidence(outcome *locationwire.AcquireGeoapifyNearbyPlaceEvidenceOutcome) {
	candidates, providerAttributions, parseErr := parseGeoapifyCandidates(outcome.Exchange.ExactResponse, outcome.GetRequest().GetProviderRequest().GetMaximumCandidates())
	outcome.Attributions = providerAttributions
	if parseErr != nil {
		outcome.Exchange.State = locationwire.OperationState_OPERATION_STATE_FAILED
		outcome.Exchange.Failure = &locationwire.OperationFailure{Class: locationwire.OperationFailureClass_OPERATION_FAILURE_CLASS_DECODE_RESPONSE, Detail: parseErr.Error()}
	} else if len(candidates) == 0 {
		outcome.Exchange.State = locationwire.OperationState_OPERATION_STATE_NO_RESULT
	} else {
		outcome.Candidates = candidates
		outcome.Exchange.State = locationwire.OperationState_OPERATION_STATE_SUCCEEDED
	}
	outcome.CompletedAt = completedAt()
}

func readGeoapifyAPIKey(apiKeyFilePath string) (string, error) {
	apiKeyBytes, err := os.ReadFile(strings.TrimSpace(apiKeyFilePath))
	if err != nil {
		return "", fmt.Errorf("read Geoapify API key: %w", err)
	}
	const geoapifyAPIKeyAssignment = "GEOAPIFY_API_KEY="
	apiKeyFile := strings.TrimSpace(string(apiKeyBytes))
	if !strings.HasPrefix(apiKeyFile, geoapifyAPIKeyAssignment) {
		return "", errors.New("Geoapify API key file must contain GEOAPIFY_API_KEY=<key>")
	}
	apiKey := strings.TrimSpace(strings.TrimPrefix(apiKeyFile, geoapifyAPIKeyAssignment))
	if apiKey == "" {
		return "", errors.New("Geoapify API key is empty")
	}
	if strings.ContainsAny(apiKey, "\r\n\t ") {
		return "", errors.New("Geoapify API key file contains more than one assignment")
	}
	return apiKey, nil
}

func transmitGeoapify(ctx context.Context, client *http.Client, endpoint string, values url.Values, retain func(*locationwire.ProviderExchange) error) (*locationwire.ProviderExchange, error) {
	requestURL, err := geoapifyProviderRequestURL(endpoint, values)
	if err != nil {
		return failedExchange(locationwire.OperationFailureClass_OPERATION_FAILURE_CLASS_BUILD_REQUEST, err.Error(), false), nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return failedExchange(locationwire.OperationFailureClass_OPERATION_FAILURE_CLASS_BUILD_REQUEST, err.Error(), false), nil
	}
	exchange := &locationwire.ProviderExchange{State: locationwire.OperationState_OPERATION_STATE_TRANSMISSION_STARTED, TransmissionStarted: true}
	if err := retain(exchange); err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return failedExchange(locationwire.OperationFailureClass_OPERATION_FAILURE_CLASS_TRANSPORT, err.Error(), true), nil
	}
	defer func() { _ = response.Body.Close() }()
	exchange.HttpStatus = int32(response.StatusCode)
	exchange.ProviderRequestId = response.Header.Get("X-Request-Id")
	rawResponse, err := io.ReadAll(io.LimitReader(response.Body, maxRawEvidenceBytes+1))
	if err != nil {
		return failedExchange(locationwire.OperationFailureClass_OPERATION_FAILURE_CLASS_READ_RESPONSE, err.Error(), true), nil
	}
	exchange.ExactResponse = rawResponse
	exchange.State = locationwire.OperationState_OPERATION_STATE_RESPONSE_RETAINED
	if err := retain(exchange); err != nil {
		return nil, err
	}
	if len(rawResponse) > maxRawEvidenceBytes {
		exchange.State = locationwire.OperationState_OPERATION_STATE_FAILED
		exchange.Failure = &locationwire.OperationFailure{Class: locationwire.OperationFailureClass_OPERATION_FAILURE_CLASS_RESPONSE_TOO_LARGE, Detail: "response exceeds " + strconv.Itoa(maxRawEvidenceBytes) + " bytes"}
	} else if response.StatusCode < 200 || response.StatusCode >= 300 {
		exchange.State = locationwire.OperationState_OPERATION_STATE_FAILED
		exchange.Failure = &locationwire.OperationFailure{Class: locationwire.OperationFailureClass_OPERATION_FAILURE_CLASS_HTTP_STATUS, Detail: response.Status}
		if retryAfter := response.Header.Get("Retry-After"); retryAfter != "" {
			exchange.Failure.RetryNotBefore = retryNotBefore(retryAfter, time.Now().UTC())
		}
	}
	return exchange, nil
}

func geoapifyProviderRequestURL(endpoint string, queryValues url.Values) (*url.URL, error) {
	requestURL, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse Geoapify endpoint: %w", err)
	}
	requestURL.RawQuery = queryValues.Encode()
	return requestURL, nil
}

func geoapifyCircleFilter(coordinate *locationwire.Coordinate, radiusMeters float64) string {
	return "circle:" + strings.Join([]string{
		geoapifyCoordinateText(coordinate.GetLongitude()),
		geoapifyCoordinateText(coordinate.GetLatitude()),
		strconv.FormatFloat(radiusMeters, 'f', -1, 64),
	}, ",")
}

func geoapifyProximityBias(coordinate *locationwire.Coordinate) string {
	return "proximity:" + strings.Join([]string{
		geoapifyCoordinateText(coordinate.GetLongitude()),
		geoapifyCoordinateText(coordinate.GetLatitude()),
	}, ",")
}

func geoapifyCoordinateText(coordinateDegrees float64) string {
	return strconv.FormatFloat(coordinateDegrees, 'f', -1, 64)
}

func retryNotBefore(retryAfter string, now time.Time) *timestamppb.Timestamp {
	if seconds, err := strconv.Atoi(retryAfter); err == nil && seconds >= 0 {
		return timestamppb.New(now.Add(time.Duration(seconds) * time.Second))
	}
	if parsed, err := http.ParseTime(retryAfter); err == nil {
		return timestamppb.New(parsed.UTC())
	}
	return nil
}
