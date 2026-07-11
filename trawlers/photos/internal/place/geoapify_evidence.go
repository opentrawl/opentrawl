package place

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
)

const (
	geoapifyReverseOperation = "osm_reverse"
	geoapifyNearbyOperation  = "osm_nearby"
	redactedTransportFailure = "configured OSM provider transport failed"
	redactedResponseFailure  = "configured OSM provider response contained the credential and was discarded"
)

func validateConfiguredGeoapify(config ConfiguredGeoapifyEvidence) error {
	if strings.TrimSpace(config.ProviderIdentity) == "" {
		return errors.New("configured OSM provider identity is required")
	}
	reverse, err := configuredEvidenceURL(config.ReverseEndpoint)
	if err != nil {
		return fmt.Errorf("configured OSM reverse endpoint: %w", err)
	}
	nearby, err := configuredEvidenceURL(config.NearbyEndpoint)
	if err != nil {
		return fmt.Errorf("configured OSM nearby endpoint: %w", err)
	}
	if strings.TrimSpace(config.CredentialReference) == "" {
		return errors.New("configured OSM credential reference is required")
	}
	if strings.TrimSpace(config.CredentialParameter) == "" {
		return errors.New("configured OSM credential parameter is required")
	}
	if !validEvidenceQueryParameter(config.CredentialParameter) {
		return errors.New("configured OSM credential parameter is invalid")
	}
	if reverse.RawQuery != "" || reverse.ForceQuery {
		return errors.New("configured OSM reverse endpoint must not contain a query string")
	}
	if nearby.RawQuery != "" || nearby.ForceQuery {
		return errors.New("configured OSM nearby endpoint must not contain a query string")
	}
	if strings.TrimSpace(config.Credential) == "" {
		return errors.New("configured OSM credential is unavailable")
	}
	if len(config.NearbyCategories) == 0 {
		return errors.New("configured OSM nearby categories are required")
	}
	for _, category := range config.NearbyCategories {
		category = strings.TrimSpace(category)
		if category == "" || strings.Contains(category, ",") {
			return errors.New("configured OSM nearby category is invalid")
		}
	}
	if config.ReverseLimit <= 0 || config.NearbyLimit <= 0 {
		return errors.New("configured OSM reverse and nearby limits must be greater than 0")
	}
	if config.HTTPClient == nil {
		return errors.New("configured OSM HTTP client is required")
	}
	return nil
}

func validEvidenceQueryParameter(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && !strings.ContainsAny(value, "&=?# \t\r\n")
}

func captureGeoapifyReverse(ctx context.Context, opts EvidenceOptions) evidenceCapture {
	query := url.Values{
		"format": {"geojson"},
		"lat":    {formatEvidenceCoordinate(opts.Input.Location.Latitude)},
		"limit":  {strconv.Itoa(opts.Geoapify.ReverseLimit)},
		"lon":    {formatEvidenceCoordinate(opts.Input.Location.Longitude)},
	}
	return captureGeoapify(ctx, opts, geoapifyReverseOperation, opts.Geoapify.ReverseEndpoint, query)
}

func captureGeoapifyNearby(ctx context.Context, opts EvidenceOptions) evidenceCapture {
	centre := formatEvidenceCoordinate(opts.Input.Location.Longitude) + "," + formatEvidenceCoordinate(opts.Input.Location.Latitude)
	categories := make([]string, 0, len(opts.Geoapify.NearbyCategories))
	for _, category := range opts.Geoapify.NearbyCategories {
		categories = append(categories, strings.TrimSpace(category))
	}
	query := url.Values{
		"bias":       {"proximity:" + centre},
		"categories": {strings.Join(categories, ",")},
		"filter":     {"circle:" + centre + "," + strconv.FormatFloat(opts.RadiusMeters, 'f', -1, 64)},
		"limit":      {strconv.Itoa(opts.Geoapify.NearbyLimit)},
	}
	return captureGeoapify(ctx, opts, geoapifyNearbyOperation, opts.Geoapify.NearbyEndpoint, query)
}

