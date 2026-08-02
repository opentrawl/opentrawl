package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/place"
	locationwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/location"
	"github.com/opentrawl/opentrawl/trawlkit/store"
	"google.golang.org/protobuf/proto"
)

func rebindTypedGeoapifyProofOutcomes(
	ctx context.Context,
	proofArchivePath string,
	legacyLocalIdentifiersByAssetID map[string]string,
	targetCapturesByLocalIdentifier map[string]*targetCaptureIdentity,
	knownPlaceOutcomes map[string]*locationwire.MatchConfiguredKnownPlaceOutcome,
) (map[string]*locationwire.AcquireGeoapifyReverseGeocodingEvidenceOutcome, map[string]*locationwire.AcquireGeoapifyNearbyPlaceEvidenceOutcome, error) {
	proofStore, err := store.OpenForeignReadOnly(ctx, proofArchivePath)
	if err != nil {
		return nil, nil, fmt.Errorf("open typed location proof archive read-only: %w", err)
	}
	defer func() { _ = proofStore.Close() }()
	_, proofCapturesByAssetID, err := loadTargetCaptureIdentities(ctx, proofStore.DB())
	if err != nil {
		return nil, nil, fmt.Errorf("read typed proof capture identities: %w", err)
	}

	reverseRows, err := proofStore.DB().QueryContext(ctx, `select outcome_proto from geoapify_reverse_geocoding_evidence_outcome order by asset_id`)
	if err != nil {
		return nil, nil, fmt.Errorf("read typed Geoapify reverse proof outcomes: %w", err)
	}
	reverseOutcomes := make(map[string]*locationwire.AcquireGeoapifyReverseGeocodingEvidenceOutcome)
	reverseRejections := make(map[string]int)
	for reverseRows.Next() {
		var encoded []byte
		if err := reverseRows.Scan(&encoded); err != nil {
			_ = reverseRows.Close()
			return nil, nil, err
		}
		source := new(locationwire.AcquireGeoapifyReverseGeocodingEvidenceOutcome)
		if err := proto.Unmarshal(encoded, source); err != nil {
			_ = reverseRows.Close()
			return nil, nil, fmt.Errorf("decode typed Geoapify reverse proof: %w", err)
		}
		targetCapture, rejection := resolveCurrentProofCapture(source.GetRequest().GetInput(), legacyLocalIdentifiersByAssetID, proofCapturesByAssetID, targetCapturesByLocalIdentifier)
		if rejection != "" {
			reverseRejections[rejection]++
			continue
		}
		outcome, err := place.ReprojectRetainedGeoapifyReverseGeocodingEvidence(
			&locationwire.AcquireGeoapifyReverseGeocodingEvidenceRequest{Input: captureLocationInput(targetCapture)},
			source.GetExchange(),
			source.GetCompletedAt(),
		)
		if err != nil {
			_ = reverseRows.Close()
			return nil, nil, fmt.Errorf("validate retained Geoapify reverse proof: %w", err)
		}
		if !proto.Equal(source.GetAddress(), outcome.GetAddress()) || source.GetExchange().GetState() != outcome.GetExchange().GetState() {
			_ = reverseRows.Close()
			return nil, nil, errors.New("typed Geoapify reverse proof differs from the live parser projection")
		}
		if !place.ProviderExchangeSatisfiesCurrentLocationEvidence(outcome.GetExchange(), false) {
			_ = reverseRows.Close()
			return nil, nil, errors.New("typed Geoapify reverse proof does not satisfy the current terminal evidence contract")
		}
		if _, duplicate := reverseOutcomes[targetCapture.assetID]; duplicate {
			_ = reverseRows.Close()
			return nil, nil, errors.New("typed Geoapify reverse proof archive contains duplicate current request identities")
		}
		reverseOutcomes[targetCapture.assetID] = outcome
	}
	if err := errors.Join(reverseRows.Err(), reverseRows.Close()); err != nil {
		return nil, nil, err
	}

	nearbyRows, err := proofStore.DB().QueryContext(ctx, `select outcome_proto from geoapify_nearby_place_evidence_outcome order by asset_id`)
	if err != nil {
		return nil, nil, fmt.Errorf("read typed Geoapify nearby proof outcomes: %w", err)
	}
	nearbyOutcomes := make(map[string]*locationwire.AcquireGeoapifyNearbyPlaceEvidenceOutcome)
	nearbyRejections := make(map[string]int)
	for nearbyRows.Next() {
		var encoded []byte
		if err := nearbyRows.Scan(&encoded); err != nil {
			_ = nearbyRows.Close()
			return nil, nil, err
		}
		source := new(locationwire.AcquireGeoapifyNearbyPlaceEvidenceOutcome)
		if err := proto.Unmarshal(encoded, source); err != nil {
			_ = nearbyRows.Close()
			return nil, nil, fmt.Errorf("decode typed Geoapify nearby proof: %w", err)
		}
		targetCapture, rejection := resolveCurrentProofCapture(source.GetRequest().GetInput(), legacyLocalIdentifiersByAssetID, proofCapturesByAssetID, targetCapturesByLocalIdentifier)
		if rejection != "" {
			nearbyRejections[rejection]++
			continue
		}
		if source.GetRequest().GetRadiusMeters() != currentNearbyPlaceRadiusMetres || source.GetRequest().GetMaximumCandidates() != currentMaximumNearbyPlaceCandidates {
			nearbyRejections["request bound is stale"]++
			continue
		}
		currentKnownPlaceOutcome := knownPlaceOutcomes[targetCapture.assetID]
		if !historicalKnownPlaceOutcomeWasTruthfulForCapture(source.GetRequest().GetKnownPlaceOutcome(), source.GetRequest().GetInput()) {
			nearbyRejections["historical known-place gate is incomplete"]++
			continue
		}
		request := &locationwire.AcquireGeoapifyNearbyPlaceEvidenceRequest{
			Input:             captureLocationInput(targetCapture),
			RadiusMeters:      currentNearbyPlaceRadiusMetres,
			MaximumCandidates: currentMaximumNearbyPlaceCandidates,
			KnownPlaceOutcome: currentKnownPlaceOutcome,
		}
		var outcome *locationwire.AcquireGeoapifyNearbyPlaceEvidenceOutcome
		if len(currentKnownPlaceOutcome.GetMatches()) > 0 {
			outcome = &locationwire.AcquireGeoapifyNearbyPlaceEvidenceOutcome{
				Request:     request,
				Exchange:    &locationwire.ProviderExchange{State: locationwire.OperationState_OPERATION_STATE_SKIPPED_KNOWN_PLACE},
				CompletedAt: currentKnownPlaceOutcome.GetCompletedAt(),
			}
		} else {
			if len(source.GetRequest().GetKnownPlaceOutcome().GetMatches()) > 0 {
				nearbyRejections["historical request skipped a known place"]++
				continue
			}
			outcome, err = place.ReprojectRetainedGeoapifyNearbyPlaceEvidence(request, source.GetExchange(), source.GetCompletedAt())
			if err != nil {
				_ = nearbyRows.Close()
				return nil, nil, fmt.Errorf("validate retained Geoapify nearby proof: %w", err)
			}
			if !placeCandidatesEqual(source.GetCandidates(), outcome.GetCandidates()) || source.GetExchange().GetState() != outcome.GetExchange().GetState() {
				_ = nearbyRows.Close()
				return nil, nil, errors.New("typed Geoapify nearby proof differs from the live parser projection")
			}
		}
		if !place.ProviderExchangeSatisfiesCurrentLocationEvidence(outcome.GetExchange(), true) {
			_ = nearbyRows.Close()
			return nil, nil, errors.New("typed Geoapify nearby proof does not satisfy the current terminal evidence contract")
		}
		if _, duplicate := nearbyOutcomes[targetCapture.assetID]; duplicate {
			_ = nearbyRows.Close()
			return nil, nil, errors.New("typed Geoapify nearby proof archive contains duplicate current request identities")
		}
		nearbyOutcomes[targetCapture.assetID] = outcome
	}
	if err := errors.Join(nearbyRows.Err(), nearbyRows.Close()); err != nil {
		return nil, nil, err
	}
	if len(reverseRejections) != 0 || len(nearbyRejections) != 0 {
		fmt.Printf("Typed Geoapify identity exclusions: reverse %v; nearby %v.\n", reverseRejections, nearbyRejections)
	}
	return reverseOutcomes, nearbyOutcomes, nil
}

