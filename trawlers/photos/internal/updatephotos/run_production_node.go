package updatephotos

import (
	"context"
	"errors"
	"fmt"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	locationwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/location"
	"google.golang.org/protobuf/proto"
)

func runProductionNode(ctx context.Context, options Options, nodeName ProductionNodeName, asset archive.PhotoUpdateAsset) (WorkDisposition, error) {
	runner := &Runner{
		options:                           options,
		appleLocationMainThreadOperations: make(chan *appleLocationMainThreadOperation),
		observations:                      newObservationAccumulator(options.Observe),
	}
	runner.observations.startNode(asset.AssetID, nodeName)
	disposition, operationErr := runProductionNodeWithRunner(ctx, runner, nodeName, asset)
	if operationErr != nil && disposition != WorkDeferred {
		disposition = WorkFailed
	}
	runner.observations.finishNode(asset.AssetID, nodeName, disposition, nil, nil)
	return disposition, operationErr
}

func runProductionNodeWithRunner(ctx context.Context, runner *Runner, nodeName ProductionNodeName, asset archive.PhotoUpdateAsset) (WorkDisposition, error) {
	options := runner.options
	knownPlaceConfigurationSHA256, err := archive.KnownPlaceConfigurationSHA256(ctx, options.OpenedArchiveStore)
	if err != nil {
		return WorkFailed, err
	}
	switch nodeName {
	case ProductionNodeCurrentMedia:
		request := archive.CurrentRenderedStillRequestForPhotoUpdateAsset(asset)
		retained, found, err := archive.LoadCurrentRenderedPhotoMediaEvidence(ctx, options.OpenedArchiveStore, asset.AssetID)
		if err != nil {
			return WorkFailed, err
		}
		if options.CurrentMediaInspectionFilePath == "" && found && archive.CurrentRenderedPhotoMediaEvidenceMatchesRequest(retained, request) {
			return WorkReused, nil
		}
		if err := runner.ensurePhotoLibraryAccess(ctx); err != nil {
			return WorkFailed, err
		}
		unavailable, err := runner.acquireAndStoreCurrentRenderedPhoto(ctx, asset, request)
		if unavailable != nil {
			return WorkDeferred, fmt.Errorf("current photo media is unavailable: %s", unavailable.GetHumanDescription())
		}
		return WorkAcquired, err
	case ProductionNodeImmutableOriginalImageFacts:
		request := archive.ImmutableOriginalImageFactsRequestForPhotoUpdateAsset(asset)
		_, found, err := archive.LoadCurrentImmutableOriginalImageFactsOutcomeForRequest(ctx, options.OpenedArchiveStore, asset.AssetID, request)
		if err != nil || found {
			return WorkReused, err
		}
		if err := runner.ensurePhotoLibraryAccess(ctx); err != nil {
			return WorkFailed, err
		}
		return runner.inspectAndStoreImmutableOriginalImageFacts(ctx, asset, request)
	case ProductionNodeKnownPlace:
		input, found, err := archive.LoadOptionalCaptureLocationInput(ctx, options.OpenedArchiveStore, string(asset.AssetID))
		if err != nil || !found {
			return WorkSkipped, err
		}
		_, disposition, err := runner.matchConfiguredKnownPlace(ctx, input, knownPlaceConfigurationSHA256)
		return disposition, err
	case ProductionNodeAppleReverseGeocoding, ProductionNodeAppleNearbyPlaces, ProductionNodeGeoapifyReverseGeocoding, ProductionNodeGeoapifyPhotographedPlaceCandidates, ProductionNodeComposeLocationEvidence:
		return runLocationProductionNode(ctx, runner, nodeName, asset, knownPlaceConfigurationSHA256)
	default:
		return WorkFailed, fmt.Errorf("Photos production node %q cannot run for one photo", nodeName)
	}
}

