package updatephotos

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/media/mediawire"
	locationwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/location"
	"google.golang.org/protobuf/proto"
)

type productionNodeOperation func(context.Context, *Runner, archive.PhotoUpdateAsset) productionNodeExecution

type productionNodeExecution struct {
	disposition             WorkDisposition
	err                     error
	currentMediaUnavailable *mediawire.PhotosMediaUnavailable
	providerExchange        *locationwire.ProviderExchange
}

func runProductionNode(ctx context.Context, options Options, nodeName ProductionNodeName, asset archive.PhotoUpdateAsset) (WorkDisposition, error) {
	runner := &Runner{
		options:                           options,
		appleLocationMainThreadOperations: make(chan *appleLocationMainThreadOperation),
		productionNodeOperationSlots:      make(chan struct{}, maximumAssetsInFlight),
		observations:                      newObservationAccumulator(options.Observe),
	}
	node, found := productionNodeForName(nodeName)
	if !found || !node.RequiresPhoto || node.operation == nil {
		return WorkFailed, fmt.Errorf("Photos production node %q cannot run for one photo", nodeName)
	}

	var execution productionNodeExecution
	err := runWithAppleMainThreadOperations(ctx, runner, func() error {
		execution = runner.executeProductionNode(ctx, node, asset)
		return execution.err
	})
	return execution.disposition, err
}

func (runner *Runner) executeProductionGraph(ctx context.Context, asset archive.PhotoUpdateAsset) (map[ProductionNodeName]productionNodeExecution, error) {
	completed := map[ProductionNodeName]productionNodeExecution{
		ProductionNodeSource: {disposition: WorkReused},
	}
	completedSignals := make(map[ProductionNodeName]chan struct{}, len(productionNodesInDependencyOrder))
	for _, node := range productionNodesInDependencyOrder {
		completedSignals[node.Name] = make(chan struct{})
	}
	close(completedSignals[ProductionNodeSource])

	var completedMutex sync.Mutex
	var operations sync.WaitGroup
	for _, node := range productionNodesInDependencyOrder {
		if !node.RequiresPhoto {
			continue
		}
		operations.Add(1)
		go func(node ProductionNode) {
			defer operations.Done()
			dependencyWaitCancelled := false
			for _, dependency := range node.Dependencies {
				select {
				case <-completedSignals[dependency]:
				case <-ctx.Done():
					dependencyWaitCancelled = true
				}
				if dependencyWaitCancelled {
					break
				}
			}
			var execution productionNodeExecution
			if dependencyWaitCancelled {
				execution = productionNodeExecution{disposition: WorkFailed, err: ctx.Err()}
				runner.observeProductionNodeExecution(asset.AssetID, node.Name, execution)
			} else {
				completedMutex.Lock()
				dependencyDidNotFinish := productionNodeDependencyDidNotFinish(node, completed)
				completedMutex.Unlock()
				if dependencyDidNotFinish {
					execution = productionNodeExecution{disposition: WorkDeferred}
					runner.observeProductionNodeExecution(asset.AssetID, node.Name, execution)
				} else {
					execution = runner.executeProductionNode(ctx, node, asset)
				}
			}
			completedMutex.Lock()
			completed[node.Name] = execution
			completedMutex.Unlock()
			close(completedSignals[node.Name])
		}(node)
	}
	operations.Wait()

	var operationErrors []error
	hasDeferredWork := false
	for _, node := range productionNodesInDependencyOrder {
		execution := completed[node.Name]
		if execution.err != nil {
			operationErrors = append(operationErrors, fmt.Errorf("%s: %w", node.Name, execution.err))
		}
		if execution.disposition == WorkDeferred {
			hasDeferredWork = true
		}
	}
	if hasDeferredWork && len(operationErrors) == 0 {
		operationErrors = append(operationErrors, &AssetDeferredError{Reason: "Photos work remains retryable"})
	}
	return completed, errors.Join(operationErrors...)
}

func productionNodeDependencyDidNotFinish(node ProductionNode, completed map[ProductionNodeName]productionNodeExecution) bool {
	for _, dependency := range node.Dependencies {
		disposition := completed[dependency].disposition
		if disposition == WorkDeferred || disposition == WorkFailed {
			return true
		}
	}
	return false
}

