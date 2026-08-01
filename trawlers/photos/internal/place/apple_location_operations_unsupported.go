//go:build !darwin

package place

import (
	"context"
	"errors"

	locationwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/location"
)

func AcquireAppleReverseGeocodingEvidence(context.Context, *locationwire.AcquireAppleReverseGeocodingEvidenceRequest) (*locationwire.AcquireAppleReverseGeocodingEvidenceOutcome, error) {
	return nil, errors.New("Apple reverse geocoding requires macOS")
}

func AcquireAppleNearbyPlaceEvidence(context.Context, *locationwire.AcquireAppleNearbyPlaceEvidenceRequest) (*locationwire.AcquireAppleNearbyPlaceEvidenceOutcome, error) {
	return nil, errors.New("Apple nearby-place evidence requires macOS")
}
