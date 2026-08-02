//go:build !darwin

package place

import (
	"context"
	"errors"

	locationwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/location"
)

func AcquireAppleReverseGeocodingEvidence(context.Context, *locationwire.AcquireAppleReverseGeocodingEvidenceRequest, RetainAppleReverseGeocodingStage) (*locationwire.AcquireAppleReverseGeocodingEvidenceOutcome, error) {
	return nil, errors.New("Apple reverse geocoding requires macOS")
}

func AcquireAppleNearbyPlaceEvidence(context.Context, *locationwire.AcquireAppleNearbyPlaceEvidenceRequest, RetainAppleNearbyPlaceStage) (*locationwire.AcquireAppleNearbyPlaceEvidenceOutcome, error) {
	return nil, errors.New("Apple nearby-place evidence requires macOS")
}

func ResumeAppleReverseGeocodingEvidence(*locationwire.AcquireAppleReverseGeocodingEvidenceOutcome, RetainAppleReverseGeocodingStage) (*locationwire.AcquireAppleReverseGeocodingEvidenceOutcome, error) {
	return nil, errors.New("Apple reverse geocoding requires macOS")
}
func ResumeAppleNearbyPlaceEvidence(*locationwire.AcquireAppleNearbyPlaceEvidenceOutcome, RetainAppleNearbyPlaceStage) (*locationwire.AcquireAppleNearbyPlaceEvidenceOutcome, error) {
	return nil, errors.New("Apple nearby-place evidence requires macOS")
}