func (runner *Runner) executeProductionNode(ctx context.Context, node ProductionNode, asset archive.PhotoUpdateAsset) productionNodeExecution {
	select {
	case runner.productionNodeOperationSlots <- struct{}{}:
		defer func() { <-runner.productionNodeOperationSlots }()
	case <-ctx.Done():
		execution := productionNodeExecution{disposition: WorkFailed, err: ctx.Err()}
		runner.observeProductionNodeExecution(asset.AssetID, node.Name, execution)
		return execution
	}
	runner.observations.startNode(asset.AssetID, node.Name)
	execution := node.operation(ctx, runner, asset)
	if execution.err != nil && execution.disposition != WorkDeferred {
		execution.disposition = WorkFailed
	}
	runner.finishProductionNodeObservation(asset.AssetID, node.Name, execution)
	return execution
}

func (runner *Runner) observeProductionNodeExecution(assetID archive.PhotoAssetID, nodeName ProductionNodeName, execution productionNodeExecution) {
	runner.observations.startNode(assetID, nodeName)
	runner.finishProductionNodeObservation(assetID, nodeName, execution)
}

func (runner *Runner) finishProductionNodeObservation(assetID archive.PhotoAssetID, nodeName ProductionNodeName, execution productionNodeExecution) {
	if execution.providerExchange != nil {
		runner.observations.finishNodeWithProvider(
			assetID,
			nodeName,
			execution.disposition,
			locationEvidenceProviderForNode(nodeName),
			execution.providerExchange.GetFailure().GetClass(),
			nil,
			nil,
		)
		return
	}
	if execution.err != nil {
		runner.finishObservedNode(assetID, nodeName, execution.err, false)
		return
	}
	runner.observations.finishNode(assetID, nodeName, execution.disposition, nil, nil)
}

func runCurrentMediaNode(ctx context.Context, runner *Runner, asset archive.PhotoUpdateAsset) productionNodeExecution {
	if asset.MediaType != archive.PhotoMediaKindImage {
		return productionNodeExecution{disposition: WorkSkipped}
	}
	request := archive.CurrentRenderedStillRequestForPhotoUpdateAsset(asset)
	retained, found, err := archive.LoadCurrentRenderedPhotoMediaOutcome(ctx, runner.options.OpenedArchiveStore, asset.AssetID)
	if err != nil {
		return productionNodeExecution{disposition: WorkFailed, err: err}
	}
	if runner.options.CurrentMediaInspectionFilePath == "" && found && archive.CurrentRenderedPhotoMediaOutcomeMatchesRequest(retained, request) {
		return productionNodeExecution{disposition: WorkReused, currentMediaUnavailable: retained.GetUnavailable().GetReason()}
	}
	if err := runner.ensurePhotoLibraryAccess(ctx); err != nil {
		return productionNodeExecution{disposition: WorkFailed, err: err}
	}
	unavailable, err := runner.acquireAndStoreCurrentRenderedPhoto(ctx, asset, request)
	if unavailable != nil {
		return productionNodeExecution{disposition: WorkSkipped, currentMediaUnavailable: unavailable}
	}
	return productionNodeExecution{disposition: WorkAcquired, err: err}
}

func runImmutableOriginalImageFactsNode(ctx context.Context, runner *Runner, asset archive.PhotoUpdateAsset) productionNodeExecution {
	if asset.MediaType != archive.PhotoMediaKindImage {
		return productionNodeExecution{disposition: WorkSkipped}
	}
	request := archive.ImmutableOriginalImageFactsRequestForPhotoUpdateAsset(asset)
	_, found, err := archive.LoadCurrentImmutableOriginalImageFactsOutcomeForRequest(ctx, runner.options.OpenedArchiveStore, asset.AssetID, request)
	if err != nil {
		return productionNodeExecution{disposition: WorkFailed, err: err}
	}
	if found {
		return productionNodeExecution{disposition: WorkReused}
	}
	if err := runner.ensurePhotoLibraryAccess(ctx); err != nil {
		return productionNodeExecution{disposition: WorkFailed, err: err}
	}
	disposition, err := runner.inspectAndStoreImmutableOriginalImageFacts(ctx, asset, request)
	return productionNodeExecution{disposition: disposition, err: err}
}

func runKnownPlaceNode(ctx context.Context, runner *Runner, asset archive.PhotoUpdateAsset) productionNodeExecution {
	input, execution := loadCaptureLocationNodeInput(ctx, runner, asset)
	if execution.disposition != 0 {
		return execution
	}
	knownPlaceConfigurationSHA256, err := archive.KnownPlaceConfigurationSHA256(ctx, runner.options.OpenedArchiveStore)
	if err != nil {
		return productionNodeExecution{disposition: WorkFailed, err: err}
	}
	_, disposition, err := runner.matchConfiguredKnownPlace(ctx, input, knownPlaceConfigurationSHA256)
	return productionNodeExecution{disposition: disposition, err: err}
}

