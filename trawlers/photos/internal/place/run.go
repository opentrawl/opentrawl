package place

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func Run(ctx context.Context, opts Options) (Result, error) {
	input, cached, err := loadInputOrResult(opts.InputPath)
	if err != nil {
		return Result{}, err
	}
	radius := opts.RadiusMeters
	if radius <= 0 {
		radius = defaultRadiusMeters
	}
	if cached != nil {
		if cached.RadiusMeters == 0 {
			cached.RadiusMeters = radius
		}
		NormalizeResult(cached)
		if err := validateComplete(*cached); err != nil {
			return Result{}, err
		}
		cached.Cached = true
		cached.CacheStatus = "hit"
		return *cached, nil
	}
	cacheDir := strings.TrimSpace(opts.CacheDir)
	if cacheDir == "" {
		return Result{}, errors.New("place context cache dir is required")
	}
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return Result{}, err
	}
	cachePath, err := cachePath(cacheDir, input, radius)
	if err != nil {
		return Result{}, err
	}
	if data, err := os.ReadFile(cachePath); err == nil {
		var cached Result
		if err := json.Unmarshal(data, &cached); err == nil {
			NormalizeResult(&cached)
			if err := validateComplete(cached); err == nil {
				cached.Cached = true
				cached.CacheStatus = "hit"
				return cached, nil
			}
		}
	}
	result, err := rawAppleResult(ctx, input, radius)
	if err != nil {
		return Result{}, err
	}
	if result.POITotal == 0 {
		result.POITotal = len(result.POICandidates)
	}
	result.POICandidates = calibrateCandidates(input, radius, result.POICandidates)
	result.CacheStatus = "miss_filled"

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(cachePath, append(data, '\n'), 0o600); err != nil {
		return Result{}, err
	}
	return result, nil
}

func LoadResult(path string) (Result, error) {
	data, err := readInputData(path)
	if err != nil {
		return Result{}, err
	}
	return decodeResult(data)
}

func rawAppleResult(ctx context.Context, input Input, radius float64) (Result, error) {
	result, err := applePlaceContext(ctx, input, radius)
	if err != nil {
		return Result{}, err
	}
	result.Input = input
	result.Provider = "apple"
	result.Source = "apple_corelocation_mapkit"
	result.RadiusMeters = radius
	result.GeneratedAt = time.Now().UTC()
	result.Area = areaFromAddress(result.Address)
	result.POITotal = len(result.POICandidates)
	result.POIStatus = poiStatus(result)
	result.POICandidates = calibrateCandidates(input, radius, result.POICandidates)
	NormalizeResult(&result)
	if err := validateComplete(result); err != nil {
		return Result{}, err
	}
	return result, nil
}

func validateComplete(result Result) error {
	if result.Address == nil {
		return errors.New("apple place context incomplete: missing reverse-geocoded address")
	}
	if err := validatePOIStatus(result.POIStatus); err != nil {
		return err
	}
	return nil
}

func poiStatus(result Result) string {
	if strings.TrimSpace(result.POIStatus) != "" {
		return result.POIStatus
	}
	if len(result.POICandidates) > 0 {
		return POIStatusFound
	}
	return POIStatusNone
}

func validatePOIStatus(status string) error {
	status = strings.TrimSpace(status)
	if status == "" {
		return nil
	}
	switch status {
	case POIStatusFound, POIStatusNone, POIStatusProviderError:
		return nil
	default:
		return fmt.Errorf("invalid poi_status %q", status)
	}
}

func areaFromAddress(address *Address) []AreaLevel {
	if address == nil {
		return nil
	}
	levels := []struct {
		level string
		name  string
	}{
		{"country", address.Country},
		{"administrative_area", address.AdministrativeArea},
		{"sub_administrative_area", address.SubAdministrativeArea},
		{"locality", address.Locality},
		{"sub_locality", address.SubLocality},
	}
	out := []AreaLevel{}
	for _, level := range levels {
		if strings.TrimSpace(level.name) == "" {
			continue
		}
		out = append(out, AreaLevel{
			Level:  level.level,
			Name:   strings.TrimSpace(level.name),
			Source: address.Source,
		})
	}
	return out
}

func calibrateCandidates(input Input, radius float64, candidates []POICandidate) []POICandidate {
	for i := range candidates {
		candidates[i].Category = shortCategory(candidates[i].Category)
	}
	candidates = TierCandidates(input, candidates)
	if len(candidates) > maxCandidates {
		candidates = candidates[:maxCandidates]
	}
	return candidates
}

func NormalizeResult(result *Result) {
	if result == nil {
		return
	}
	for i := range result.POICandidates {
		result.POICandidates[i].Category = shortCategory(result.POICandidates[i].Category)
	}
}

func cachePath(dir string, input Input, radius float64) (string, error) {
	key := roundedCoordinateKey(input, radius)
	return filepath.Join(dir, key+".json"), nil
}

func roundedCoordinateKey(input Input, radius float64) string {
	lat, lon := input.Location.Latitude, input.Location.Longitude
	return strings.NewReplacer("+", "p", "-", "m", ".", "_").Replace(
		fmt.Sprintf("coordinate-latitude%+.4f-longitude%+.4f-radius%.0f", lat, lon, radius),
	)
}

func compactStrings(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}
