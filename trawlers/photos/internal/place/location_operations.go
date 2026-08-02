package place

import (
	"crypto/sha256"
	"errors"
	"math"
	"strings"

	locationwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/location"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	MaximumNearbyPlaceCandidates = 100
	maxRawEvidenceBytes          = 4 << 20
)

type RetainAppleReverseGeocodingStage func(*locationwire.AcquireAppleReverseGeocodingEvidenceOutcome) error
type RetainAppleNearbyPlaceStage func(*locationwire.AcquireAppleNearbyPlaceEvidenceOutcome) error
type RetainGeoapifyPhotographedPlaceCandidateEvidenceStage func(*locationwire.AcquireGeoapifyPhotographedPlaceCandidateEvidenceOutcome) error

func retainAppleReverseGeocodingStage(retain RetainAppleReverseGeocodingStage, outcome *locationwire.AcquireAppleReverseGeocodingEvidenceOutcome) error {
	if retain == nil {
		return nil
	}
	return retain(outcome)
}

func retainAppleNearbyPlaceStage(retain RetainAppleNearbyPlaceStage, outcome *locationwire.AcquireAppleNearbyPlaceEvidenceOutcome) error {
	if retain == nil {
		return nil
	}
	return retain(outcome)
}

func retainGeoapifyPhotographedPlaceCandidateEvidenceStage(retain RetainGeoapifyPhotographedPlaceCandidateEvidenceStage, outcome *locationwire.AcquireGeoapifyPhotographedPlaceCandidateEvidenceOutcome) error {
	if retain == nil {
		return nil
	}
	return retain(outcome)
}

func ProviderExchangeSatisfiesCurrentLocationEvidence(exchange *locationwire.ProviderExchange, allowKnownPlaceSkip bool) bool {
	if exchange == nil {
		return false
	}
	switch exchange.GetState() {
	case locationwire.OperationState_OPERATION_STATE_SUCCEEDED, locationwire.OperationState_OPERATION_STATE_NO_RESULT:
		return exchange.GetFailure() == nil
	case locationwire.OperationState_OPERATION_STATE_SKIPPED_KNOWN_PLACE:
		return allowKnownPlaceSkip && exchange.GetFailure() == nil
	default:
		return false
	}
}

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

func completedAt() *timestamppb.Timestamp { return timestamppb.Now() }

func failedExchange(class locationwire.OperationFailureClass, detail string, transmissionStarted bool) *locationwire.ProviderExchange {
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
		result.Areas = append(result.Areas, &locationwire.NamedArea{Kind: locationwire.NamedAreaKind_NAMED_AREA_KIND_AREA_OF_INTEREST, Name: area})
	}
	return result
}

func protoDigest(message proto.Message) []byte {
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	if err != nil {
		return nil
	}
	digest := sha256.Sum256(encoded)
	return digest[:]
}

func captureLocationInputsMatch(first, second *locationwire.CaptureLocationInput) bool {
	return first != nil && second != nil && proto.Equal(first, second)
}

func terminalLocationOperationStatus(state locationwire.OperationState, failure *locationwire.OperationFailure, allowKnownPlaceSkip bool) (*locationwire.LocationOperationTerminalStatus, error) {
	switch state {
	case locationwire.OperationState_OPERATION_STATE_SUCCEEDED, locationwire.OperationState_OPERATION_STATE_NO_RESULT:
		if failure != nil {
			return nil, errors.New("successful location operation carries a failure")
		}
	case locationwire.OperationState_OPERATION_STATE_SKIPPED_KNOWN_PLACE:
		if !allowKnownPlaceSkip || failure != nil {
			return nil, errors.New("location operation has an invalid known-place skip state")
		}
	default:
		return nil, errors.New("location operation does not provide current reusable evidence")
	}
	return &locationwire.LocationOperationTerminalStatus{State: state, Failure: failure}, nil
}

