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
type RetainGeoapifyReverseGeocodingStage func(*locationwire.AcquireGeoapifyReverseGeocodingEvidenceOutcome) error
type RetainGeoapifyNearbyPlaceEvidenceStage func(*locationwire.AcquireGeoapifyNearbyPlaceEvidenceOutcome) error

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

func retainGeoapifyReverseGeocodingStage(retain RetainGeoapifyReverseGeocodingStage, outcome *locationwire.AcquireGeoapifyReverseGeocodingEvidenceOutcome) error {
	if retain == nil {
		return nil
	}
	return retain(outcome)
}

func retainGeoapifyNearbyPlaceEvidenceStage(retain RetainGeoapifyNearbyPlaceEvidenceStage, outcome *locationwire.AcquireGeoapifyNearbyPlaceEvidenceOutcome) error {
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

func validateProviderCoordinate(coordinate *locationwire.Coordinate) error {
	if coordinate == nil || math.IsNaN(coordinate.Latitude) || math.IsNaN(coordinate.Longitude) || math.IsInf(coordinate.Latitude, 0) || math.IsInf(coordinate.Longitude, 0) ||
		coordinate.Latitude < -90 || coordinate.Latitude > 90 || coordinate.Longitude < -180 || coordinate.Longitude > 180 {
		return errors.New("provider coordinate is invalid")
	}
	return nil
}

func providerCoordinateMatchesCaptureLocation(providerCoordinate *locationwire.Coordinate, captureLocation *locationwire.CaptureLocationInput) bool {
	return captureLocation != nil && proto.Equal(providerCoordinate, captureLocation.GetCoordinate())
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
	geoapifyReverse *locationwire.AcquireGeoapifyReverseGeocodingEvidenceOutcome,
	geoapifyNearbyPlaceEvidence *locationwire.AcquireGeoapifyNearbyPlaceEvidenceOutcome,
) (*locationwire.ComposePhotoLocationEvidenceOutcome, error) {
	if knownPlace == nil || knownPlace.GetRequest() == nil || appleReverse == nil || appleReverse.GetRequest() == nil || appleReverse.GetExchange() == nil ||
		appleNearby == nil || appleNearby.GetRequest() == nil || appleNearby.GetExchange() == nil || geoapifyReverse == nil || geoapifyReverse.GetRequest() == nil ||
		geoapifyReverse.GetExchange() == nil || geoapifyNearbyPlaceEvidence == nil ||
		geoapifyNearbyPlaceEvidence.GetRequest() == nil || geoapifyNearbyPlaceEvidence.GetExchange() == nil {
		return nil, errors.New("photo location composition requires all five typed operation outcomes")
	}
	captureLocationInput := knownPlace.GetRequest().GetInput()
	if err := validateCaptureLocationInput(captureLocationInput); err != nil {
		return nil, errors.New("photo location composition has an invalid capture input")
	}
	for _, dependencyInput := range []*locationwire.CaptureLocationInput{
		appleReverse.GetRequest().GetInput(), appleNearby.GetRequest().GetInput(), geoapifyReverse.GetRequest().GetInput(), geoapifyNearbyPlaceEvidence.GetRequest().GetInput(),
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
	geoapifyReverseStatus, err := terminalLocationOperationStatus(geoapifyReverse.GetExchange().GetState(), geoapifyReverse.GetExchange().GetFailure(), false)
	if err != nil {
		return nil, err
	}
	geoapifyNearbyPlaceEvidenceStatus, err := terminalLocationOperationStatus(geoapifyNearbyPlaceEvidence.GetExchange().GetState(), geoapifyNearbyPlaceEvidence.GetExchange().GetFailure(), true)
	if err != nil {
		return nil, err
	}
	knownMatch := len(knownPlace.GetMatches()) > 0
	if (knownPlaceStatus.GetState() == locationwire.OperationState_OPERATION_STATE_SUCCEEDED) != knownMatch {
		return nil, errors.New("known-place outcome state does not match its evidence")
	}
	if knownMatch != (appleNearbyStatus.GetState() == locationwire.OperationState_OPERATION_STATE_SKIPPED_KNOWN_PLACE) ||
		knownMatch != (geoapifyNearbyPlaceEvidenceStatus.GetState() == locationwire.OperationState_OPERATION_STATE_SKIPPED_KNOWN_PLACE) {
		return nil, errors.New("nearby location outcomes do not honour the known-place match")
	}
	if appleReverseStatus.GetState() == locationwire.OperationState_OPERATION_STATE_SUCCEEDED && appleReverse.GetAddress() == nil {
		return nil, errors.New("successful Apple reverse-geocoding outcome has no address hierarchy")
	}
	if geoapifyReverseStatus.GetState() == locationwire.OperationState_OPERATION_STATE_SUCCEEDED && geoapifyReverse.GetAddress() == nil {
		return nil, errors.New("successful Geoapify reverse-geocoding outcome has no address hierarchy")
	}
	if appleNearbyStatus.GetState() == locationwire.OperationState_OPERATION_STATE_SUCCEEDED && len(appleNearby.GetCandidates()) == 0 {
		return nil, errors.New("successful Apple nearby-place outcome has no candidates")
	}
	if geoapifyNearbyPlaceEvidenceStatus.GetState() == locationwire.OperationState_OPERATION_STATE_SUCCEEDED && len(geoapifyNearbyPlaceEvidence.GetCandidates()) == 0 {
		return nil, errors.New("successful Geoapify nearby-place outcome has no candidates")
	}
	outcome := &locationwire.ComposePhotoLocationEvidenceOutcome{
		Request: &locationwire.ComposePhotoLocationEvidenceRequest{
			AssetId: captureLocationInput.GetAssetId(), KnownPlaceOutcomeSha256: protoDigest(knownPlace), AppleReverseOutcomeSha256: protoDigest(appleReverse),
			AppleNearbyOutcomeSha256: protoDigest(appleNearby), GeoapifyNearbyPlaceEvidenceOutcomeSha256: protoDigest(geoapifyNearbyPlaceEvidence),
			GeoapifyReverseGeocodingOutcomeSha256: protoDigest(geoapifyReverse),
		},
		State:       locationwire.OperationState_OPERATION_STATE_SUCCEEDED,
		CompletedAt: completedAt(),
		Briefing: &locationwire.PhotoLocationBriefing{
			CaptureLocation:                         proto.Clone(captureLocationInput).(*locationwire.CaptureLocationInput),
			KnownPlaceMatches:                       cloneKnownPlaceMatches(knownPlace.GetMatches()),
			NearbyCandidatesSuppressedForKnownPlace: knownMatch,
		},
	}
	if appleReverseStatus.GetState() == locationwire.OperationState_OPERATION_STATE_SUCCEEDED {
		outcome.Briefing.AppleCameraLocation = proto.Clone(appleReverse.GetAddress()).(*locationwire.AddressHierarchy)
	}
	if geoapifyReverseStatus.GetState() == locationwire.OperationState_OPERATION_STATE_SUCCEEDED {
		outcome.Briefing.GeoapifyCameraLocation = proto.Clone(geoapifyReverse.GetAddress()).(*locationwire.AddressHierarchy)
	}
	outcome.Briefing.ProviderEvidence = []*locationwire.PhotoLocationProviderEvidence{
		photoLocationProviderEvidence(appleReverse.GetProvider(), appleReverseStatus, appleReverse.GetEvidenceUse(), appleReverse.GetObservedAt(), appleReverse.GetAttributions(), 0),
		photoLocationProviderEvidence(geoapifyReverse.GetProvider(), geoapifyReverseStatus, geoapifyReverse.GetEvidenceUse(), geoapifyReverse.GetObservedAt(), geoapifyReverse.GetAttributions(), 0),
		photoLocationProviderEvidence(appleNearby.GetProvider(), appleNearbyStatus, appleNearby.GetEvidenceUse(), appleNearby.GetObservedAt(), appleNearby.GetAttributions(), len(appleNearby.GetCandidates())),
		photoLocationProviderEvidence(
			geoapifyNearbyPlaceEvidence.GetProvider(),
			geoapifyNearbyPlaceEvidenceStatus,
			geoapifyNearbyPlaceEvidence.GetEvidenceUse(),
			geoapifyNearbyPlaceEvidence.GetObservedAt(),
			geoapifyNearbyPlaceEvidence.GetAttributions(),
			len(geoapifyNearbyPlaceEvidence.GetCandidates()),
		),
	}
	return outcome, nil
}

func cloneKnownPlaceMatches(matches []*locationwire.ConfiguredKnownPlaceMatch) []*locationwire.ConfiguredKnownPlaceMatch {
	cloned := make([]*locationwire.ConfiguredKnownPlaceMatch, 0, len(matches))
	for _, match := range matches {
		if match != nil {
			cloned = append(cloned, proto.Clone(match).(*locationwire.ConfiguredKnownPlaceMatch))
		}
	}
	return cloned
}

func photoLocationProviderEvidence(
	provider locationwire.LocationEvidenceProvider,
	terminalStatus *locationwire.LocationOperationTerminalStatus,
	evidenceUse locationwire.ProviderEvidenceUse,
	observedAt *timestamppb.Timestamp,
	attributions []*locationwire.LocationEvidenceAttribution,
	nearbyPlaceCandidateCount int,
) *locationwire.PhotoLocationProviderEvidence {
	evidence := &locationwire.PhotoLocationProviderEvidence{
		Provider:                  provider,
		TerminalStatus:            proto.Clone(terminalStatus).(*locationwire.LocationOperationTerminalStatus),
		EvidenceUse:               evidenceUse,
		ObservedAt:                observedAt,
		Attributions:              cloneLocationEvidenceAttributions(attributions),
		NearbyPlaceCandidateCount: uint32(nearbyPlaceCandidateCount),
	}
	if len(evidence.Attributions) == 0 {
		evidence.Attributions = []*locationwire.LocationEvidenceAttribution{{ProviderName: locationEvidenceProviderName(provider)}}
	}
	return evidence
}

func cloneLocationEvidenceAttributions(attributions []*locationwire.LocationEvidenceAttribution) []*locationwire.LocationEvidenceAttribution {
	cloned := make([]*locationwire.LocationEvidenceAttribution, 0, len(attributions))
	for _, attribution := range attributions {
		if attribution != nil {
			cloned = append(cloned, proto.Clone(attribution).(*locationwire.LocationEvidenceAttribution))
		}
	}
	return cloned
}

func locationEvidenceProviderName(provider locationwire.LocationEvidenceProvider) string {
	switch provider {
	case locationwire.LocationEvidenceProvider_LOCATION_EVIDENCE_PROVIDER_APPLE_REVERSE_GEOCODING:
		return "Apple reverse geocoding"
	case locationwire.LocationEvidenceProvider_LOCATION_EVIDENCE_PROVIDER_APPLE_NEARBY_PLACES:
		return "Apple nearby places"
	case locationwire.LocationEvidenceProvider_LOCATION_EVIDENCE_PROVIDER_GEOAPIFY_PLACES:
		return "Geoapify"
	case locationwire.LocationEvidenceProvider_LOCATION_EVIDENCE_PROVIDER_GEOAPIFY_REVERSE_GEOCODING:
		return "Geoapify reverse geocoding"
	default:
		return "Unknown location evidence provider"
	}
}
