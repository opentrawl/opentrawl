package cardinput

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/place"
	cardwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/card/v1"
)

func projectEvidence(source SourceFacts, records []place.EvidenceRecord) ([]*cardwire.PlaceProjection, error) {
	if len(records) != len(source.RequiredPlaceOperations) {
		return nil, fmt.Errorf("%w: got %d records for %d operations", ErrMissingEvidence, len(records), len(source.RequiredPlaceOperations))
	}
	projections := make([]*cardwire.PlaceProjection, 0, len(records))
	for index, record := range records {
		operation := source.RequiredPlaceOperations[index]
		if record.Operation != operation {
			return nil, fmt.Errorf("%w: record %d operation %q, want %q", ErrEvidenceMismatch, index, record.Operation, operation)
		}
		if err := validateEvidenceInput(source, record); err != nil {
			return nil, fmt.Errorf("record %d: %w", index, err)
		}
		if err := validateCompleteRecord(record); err != nil {
			return nil, fmt.Errorf("record %d: %w", index, err)
		}
		projections = append(projections, projectRecord(index, record))
	}
	return projections, nil
}

// ValidateModelVisibleCandidates keeps each checked place projection within
// the fixed model-input evidence bound without changing candidate order.
func ValidateModelVisibleCandidates(input *cardwire.CardInput) error {
	for index, projection := range input.GetPlaces() {
		if len(projection.GetCandidates()) > MaxModelCandidatesPerProjection {
			return fmt.Errorf("%w: place projection %d has %d candidates, maximum is %d", ErrUnsafeEvidence, index+1, len(projection.GetCandidates()), MaxModelCandidatesPerProjection)
		}
	}
	return nil
}

func validateEvidenceInput(source SourceFacts, record place.EvidenceRecord) error {
	if source.Location == nil {
		return fmt.Errorf("%w: evidence has no source location", ErrEvidenceMismatch)
	}
	wantAccuracy := -1.0
	if source.Location.HorizontalAccuracyMeters != nil {
		wantAccuracy = *source.Location.HorizontalAccuracyMeters
	}
	if record.Input.AssetID != source.AssetID || record.Input.TakenAt != source.CaptureTime || record.Input.Location.Latitude != source.Location.Latitude || record.Input.Location.Longitude != source.Location.Longitude || record.Input.AccuracyMeters != wantAccuracy {
		return ErrEvidenceMismatch
	}
	return nil
}

func validateCompleteRecord(record place.EvidenceRecord) error {
	if record.CompletionState != "complete" {
		state := strings.TrimSpace(record.CompletionState)
		if state == "" {
			state = "empty"
		}
		return fmt.Errorf("%w: state %s", ErrIncompleteEvidence, state)
	}
	if record.StopReason != "" || record.StopDetail != "" || record.ProviderErrorClass != "" {
		return fmt.Errorf("%w: complete record carries stop or provider failure fields", ErrUnsafeEvidence)
	}
	if strings.TrimSpace(record.ProviderIdentity) == "" || strings.TrimSpace(record.Operation) == "" || strings.TrimSpace(record.CoordinateVariant) == "" || strings.TrimSpace(record.ParserVersion) == "" {
		return fmt.Errorf("%w: record identity is incomplete", ErrMalformedEvidence)
	}
	if !validEvidenceSelectionPolicy(record.SelectionPolicy, len(record.Candidates)) {
		return fmt.Errorf("%w: selection policy is incomplete or contradictory", ErrUnsafeEvidence)
	}
	if !validDigest(record.PreAuthRequestSHA256) || !validDigest(record.RawResponseSHA256) {
		return fmt.Errorf("%w: request or response digest is invalid", ErrUnsafeEvidence)
	}
	if record.RawHeadersSHA256 != "" && !validDigest(record.RawHeadersSHA256) {
		return fmt.Errorf("%w: raw headers digest is invalid", ErrUnsafeEvidence)
	}
	if record.HTTPStatus != 0 && (record.HTTPStatus < 200 || record.HTTPStatus >= 300) {
		return fmt.Errorf("%w: HTTP status %d", ErrUnsafeEvidence, record.HTTPStatus)
	}
	if record.Address == nil && len(record.Candidates) == 0 {
		return fmt.Errorf("%w: record has no address or candidates", ErrIncompleteEvidence)
	}
	if record.Address != nil && !addressHasMeaningfulFacts(record.Address) {
		return fmt.Errorf("%w: record address is empty", ErrIncompleteEvidence)
	}
	for index, candidate := range record.Candidates {
		if err := validateCandidate(index, candidate); err != nil {
			return err
		}
	}
	return nil
}

