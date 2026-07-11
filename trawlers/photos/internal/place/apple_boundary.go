package place

import (
	"encoding/json"
)

type appleBoundaryOutput struct {
	Request  []byte
	Response []byte
	Err      error
}

func appleRequestJSON(input Input, radius float64) ([]byte, error) {
	request := struct {
		Latitude     float64 `json:"latitude"`
		Longitude    float64 `json:"longitude"`
		RadiusMeters float64 `json:"radius_meters"`
	}{
		Latitude:     input.Location.Latitude,
		Longitude:    input.Location.Longitude,
		RadiusMeters: radius,
	}
	return json.Marshal(request)
}
