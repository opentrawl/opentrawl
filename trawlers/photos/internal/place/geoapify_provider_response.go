package place

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	locationwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/location"
)

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
	Datasource    struct {
		SourceName  string `json:"sourcename"`
		Attribution string `json:"attribution"`
		License     string `json:"license"`
		URL         string `json:"url"`
	} `json:"datasource"`
	Timezone struct {
		Name string `json:"name"`
	} `json:"timezone"`
}

func parseGeoapifyReverseGeocodingResponse(rawResponse []byte, maximumResults int32) (*locationwire.AddressHierarchy, []*locationwire.LocationEvidenceAttribution, error) {
	var response geoapifyResponse
	if err := json.Unmarshal(rawResponse, &response); err != nil {
		return nil, nil, fmt.Errorf("decode Geoapify reverse response: %w", err)
	}
	if len(response.Features) > int(maximumResults) {
		return nil, nil, errors.New("Geoapify returned more reverse-geocoding results than requested")
	}
	if len(response.Features) == 0 {
		return nil, nil, nil
	}
	properties := response.Features[0].Properties
	return geoapifyAddress(properties), geoapifyAttributions([]geoapifyProperties{properties}), nil
}

func parseGeoapifyCandidates(rawResponse []byte, maximum int32) ([]*locationwire.PlaceCandidate, []*locationwire.LocationEvidenceAttribution, error) {
	var response geoapifyResponse
	if err := json.Unmarshal(rawResponse, &response); err != nil {
		return nil, nil, fmt.Errorf("decode Geoapify nearby response: %w", err)
	}
	if len(response.Features) > int(maximum) {
		return nil, nil, errors.New("Geoapify returned more candidates than requested")
	}
	candidates := make([]*locationwire.PlaceCandidate, 0, len(response.Features))
	seenProviderReferences := make(map[string]struct{}, len(response.Features))
	propertiesForAttribution := make([]geoapifyProperties, 0, len(response.Features))
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
		propertiesForAttribution = append(propertiesForAttribution, feature.Properties)
	}
	return candidates, geoapifyAttributions(propertiesForAttribution), nil
}

func geoapifyAttributions(propertiesList []geoapifyProperties) []*locationwire.LocationEvidenceAttribution {
	providerAttributions := make([]*locationwire.LocationEvidenceAttribution, 0, len(propertiesList))
	seenProviderAttributions := make(map[string]struct{}, len(propertiesList))
	for _, properties := range propertiesList {
		dataSource := properties.Datasource
		providerAttribution := &locationwire.LocationEvidenceAttribution{
			ProviderName:      "Geoapify",
			DataSourceName:    strings.TrimSpace(dataSource.SourceName),
			DataSourceCredit:  strings.TrimSpace(dataSource.Attribution),
			DataSourceLicense: strings.TrimSpace(dataSource.License),
			DataSourceUrl:     strings.TrimSpace(dataSource.URL),
		}
		attributionIdentity := strings.Join([]string{providerAttribution.GetDataSourceName(), providerAttribution.GetDataSourceCredit(), providerAttribution.GetDataSourceLicense(), providerAttribution.GetDataSourceUrl()}, "\x00")
		if attributionIdentity == "\x00\x00\x00" {
			continue
		}
		if _, alreadyRetained := seenProviderAttributions[attributionIdentity]; alreadyRetained {
			continue
		}
		seenProviderAttributions[attributionIdentity] = struct{}{}
		providerAttributions = append(providerAttributions, providerAttribution)
	}
	return providerAttributions
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