func runLocationProductionNode(ctx context.Context, runner *Runner, nodeName ProductionNodeName, asset archive.PhotoUpdateAsset, knownPlaceConfigurationSHA256 []byte) (WorkDisposition, error) {
	input, found, err := archive.LoadOptionalCaptureLocationInput(ctx, runner.options.OpenedArchiveStore, string(asset.AssetID))
	if err != nil || !found {
		return WorkSkipped, err
	}
	knownRequest := &locationwire.MatchConfiguredKnownPlaceRequest{Input: input, KnownPlaceConfigurationSha256: knownPlaceConfigurationSHA256}
	known, knownFound, err := archive.LoadMatchConfiguredKnownPlaceOutcome(ctx, runner.options.OpenedArchiveStore, input.GetAssetId())
	if err != nil {
		return WorkFailed, err
	}
	if !knownFound || !proto.Equal(known.GetRequest(), knownRequest) {
		return WorkFailed, missingUpstreamProductionNode(ProductionNodeKnownPlace)
	}
	switch nodeName {
	case ProductionNodeAppleReverseGeocoding:
		var outcome *locationwire.AcquireAppleReverseGeocodingEvidenceOutcome
		err := runWithAppleMainThreadOperations(ctx, runner, func() error {
			var operationErr error
			outcome, operationErr = runner.acquireAppleReverseGeocodingEvidence(ctx, input)
			return operationErr
		})
		return appleProviderEvidenceWorkDisposition(outcome.GetEvidenceUse(), err), err
	case ProductionNodeAppleNearbyPlaces:
		if len(known.GetMatches()) > 0 {
			appleNearby, _ := suppressedNearbyProviderOutcomes(input)
			return WorkSkipped, archive.StoreAppleNearbyPlaceEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, appleNearby)
		}
		var outcome *locationwire.AcquireAppleNearbyPlaceEvidenceOutcome
		err := runWithAppleMainThreadOperations(ctx, runner, func() error {
			var operationErr error
			outcome, operationErr = runner.acquireAppleNearbyPlaceEvidence(ctx, input)
			return operationErr
		})
		return appleProviderEvidenceWorkDisposition(outcome.GetEvidenceUse(), err), err
	case ProductionNodeGeoapifyReverseGeocoding:
		outcome, err := runner.acquireGeoapifyReverseGeocodingEvidence(ctx, input)
		if err != nil {
			var deferred *AssetDeferredError
			if errors.As(err, &deferred) {
				return WorkDeferred, err
			}
			return WorkFailed, err
		}
		return providerEvidenceWorkDisposition(outcome.GetEvidenceUse()), nil
	case ProductionNodeGeoapifyPhotographedPlaceCandidates:
		if len(known.GetMatches()) > 0 {
			_, geoapify := suppressedNearbyProviderOutcomes(input)
			return WorkSkipped, archive.StoreGeoapifyPhotographedPlaceCandidateEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, geoapify)
		}
		outcome, err := runner.acquireGeoapifyPhotographedPlaceCandidateEvidence(ctx, input)
		if err != nil {
			var deferred *AssetDeferredError
			if errors.As(err, &deferred) {
				return WorkDeferred, err
			}
			return WorkFailed, err
		}
		return providerEvidenceWorkDisposition(outcome.GetEvidenceUse()), err
	case ProductionNodeComposeLocationEvidence:
		appleReverseRequest := appleReverseGeocodingEvidenceRequest(input)
		appleReverse, found, err := archive.LoadAppleReverseGeocodingEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, input.GetAssetId())
		if err != nil || !found || !proto.Equal(appleReverse.GetRequest(), appleReverseRequest) {
			return WorkFailed, errors.Join(missingUpstreamProductionNode(ProductionNodeAppleReverseGeocoding), err)
		}
		appleNearbyRequest := appleNearbyPlaceEvidenceRequest(input)
		appleNearby, found, err := archive.LoadAppleNearbyPlaceEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, input.GetAssetId())
		if err != nil || !found || !proto.Equal(appleNearby.GetRequest(), appleNearbyRequest) {
			return WorkFailed, errors.Join(missingUpstreamProductionNode(ProductionNodeAppleNearbyPlaces), err)
		}
		geoapifyReverseRequest := geoapifyReverseGeocodingEvidenceRequest(input)
		geoapifyReverse, found, err := archive.LoadGeoapifyReverseGeocodingEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, input.GetAssetId())
		if err != nil || !found || !proto.Equal(geoapifyReverse.GetRequest(), geoapifyReverseRequest) {
			return WorkFailed, errors.Join(missingUpstreamProductionNode(ProductionNodeGeoapifyReverseGeocoding), err)
		}
		geoapifyRequest := geoapifyPhotographedPlaceCandidateEvidenceRequest(input)
		geoapify, found, err := archive.LoadGeoapifyPhotographedPlaceCandidateEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, input.GetAssetId())
		if err != nil || !found || !proto.Equal(geoapify.GetRequest(), geoapifyRequest) {
			return WorkFailed, errors.Join(missingUpstreamProductionNode(ProductionNodeGeoapifyPhotographedPlaceCandidates), err)
		}
		if retained, found, retainedErr := archive.LoadCurrentPhotoLocationEvidence(ctx, runner.options.OpenedArchiveStore, asset, knownPlaceConfigurationSHA256); retainedErr != nil {
			return WorkFailed, retainedErr
		} else if found && archive.CurrentPhotoLocationEvidenceMatchesInput(retained, input) && composePhotoLocationEvidenceRequestMatchesDependencies(retained, known, appleReverse, appleNearby, geoapifyReverse, geoapify) {
			return WorkReused, nil
		}
		_, err = runner.composePhotoLocationEvidence(ctx, asset, knownPlaceConfigurationSHA256, known, appleReverse, appleNearby, geoapifyReverse, geoapify)
		return WorkAcquired, err
	default:
		return WorkFailed, fmt.Errorf("unknown Photos location production node %q", nodeName)
	}
}

func providerEvidenceWorkDisposition(evidenceUse locationwire.ProviderEvidenceUse) WorkDisposition {
	if evidenceUse == locationwire.ProviderEvidenceUse_PROVIDER_EVIDENCE_USE_REUSED {
		return WorkReused
	}
	return WorkAcquired
}

func appleProviderEvidenceWorkDisposition(evidenceUse locationwire.ProviderEvidenceUse, operationErr error) WorkDisposition {
	if operationErr != nil {
		var deferred *AssetDeferredError
		if errors.As(operationErr, &deferred) {
			return WorkDeferred
		}
		return WorkFailed
	}
	return providerEvidenceWorkDisposition(evidenceUse)
}

func runWithAppleMainThreadOperations(ctx context.Context, runner *Runner, operation func() error) error {
	completed := make(chan error, 1)
	go func() { completed <- operation() }()
	for {
		select {
		case appleOperation := <-runner.appleLocationMainThreadOperations:
			runner.completeAppleLocationMainThreadOperation(appleOperation)
		case err := <-completed:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func missingUpstreamProductionNode(nodeName ProductionNodeName) error {
	return fmt.Errorf("%s has no current retained output; run that node first", nodeName)
}