func resolveCurrentProofCapture(
	sourceInput *locationwire.CaptureLocationInput,
	legacyLocalIdentifiersByAssetID map[string]string,
	proofCapturesByAssetID map[string]*targetCaptureIdentity,
	targetCapturesByLocalIdentifier map[string]*targetCaptureIdentity,
) (*targetCaptureIdentity, string) {
	if sourceInput == nil || sourceInput.GetCoordinate() == nil || sourceInput.GetCaptureTime() == nil {
		return nil, "incomplete request input"
	}
	proofCapture, foundInProofArchive := proofCapturesByAssetID[sourceInput.GetAssetId()]
	localIdentifier, foundInLegacy := legacyLocalIdentifiersByAssetID[sourceInput.GetAssetId()]
	if foundInProofArchive {
		localIdentifier = proofCapture.localIdentifier
	} else if !foundInLegacy {
		return nil, "asset identity is absent"
	}
	targetCapture, found := targetCapturesByLocalIdentifier[localIdentifier]
	if !found {
		return nil, "Photos local identifier is absent"
	}
	if !coordinatesExactlyEqual(
		sourceInput.GetCoordinate().GetLatitude(),
		sourceInput.GetCoordinate().GetLongitude(),
		targetCapture.coordinate.GetLatitude(),
		targetCapture.coordinate.GetLongitude(),
	) {
		return nil, "capture coordinate is stale"
	}
	if !proto.Equal(sourceInput.GetCaptureTime(), targetCapture.captureTime) {
		return nil, "capture time is stale"
	}
	return targetCapture, ""
}

func historicalKnownPlaceOutcomeWasTruthfulForCapture(source *locationwire.MatchConfiguredKnownPlaceOutcome, capture *locationwire.CaptureLocationInput) bool {
	if source == nil || source.GetRequest().GetInput() == nil || !proto.Equal(source.GetRequest().GetInput(), capture) || source.GetFailure() != nil {
		return false
	}
	if source.GetCompletedAt() == nil || source.GetCompletedAt().CheckValid() != nil {
		return false
	}
	if len(source.GetMatches()) > 0 {
		return source.GetState() == locationwire.OperationState_OPERATION_STATE_SUCCEEDED
	}
	return source.GetState() == locationwire.OperationState_OPERATION_STATE_NO_RESULT
}

func placeCandidatesEqual(first, second []*locationwire.PlaceCandidate) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if !proto.Equal(first[index], second[index]) {
			return false
		}
	}
	return true
}