func validEvidenceSelectionPolicy(policy place.SelectionPolicy, candidateCount int) bool {
	if policy.RequestedLimit == 0 {
		return !policy.LimitReached && !policy.MoreResultsNotRequested && !policy.BoundedReverse
	}
	if policy.RequestedLimit < 1 || candidateCount > policy.RequestedLimit || policy.LimitReached != (candidateCount == policy.RequestedLimit) {
		return false
	}
	return policy.MoreResultsNotRequested == (policy.BoundedReverse && policy.LimitReached)
}

func validateCandidate(index int, candidate place.EvidenceCandidate) error {
	if candidate.ProviderIndex < 0 {
		return fmt.Errorf("%w: candidate %d has provider index %d", ErrMalformedEvidence, index, candidate.ProviderIndex)
	}
	if strings.TrimSpace(candidate.Source) == "" || (strings.TrimSpace(candidate.ProviderID) == "" && strings.TrimSpace(candidate.Name) == "") {
		return fmt.Errorf("%w: candidate %d has no identity or source", ErrMalformedEvidence, index)
	}
	if math.IsNaN(candidate.DistanceM) || math.IsInf(candidate.DistanceM, 0) || candidate.DistanceM < 0 {
		return fmt.Errorf("%w: candidate %d distance is invalid", ErrMalformedEvidence, index)
	}
	if candidate.Coordinate != nil && !validCoordinate(candidate.Coordinate.Latitude, candidate.Coordinate.Longitude) {
		return fmt.Errorf("%w: candidate %d coordinate is invalid", ErrMalformedEvidence, index)
	}
	if candidate.Address != nil && !addressHasMeaningfulFacts(candidate.Address) {
		return fmt.Errorf("%w: candidate %d address is empty", ErrMalformedEvidence, index)
	}
	for categoryIndex, category := range candidate.Categories {
		if strings.TrimSpace(category) == "" {
			return fmt.Errorf("%w: candidate %d category %d is empty", ErrMalformedEvidence, index, categoryIndex)
		}
	}
	return nil
}

func addressHasMeaningfulFacts(address *place.Address) bool {
	values := []string{
		address.Name,
		address.Thoroughfare,
		address.SubThoroughfare,
		address.Locality,
		address.SubLocality,
		address.AdministrativeArea,
		address.SubAdministrativeArea,
		address.PostalCode,
		address.Country,
		address.ISOCountryCode,
		address.TimeZone,
		address.Formatted,
	}
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	for _, area := range address.AreasOfInterest {
		if strings.TrimSpace(area) != "" {
			return true
		}
	}
	return false
}

func projectRecord(recordIndex int, record place.EvidenceRecord) *cardwire.PlaceProjection {
	projection := &cardwire.PlaceProjection{
		ProviderIdentity:     record.ProviderIdentity,
		Operation:            record.Operation,
		CoordinateVariant:    record.CoordinateVariant,
		ParserVersion:        record.ParserVersion,
		PreAuthRequestSha256: record.PreAuthRequestSHA256,
		RawResponseSha256:    record.RawResponseSHA256,
		Address:              projectAddress(record.Address),
	}
	for candidateIndex, candidate := range record.Candidates {
		var coordinate *cardwire.Coordinate
		if candidate.Coordinate != nil {
			coordinate = &cardwire.Coordinate{Latitude: candidate.Coordinate.Latitude, Longitude: candidate.Coordinate.Longitude}
		}
		projection.Candidates = append(projection.Candidates, &cardwire.PlaceCandidate{
			ProviderIndex:  int32(candidate.ProviderIndex),
			ProviderId:     candidate.ProviderID,
			Name:           candidate.Name,
			Categories:     slices.Clone(candidate.Categories),
			Coordinate:     coordinate,
			DistanceMeters: candidate.DistanceM,
			Address:        projectAddress(candidate.Address),
			Source:         candidate.Source,
			CandidateId:    candidateID(recordIndex, candidateIndex),
		})
	}
	return projection
}

func candidateID(recordIndex, candidateIndex int) string {
	return fmt.Sprintf("place_%d_candidate_%d", recordIndex+1, candidateIndex+1)
}

func projectAddress(address *place.Address) *cardwire.Address {
	if address == nil {
		return nil
	}
	return &cardwire.Address{
		Name:                  address.Name,
		Thoroughfare:          address.Thoroughfare,
		SubThoroughfare:       address.SubThoroughfare,
		Locality:              address.Locality,
		SubLocality:           address.SubLocality,
		AdministrativeArea:    address.AdministrativeArea,
		SubAdministrativeArea: address.SubAdministrativeArea,
		PostalCode:            address.PostalCode,
		Country:               address.Country,
		IsoCountryCode:        address.ISOCountryCode,
		TimeZone:              address.TimeZone,
		AreasOfInterest:       slices.Clone(address.AreasOfInterest),
		Formatted:             address.Formatted,
		Source:                address.Source,
	}
}