func runAppleReverseGeocodingNode(ctx context.Context, runner *Runner, asset archive.PhotoUpdateAsset) productionNodeExecution {
	input, execution := loadCaptureLocationNodeInput(ctx, runner, asset)
	if execution.disposition != 0 {
		return execution
	}
	outcome, err := runner.acquireAppleReverseGeocodingEvidence(ctx, input)
	return providerNodeExecution(outcome.GetExchange(), outcome.GetEvidenceUse(), err)
}

func runGeoapifyReverseGeocodingNode(ctx context.Context, runner *Runner, asset archive.PhotoUpdateAsset) productionNodeExecution {
	input, execution := loadCaptureLocationNodeInput(ctx, runner, asset)
	if execution.disposition != 0 {
		return execution
	}
	outcome, err := runner.acquireGeoapifyReverseGeocodingEvidence(ctx, input)
	return providerNodeExecution(outcome.GetExchange(), outcome.GetEvidenceUse(), err)
}

func runAppleNearbyPlacesNode(ctx context.Context, runner *Runner, asset archive.PhotoUpdateAsset) productionNodeExecution {
	input, known, execution := loadNearbyPlaceNodeInput(ctx, runner, asset)
	if execution.disposition != 0 {
		return execution
	}
	if len(known.GetMatches()) > 0 {
		outcome, _ := suppressedNearbyProviderOutcomes(input)
		err := archive.StoreAppleNearbyPlaceEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, outcome)
		return providerNodeExecution(outcome.GetExchange(), outcome.GetEvidenceUse(), err)
	}
	outcome, err := runner.acquireAppleNearbyPlaceEvidence(ctx, input)
	return providerNodeExecution(outcome.GetExchange(), outcome.GetEvidenceUse(), err)
}

func runGeoapifyNearbyPlacesNode(ctx context.Context, runner *Runner, asset archive.PhotoUpdateAsset) productionNodeExecution {
	input, known, execution := loadNearbyPlaceNodeInput(ctx, runner, asset)
	if execution.disposition != 0 {
		return execution
	}
	if len(known.GetMatches()) > 0 {
		_, outcome := suppressedNearbyProviderOutcomes(input)
		err := archive.StoreGeoapifyNearbyPlaceEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, outcome)
		return providerNodeExecution(outcome.GetExchange(), outcome.GetEvidenceUse(), err)
	}
	outcome, err := runner.acquireGeoapifyNearbyPlaceEvidence(ctx, input)
	return providerNodeExecution(outcome.GetExchange(), outcome.GetEvidenceUse(), err)
}

func loadNearbyPlaceNodeInput(ctx context.Context, runner *Runner, asset archive.PhotoUpdateAsset) (*locationwire.CaptureLocationInput, *locationwire.MatchConfiguredKnownPlaceOutcome, productionNodeExecution) {
	input, execution := loadCaptureLocationNodeInput(ctx, runner, asset)
	if execution.disposition != 0 {
		return nil, nil, execution
	}
	knownPlaceConfigurationSHA256, err := archive.KnownPlaceConfigurationSHA256(ctx, runner.options.OpenedArchiveStore)
	if err != nil {
		return nil, nil, productionNodeExecution{disposition: WorkFailed, err: err}
	}
	knownRequest := &locationwire.MatchConfiguredKnownPlaceRequest{Input: input, KnownPlaceConfigurationSha256: knownPlaceConfigurationSHA256}
	known, retained, err := archive.LoadMatchConfiguredKnownPlaceOutcome(ctx, runner.options.OpenedArchiveStore, input.GetAssetId())
	if err != nil {
		return nil, nil, productionNodeExecution{disposition: WorkFailed, err: err}
	}
	if !retained || !proto.Equal(known.GetRequest(), knownRequest) {
		return nil, nil, productionNodeExecution{disposition: WorkFailed, err: missingUpstreamProductionNode(ProductionNodeKnownPlace)}
	}
	return input, known, productionNodeExecution{}
}

