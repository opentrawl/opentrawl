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
)

const (
	geoapifyReverseEndpoint = "https://api.geoapify.com/v1/geocode/reverse"
	geoapifyPlacesEndpoint  = "https://api.geoapify.com/v2/places"
	geoapifyPlaceCategories = "accommodation,catering,commercial,education,entertainment,healthcare,heritage,leisure,natural,office,parking,public_transport,service,sport,tourism"
)

func AcquireGeoapifyReverseGeocodingEvidence(ctx context.Context, request *locationwire.AcquireGeoapifyReverseGeocodingEvidenceRequest, apiKeyFilePath string, client *http.Client) (*locationwire.AcquireGeoapifyReverseGeocodingEvidenceOutcome, error) {
	if request == nil || validateCaptureLocationInput(request.Input) != nil {
		return nil, errors.New("Geoapify reverse-geocoding request is incomplete")
	}
	if request.MaximumNearbyCandidates <= 0 || request.MaximumNearbyCandidates > MaximumNearbyPlaceCandidates {
		return nil, fmt.Errorf("Geoapify maximum nearby candidates must be between 1 and %d", MaximumNearbyPlaceCandidates)
	}
	if request.NearbyRadiusMeters <= 0 {
		return nil, errors.New("Geoapify nearby radius must be positive")
	}
	apiKeyBytes, err := os.ReadFile(strings.TrimSpace(apiKeyFilePath))
	if err != nil {
		return nil, fmt.Errorf("read Geoapify API key: %w", err)
	}
	apiKey := strings.TrimSpace(string(apiKeyBytes))
	if apiKey == "" {
		return nil, errors.New("Geoapify API key is empty")
	}
	if client == nil {
		client = http.DefaultClient
	}
	outcome := &locationwire.AcquireGeoapifyReverseGeocodingEvidenceOutcome{Request: request}
	coordinate := request.Input.Coordinate
	reverseValues := url.Values{
		"lat": {strconv.FormatFloat(coordinate.Latitude, 'f', -1, 64)}, "lon": {strconv.FormatFloat(coordinate.Longitude, 'f', -1, 64)},
		"format": {"geojson"}, "apiKey": {apiKey},
	}
	outcome.ReverseExchange = transmitGeoapify(ctx, client, geoapifyReverseEndpoint, reverseValues)
	if outcome.ReverseExchange.State == locationwire.OperationState_OPERATION_STATE_RESPONSE_RETAINED {
		address, parseErr := parseGeoapifyAddress(outcome.ReverseExchange.ExactResponse)
		if parseErr != nil {
			outcome.ReverseExchange.State = locationwire.OperationState_OPERATION_STATE_FAILED
			outcome.ReverseExchange.Failure = &locationwire.OperationFailure{Class: "decode_response", Detail: parseErr.Error()}
		} else if address == nil {
			outcome.ReverseExchange.State = locationwire.OperationState_OPERATION_STATE_NO_RESULT
		} else {
			outcome.Address = address
			outcome.ReverseExchange.State = locationwire.OperationState_OPERATION_STATE_SUCCEEDED
		}
	}
	placesValues := url.Values{
		"categories": {geoapifyPlaceCategories},
		"filter":     {fmt.Sprintf("circle:%s,%s,%s", strconv.FormatFloat(coordinate.Longitude, 'f', -1, 64), strconv.FormatFloat(coordinate.Latitude, 'f', -1, 64), strconv.FormatFloat(request.NearbyRadiusMeters, 'f', -1, 64))},
		"bias":       {fmt.Sprintf("proximity:%s,%s", strconv.FormatFloat(coordinate.Longitude, 'f', -1, 64), strconv.FormatFloat(coordinate.Latitude, 'f', -1, 64))},
		"limit":      {strconv.Itoa(int(request.MaximumNearbyCandidates))}, "apiKey": {apiKey},
	}
	outcome.NearbyExchange = transmitGeoapify(ctx, client, geoapifyPlacesEndpoint, placesValues)
	if outcome.NearbyExchange.State == locationwire.OperationState_OPERATION_STATE_RESPONSE_RETAINED {
		candidates, parseErr := parseGeoapifyCandidates(outcome.NearbyExchange.ExactResponse, request.MaximumNearbyCandidates)
		if parseErr != nil {
			outcome.NearbyExchange.State = locationwire.OperationState_OPERATION_STATE_FAILED
			outcome.NearbyExchange.Failure = &locationwire.OperationFailure{Class: "decode_response", Detail: parseErr.Error()}
		} else if len(candidates) == 0 {
			outcome.NearbyExchange.State = locationwire.OperationState_OPERATION_STATE_NO_RESULT
		} else {
			outcome.NearbyCandidates = candidates
			outcome.NearbyExchange.State = locationwire.OperationState_OPERATION_STATE_SUCCEEDED
		}
	}
	outcome.CompletedAt = completedAt()
	return outcome, nil
}

func transmitGeoapify(ctx context.Context, client *http.Client, endpoint string, values url.Values) *locationwire.ProviderExchange {
	requestURL := endpoint + "?" + values.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return failedExchange("build_request", err.Error(), false)
	}
	exchange := &locationwire.ProviderExchange{State: locationwire.OperationState_OPERATION_STATE_TRANSMISSION_STARTED, TransmissionStarted: true}
	response, err := client.Do(request)
	if err != nil {
		return failedExchange("transport", err.Error(), true)
	}
	defer func() { _ = response.Body.Close() }()
	exchange.HttpStatus = int32(response.StatusCode)
	exchange.ProviderRequestId = response.Header.Get("X-Request-Id")
	rawResponse, err := io.ReadAll(io.LimitReader(response.Body, maxRawEvidenceBytes+1))
	if err != nil {
		return failedExchange("read_response", err.Error(), true)
	}
	exchange.ExactResponse = rawResponse
	exchange.State = locationwire.OperationState_OPERATION_STATE_RESPONSE_RETAINED
	if len(rawResponse) > maxRawEvidenceBytes {
		exchange.State = locationwire.OperationState_OPERATION_STATE_FAILED
		exchange.Failure = &locationwire.OperationFailure{Class: "response_too_large", Detail: fmt.Sprintf("response exceeds %d bytes", maxRawEvidenceBytes)}
	} else if response.StatusCode < 200 || response.StatusCode >= 300 {
		exchange.State = locationwire.OperationState_OPERATION_STATE_FAILED
		exchange.Failure = &locationwire.OperationFailure{Class: "http_status", Detail: response.Status}
		if retryAfter := response.Header.Get("Retry-After"); retryAfter != "" {
			exchange.Failure.RetryNotBefore = retryNotBefore(retryAfter, time.Now().UTC())
		}
	}
	return exchange
}

func retryNotBefore(retryAfter string, now time.Time) string {
	if seconds, err := strconv.Atoi(retryAfter); err == nil && seconds >= 0 {
		return now.Add(time.Duration(seconds) * time.Second).Format(time.RFC3339)
	}
	if parsed, err := http.ParseTime(retryAfter); err == nil {
		return parsed.UTC().Format(time.RFC3339)
	}
	return ""
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
		address.Areas = append(address.Areas, &locationwire.NamedArea{Kind: "suburb", Name: properties.Suburb})
	}
	if properties.Municipality != "" {
		address.Areas = append(address.Areas, &locationwire.NamedArea{Kind: "municipality", Name: properties.Municipality})
	}
	return address
}
