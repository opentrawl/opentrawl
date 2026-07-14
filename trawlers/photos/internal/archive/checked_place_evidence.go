package archive

import (
	"errors"
	"path/filepath"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/cardinput"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/place"
)

func checkedPlaceEvidence(cacheDir string, input classifyInput, operations []place.CheckedOperation) ([]place.EvidenceRecord, bool) {
	if !input.HasLocation {
		return nil, true
	}
	records, err := place.LoadCheckedEvidence(filepath.Join(cacheDir, "place-evidence"), place.Input{
		AssetID:        input.AssetID,
		TakenAt:        input.CreationDate,
		Location:       place.Coordinate{Latitude: input.Latitude, Longitude: input.Longitude},
		AccuracyMeters: input.AccuracyMeters,
	}, operations)
	return records, err == nil
}

func isPlaceEvidenceError(err error) bool {
	return errors.Is(err, cardinput.ErrMissingEvidence) || errors.Is(err, cardinput.ErrEvidenceMismatch) ||
		errors.Is(err, cardinput.ErrIncompleteEvidence) || errors.Is(err, cardinput.ErrMalformedEvidence) ||
		errors.Is(err, cardinput.ErrUnsafeEvidence)
}

func cardPlaceOperationNames(operations []place.CheckedOperation) []string {
	names := make([]string, 0, len(operations))
	for _, operation := range operations {
		names = append(names, operation.Operation)
	}
	return names
}
