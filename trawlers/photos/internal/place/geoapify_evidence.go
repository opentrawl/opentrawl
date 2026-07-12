package place

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const (
	geoapifyReverseOperation = "osm_reverse"
	geoapifyNearbyOperation  = "osm_nearby"
	redactedTransportFailure = "configured OSM provider transport failed"
	redactedResponseFailure  = "configured OSM provider response contained the credential and was discarded"
)

func validateConfiguredGeoapify(config ConfiguredGeoapifyEvidence) error {
	if err := validateConfiguredGeoapifyShape(config); err != nil {
		return err
	}
	if strings.TrimSpace(config.Credential) == "" {
		return errors.New("configured OSM credential is unavailable")
	}
	if config.HTTPClient == nil {
		return errors.New("configured OSM HTTP client is required")
	}
	return nil
}

func ensurePrivateOutputRoot(path string) error {
	clean := filepath.Clean(strings.TrimSpace(path))
	if clean == "." || !filepath.IsAbs(clean) {
		return errors.New("private output root must be an absolute path")
	}
	info, err := os.Lstat(clean)
	if err != nil {
		return fmt.Errorf("private output root: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("private output root must be a non-symlink directory")
	}
	if info.Mode().Perm() != 0o700 {
		return errors.New("private output root permissions must be 0700")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Getuid() {
		return errors.New("private output root must be owned by the current user")
	}
	for current := clean; ; current = filepath.Dir(current) {
		if _, err := os.Lstat(filepath.Join(current, ".git")); err == nil {
			return errors.New("private output root must be outside a repository or worktree")
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
	}
	return nil
}

func ensurePrivateInputFile(path string) error {
	clean := filepath.Clean(strings.TrimSpace(path))
	if !filepath.IsAbs(clean) {
		return errors.New("private input file must use an absolute path")
	}
	if err := ensurePrivateOutputRoot(filepath.Dir(clean)); err != nil {
		return err
	}
	info, err := os.Lstat(clean)
	if err != nil {
		return fmt.Errorf("private input file: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return errors.New("private input file must be a non-symlink regular file with permissions 0600")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Getuid() {
		return errors.New("private input file must be owned by the current user")
	}
	return nil
}

func ValidatePrivateEvidenceInputFile(path string) error {
	return ensurePrivateInputFile(path)
}

func ensurePrivateEvidenceCacheRoot(path string) error {
	clean := filepath.Clean(strings.TrimSpace(path))
	if _, err := os.Lstat(clean); os.IsNotExist(err) {
		if err := ensurePrivateOutputRoot(filepath.Dir(clean)); err != nil {
			return err
		}
		if err := os.Mkdir(clean, 0o700); err != nil {
			return fmt.Errorf("private evidence cache root: %w", err)
		}
	}
	return ensurePrivateOutputRoot(clean)
}

func writePrivateFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".place-evidence-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func validateConfiguredGeoapifyShape(config ConfiguredGeoapifyEvidence) error {
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
	return nil
}

func validEvidenceQueryParameter(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && !strings.ContainsAny(value, "&=?# \t\r\n")
}

func captureGeoapifyReverse(ctx context.Context, opts EvidenceOptions) evidenceCapture {
	query := geoapifyReverseQuery(opts.Input, opts.Geoapify.ReverseLimit)
	return captureGeoapify(ctx, opts, geoapifyReverseOperation, opts.Geoapify.ReverseEndpoint, query, opts.Geoapify.ReverseLimit)
}

func geoapifyReverseQuery(input Input, limit int) url.Values {
	return url.Values{
		"format": {"geojson"},
		"lat":    {formatEvidenceCoordinate(input.Location.Latitude)},
		"limit":  {strconv.Itoa(limit)},
		"lon":    {formatEvidenceCoordinate(input.Location.Longitude)},
	}
}

func captureGeoapifyNearby(ctx context.Context, opts EvidenceOptions) evidenceCapture {
	query := geoapifyNearbyQuery(opts.Input, opts.RadiusMeters, opts.Geoapify.NearbyCategories, opts.Geoapify.NearbyLimit)
	return captureGeoapify(ctx, opts, geoapifyNearbyOperation, opts.Geoapify.NearbyEndpoint, query, opts.Geoapify.NearbyLimit)
}

func geoapifyNearbyQuery(input Input, radius float64, configuredCategories []string, limit int) url.Values {
	centre := formatEvidenceCoordinate(input.Location.Longitude) + "," + formatEvidenceCoordinate(input.Location.Latitude)
	categories := make([]string, 0, len(configuredCategories))
	for _, category := range configuredCategories {
		categories = append(categories, strings.TrimSpace(category))
	}
	return url.Values{
		"bias":       {"proximity:" + centre},
		"categories": {strings.Join(categories, ",")},
		"filter":     {"circle:" + centre + "," + strconv.FormatFloat(radius, 'f', -1, 64)},
		"limit":      {strconv.Itoa(limit)},
	}
}

func captureGeoapify(ctx context.Context, opts EvidenceOptions, operation, endpoint string, query url.Values, requestedLimit int) evidenceCapture {
	provider := strings.TrimSpace(opts.Geoapify.ProviderIdentity)
	credentialReference := strings.TrimSpace(opts.Geoapify.CredentialReference)
	parser := parseGeoapifyEvidenceAtLimit(requestedLimit)
	request, preAuth, err := geoapifyRequest(ctx, endpoint, query)
	if err != nil {
		failed := fmt.Errorf("%w: %v", errEvidenceFailed, err)
		return stoppedCapture(opts.Input, provider, operation, opts.CoordinateVariant, credentialReference, nil, []byte(err.Error()), 0, parsedEvidence{}, failed)
	}
	if cached, found, cacheErr := checkedCachedCapture(opts.CacheDir, provider, operation, opts.CoordinateVariant, credentialReference, opts.Geoapify.Credential, preAuth, opts.Input, parser); found {
		if cacheErr != nil {
			if errors.Is(cacheErr, errEvidenceCredential) {
				return discardedCredentialCapture(opts.Input, provider, operation, opts.CoordinateVariant, credentialReference, preAuth, 0)
			}
			return stoppedCapture(opts.Input, provider, operation, opts.CoordinateVariant, credentialReference, preAuth, nil, 0, parsedEvidence{}, cacheErr)
		}
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
		if errors.Is(transportErr, errEvidenceRateLimited) {
			return stoppedCapture(opts.Input, provider, operation, opts.CoordinateVariant, credentialReference, preAuth, nil, 0, parsedEvidence{}, errEvidenceRateLimited)
		}
		err := fmt.Errorf("%w: %s", errEvidenceFailed, redactedTransportFailure)
		return stoppedCapture(opts.Input, provider, operation, opts.CoordinateVariant, credentialReference, preAuth, []byte(redactedTransportFailure), 0, parsedEvidence{}, err)
	}
	rawHeaders, headerErr := httputil.DumpResponse(response, false)
	raw, readErr := readBoundedResponse(response)
	if responseContainsCredential(rawHeaders, opts.Geoapify.Credential) || responseContainsCredential(raw, opts.Geoapify.Credential) {
		return discardedCredentialCapture(opts.Input, provider, operation, opts.CoordinateVariant, credentialReference, preAuth, response.StatusCode)
	}
	if headerErr != nil {
		err := fmt.Errorf("%w: configured OSM provider headers were incomplete", errEvidenceMalformed)
		return stoppedCapture(opts.Input, provider, operation, opts.CoordinateVariant, credentialReference, preAuth, raw, response.StatusCode, parsedEvidence{}, err)
	}
	if readErr != nil {
		err := fmt.Errorf("%w: configured OSM provider response read failed", errEvidenceFailed)
		if errors.Is(readErr, errRawEvidenceTooLarge) {
			err = errRawEvidenceTooLarge
		}
		return attachRawHeaders(stoppedCapture(opts.Input, provider, operation, opts.CoordinateVariant, credentialReference, preAuth, raw, response.StatusCode, parsedEvidence{}, err), rawHeaders)
	}
	if response.StatusCode == http.StatusPaymentRequired {
		return attachRawHeaders(stoppedCapture(opts.Input, provider, operation, opts.CoordinateVariant, credentialReference, preAuth, raw, response.StatusCode, parsedEvidence{}, errEvidenceBilling), rawHeaders)
	}
	if response.StatusCode == http.StatusTooManyRequests {
		return attachRawHeaders(stoppedCapture(opts.Input, provider, operation, opts.CoordinateVariant, credentialReference, preAuth, raw, response.StatusCode, parsedEvidence{}, errEvidenceRateLimited), rawHeaders)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		err := fmt.Errorf("%w: configured OSM provider returned HTTP %d", errEvidenceFailed, response.StatusCode)
		return attachRawHeaders(stoppedCapture(opts.Input, provider, operation, opts.CoordinateVariant, credentialReference, preAuth, raw, response.StatusCode, parsedEvidence{}, err), rawHeaders)
	}
	parsed, parseErr := parser(raw, response.StatusCode, opts.Input)
	if parseErr != nil {
		return attachRawHeaders(stoppedCapture(opts.Input, provider, operation, opts.CoordinateVariant, credentialReference, preAuth, raw, response.StatusCode, parsed, parseErr), rawHeaders)
	}
	return attachRawHeaders(completeCapture(opts.Input, provider, operation, opts.CoordinateVariant, credentialReference, preAuth, raw, response.StatusCode, parsed), rawHeaders)
}

func parseGeoapifyEvidenceAtLimit(requestedLimit int) evidenceParser {
	return func(raw []byte, status int, input Input) (parsedEvidence, error) {
		parsed, err := parseGeoapifyEvidence(raw, status, input)
		if err != nil {
			return parsed, err
		}
		if len(parsed.candidates) >= requestedLimit {
			return parsed, errEvidenceSaturated
		}
		return parsed, nil
	}
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
		return parsedEvidence{}, fmt.Errorf("%w: configured OSM provider returned an empty response", errEvidenceEmpty)
	}
	var collection geoapifyEvidenceCollection
	if err := json.Unmarshal(raw, &collection); err != nil {
		return parsedEvidence{}, fmt.Errorf("%w: parse raw configured OSM response: %v", errEvidenceMalformed, err)
	}
	if len(collection.Features) == 0 {
		return parsedEvidence{}, ErrProviderNoResult
	}
	parsed := parsedEvidence{}
	useful := 0
	for index, feature := range collection.Features {
		candidate, err := geoapifyEvidenceCandidate(index, feature, input)
		if err != nil {
			return parsed, fmt.Errorf("%w: %v", errEvidenceMalformed, err)
		}
		if strings.TrimSpace(candidate.Name) != "" || candidate.Address != nil && strings.TrimSpace(candidate.Address.Formatted) != "" {
			useful++
		}
		parsed.candidates = append(parsed.candidates, candidate)
	}
	sortEvidenceCandidates(parsed.candidates)
	if useful == 0 {
		return parsed, fmt.Errorf("%w: configured OSM provider returned no named or formatted candidates", errEvidenceMalformed)
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
