package updatephotos

import (
	"context"
	"crypto/sha256"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/place"
	locationwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/location"
	"google.golang.org/protobuf/proto"
)

type photoFoundationSelection struct {
	assets                        []archive.PhotoUpdateAsset
	geoapifyTransmissionsReserved int
}

type geoapifyTransmissionKind uint8

const (
	geoapifyReverseGeocodingTransmission geoapifyTransmissionKind = iota + 1
	geoapifyNearbyPlacesTransmission
)

type geoapifyTransmissionReservationKey struct {
	kind                  geoapifyTransmissionKind
	providerRequestSHA256 [sha256.Size]byte
}

func (runner *Runner) selectPhotoFoundationAssetsWithinGeoapifyAllowance(
	ctx context.Context,
	pendingAssets []archive.PhotoUpdateAsset,
	knownPlaceConfigurationSHA256 []byte,
	geoapifyTransmissionAllowance int,
	maximumAssetsToProcess int,
) (photoFoundationSelection, error) {
	selection := photoFoundationSelection{assets: make([]archive.PhotoUpdateAsset, 0, len(pendingAssets))}
	reservedTransmissions := make(map[geoapifyTransmissionReservationKey]struct{})
	for _, asset := range pendingAssets {
		if maximumAssetsToProcess > 0 && len(selection.assets) >= maximumAssetsToProcess {
			break
		}
		transmissionKeys, err := runner.geoapifyTransmissionReservationKeysForPhotoFoundation(ctx, asset, knownPlaceConfigurationSHA256)
		if err != nil {
			return photoFoundationSelection{}, err
		}
		additionalTransmissions := 0
		for _, transmissionKey := range transmissionKeys {
			if _, alreadyReserved := reservedTransmissions[transmissionKey]; !alreadyReserved {
				additionalTransmissions++
			}
		}
		if selection.geoapifyTransmissionsReserved+additionalTransmissions > geoapifyTransmissionAllowance {
			continue
		}
		selection.assets = append(selection.assets, asset)
		selection.geoapifyTransmissionsReserved += additionalTransmissions
		for _, transmissionKey := range transmissionKeys {
			reservedTransmissions[transmissionKey] = struct{}{}
		}
	}
	return selection, nil
}

func (runner *Runner) geoapifyTransmissionReservationKeysForPhotoFoundation(
	ctx context.Context,
	asset archive.PhotoUpdateAsset,
	knownPlaceConfigurationSHA256 []byte,
) ([]geoapifyTransmissionReservationKey, error) {
	if asset.MediaType != archive.PhotoMediaKindImage {
		return nil, nil
	}
	captureInput, hasCaptureLocation, err := archive.LoadOptionalCaptureLocationInput(ctx, runner.options.OpenedArchiveStore, string(asset.AssetID))
	if err != nil || !hasCaptureLocation {
		return nil, err
	}

	transmissionKeys := make([]geoapifyTransmissionReservationKey, 0, 2)
	reverseRequest := geoapifyReverseGeocodingEvidenceRequest(captureInput)
	retainedReverse, reverseFound, err := archive.LoadGeoapifyReverseGeocodingEvidenceOutcomeForRequest(ctx, runner.options.OpenedArchiveStore, reverseRequest)
	if err != nil {
		return nil, err
	}
	if geoapifyOutcomeRequiresTransmissionReservation(reverseFound, retainedReverse.GetExchange(), time.Now()) {
		transmissionKey, keyErr := newGeoapifyTransmissionReservationKey(geoapifyReverseGeocodingTransmission, reverseRequest.GetProviderRequest())
		if keyErr != nil {
			return nil, keyErr
		}
		transmissionKeys = append(transmissionKeys, transmissionKey)
	}

	knownPlaceRequest := &locationwire.MatchConfiguredKnownPlaceRequest{
		Input:                         captureInput,
		KnownPlaceConfigurationSha256: knownPlaceConfigurationSHA256,
	}
	retainedKnownPlace, knownPlaceFound, err := archive.LoadMatchConfiguredKnownPlaceOutcome(ctx, runner.options.OpenedArchiveStore, string(asset.AssetID))
	if err != nil {
		return nil, err
	}
	retainedKnownPlaceIsCurrent := knownPlaceFound && proto.Equal(retainedKnownPlace.GetRequest(), knownPlaceRequest)
	knownPlaceMatches := retainedKnownPlaceIsCurrent && len(retainedKnownPlace.GetMatches()) > 0
	if !retainedKnownPlaceIsCurrent {
		currentKnownPlace, matchErr := archive.MatchConfiguredKnownPlace(ctx, runner.options.OpenedArchiveStore, knownPlaceRequest)
		if matchErr != nil {
			return nil, matchErr
		}
		knownPlaceMatches = len(currentKnownPlace.GetMatches()) > 0
	}
	if knownPlaceMatches {
		return transmissionKeys, nil
	}

	placesRequest := geoapifyPhotographedPlaceCandidateEvidenceRequest(captureInput)
	retainedPlaces, placesFound, err := archive.LoadGeoapifyPhotographedPlaceCandidateEvidenceOutcomeForRequest(ctx, runner.options.OpenedArchiveStore, placesRequest)
	if err != nil {
		return nil, err
	}
	if geoapifyOutcomeRequiresTransmissionReservation(placesFound, retainedPlaces.GetExchange(), time.Now()) {
		transmissionKey, keyErr := newGeoapifyTransmissionReservationKey(geoapifyNearbyPlacesTransmission, placesRequest.GetProviderRequest())
		if keyErr != nil {
			return nil, keyErr
		}
		transmissionKeys = append(transmissionKeys, transmissionKey)
	}
	return transmissionKeys, nil
}

func newGeoapifyTransmissionReservationKey(kind geoapifyTransmissionKind, providerRequest proto.Message) (geoapifyTransmissionReservationKey, error) {
	encodedRequest, err := proto.MarshalOptions{Deterministic: true}.Marshal(providerRequest)
	if err != nil {
		return geoapifyTransmissionReservationKey{}, err
	}
	return geoapifyTransmissionReservationKey{kind: kind, providerRequestSHA256: sha256.Sum256(encodedRequest)}, nil
}

func geoapifyOutcomeRequiresTransmissionReservation(found bool, exchange *locationwire.ProviderExchange, now time.Time) bool {
	if !found {
		return true
	}
	if place.ProviderExchangeSatisfiesCurrentLocationEvidence(exchange, false) {
		return false
	}
	return exchange.GetState() != locationwire.OperationState_OPERATION_STATE_RESPONSE_RETAINED && !providerRetryNotBeforeIsFuture(exchange, now)
}
