package cardinput

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/place"
	cardwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/card/v1"
	"google.golang.org/protobuf/proto"
)

func Build(source SourceFacts, artifacts CheckedArtifacts, records []place.EvidenceRecord) (Result, error) {
	if err := validateSource(source); err != nil {
		return Result{}, err
	}
	if err := validateArtifacts(source, artifacts); err != nil {
		return Result{}, err
	}
	places, err := projectEvidence(source, records)
	if err != nil {
		return Result{}, err
	}
	input := projectSource(source, places)
	if err := ValidateModelVisibleCandidates(input); err != nil {
		return Result{}, err
	}
	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(input)
	if err != nil {
		return Result{}, fmt.Errorf("marshal CardInput: %w", err)
	}
	digest := sha256.Sum256(data)
	return Result{Input: input, Bytes: data, ID: "card_input:" + hex.EncodeToString(digest[:])}, nil
}

func validateSource(source SourceFacts) error {
	if strings.TrimSpace(source.AssetID) == "" || strings.TrimSpace(source.CaptureTime) == "" || strings.TrimSpace(source.MediaType) == "" {
		return fmt.Errorf("%w: asset id, capture time and media type are required", ErrInvalidSource)
	}
	if source.Timezone != nil && strings.TrimSpace(*source.Timezone) == "" {
		return fmt.Errorf("%w: present timezone is empty", ErrInvalidSource)
	}
	if source.PixelWidth <= 0 || source.PixelHeight <= 0 || !finiteNonNegative(source.DurationSeconds) {
		return fmt.Errorf("%w: media dimensions or duration are invalid", ErrInvalidSource)
	}
	for index, subtype := range source.MediaSubtypes {
		if strings.TrimSpace(subtype) == "" {
			return fmt.Errorf("%w: media subtype %d is empty", ErrInvalidSource, index)
		}
	}
	for index, album := range source.Albums {
		if strings.TrimSpace(album.Title) == "" || strings.TrimSpace(album.Kind) == "" {
			return fmt.Errorf("%w: album %d is incomplete", ErrInvalidSource, index)
		}
	}
	if source.Location != nil {
		if !validCoordinate(source.Location.Latitude, source.Location.Longitude) {
			return fmt.Errorf("%w: location is invalid", ErrInvalidSource)
		}
		if source.Location.HorizontalAccuracyMeters != nil && !finiteNonNegative(*source.Location.HorizontalAccuracyMeters) {
			return fmt.Errorf("%w: horizontal accuracy is invalid", ErrInvalidSource)
		}
		if len(source.RequiredPlaceOperations) == 0 {
			return ErrMissingEvidence
		}
	} else if len(source.RequiredPlaceOperations) != 0 {
		return fmt.Errorf("%w: place operations require a location", ErrInvalidSource)
	}
	seenOperations := map[string]bool{}
	for index, operation := range source.RequiredPlaceOperations {
		if strings.TrimSpace(operation) == "" || seenOperations[operation] {
			return fmt.Errorf("%w: required place operation %d is empty or duplicated", ErrInvalidSource, index)
		}
		seenOperations[operation] = true
	}
	if source.KnownPlace != nil && strings.TrimSpace(source.KnownPlace.Relationship) == "" {
		return fmt.Errorf("%w: known-place relationship is required", ErrInvalidSource)
	}
	if source.Camera != nil {
		measurements := []struct {
			name  string
			value *float64
		}{
			{"focal length", source.Camera.FocalLengthMM},
			{"35mm focal length", source.Camera.FocalLength35MM},
			{"aperture", source.Camera.Aperture},
			{"shutter speed", source.Camera.ShutterSpeed},
		}
		for _, measurement := range measurements {
			name, value := measurement.name, measurement.value
			if value != nil && !finiteNonNegative(*value) {
				return fmt.Errorf("%w: camera %s is invalid", ErrInvalidSource, name)
			}
		}
		if source.Camera.ISO != nil && *source.Camera.ISO < 0 {
			return fmt.Errorf("%w: camera ISO is invalid", ErrInvalidSource)
		}
	}
	return nil
}

func validateArtifacts(source SourceFacts, artifacts CheckedArtifacts) error {
	if strings.TrimSpace(artifacts.ImmutableOriginal.ResourceID) == "" {
		return fmt.Errorf("%w: immutable-original resource id", ErrIncompleteArtifact)
	}
	if strings.TrimSpace(artifacts.Metadata.RecordID) == "" || strings.TrimSpace(artifacts.Metadata.ProjectionID) == "" {
		return fmt.Errorf("%w: metadata record or projection id", ErrIncompleteArtifact)
	}
	if !validDigest(artifacts.FullCurrent.ProofSHA256) {
		return fmt.Errorf("%w: full-current proof digest", ErrIncompleteArtifact)
	}
	if err := validateOriginal(artifacts.ImmutableOriginal.Fact); err != nil {
		return err
	}
	if err := validateMetadata(artifacts.Metadata.Fact); err != nil {
		return err
	}
	if err := validateFullCurrent(artifacts.FullCurrent.Fact); err != nil {
		return err
	}
	if source.ImmutableOriginal != artifacts.ImmutableOriginal.Fact {
		return fmt.Errorf("%w: immutable original", ErrArtifactMismatch)
	}
	if source.Metadata.RecordSHA256 != artifacts.Metadata.Fact.RecordSHA256 || source.Metadata.ProjectionSHA256 != artifacts.Metadata.Fact.ProjectionSHA256 || !slices.Equal(source.Metadata.ProjectionLines, artifacts.Metadata.Fact.ProjectionLines) {
		return fmt.Errorf("%w: metadata", ErrArtifactMismatch)
	}
	if source.FullCurrent != artifacts.FullCurrent.Fact {
		return fmt.Errorf("%w: full current", ErrArtifactMismatch)
	}
	return nil
}

