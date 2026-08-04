package updatephotos

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	photosmedia "github.com/opentrawl/opentrawl/trawlers/photos/internal/media"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/media/mediawire"
	locationwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/location"
	"google.golang.org/protobuf/proto"
)

type photoProductionResult struct {
	assetID                 archive.PhotoAssetID
	currentMediaUnavailable bool
	err                     error
}

func Run(ctx context.Context, options Options) (Result, error) {
	startedAt := time.Now()
	if options.OpenedArchiveStore == nil {
		return Result{}, errors.New("Photos update archive store is required")
	}
	runner := &Runner{
		options:                           options,
		appleLocationMainThreadOperations: make(chan *appleLocationMainThreadOperation),
		productionNodeOperationSlots:      make(chan struct{}, maximumAssetsInFlight),
		observations:                      newObservationAccumulator(options.Observe),
	}
	geoapifyAttemptsInRollingDay, err := archive.CountGeoapifyProviderTransmissionAttemptsSince(
		ctx,
		options.OpenedArchiveStore,
		time.Now().Add(-24*time.Hour),
	)
	if err != nil {
		return Result{}, err
	}
	runner.geoapifyAttemptsInRollingDay = geoapifyAttemptsInRollingDay
	runner.geoapifyAttemptsInRollingDayKnown = true
	geoapifyTransmissionAllowance := max(0, maximumGeoapifyTransmissionsPerRollingDay-geoapifyAttemptsInRollingDay)
	knownPlaceConfigurationSHA256, err := archive.KnownPlaceConfigurationSHA256(ctx, options.OpenedArchiveStore)
	if err != nil {
		return Result{}, err
	}
	assets, err := archive.SelectPhotosWithPendingProductionNodes(ctx, options.OpenedArchiveStore, knownPlaceConfigurationSHA256, runner.currentPhotoLocationEvidenceMatchesDependencies)
	if err != nil {
		return Result{}, err
	}
	pendingAssetCount := len(assets)
	if options.MaximumAssetsToProcess > 0 && len(assets) > options.MaximumAssetsToProcess {
		assets = assets[:options.MaximumAssetsToProcess]
	}
	for _, asset := range assets {
		if asset.MediaType == archive.PhotoMediaKindImage {
			if err := runner.ensurePhotoLibraryAccess(ctx); err != nil {
				return Result{}, err
			}
			break
		}
	}
	result := Result{
		PendingAssets:                        pendingAssetCount,
		SelectedAssets:                       len(assets),
		GeoapifyTransmissionAllowanceAtStart: geoapifyTransmissionAllowance,
	}
	workerContext, cancelWorkers := context.WithCancel(ctx)
	defer cancelWorkers()
	jobs := make(chan archive.PhotoUpdateAsset)
	completedAssets := make(chan photoProductionResult)
	workerCount := min(maximumAssetsInFlight, len(assets))
	runner.observations.snapshot(0, len(assets), workerCount)
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for workerIndex := 0; workerIndex < workerCount; workerIndex++ {
		go func() {
			defer workers.Done()
			for asset := range jobs {
				runner.observations.startAsset(asset.AssetID)
				currentMediaUnavailable, operationErr := runner.executePhotoProductionGraph(workerContext, asset)
				runner.observations.finishAsset(asset.AssetID)
				completedAssets <- photoProductionResult{assetID: asset.AssetID, currentMediaUnavailable: currentMediaUnavailable, err: operationErr}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, asset := range assets {
			select {
			case jobs <- asset:
			case <-workerContext.Done():
				return
			}
		}
	}()
	go func() {
		workers.Wait()
		close(completedAssets)
	}()

	completedCount := 0
	var fatalErr error
	observationTicker := time.NewTicker(observationInterval)
	defer observationTicker.Stop()
	for completedAssets != nil {
		select {
		case appleOperation := <-runner.appleLocationMainThreadOperations:
			runner.completeAppleLocationMainThreadOperation(appleOperation)
		case <-observationTicker.C:
			runner.observations.snapshot(completedCount, len(assets), workerCount)
		case completed, open := <-completedAssets:
			if !open {
				completedAssets = nil
				continue
			}
			completedCount++
			if completed.err != nil {
				var mediaOutcomeError *photosmedia.PhotosMediaOutcomeError
				var deferred *AssetDeferredError
				if errors.As(completed.err, &deferred) || errors.As(completed.err, &mediaOutcomeError) && (mediaOutcomeError.AdmissionDeferred != nil || mediaOutcomeError.OperationFailure != nil) {
					result.DeferredOrFailed++
				} else if fatalErr == nil {
					fatalErr = fmt.Errorf("photo %s: %w", archive.AssetRef(string(completed.assetID)), completed.err)
					cancelWorkers()
				}
			} else {
				result.AssetsProcessed++
				if completed.currentMediaUnavailable {
					result.MediaUnavailable++
				}
			}
		}
	}
	if fatalErr != nil {
		result.Duration = time.Since(startedAt)
		return result, fmt.Errorf("Photos update stopped before all selected assets were processed: %w", fatalErr)
	}
	if err := ctx.Err(); err != nil {
		result.Duration = time.Since(startedAt)
		return result, err
	}
	runner.observations.snapshot(len(assets), len(assets), workerCount)
	result.Duration = time.Since(startedAt)
	return result, nil
}

func (runner *Runner) executePhotoProductionGraph(ctx context.Context, asset archive.PhotoUpdateAsset) (bool, error) {
	executions, err := runner.executeProductionGraph(ctx, asset)
	if err != nil {
		return false, err
	}
	return executions[ProductionNodeCurrentMedia].currentMediaUnavailable != nil, nil
}

func (runner *Runner) acquireAndStoreCurrentRenderedPhoto(ctx context.Context, asset archive.PhotoUpdateAsset, request *mediawire.AcquireCurrentRenderedStillRequest) (unavailable *mediawire.PhotosMediaUnavailable, operationErr error) {
	client := photosmedia.NewInstalledOpenTrawlClient(runner.photosMediaWorkingRoot())
	currentStill, err := client.AcquireCurrentRenderedStill(ctx, request)
	if err != nil {
		var outcomeError *photosmedia.PhotosMediaOutcomeError
		if errors.As(err, &outcomeError) && outcomeError.Unavailable != nil {
			if storeErr := archive.StoreUnavailableCurrentRenderedPhotoMediaOutcome(ctx, runner.options.OpenedArchiveStore, asset.AssetID, request, outcomeError.Unavailable); storeErr != nil {
				return nil, storeErr
			}
			return outcomeError.Unavailable, nil
		}
		return nil, err
	}
	runner.observations.acquireMediaLease(asset.AssetID, currentStill.Outcome.GetByteCount())
	defer func() {
		operationErr = errors.Join(operationErr, runner.closeCurrentRenderedStill(asset.AssetID, currentStill))
	}()
	if err := archive.StoreAvailableCurrentRenderedPhotoMediaOutcome(ctx, runner.options.OpenedArchiveStore, asset.AssetID, currentStill.Outcome); err != nil {
		return nil, err
	}
	if inspectionFilePath := runner.options.CurrentMediaInspectionFilePath; inspectionFilePath != "" {
		if err := publishCurrentRenderedImageInspectionFile(currentStill, inspectionFilePath); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

func publishCurrentRenderedImageInspectionFile(currentStill *photosmedia.CurrentRenderedStillLease, inspectionFilePath CurrentRenderedImageInspectionFilePath) error {
	imageBytes, err := currentStill.Read()
	if err != nil {
		return err
	}
	targetFilePath := string(inspectionFilePath)
	inspectionDirectory := filepath.Dir(targetFilePath)
	if err := os.MkdirAll(inspectionDirectory, 0o700); err != nil {
		return fmt.Errorf("create current photo inspection directory: %w", err)
	}
	temporaryFile, err := os.CreateTemp(inspectionDirectory, ".current-rendered-photo-*.jpg")
	if err != nil {
		return fmt.Errorf("create temporary current photo inspection file: %w", err)
	}
	temporaryFilePath := temporaryFile.Name()
	defer os.Remove(temporaryFilePath)
	if _, err := temporaryFile.Write(imageBytes); err != nil {
		_ = temporaryFile.Close()
		return fmt.Errorf("write current photo inspection file: %w", err)
	}
	if err := temporaryFile.Close(); err != nil {
		return fmt.Errorf("close current photo inspection file: %w", err)
	}
	if err := os.Rename(temporaryFilePath, targetFilePath); err != nil {
		return fmt.Errorf("publish current photo inspection file: %w", err)
	}
	return nil
}

func (runner *Runner) inspectAndStoreImmutableOriginalImageFacts(ctx context.Context, asset archive.PhotoUpdateAsset, request *mediawire.InspectImmutableOriginalImageFactsRequest) (WorkDisposition, error) {
	client := photosmedia.NewInstalledOpenTrawlClient(runner.photosMediaWorkingRoot())
	outcome, err := client.InspectImmutableOriginalImageFacts(ctx, request)
	if err != nil {
		return WorkFailed, err
	}
	if err := archive.StoreCurrentImmutableOriginalImageFactsOutcome(ctx, runner.options.OpenedArchiveStore, asset.AssetID, outcome); err != nil {
		return WorkFailed, err
	}
	switch outcome.GetState() {
	case mediawire.ImmutableOriginalImageFactsState_IMMUTABLE_ORIGINAL_IMAGE_FACTS_STATE_AVAILABLE:
		return WorkAcquired, nil
	case mediawire.ImmutableOriginalImageFactsState_IMMUTABLE_ORIGINAL_IMAGE_FACTS_STATE_UNAVAILABLE:
		return WorkSkipped, nil
	case mediawire.ImmutableOriginalImageFactsState_IMMUTABLE_ORIGINAL_IMAGE_FACTS_STATE_FAILED:
		return WorkFailed, &photosmedia.PhotosMediaOutcomeError{AdmissionDeferred: outcome.GetAdmissionDeferred(), OperationFailure: outcome.GetFailure()}
	default:
		return WorkFailed, errors.New("OpenTrawl returned no immutable original image facts outcome")
	}
}

func (runner *Runner) currentPhotoLocationEvidenceMatchesDependencies(ctx context.Context, asset archive.PhotoUpdateAsset, input *locationwire.CaptureLocationInput, retained *locationwire.ComposePhotoLocationEvidenceOutcome) (bool, error) {
	knownPlaceConfigurationSHA256, err := archive.KnownPlaceConfigurationSHA256(ctx, runner.options.OpenedArchiveStore)
	if err != nil {
		return false, err
	}
	knownRequest := &locationwire.MatchConfiguredKnownPlaceRequest{Input: input, KnownPlaceConfigurationSha256: knownPlaceConfigurationSHA256}
	known, found, err := archive.LoadMatchConfiguredKnownPlaceOutcome(ctx, runner.options.OpenedArchiveStore, input.GetAssetId())
	if err != nil || !found || !proto.Equal(known.GetRequest(), knownRequest) {
		return false, err
	}
	appleReverse, found, err := archive.LoadAppleReverseGeocodingEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, input.GetAssetId())
	if err != nil || !found || !proto.Equal(appleReverse.GetRequest(), appleReverseGeocodingEvidenceRequest(input)) {
		return false, err
	}
	appleNearby, found, err := archive.LoadAppleNearbyPlaceEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, input.GetAssetId())
	if err != nil || !found || !proto.Equal(appleNearby.GetRequest(), appleNearbyPlaceEvidenceRequest(input)) {
		return false, err
	}
	geoapifyReverse, found, err := archive.LoadGeoapifyReverseGeocodingEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, input.GetAssetId())
	if err != nil || !found || !proto.Equal(geoapifyReverse.GetRequest(), geoapifyReverseGeocodingEvidenceRequest(input)) {
		return false, err
	}
	geoapify, found, err := archive.LoadGeoapifyNearbyPlaceEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, input.GetAssetId())
	if err != nil || !found || !proto.Equal(geoapify.GetRequest(), geoapifyNearbyPlaceEvidenceRequest(input)) {
		return false, err
	}
	return composePhotoLocationEvidenceRequestMatchesDependencies(retained, known, appleReverse, appleNearby, geoapifyReverse, geoapify), nil
}

func composePhotoLocationEvidenceRequestMatchesDependencies(
	retained *locationwire.ComposePhotoLocationEvidenceOutcome,
	known *locationwire.MatchConfiguredKnownPlaceOutcome,
	appleReverse *locationwire.AcquireAppleReverseGeocodingEvidenceOutcome,
	appleNearby *locationwire.AcquireAppleNearbyPlaceEvidenceOutcome,
	geoapifyReverse *locationwire.AcquireGeoapifyReverseGeocodingEvidenceOutcome,
	geoapify *locationwire.AcquireGeoapifyNearbyPlaceEvidenceOutcome,
) bool {
	return archive.PhotoLocationEvidenceCompositionMatchesDependencies(retained, known, appleReverse, appleNearby, geoapifyReverse, geoapify)
}
