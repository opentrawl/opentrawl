package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/place"
	locationwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/location"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type retainedAppleAdapterOutputIdentity struct {
	Input struct {
		AssetID  string `json:"asset_id"`
		Location struct {
			Latitude  float64 `json:"latitude"`
			Longitude float64 `json:"longitude"`
		} `json:"location"`
	} `json:"input"`
	GeneratedAt  string  `json:"generated_at"`
	RadiusMetres float64 `json:"radius_meters"`
}

func reprojectAppleReverseOutcomes(
	outputRoot string,
	legacyLocalIdentifiersByAssetID map[string]string,
	targetCapturesByLocalIdentifier map[string]*targetCaptureIdentity,
) (map[string]*locationwire.AcquireAppleReverseGeocodingEvidenceOutcome, int, int, int, int, int, error) {
	outputPaths, err := retainedAppleOutputPaths(outputRoot)
	if err != nil {
		return nil, 0, 0, 0, 0, 0, err
	}
	outcomes := make(map[string]*locationwire.AcquireAppleReverseGeocodingEvidenceOutcome, len(outputPaths))
	coordinateMatches := 0
	coordinateMismatches := 0
	missingTargetAssets := 0
	floatingPointRoundTripMatches := 0
	for _, outputPath := range outputPaths {
		exactRetainedResponse, err := os.ReadFile(outputPath)
		if err != nil {
			return nil, 0, 0, 0, 0, 0, fmt.Errorf("read retained Apple adapter output: %w", err)
		}
		var identity retainedAppleAdapterOutputIdentity
		if err := json.Unmarshal(exactRetainedResponse, &identity); err != nil {
			return nil, 0, 0, 0, 0, 0, fmt.Errorf("decode retained Apple adapter output identity: %w", err)
		}
		if strings.TrimSpace(identity.Input.AssetID) == "" || !finiteCoordinate(identity.Input.Location.Latitude, identity.Input.Location.Longitude) {
			return nil, 0, 0, 0, 0, 0, errors.New("retained Apple adapter output has an incomplete request identity")
		}
		if identity.RadiusMetres != 150 {
			return nil, 0, 0, 0, 0, 0, errors.New("retained Apple output does not have the audited legacy 150 metre nearby bound")
		}
		localIdentifier, legacyAssetExists := legacyLocalIdentifiersByAssetID[identity.Input.AssetID]
		targetCapture, targetAssetExists := targetCapturesByLocalIdentifier[localIdentifier]
		if !legacyAssetExists {
			return nil, 0, 0, 0, 0, 0, errors.New("retained Apple output names an asset absent from the canonical legacy archive")
		}
		if !targetAssetExists {
			missingTargetAssets++
			continue
		}
		exactCoordinateMatch := coordinatesExactlyEqual(
			identity.Input.Location.Latitude,
			identity.Input.Location.Longitude,
			targetCapture.coordinate.Latitude,
			targetCapture.coordinate.Longitude,
		)
		if !exactCoordinateMatch && !coordinatesDifferOnlyByFloatingPointRoundTrip(
			identity.Input.Location.Latitude,
			identity.Input.Location.Longitude,
			targetCapture.coordinate.Latitude,
			targetCapture.coordinate.Longitude,
		) {
			coordinateMismatches++
			continue
		}
		if !exactCoordinateMatch {
			floatingPointRoundTripMatches++
		}
		completedAt, err := parseRetainedOperationTime(identity.GeneratedAt)
		if err != nil {
			return nil, 0, 0, 0, 0, 0, err
		}
		outcome, err := place.ReprojectRetainedAppleReverseGeocodingEvidence(
			&locationwire.AcquireAppleReverseGeocodingEvidenceRequest{Input: captureLocationInput(targetCapture)},
			exactRetainedResponse,
			completedAt,
		)
		if err != nil {
			return nil, 0, 0, 0, 0, 0, fmt.Errorf("reproject retained Apple reverse-geocoding evidence: %w", err)
		}
		if previous, duplicate := outcomes[targetCapture.assetID]; duplicate {
			if !protoOutcomesEqualIgnoringExactRawFile(previous, outcome) {
				return nil, 0, 0, 0, 0, 0, errors.New("retained Apple outputs conflict for one current target capture identity")
			}
			continue
		}
		outcomes[targetCapture.assetID] = outcome
		coordinateMatches++
	}
	return outcomes, len(outputPaths), coordinateMatches, coordinateMismatches, floatingPointRoundTripMatches, missingTargetAssets, nil
}

func retainedAppleOutputPaths(root string) ([]string, error) {
	paths := make([]string, 0, expectedRetainedAppleOutputs)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("enumerate retained Apple adapter outputs: %w", err)
	}
	sort.Strings(paths)
	return paths, nil
}

func coordinatesExactlyEqual(firstLatitude, firstLongitude, secondLatitude, secondLongitude float64) bool {
	return firstLatitude == secondLatitude && firstLongitude == secondLongitude
}

func coordinatesDifferOnlyByFloatingPointRoundTrip(firstLatitude, firstLongitude, secondLatitude, secondLongitude float64) bool {
	return floatingPointValuesDifferOnlyByRepresentation(firstLatitude, secondLatitude) &&
		floatingPointValuesDifferOnlyByRepresentation(firstLongitude, secondLongitude)
}

func floatingPointValuesDifferOnlyByRepresentation(first, second float64) bool {
	const maximumRepresentableFloat64Steps = 4
	for step := 0; step <= maximumRepresentableFloat64Steps; step++ {
		if first == second {
			return true
		}
		first = math.Nextafter(first, second)
	}
	return false
}

func parseRetainedOperationTime(value string) (*timestamppb.Timestamp, error) {
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil {
		return nil, errors.New("retained provider output has an invalid completion time")
	}
	return timestamppb.New(parsed), nil
}

func protoOutcomesEqualIgnoringExactRawFile(first, second *locationwire.AcquireAppleReverseGeocodingEvidenceOutcome) bool {
	if first == nil || second == nil {
		return false
	}
	return first.GetRequest().GetInput().GetAssetId() == second.GetRequest().GetInput().GetAssetId() &&
		coordinatesExactlyEqual(
			first.GetRequest().GetInput().GetCoordinate().GetLatitude(),
			first.GetRequest().GetInput().GetCoordinate().GetLongitude(),
			second.GetRequest().GetInput().GetCoordinate().GetLatitude(),
			second.GetRequest().GetInput().GetCoordinate().GetLongitude(),
		)
}
