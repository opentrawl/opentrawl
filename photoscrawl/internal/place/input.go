package place

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
)

func loadInput(path string) (Input, error) {
	data, err := readInputData(path)
	if err != nil {
		return Input{}, err
	}
	return decodeInput(data)
}

func loadInputOrResult(path string) (Input, *Result, error) {
	data, err := readInputData(path)
	if err != nil {
		return Input{}, nil, err
	}
	if looksLikeResult(data) {
		result, err := decodeResult(data)
		if err != nil {
			return Input{}, nil, err
		}
		return Input{}, &result, nil
	}
	input, err := decodeInput(data)
	if err != nil {
		return Input{}, nil, err
	}
	return input, nil, nil
}

func readInputData(path string) ([]byte, error) {
	reader, closeReader, err := inputReader(path)
	if err != nil {
		return nil, err
	}
	if closeReader != nil {
		defer closeReader()
	}
	return io.ReadAll(reader)
}

func decodeInput(data []byte) (Input, error) {
	var raw map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		return Input{}, fmt.Errorf("read place input: %w", err)
	}
	input := parseInput(raw)
	return input, validateInput(input)
}

func decodeResult(data []byte) (Result, error) {
	var result Result
	if err := json.Unmarshal(data, &result); err != nil {
		return Result{}, err
	}
	NormalizeResult(&result)
	if result.POITotal == 0 {
		result.POITotal = len(result.POICandidates)
	}
	if err := validatePOIStatus(result.POIStatus); err != nil {
		return Result{}, err
	}
	return result, nil
}

func looksLikeResult(data []byte) bool {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return false
	}
	if _, ok := raw["input"]; !ok {
		return false
	}
	for _, key := range []string{"provider", "source", "address", "map_features", "poi_status", "poi_candidates", "radius_meters"} {
		if _, ok := raw[key]; ok {
			return true
		}
	}
	return false
}

func inputReader(path string) (io.Reader, func(), error) {
	if strings.TrimSpace(path) == "" || strings.TrimSpace(path) == "-" {
		return os.Stdin, nil, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	return file, func() { _ = file.Close() }, nil
}

func parseInput(raw map[string]any) Input {
	input := Input{
		AssetID:        firstString(raw, "asset_id", "assetID", "local_identifier", "localIdentifier"),
		ImagePath:      firstString(raw, "image_path", "imagePath"),
		TakenAt:        firstString(raw, "taken_at", "takenAt", "creation_date", "creationDate"),
		AccuracyMeters: firstFloat(raw, "accuracy_meters", "accuracyMeters", "horizontal_accuracy", "horizontalAccuracy"),
	}
	if location, ok := objectValue(raw, "location"); ok {
		input.Location = coordinateFrom(location)
		if input.AccuracyMeters == 0 {
			input.AccuracyMeters = firstFloat(location, "accuracy_meters", "accuracyMeters", "horizontal_accuracy", "horizontalAccuracy")
		}
	}
	if asset, ok := objectValue(raw, "asset"); ok {
		if input.AssetID == "" {
			input.AssetID = firstString(asset, "local_identifier", "localIdentifier", "asset_id", "assetID")
		}
		if input.TakenAt == "" {
			input.TakenAt = firstString(asset, "creation_date", "creationDate", "taken_at", "takenAt")
		}
		if location, ok := objectValue(asset, "location"); ok {
			input.Location = coordinateFrom(location)
			if input.AccuracyMeters == 0 {
				input.AccuracyMeters = firstFloat(location, "accuracy_meters", "accuracyMeters", "horizontal_accuracy", "horizontalAccuracy")
			}
		}
	}
	if input.Location.Latitude == 0 && input.Location.Longitude == 0 {
		input.Location = Coordinate{
			Latitude:  firstFloat(raw, "latitude", "lat"),
			Longitude: firstFloat(raw, "longitude", "lon", "lng"),
		}
	}
	return input
}

func validateInput(input Input) error {
	if math.IsNaN(input.Location.Latitude) || math.IsNaN(input.Location.Longitude) {
		return errors.New("latitude and longitude must be finite")
	}
	if input.Location.Latitude < -90 || input.Location.Latitude > 90 {
		return fmt.Errorf("latitude out of range: %f", input.Location.Latitude)
	}
	if input.Location.Longitude < -180 || input.Location.Longitude > 180 {
		return fmt.Errorf("longitude out of range: %f", input.Location.Longitude)
	}
	if input.Location.Latitude == 0 && input.Location.Longitude == 0 {
		return errors.New("latitude and longitude are required")
	}
	return nil
}

func coordinateFrom(raw map[string]any) Coordinate {
	return Coordinate{
		Latitude:  firstFloat(raw, "latitude", "lat"),
		Longitude: firstFloat(raw, "longitude", "lon", "lng"),
	}
}

func objectValue(raw map[string]any, key string) (map[string]any, bool) {
	value, ok := raw[key]
	if !ok {
		return nil, false
	}
	object, ok := value.(map[string]any)
	return object, ok
}

func firstString(raw map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := raw[key].(string); ok {
			value = strings.TrimSpace(value)
			if value != "" {
				return value
			}
		}
	}
	return ""
}

func firstFloat(raw map[string]any, keys ...string) float64 {
	for _, key := range keys {
		value, ok := raw[key]
		if !ok {
			continue
		}
		switch v := value.(type) {
		case float64:
			return v
		case json.Number:
			f, err := v.Float64()
			if err == nil {
				return f
			}
		case string:
			var f float64
			if _, err := fmt.Sscanf(strings.TrimSpace(v), "%f", &f); err == nil {
				return f
			}
		}
	}
	return 0
}