func runComposeLocationEvidenceNode(ctx context.Context, runner *Runner, asset archive.PhotoUpdateAsset) productionNodeExecution {
	input, execution := loadCaptureLocationNodeInput(ctx, runner, asset)
	if execution.disposition != 0 {
		return execution
	}
	knownPlaceConfigurationSHA256, err := archive.KnownPlaceConfigurationSHA256(ctx, runner.options.OpenedArchiveStore)
	if err != nil {
		return productionNodeExecution{disposition: WorkFailed, err: err}
	}
	knownRequest := &locationwire.MatchConfiguredKnownPlaceRequest{Input: input, KnownPlaceConfigurationSha256: knownPlaceConfigurationSHA256}
	known, retained, err := archive.LoadMatchConfiguredKnownPlaceOutcome(ctx, runner.options.OpenedArchiveStore, input.GetAssetId())
	if err != nil || !retained || !proto.Equal(known.GetRequest(), knownRequest) {
		return productionNodeExecution{disposition: WorkFailed, err: errors.Join(missingUpstreamProductionNode(ProductionNodeKnownPlace), err)}
	}
	appleReverse, retained, err := archive.LoadAppleReverseGeocodingEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, input.GetAssetId())
	if err != nil || !retained || !proto.Equal(appleReverse.GetRequest(), appleReverseGeocodingEvidenceRequest(input)) {
		return productionNodeExecution{disposition: WorkFailed, err: errors.Join(missingUpstreamProductionNode(ProductionNodeAppleReverseGeocoding), err)}
	}
	appleNearby, retained, err := archive.LoadAppleNearbyPlaceEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, input.GetAssetId())
	if err != nil || !retained || !proto.Equal(appleNearby.GetRequest(), appleNearbyPlaceEvidenceRequest(input)) {
		return productionNodeExecution{disposition: WorkFailed, err: errors.Join(missingUpstreamProductionNode(ProductionNodeAppleNearbyPlaces), err)}
	}
	geoapifyReverse, retained, err := archive.LoadGeoapifyReverseGeocodingEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, input.GetAssetId())
	if err != nil || !retained || !proto.Equal(geoapifyReverse.GetRequest(), geoapifyReverseGeocodingEvidenceRequest(input)) {
		return productionNodeExecution{disposition: WorkFailed, err: errors.Join(missingUpstreamProductionNode(ProductionNodeGeoapifyReverseGeocoding), err)}
	}
	geoapify, retained, err := archive.LoadGeoapifyNearbyPlaceEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, input.GetAssetId())
	if err != nil || !retained || !proto.Equal(geoapify.GetRequest(), geoapifyNearbyPlaceEvidenceRequest(input)) {
		return productionNodeExecution{disposition: WorkFailed, err: errors.Join(missingUpstreamProductionNode(ProductionNodeGeoapifyNearbyPlaces), err)}
	}
	if current, retained, retainedErr := archive.LoadCurrentPhotoLocationEvidence(ctx, runner.options.OpenedArchiveStore, asset.AssetID); retainedErr != nil {
		return productionNodeExecution{disposition: WorkFailed, err: retainedErr}
	} else if retained && composePhotoLocationEvidenceRequestMatchesDependencies(current, known, appleReverse, appleNearby, geoapifyReverse, geoapify) {
		return productionNodeExecution{disposition: WorkReused}
	}
	_, err = runner.composePhotoLocationEvidence(ctx, asset, knownPlaceConfigurationSHA256, known, appleReverse, appleNearby, geoapifyReverse, geoapify)
	return productionNodeExecution{disposition: WorkAcquired, err: err}
}

func loadCaptureLocationNodeInput(ctx context.Context, runner *Runner, asset archive.PhotoUpdateAsset) (*locationwire.CaptureLocationInput, productionNodeExecution) {
	if asset.MediaType != archive.PhotoMediaKindImage {
		return nil, productionNodeExecution{disposition: WorkSkipped}
	}
	input, found, err := archive.LoadOptionalCaptureLocationInput(ctx, runner.options.OpenedArchiveStore, string(asset.AssetID))
	if err != nil {
		return nil, productionNodeExecution{disposition: WorkFailed, err: err}
	}
	if !found {
		return nil, productionNodeExecution{disposition: WorkSkipped}
	}
	return input, productionNodeExecution{}
}

func providerNodeExecution(exchange *locationwire.ProviderExchange, evidenceUse locationwire.ProviderEvidenceUse, err error) productionNodeExecution {
	return productionNodeExecution{
		disposition:      locationProviderEvidenceWorkDisposition(exchange, evidenceUse, err),
		err:              err,
		providerExchange: exchange,
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
