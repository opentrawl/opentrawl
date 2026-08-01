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
)

const geoapifyReverseGeocodingEndpoint = "https://api.geoapify.com/v1/geocode/reverse"

func acquireGeoapifyReverseLocationEvidence(ctx context.Context, input Input, apiKeyFilePath string) (FactualLocationEvidence, error) {
	apiKey, err := os.ReadFile(strings.TrimSpace(apiKeyFilePath))
	if err != nil {
		return FactualLocationEvidence{}, fmt.Errorf("read Geoapify API key: %w", err)
	}
	if strings.TrimSpace(string(apiKey)) == "" {
		return FactualLocationEvidence{}, errors.New("Geoapify API key is empty")
	}
	requestURL, err := url.Parse(geoapifyReverseGeocodingEndpoint)
	if err != nil {
		return FactualLocationEvidence{}, err
	}
	query := requestURL.Query()
	query.Set("lat", strconv.FormatFloat(input.Location.Latitude, 'f', -1, 64))
	query.Set("lon", strconv.FormatFloat(input.Location.Longitude, 'f', -1, 64))
	query.Set("format", "geojson")
	query.Set("apiKey", strings.TrimSpace(string(apiKey)))
	requestURL.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return FactualLocationEvidence{}, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return FactualLocationEvidence{}, fmt.Errorf("Geoapify reverse geocode: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	rawResponse, err := io.ReadAll(io.LimitReader(response.Body, maxRawEvidenceBytes+1))
	if err != nil {
		return FactualLocationEvidence{}, fmt.Errorf("read Geoapify reverse response: %w", err)
	}
	if len(rawResponse) > maxRawEvidenceBytes {
		return FactualLocationEvidence{}, fmt.Errorf("Geoapify reverse response exceeds %d bytes", maxRawEvidenceBytes)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return FactualLocationEvidence{}, fmt.Errorf("Geoapify reverse geocode returned HTTP %d", response.StatusCode)
	}
	address, err := parseGeoapifyReverseAddress(rawResponse)
	if err != nil {
		return FactualLocationEvidence{}, err
	}
	return FactualLocationEvidence{
		ProviderIdentity: GeoapifyLocationProviderIdentity,
		Operation:        GeoapifyReverseLocationOperation,
		Address:          address,
		RawResponse:      rawResponse,
	}, nil
}

type geoapifyReverseResponse struct {
	Features []struct {
		Properties struct {
			Name        string `json:"name"`
			Housenumber string `json:"housenumber"`
			Street      string `json:"street"`
			Postcode    string `json:"postcode"`
			District    string `json:"district"`
			City        string `json:"city"`
			County      string `json:"county"`
			State       string `json:"state"`
			Country     string `json:"country"`
			CountryCode string `json:"country_code"`
			Formatted   string `json:"formatted"`
		} `json:"properties"`
	} `json:"features"`
}

func parseGeoapifyReverseAddress(rawResponse []byte) (*Address, error) {
	var response geoapifyReverseResponse
	if err := json.Unmarshal(rawResponse, &response); err != nil {
		return nil, fmt.Errorf("decode Geoapify reverse response: %w", err)
	}
	if len(response.Features) == 0 {
		return nil, nil
	}
	properties := response.Features[0].Properties
	return &Address{
		Name:                  properties.Name,
		Thoroughfare:          properties.Street,
		SubThoroughfare:       properties.Housenumber,
		Locality:              properties.City,
		SubLocality:           properties.District,
		AdministrativeArea:    properties.State,
		SubAdministrativeArea: properties.County,
		PostalCode:            properties.Postcode,
		Country:               properties.Country,
		ISOCountryCode:        strings.ToUpper(properties.CountryCode),
		Formatted:             properties.Formatted,
		Source:                GeoapifyLocationProviderIdentity,
	}, nil
}