func captureGeoapify(ctx context.Context, opts EvidenceOptions, operation, endpoint string, query url.Values) evidenceCapture {
	provider := strings.TrimSpace(opts.Geoapify.ProviderIdentity)
	credentialReference := strings.TrimSpace(opts.Geoapify.CredentialReference)
	request, preAuth, err := geoapifyRequest(ctx, endpoint, query)
	if err != nil {
		return stoppedCapture(opts.Input, provider, operation, opts.CoordinateVariant, credentialReference, nil, []byte(err.Error()), 0, parsedEvidence{}, err)
	}
	if cached, ok := cachedCapture(opts.CacheDir, provider, operation, opts.CoordinateVariant, credentialReference, preAuth, opts.Input, parseGeoapifyEvidence); ok {
		return cached
	}
	authenticated := request.Clone(ctx)
	authenticated.URL = cloneURL(request.URL)
	authQuery := authenticated.URL.Query()
	authQuery.Set(strings.TrimSpace(opts.Geoapify.CredentialParameter), opts.Geoapify.Credential)
	authenticated.URL.RawQuery = authQuery.Encode()
	response, transportErr := opts.Geoapify.HTTPClient.Do(authenticated)
	if transportErr != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		err := errors.New(redactedTransportFailure)
		return stoppedCapture(opts.Input, provider, operation, opts.CoordinateVariant, credentialReference, preAuth, []byte(redactedTransportFailure), 0, parsedEvidence{}, err)
	}
	raw, readErr := readBoundedResponse(response)
	if responseContainsCredential(raw, opts.Geoapify.Credential) {
		err := errors.New(redactedResponseFailure)
		return stoppedCapture(opts.Input, provider, operation, opts.CoordinateVariant, credentialReference, preAuth, []byte(redactedResponseFailure), response.StatusCode, parsedEvidence{}, err)
	}
	if readErr != nil {
		err := errors.New("configured OSM provider response read failed")
		if errors.Is(readErr, errRawEvidenceTooLarge) {
			err = errRawEvidenceTooLarge
		}
		return stoppedCapture(opts.Input, provider, operation, opts.CoordinateVariant, credentialReference, preAuth, raw, response.StatusCode, parsedEvidence{}, err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		err := fmt.Errorf("configured OSM provider returned HTTP %d", response.StatusCode)
		return stoppedCapture(opts.Input, provider, operation, opts.CoordinateVariant, credentialReference, preAuth, raw, response.StatusCode, parsedEvidence{}, err)
	}
	parsed, parseErr := parseGeoapifyEvidence(raw, response.StatusCode, opts.Input)
	if parseErr != nil {
		return stoppedCapture(opts.Input, provider, operation, opts.CoordinateVariant, credentialReference, preAuth, raw, response.StatusCode, parsed, parseErr)
	}
	return completeCapture(opts.Input, provider, operation, opts.CoordinateVariant, credentialReference, preAuth, raw, response.StatusCode, parsed)
}

func geoapifyRequest(ctx context.Context, endpoint string, query url.Values) (*http.Request, []byte, error) {
	parsed, err := configuredEvidenceURL(endpoint)
	if err != nil {
		return nil, nil, err
	}
	values := parsed.Query()
	for key, entries := range query {
		values.Del(key)
		for _, entry := range entries {
			values.Add(key, entry)
		}
	}
	parsed.RawQuery = values.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, nil, err
	}
	request.Header.Set("Accept", "application/json")
	preAuth, err := httputil.DumpRequest(request, false)
	if err != nil {
		return nil, nil, err
	}
	return request, preAuth, nil
}

func configuredEvidenceURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, errors.New("endpoint is invalid")
	}
	if parsed.Scheme != "https" || parsed.Hostname() == "" {
		return nil, errors.New("endpoint must use HTTPS and include a host")
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return nil, errors.New("endpoint contains unsafe URL components")
	}
	return parsed, nil
}

func responseContainsCredential(raw []byte, credential string) bool {
	credential = strings.TrimSpace(credential)
	if credential == "" {
		return false
	}
	patterns := []string{credential, url.QueryEscape(credential), url.PathEscape(credential)}
	for _, pattern := range patterns {
		if pattern != "" && strings.Contains(string(raw), pattern) {
			return true
		}
	}
	return false
}

func cloneURL(source *url.URL) *url.URL {
	clone := *source
	return &clone
}

func formatEvidenceCoordinate(value float64) string {
	return strconv.FormatFloat(value, 'f', 7, 64)
}

type geoapifyEvidenceCollection struct {
	Features []geoapifyEvidenceFeature `json:"features"`
}

type geoapifyEvidenceFeature struct {
	ID         string                     `json:"id"`
	Properties geoapifyEvidenceProperties `json:"properties"`
	Geometry   geoapifyEvidenceGeometry   `json:"geometry"`
}