func validateOriginal(fact ImmutableOriginalFact) error {
	if strings.TrimSpace(fact.ResourceType) == "" || strings.TrimSpace(fact.UTI) == "" || strings.TrimSpace(fact.Filename) == "" || strings.TrimSpace(fact.Availability) == "" || fact.SizeBytes <= 0 || !validDigest(fact.SHA256) {
		return fmt.Errorf("%w: immutable original", ErrIncompleteArtifact)
	}
	return nil
}

func validateMetadata(fact MetadataFact) error {
	if !validDigest(fact.RecordSHA256) || !validDigest(fact.ProjectionSHA256) || len(fact.ProjectionLines) == 0 {
		return fmt.Errorf("%w: metadata", ErrIncompleteArtifact)
	}
	for index, line := range fact.ProjectionLines {
		if strings.TrimSpace(line) == "" {
			return fmt.Errorf("%w: metadata projection line %d is empty", ErrIncompleteArtifact, index)
		}
	}
	return nil
}

func validateFullCurrent(fact FullCurrentFact) error {
	if fact.Role != "full_current" || strings.TrimSpace(fact.MediaType) == "" || fact.Orientation <= 0 || fact.PixelWidth <= 0 || fact.PixelHeight <= 0 || fact.SizeBytes <= 0 || !validDigest(fact.SHA256) {
		return fmt.Errorf("%w: full current", ErrIncompleteArtifact)
	}
	return nil
}

func projectSource(source SourceFacts, places []*cardwire.PlaceProjection) *cardwire.CardInput {
	input := &cardwire.CardInput{
		SchemaVersion:   SchemaVersion,
		CaptureTime:     source.CaptureTime,
		Timezone:        clone(source.Timezone),
		MediaType:       source.MediaType,
		MediaSubtypes:   slices.Clone(source.MediaSubtypes),
		PixelWidth:      source.PixelWidth,
		PixelHeight:     source.PixelHeight,
		DurationSeconds: source.DurationSeconds,
		ImmutableOriginal: &cardwire.ImmutableOriginal{
			ResourceType: source.ImmutableOriginal.ResourceType,
			Uti:          source.ImmutableOriginal.UTI,
			Filename:     source.ImmutableOriginal.Filename,
			Availability: source.ImmutableOriginal.Availability,
			SizeBytes:    source.ImmutableOriginal.SizeBytes,
			Sha256:       source.ImmutableOriginal.SHA256,
		},
		Favorite:    source.Favorite,
		Hidden:      source.Hidden,
		BurstMember: source.BurstMember,
		Metadata: &cardwire.Metadata{
			RecordSha256:     source.Metadata.RecordSHA256,
			ProjectionSha256: source.Metadata.ProjectionSHA256,
			ProjectionLines:  slices.Clone(source.Metadata.ProjectionLines),
		},
		FullCurrent: &cardwire.FullCurrent{
			Role:        source.FullCurrent.Role,
			MediaType:   source.FullCurrent.MediaType,
			Orientation: source.FullCurrent.Orientation,
			PixelWidth:  source.FullCurrent.PixelWidth,
			PixelHeight: source.FullCurrent.PixelHeight,
			SizeBytes:   source.FullCurrent.SizeBytes,
			Sha256:      source.FullCurrent.SHA256,
		},
		Places: places,
	}
	for _, album := range source.Albums {
		input.Albums = append(input.Albums, &cardwire.Album{Title: album.Title, Kind: album.Kind})
	}
	if source.Location != nil {
		input.Location = &cardwire.Location{Latitude: source.Location.Latitude, Longitude: source.Location.Longitude, HorizontalAccuracyMeters: clone(source.Location.HorizontalAccuracyMeters)}
	}
	if source.KnownPlace != nil {
		input.KnownPlace = &cardwire.KnownPlace{Name: source.KnownPlace.Name, Relationship: source.KnownPlace.Relationship}
	}
	if source.Camera != nil {
		input.Camera = &cardwire.Camera{Make: source.Camera.Make, Model: source.Camera.Model, LensModel: source.Camera.LensModel, FocalLengthMm: clone(source.Camera.FocalLengthMM), FocalLength_35Mm: clone(source.Camera.FocalLength35MM), Aperture: clone(source.Camera.Aperture), ShutterSpeed: clone(source.Camera.ShutterSpeed), Iso: clone(source.Camera.ISO)}
	}
	return input
}

func validDigest(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validCoordinate(latitude, longitude float64) bool {
	return !math.IsNaN(latitude) && !math.IsNaN(longitude) && !math.IsInf(latitude, 0) && !math.IsInf(longitude, 0) && latitude >= -90 && latitude <= 90 && longitude >= -180 && longitude <= 180
}

func finiteNonNegative(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

func clone[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
