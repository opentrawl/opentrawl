package place

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"strings"
	"time"

	locationwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/location"
	"google.golang.org/protobuf/proto"
)

const MaximumNearbyPlaceCandidates = 100

func validateCaptureLocationInput(input *locationwire.CaptureLocationInput) error {
	if input == nil || input.Coordinate == nil || strings.TrimSpace(input.AssetId) == "" {
		return errors.New("capture location input is incomplete")
	}
	coordinate := input.Coordinate
	if math.IsNaN(coordinate.Latitude) || math.IsNaN(coordinate.Longitude) || math.IsInf(coordinate.Latitude, 0) || math.IsInf(coordinate.Longitude, 0) ||
		coordinate.Latitude < -90 || coordinate.Latitude > 90 || coordinate.Longitude < -180 || coordinate.Longitude > 180 {
		return errors.New("capture coordinate is invalid")
	}
	return nil
}

func completedAt() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func failedExchange(class, detail string, transmissionStarted bool) *locationwire.ProviderExchange {
	return &locationwire.ProviderExchange{
		State: locationwire.OperationState_OPERATION_STATE_FAILED, TransmissionStarted: transmissionStarted,
		Failure: &locationwire.OperationFailure{Class: class, Detail: detail},
	}
}

func addressHierarchy(address *Address) *locationwire.AddressHierarchy {
	if address == nil {
		return nil
	}
	result := &locationwire.AddressHierarchy{
		Name: address.Name, HouseNumber: address.SubThoroughfare, Street: address.Thoroughfare,
		Neighbourhood: address.SubLocality, City: address.Locality, County: address.SubAdministrativeArea,
		Region: address.AdministrativeArea, Postcode: address.PostalCode, Country: address.Country,
		CountryCode: address.ISOCountryCode, TimeZone: address.TimeZone,
		Formatted: address.Formatted,
	}
	for _, area := range address.AreasOfInterest {
		result.Areas = append(result.Areas, &locationwire.NamedArea{Kind: "area_of_interest", Name: area})
	}
	return result
}

func protoDigest(message proto.Message) string {
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func ProjectGeoapifyEvidenceForBriefing(
	knownPlace *locationwire.MatchConfiguredKnownPlaceOutcome,
	appleReverse *locationwire.AcquireAppleReverseGeocodingEvidenceOutcome,
	appleNearby *locationwire.AcquireAppleNearbyPlaceEvidenceOutcome,
	geoapify *locationwire.AcquireGeoapifyReverseGeocodingEvidenceOutcome,
) (*locationwire.ProjectGeoapifyEvidenceForBriefingOutcome, error) {
	assetID := ""
	for _, candidate := range []string{
		knownPlace.GetRequest().GetInput().GetAssetId(), appleReverse.GetRequest().GetInput().GetAssetId(),
		appleNearby.GetRequest().GetInput().GetAssetId(), geoapify.GetRequest().GetInput().GetAssetId(),
	} {
		if candidate == "" {
			continue
		}
		if assetID != "" && assetID != candidate {
			return nil, errors.New("location outcomes belong to different Photos assets")
		}
		assetID = candidate
	}
	if assetID == "" {
		return nil, errors.New("location outcomes have no Photos asset identity")
	}
	knownMatch := len(knownPlace.GetMatches()) > 0
	outcome := &locationwire.ProjectGeoapifyEvidenceForBriefingOutcome{
		Request: &locationwire.ProjectGeoapifyEvidenceForBriefingRequest{
			AssetId: assetID, KnownPlaceOutcomeSha256: protoDigest(knownPlace), AppleReverseOutcomeSha256: protoDigest(appleReverse),
			AppleNearbyOutcomeSha256: protoDigest(appleNearby), GeoapifyOutcomeSha256: protoDigest(geoapify),
		},
		State:             locationwire.OperationState_OPERATION_STATE_SUCCEEDED,
		KnownPlaceMatches: append([]*locationwire.ConfiguredKnownPlaceMatch(nil), knownPlace.GetMatches()...),
		AppleAddress:      appleReverse.GetAddress(), GeoapifyAddress: geoapify.GetAddress(),
		NearbySuppressedForKnownPlace: knownMatch,
		Caution:                       "Capture coordinates and nearby places are location context only; they do not identify the photographed subject.",
		CompletedAt:                   completedAt(),
	}
	if !knownMatch {
		outcome.AppleNearbyCandidates = append([]*locationwire.PlaceCandidate(nil), appleNearby.GetCandidates()...)
		outcome.GeoapifyNearbyCandidates = append([]*locationwire.PlaceCandidate(nil), geoapify.GetNearbyCandidates()...)
	}
	return outcome, nil
}
