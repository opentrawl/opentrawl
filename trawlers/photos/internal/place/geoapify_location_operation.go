package place

import (
	"context"
	"encoding/json"
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
	geoapifyPlacesEndpoint = "https://api.geoapify.com/v2/places"
)

func AcquireGeoapifyPhotographedPlaceCandidateEvidence(ctx context.Context, request *locationwire.AcquireGeoapifyPhotographedPlaceCandidateEvidenceRequest, apiKeyFilePath string, client *http.Client, retain RetainGeoapifyPhotographedPlaceCandidateEvidenceStage) (*locationwire.AcquireGeoapifyPhotographedPlaceCandidateEvidenceOutcome, error) {
	providerRequest := request.GetProviderRequest()
	if request == nil || validateCaptureLocationInput(request.GetInput()) != nil || validateProviderCoordinate(providerRequest.GetCoordinate()) != nil ||
		!providerCoordinateMatchesCaptureLocation(providerRequest.GetCoordinate(), request.GetInput()) {
		return nil, errors.New("Geoapify photographed-place candidate request is incomplete")
	}
	if providerRequest.GetMaximumCandidates() <= 0 || providerRequest.GetMaximumCandidates() > MaximumNearbyPlaceCandidates {
		return nil, fmt.Errorf("Geoapify maximum photographed-place candidates must be between 1 and %d", MaximumNearbyPlaceCandidates)
	}
	if providerRequest.GetRadiusMeters() <= 0 {
		return nil, errors.New("Geoapify photographed-place candidate search radius must be positive")
	}
	if err := validateGeoapifyProviderCategories(providerRequest.GetProviderCategories()); err != nil {
		return nil, err
	}
	outcome := &locationwire.AcquireGeoapifyPhotographedPlaceCandidateEvidenceOutcome{
		Request: request, Exchange: &locationwire.ProviderExchange{State: locationwire.OperationState_OPERATION_STATE_REQUEST_RETAINED},
		Provider:    locationwire.LocationEvidenceProvider_LOCATION_EVIDENCE_PROVIDER_GEOAPIFY_PLACES,
		EvidenceUse: locationwire.ProviderEvidenceUse_PROVIDER_EVIDENCE_USE_ACQUIRED,
	}
	if err := retainGeoapifyPhotographedPlaceCandidateEvidenceStage(retain, outcome); err != nil {
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
		"filter":     {fmt.Sprintf("circle:%s,%s,%s", strconv.FormatFloat(coordinate.Longitude, 'f', -1, 64), strconv.FormatFloat(coordinate.Latitude, 'f', -1, 64), strconv.FormatFloat(providerRequest.GetRadiusMeters(), 'f', -1, 64))},
		"bias":       {fmt.Sprintf("proximity:%s,%s", strconv.FormatFloat(coordinate.Longitude, 'f', -1, 64), strconv.FormatFloat(coordinate.Latitude, 'f', -1, 64))},
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
		return retainGeoapifyPhotographedPlaceCandidateEvidenceStage(retain, outcome)
	})
	if err != nil {
		return nil, err
	}
	if outcome.Exchange.State == locationwire.OperationState_OPERATION_STATE_RESPONSE_RETAINED {
		completeGeoapifyPhotographedPlaceCandidateEvidence(outcome)
	} else {
		outcome.CompletedAt = completedAt()
	}
	return outcome, retainGeoapifyPhotographedPlaceCandidateEvidenceStage(retain, outcome)
}

func validateGeoapifyProviderCategories(providerCategories []string) error {
	if len(providerCategories) == 0 {
		return errors.New("Geoapify photographed-place candidate categories are required")
	}
	seen := make(map[string]struct{}, len(providerCategories))
	for _, providerCategory := range providerCategories {
		if strings.TrimSpace(providerCategory) != providerCategory || providerCategory == "" || strings.Contains(providerCategory, ",") {
			return errors.New("Geoapify photographed-place candidate category is invalid")
		}
		if _, duplicate := seen[providerCategory]; duplicate {
			return errors.New("Geoapify photographed-place candidate category is duplicated")
		}
		seen[providerCategory] = struct{}{}
	}
	return nil
}

func ResumeGeoapifyPhotographedPlaceCandidateEvidence(outcome *locationwire.AcquireGeoapifyPhotographedPlaceCandidateEvidenceOutcome, retain RetainGeoapifyPhotographedPlaceCandidateEvidenceStage) (*locationwire.AcquireGeoapifyPhotographedPlaceCandidateEvidenceOutcome, error) {
	if outcome == nil || outcome.GetExchange().GetState() != locationwire.OperationState_OPERATION_STATE_RESPONSE_RETAINED || len(outcome.GetExchange().GetExactResponse()) == 0 {
		return nil, errors.New("Geoapify photographed-place candidate response is not retained")
	}
	completeGeoapifyPhotographedPlaceCandidateEvidence(outcome)
	return outcome, retainGeoapifyPhotographedPlaceCandidateEvidenceStage(retain, outcome)
}

