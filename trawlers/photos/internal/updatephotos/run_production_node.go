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
		knownPlaceConfigurationSHA256, err := archive.KnownPlaceConfigurationSHA256(ctx, options.OpenedArchiveStore)
		if err != nil {
			return WorkFailed, err
		}
		_, disposition, err := runner.matchConfiguredKnownPlace(ctx, input, knownPlaceConfigurationSHA256)
		return disposition, err
	case ProductionNodeAppleReverseGeocoding, ProductionNodeAppleNearbyPlaces, ProductionNodeGeoapifyReverseGeocoding, ProductionNodeGeoapifyNearbyPlaces, ProductionNodeComposeLocationEvidence:
		return runLocationProductionNode(ctx, runner, nodeName, asset)
	default:
		return WorkFailed, fmt.Errorf("Photos production node %q cannot run for one photo", nodeName)
	}
}

func runLocationProductionNode(ctx context.Context, runner *Runner, nodeName ProductionNodeName, asset archive.PhotoUpdateAsset) (WorkDisposition, error) {
	input, found, err := archive.LoadOptionalCaptureLocationInput(ctx, runner.options.OpenedArchiveStore, string(asset.AssetID))
	if err != nil || !found {
		return WorkSkipped, err
	}
	switch nodeName {
	case ProductionNodeAppleReverseGeocoding:
		var outcome *locationwire.AcquireAppleReverseGeocodingEvidenceOutcome
		err := runWithAppleMainThreadOperations(ctx, runner, func() error {
			var operationErr error
			outcome, operationErr = runner.acquireAppleReverseGeocodingEvidence(ctx, input)
			return operationErr
		})
		return locationProviderEvidenceWorkDisposition(outcome.GetExchange(), outcome.GetEvidenceUse(), err), err
	case ProductionNodeGeoapifyReverseGeocoding:
		outcome, err := runner.acquireGeoapifyReverseGeocodingEvidence(ctx, input)
		return locationProviderEvidenceWorkDisposition(outcome.GetExchange(), outcome.GetEvidenceUse(), err), err
	}
	knownPlaceConfigurationSHA256, err := archive.KnownPlaceConfigurationSHA256(ctx, runner.options.OpenedArchiveStore)
	if err != nil {
		return WorkFailed, err
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
		return locationProviderEvidenceWorkDisposition(outcome.GetExchange(), outcome.GetEvidenceUse(), err), err
	case ProductionNodeGeoapifyNearbyPlaces:
		if len(known.GetMatches()) > 0 {
			_, geoapify := suppressedNearbyProviderOutcomes(input)
			return WorkSkipped, archive.StoreGeoapifyNearbyPlaceEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, geoapify)
		}
		outcome, err := runner.acquireGeoapifyNearbyPlaceEvidence(ctx, input)
		return locationProviderEvidenceWorkDisposition(outcome.GetExchange(), outcome.GetEvidenceUse(), err), err
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
		geoapifyRequest := geoapifyNearbyPlaceEvidenceRequest(input)
		geoapify, found, err := archive.LoadGeoapifyNearbyPlaceEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, input.GetAssetId())
		if err != nil || !found || !proto.Equal(geoapify.GetRequest(), geoapifyRequest) {
			return WorkFailed, errors.Join(missingUpstreamProductionNode(ProductionNodeGeoapifyNearbyPlaces), err)
		}
		if retained, found, retainedErr := archive.LoadCurrentPhotoLocationEvidence(ctx, runner.options.OpenedArchiveStore, asset.AssetID); retainedErr != nil {
			return WorkFailed, retainedErr
		} else if found && composePhotoLocationEvidenceRequestMatchesDependencies(retained, known, appleReverse, appleNearby, geoapifyReverse, geoapify) {
			return WorkReused, nil
		}
		_, err = runner.composePhotoLocationEvidence(ctx, asset, knownPlaceConfigurationSHA256, known, appleReverse, appleNearby, geoapifyReverse, geoapify)
		return WorkAcquired, err
	default:
		return WorkFailed, fmt.Errorf("unknown Photos location production node %q", nodeName)
	}
}

func locationProviderEvidenceWorkDisposition(exchange *locationwire.ProviderExchange, evidenceUse locationwire.ProviderEvidenceUse, operationErr error) WorkDisposition {
	if operationErr != nil {
		var deferred *AssetDeferredError
		if errors.As(operationErr, &deferred) {
			return WorkDeferred
		}
		return WorkFailed
	}
	switch exchange.GetState() {
	case locationwire.OperationState_OPERATION_STATE_SUCCEEDED, locationwire.OperationState_OPERATION_STATE_NO_RESULT:
		if evidenceUse == locationwire.ProviderEvidenceUse_PROVIDER_EVIDENCE_USE_REUSED {
			return WorkReused
		}
		return WorkAcquired
	case locationwire.OperationState_OPERATION_STATE_SKIPPED_KNOWN_PLACE:
		return WorkSkipped
	case locationwire.OperationState_OPERATION_STATE_REQUEST_RETAINED,
		locationwire.OperationState_OPERATION_STATE_TRANSMISSION_STARTED,
		locationwire.OperationState_OPERATION_STATE_RESPONSE_RETAINED,
		locationwire.OperationState_OPERATION_STATE_FAILED:
		return WorkDeferred
	default:
		return WorkFailed
	}
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