func ComposePhotoLocationEvidence(
	knownPlace *locationwire.MatchConfiguredKnownPlaceOutcome,
	appleReverse *locationwire.AcquireAppleReverseGeocodingEvidenceOutcome,
	appleNearby *locationwire.AcquireAppleNearbyPlaceEvidenceOutcome,
	geoapifyPhotographedPlaceCandidateEvidence *locationwire.AcquireGeoapifyPhotographedPlaceCandidateEvidenceOutcome,
) (*locationwire.ComposePhotoLocationEvidenceOutcome, error) {
	if knownPlace == nil || knownPlace.GetRequest() == nil || appleReverse == nil || appleReverse.GetRequest() == nil || appleReverse.GetExchange() == nil ||
		appleNearby == nil || appleNearby.GetRequest() == nil || appleNearby.GetExchange() == nil || geoapifyPhotographedPlaceCandidateEvidence == nil ||
		geoapifyPhotographedPlaceCandidateEvidence.GetRequest() == nil || geoapifyPhotographedPlaceCandidateEvidence.GetExchange() == nil {
		return nil, errors.New("photo location composition requires all four typed operation outcomes")
	}
	captureLocationInput := knownPlace.GetRequest().GetInput()
	if err := validateCaptureLocationInput(captureLocationInput); err != nil {
		return nil, errors.New("photo location composition has an invalid capture input")
	}
	for _, dependencyInput := range []*locationwire.CaptureLocationInput{
		appleReverse.GetRequest().GetInput(), appleNearby.GetRequest().GetInput(), geoapifyPhotographedPlaceCandidateEvidence.GetRequest().GetInput(),
	} {
		if !captureLocationInputsMatch(captureLocationInput, dependencyInput) {
			return nil, errors.New("photo location operation outcomes have different capture inputs")
		}
	}
	knownPlaceStatus, err := terminalLocationOperationStatus(knownPlace.GetState(), knownPlace.GetFailure(), false)
	if err != nil {
		return nil, err
	}
	appleReverseStatus, err := terminalLocationOperationStatus(appleReverse.GetExchange().GetState(), appleReverse.GetExchange().GetFailure(), false)
	if err != nil {
		return nil, err
	}
	appleNearbyStatus, err := terminalLocationOperationStatus(appleNearby.GetExchange().GetState(), appleNearby.GetExchange().GetFailure(), true)
	if err != nil {
		return nil, err
	}
	geoapifyPhotographedPlaceCandidateEvidenceStatus, err := terminalLocationOperationStatus(geoapifyPhotographedPlaceCandidateEvidence.GetExchange().GetState(), geoapifyPhotographedPlaceCandidateEvidence.GetExchange().GetFailure(), true)
	if err != nil {
		return nil, err
	}
	knownMatch := len(knownPlace.GetMatches()) > 0
	if (knownPlaceStatus.GetState() == locationwire.OperationState_OPERATION_STATE_SUCCEEDED) != knownMatch {
		return nil, errors.New("known-place outcome state does not match its evidence")
	}
	if knownMatch != (appleNearbyStatus.GetState() == locationwire.OperationState_OPERATION_STATE_SKIPPED_KNOWN_PLACE) ||
		knownMatch != (geoapifyPhotographedPlaceCandidateEvidenceStatus.GetState() == locationwire.OperationState_OPERATION_STATE_SKIPPED_KNOWN_PLACE) {
		return nil, errors.New("nearby location outcomes do not honour the known-place match")
	}
	if appleReverseStatus.GetState() == locationwire.OperationState_OPERATION_STATE_SUCCEEDED && appleReverse.GetAddress() == nil {
		return nil, errors.New("successful Apple reverse-geocoding outcome has no address hierarchy")
	}
	if appleNearbyStatus.GetState() == locationwire.OperationState_OPERATION_STATE_SUCCEEDED && len(appleNearby.GetCandidates()) == 0 {
		return nil, errors.New("successful Apple nearby-place outcome has no candidates")
	}
	if geoapifyPhotographedPlaceCandidateEvidenceStatus.GetState() == locationwire.OperationState_OPERATION_STATE_SUCCEEDED && len(geoapifyPhotographedPlaceCandidateEvidence.GetCandidates()) == 0 {
		return nil, errors.New("successful Geoapify photographed-place candidate outcome has no candidates")
	}
	outcome := &locationwire.ComposePhotoLocationEvidenceOutcome{
		Request: &locationwire.ComposePhotoLocationEvidenceRequest{
			AssetId: captureLocationInput.GetAssetId(), KnownPlaceOutcomeSha256: protoDigest(knownPlace), AppleReverseOutcomeSha256: protoDigest(appleReverse),
			AppleNearbyOutcomeSha256: protoDigest(appleNearby), GeoapifyPhotographedPlaceCandidateEvidenceOutcomeSha256: protoDigest(geoapifyPhotographedPlaceCandidateEvidence),
		},
		State:                         locationwire.OperationState_OPERATION_STATE_SUCCEEDED,
		KnownPlaceMatches:             append([]*locationwire.ConfiguredKnownPlaceMatch(nil), knownPlace.GetMatches()...),
		NearbySuppressedForKnownPlace: knownMatch,
		Caution:                       "Capture coordinates and nearby places are location context only; they do not identify the photographed subject.",
		CompletedAt:                   completedAt(),
		KnownPlaceMatchStatus:         knownPlaceStatus,
		AppleReverseGeocodingStatus:   appleReverseStatus,
		AppleNearbyPlaceStatus:        appleNearbyStatus,
		GeoapifyPhotographedPlaceCandidateEvidenceStatus: geoapifyPhotographedPlaceCandidateEvidenceStatus,
	}
	if appleReverseStatus.GetState() == locationwire.OperationState_OPERATION_STATE_SUCCEEDED {
		outcome.AppleAddress = appleReverse.GetAddress()
	}
	if !knownMatch {
		if appleNearbyStatus.GetState() == locationwire.OperationState_OPERATION_STATE_SUCCEEDED {
			outcome.AppleNearbyCandidates = append([]*locationwire.PlaceCandidate(nil), appleNearby.GetCandidates()...)
		}
		if geoapifyPhotographedPlaceCandidateEvidenceStatus.GetState() == locationwire.OperationState_OPERATION_STATE_SUCCEEDED {
			outcome.GeoapifyPhotographedPlaceCandidates = append([]*locationwire.PlaceCandidate(nil), geoapifyPhotographedPlaceCandidateEvidence.GetCandidates()...)
		}
	}
	return outcome, nil
}