func completeGeoapifyPhotographedPlaceCandidateEvidence(outcome *locationwire.AcquireGeoapifyPhotographedPlaceCandidateEvidenceOutcome) {
	candidates, parseErr := parseGeoapifyCandidates(outcome.Exchange.ExactResponse, outcome.GetRequest().GetProviderRequest().GetMaximumCandidates())
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
	requestURL := endpoint + "?" + values.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
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
		exchange.Failure = &locationwire.OperationFailure{Class: locationwire.OperationFailureClass_OPERATION_FAILURE_CLASS_RESPONSE_TOO_LARGE, Detail: fmt.Sprintf("response exceeds %d bytes", maxRawEvidenceBytes)}
	} else if response.StatusCode < 200 || response.StatusCode >= 300 {
		exchange.State = locationwire.OperationState_OPERATION_STATE_FAILED
		exchange.Failure = &locationwire.OperationFailure{Class: locationwire.OperationFailureClass_OPERATION_FAILURE_CLASS_HTTP_STATUS, Detail: response.Status}
		if retryAfter := response.Header.Get("Retry-After"); retryAfter != "" {
			exchange.Failure.RetryNotBefore = retryNotBefore(retryAfter, time.Now().UTC())
		}
	}
	return exchange, nil
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

type geoapifyResponse struct {
	Features []struct {
		Properties geoapifyProperties `json:"properties"`
		Geometry   struct {
			Coordinates []float64 `json:"coordinates"`
		} `json:"geometry"`
	} `json:"features"`
}

type geoapifyProperties struct {
	PlaceID       string   `json:"place_id"`
	Name          string   `json:"name"`
	Housenumber   string   `json:"housenumber"`
	Street        string   `json:"street"`
	Neighbourhood string   `json:"neighbourhood"`
	Suburb        string   `json:"suburb"`
	Municipality  string   `json:"municipality"`
	District      string   `json:"district"`
	City          string   `json:"city"`
	County        string   `json:"county"`
	State         string   `json:"state"`
	Postcode      string   `json:"postcode"`
	Country       string   `json:"country"`
	CountryCode   string   `json:"country_code"`
	Formatted     string   `json:"formatted"`
	Distance      float64  `json:"distance"`
	Categories    []string `json:"categories"`
	Timezone      struct {
		Name string `json:"name"`
	} `json:"timezone"`
}

func parseGeoapifyAddress(rawResponse []byte) (*locationwire.AddressHierarchy, error) {
	var response geoapifyResponse
	if err := json.Unmarshal(rawResponse, &response); err != nil {
		return nil, fmt.Errorf("decode Geoapify reverse response: %w", err)
	}
	if len(response.Features) == 0 {
		return nil, nil
	}
	return geoapifyAddress(response.Features[0].Properties), nil
}

func parseGeoapifyCandidates(rawResponse []byte, maximum int32) ([]*locationwire.PlaceCandidate, error) {
	var response geoapifyResponse
	if err := json.Unmarshal(rawResponse, &response); err != nil {
		return nil, fmt.Errorf("decode Geoapify nearby response: %w", err)
	}
	if len(response.Features) > int(maximum) {
		return nil, errors.New("Geoapify returned more candidates than requested")
	}
	candidates := make([]*locationwire.PlaceCandidate, 0, len(response.Features))
	seenProviderReferences := make(map[string]struct{}, len(response.Features))
	for providerPosition, feature := range response.Features {
		if feature.Properties.PlaceID != "" {
			if _, seen := seenProviderReferences[feature.Properties.PlaceID]; seen {
				continue
			}
			seenProviderReferences[feature.Properties.PlaceID] = struct{}{}
		}
		candidate := &locationwire.PlaceCandidate{ProviderPosition: int32(providerPosition), ProviderReference: feature.Properties.PlaceID, Name: feature.Properties.Name, Categories: feature.Properties.Categories, DistanceMeters: feature.Properties.Distance, Address: geoapifyAddress(feature.Properties)}
		if len(feature.Geometry.Coordinates) >= 2 {
			candidate.Coordinate = &locationwire.Coordinate{Longitude: feature.Geometry.Coordinates[0], Latitude: feature.Geometry.Coordinates[1]}
		}
		candidates = append(candidates, candidate)
	}
	return candidates, nil
}

func geoapifyAddress(properties geoapifyProperties) *locationwire.AddressHierarchy {
	neighbourhood := properties.Neighbourhood
	if neighbourhood == "" {
		neighbourhood = properties.Suburb
	}
	address := &locationwire.AddressHierarchy{Name: properties.Name, HouseNumber: properties.Housenumber, Street: properties.Street, Neighbourhood: neighbourhood, District: properties.District, City: properties.City, County: properties.County, Region: properties.State, Postcode: properties.Postcode, Country: properties.Country, CountryCode: strings.ToUpper(properties.CountryCode), TimeZone: properties.Timezone.Name, Formatted: properties.Formatted}
	if properties.Suburb != "" {
		address.Areas = append(address.Areas, &locationwire.NamedArea{Kind: locationwire.NamedAreaKind_NAMED_AREA_KIND_SUBURB, Name: properties.Suburb})
	}
	if properties.Municipality != "" {
		address.Areas = append(address.Areas, &locationwire.NamedArea{Kind: locationwire.NamedAreaKind_NAMED_AREA_KIND_MUNICIPALITY, Name: properties.Municipality})
	}
	return address
}
