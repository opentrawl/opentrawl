package place

// Coordinate and Address mirror the small provider-response vocabulary used
// by the Apple bridge. They are decoded only at that provider boundary; the
// Photos DAG carries the canonical Protobuf location contract.
type Coordinate struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type Address struct {
	Name                  string   `json:"name,omitempty"`
	Thoroughfare          string   `json:"thoroughfare,omitempty"`
	SubThoroughfare       string   `json:"sub_thoroughfare,omitempty"`
	Locality              string   `json:"locality,omitempty"`
	SubLocality           string   `json:"sub_locality,omitempty"`
	AdministrativeArea    string   `json:"administrative_area,omitempty"`
	SubAdministrativeArea string   `json:"sub_administrative_area,omitempty"`
	PostalCode            string   `json:"postal_code,omitempty"`
	Country               string   `json:"country,omitempty"`
	ISOCountryCode        string   `json:"iso_country_code,omitempty"`
	TimeZone              string   `json:"time_zone,omitempty"`
	AreasOfInterest       []string `json:"areas_of_interest,omitempty"`
	Formatted             string   `json:"formatted,omitempty"`
}
