package place

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
)

const (
	AppleReverseLocationOperation    = "apple_reverse"
	AppleNearbyLocationOperation     = "apple_nearby"
	GeoapifyReverseLocationOperation = "geoapify_reverse"

	AppleLocationProviderIdentity    = "apple_maps"
	GeoapifyLocationProviderIdentity = "geoapify"
	MaximumNearbyPlaceCandidates     = 100
)

// FactualLocationEvidence is one provider operation over a Photos capture
// coordinate. It records where the camera was, never what the image depicts.
type FactualLocationEvidence struct {
	ProviderIdentity string
	Operation        string
	Address          *Address
	Candidates       []FactualLocationCandidate
	RawResponse      []byte
}

// FactualLocationCandidate preserves provider order. It intentionally has no
// score, tier or selected flag: only the card model may judge relevance.
type FactualLocationCandidate struct {
	ProviderPlaceIdentity string
	Name                  string
	Categories            []string
	Coordinate            *Coordinate
	Address               *Address
	DistanceMeters        float64
}

// KnownCapturePlace is user-configured capture context. A match is not a
// claim about the photographed subject.
type KnownCapturePlace struct {
	Identity     string
	Relationship string
	DisplayName  string
	Location     Coordinate
	RadiusMeters float64
}

type KnownCapturePlaceMatch struct {
	KnownCapturePlaceIdentity string
	Relationship              string
	DisplayName               string
	DistanceMeters            float64
}

func MatchKnownCapturePlace(capture Coordinate, knownCapturePlaces []KnownCapturePlace) (*KnownCapturePlaceMatch, error) {
	if !validFactualCoordinate(capture) {
		return nil, errors.New("capture coordinate is invalid")
	}
	for _, knownCapturePlace := range knownCapturePlaces {
		if strings.TrimSpace(knownCapturePlace.Identity) == "" || strings.TrimSpace(knownCapturePlace.Relationship) == "" ||
			!validFactualCoordinate(knownCapturePlace.Location) || knownCapturePlace.RadiusMeters <= 0 ||
			math.IsNaN(knownCapturePlace.RadiusMeters) || math.IsInf(knownCapturePlace.RadiusMeters, 0) {
			return nil, errors.New("known capture place is invalid")
		}
		distanceMeters := metersBetween(capture, knownCapturePlace.Location)
		if distanceMeters <= knownCapturePlace.RadiusMeters {
			return &KnownCapturePlaceMatch{
				KnownCapturePlaceIdentity: knownCapturePlace.Identity,
				Relationship:              knownCapturePlace.Relationship,
				DisplayName:               knownCapturePlace.DisplayName,
				DistanceMeters:            distanceMeters,
			}, nil
		}
	}
	return nil, nil
}

type FactualLocationEvidenceAcquirer struct {
	GeoapifyAPIKeyFilePath string
	NearbyRadiusMeters     float64
}

func (acquirer FactualLocationEvidenceAcquirer) Acquire(ctx context.Context, input Input, knownCapturePlaceMatch *KnownCapturePlaceMatch) ([]FactualLocationEvidence, error) {
	if err := validateInput(input); err != nil {
		return nil, fmt.Errorf("capture location: %w", err)
	}
	nearbyRadiusMeters := acquirer.NearbyRadiusMeters
	if nearbyRadiusMeters <= 0 {
		nearbyRadiusMeters = defaultRadiusMeters
	}

	appleReverseEvidence, err := acquireAppleFactualLocationEvidence(ctx, input, AppleReverseLocationOperation, nearbyRadiusMeters)
	if err != nil {
		return nil, err
	}
	geoapifyReverseEvidence, err := acquireGeoapifyReverseLocationEvidence(ctx, input, acquirer.GeoapifyAPIKeyFilePath)
	if err != nil {
		return nil, err
	}
	evidence := []FactualLocationEvidence{appleReverseEvidence, geoapifyReverseEvidence}
	if knownCapturePlaceMatch != nil {
		return evidence, nil
	}
	appleNearbyEvidence, err := acquireAppleFactualLocationEvidence(ctx, input, AppleNearbyLocationOperation, nearbyRadiusMeters)
	if err != nil {
		return nil, err
	}
	return []FactualLocationEvidence{appleReverseEvidence, appleNearbyEvidence, geoapifyReverseEvidence}, nil
}

func validFactualCoordinate(coordinate Coordinate) bool {
	return !math.IsNaN(coordinate.Latitude) && !math.IsNaN(coordinate.Longitude) &&
		!math.IsInf(coordinate.Latitude, 0) && !math.IsInf(coordinate.Longitude, 0) &&
		coordinate.Latitude >= -90 && coordinate.Latitude <= 90 && coordinate.Longitude >= -180 && coordinate.Longitude <= 180
}