type geoapifyEvidenceProperties struct {
	PlaceID      string   `json:"place_id"`
	Name         string   `json:"name"`
	Formatted    string   `json:"formatted"`
	HouseNumber  string   `json:"housenumber"`
	Street       string   `json:"street"`
	Suburb       string   `json:"suburb"`
	City         string   `json:"city"`
	County       string   `json:"county"`
	State        string   `json:"state"`
	Postcode     string   `json:"postcode"`
	Country      string   `json:"country"`
	CountryCode  string   `json:"country_code"`
	Category     string   `json:"category"`
	Categories   []string `json:"categories"`
	Distance     float64  `json:"distance"`
	TimezoneName string   `json:"timezone_name"`
	Timezone     struct {
		Name string `json:"name"`
	} `json:"timezone"`
}

type geoapifyEvidenceGeometry struct {
	Coordinates []float64 `json:"coordinates"`
}

func parseGeoapifyEvidence(raw []byte, status int, input Input) (parsedEvidence, error) {
	if status < 200 || status >= 300 {
		return parsedEvidence{}, fmt.Errorf("configured OSM provider returned HTTP %d", status)
	}
	if len(raw) == 0 {
		return parsedEvidence{}, errors.New("configured OSM provider returned an empty response")
	}
	var collection geoapifyEvidenceCollection
	if err := json.Unmarshal(raw, &collection); err != nil {
		return parsedEvidence{}, fmt.Errorf("parse raw configured OSM response: %w", err)
	}
	if len(collection.Features) == 0 {
		return parsedEvidence{}, ErrProviderNoResult
	}
	parsed := parsedEvidence{}
	useful := 0
	for index, feature := range collection.Features {
		candidate, err := geoapifyEvidenceCandidate(index, feature, input)
		if err != nil {
			return parsed, err
		}
		if strings.TrimSpace(candidate.Name) != "" || candidate.Address != nil && strings.TrimSpace(candidate.Address.Formatted) != "" {
			useful++
		}
		parsed.candidates = append(parsed.candidates, candidate)
	}
	sortEvidenceCandidates(parsed.candidates)
	if useful == 0 {
		return parsed, errors.New("configured OSM provider returned no named or formatted candidates")
	}
	return parsed, nil
}

func geoapifyEvidenceCandidate(index int, feature geoapifyEvidenceFeature, input Input) (EvidenceCandidate, error) {
	if len(feature.Geometry.Coordinates) < 2 {
		return EvidenceCandidate{}, fmt.Errorf("configured OSM feature %d has no complete coordinate", index)
	}
	coordinate := &Coordinate{Latitude: feature.Geometry.Coordinates[1], Longitude: feature.Geometry.Coordinates[0]}
	if err := validateInput(Input{Location: *coordinate}); err != nil {
		return EvidenceCandidate{}, fmt.Errorf("configured OSM feature %d coordinate: %w", index, err)
	}
	properties := feature.Properties
	categories := append([]string(nil), properties.Categories...)
	if len(categories) == 0 && strings.TrimSpace(properties.Category) != "" {
		categories = append(categories, strings.TrimSpace(properties.Category))
	}
	distance := properties.Distance
	if distance <= 0 {
		distance = metersBetween(input.Location, *coordinate)
	}
	providerID := strings.TrimSpace(properties.PlaceID)
	if providerID == "" {
		providerID = strings.TrimSpace(feature.ID)
	}
	return EvidenceCandidate{
		ProviderIndex: index,
		ProviderID:    providerID,
		Name:          strings.TrimSpace(properties.Name),
		Categories:    categories,
		Coordinate:    coordinate,
		DistanceM:     distance,
		Address:       geoapifyEvidenceAddress(properties),
		Source:        "configured_osm_response",
	}, nil
}

func geoapifyEvidenceAddress(properties geoapifyEvidenceProperties) *Address {
	formatted := strings.TrimSpace(properties.Formatted)
	if formatted == "" && strings.TrimSpace(properties.Country) == "" {
		return nil
	}
	timezone := strings.TrimSpace(properties.TimezoneName)
	if timezone == "" {
		timezone = strings.TrimSpace(properties.Timezone.Name)
	}
	return &Address{
		Name:                  strings.TrimSpace(properties.Name),
		Thoroughfare:          strings.TrimSpace(properties.Street),
		SubThoroughfare:       strings.TrimSpace(properties.HouseNumber),
		Locality:              strings.TrimSpace(properties.City),
		SubLocality:           strings.TrimSpace(properties.Suburb),
		AdministrativeArea:    strings.TrimSpace(properties.State),
		SubAdministrativeArea: strings.TrimSpace(properties.County),
		PostalCode:            strings.TrimSpace(properties.Postcode),
		Country:               strings.TrimSpace(properties.Country),
		ISOCountryCode:        strings.ToUpper(strings.TrimSpace(properties.CountryCode)),
		TimeZone:              timezone,
		Formatted:             formatted,
		Source:                "configured_osm_response",
	}
}
