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
	foundationwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/foundation"
	locationwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/location"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type photoFoundationResult struct {
	state foundationwire.PhotoFoundationOutcomeState
	err   error
}

func Run(ctx context.Context, options Options) (Result, error) {
	if options.OpenedArchiveStore == nil {
		return Result{}, errors.New("Photos update archive store is required")
	}
	runner := &Runner{
		options:                           options,
		appleLocationMainThreadOperations: make(chan appleLocationMainThreadOperation),
		observations:                      newObservationAccumulator(options.Observe),
	}
	knownPlaceConfigurationSHA256, err := archive.KnownPlaceConfigurationSHA256(ctx, options.OpenedArchiveStore)
	if err != nil {
		return Result{}, err
	}
	assets, err := archive.SelectPendingPhotoFoundationAssets(ctx, options.OpenedArchiveStore, knownPlaceConfigurationSHA256)
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
	result := Result{PendingAssets: pendingAssetCount, SelectedAssets: len(assets)}
	workerContext, cancelWorkers := context.WithCancel(ctx)
	defer cancelWorkers()
	jobs := make(chan archive.PhotoUpdateAsset)
	completedAssets := make(chan photoFoundationResult)
	workerCount := min(maximumAssetsInFlight, len(assets))
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for workerIndex := 0; workerIndex < workerCount; workerIndex++ {
		go func() {
			defer workers.Done()
			for asset := range jobs {
				runner.observations.startAsset(asset.AssetID)
				state, operationErr := runner.processPhotoFoundation(workerContext, asset, knownPlaceConfigurationSHA256)
				runner.observations.finishAsset(asset.AssetID)
				completedAssets <- photoFoundationResult{state: state, err: operationErr}
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
			if appleOperation.context.Err() == nil {
				appleOperation.execute()
			}
			close(appleOperation.completed)
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
					fatalErr = completed.err
					cancelWorkers()
				}
			} else {
				result.FoundationsStored++
				switch completed.state {
				case foundationwire.PhotoFoundationOutcomeState_PHOTO_FOUNDATION_OUTCOME_STATE_CURRENT_MEDIA_UNAVAILABLE:
					result.MediaUnavailable++
				case foundationwire.PhotoFoundationOutcomeState_PHOTO_FOUNDATION_OUTCOME_STATE_UNSUPPORTED_MEDIA:
					result.UnsupportedMedia++
				}
			}
		}
	}
	if fatalErr != nil {
		return result, fmt.Errorf("Photos update stopped before all selected foundations were stored: %w", fatalErr)
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	runner.observations.snapshot(len(assets), len(assets), workerCount)
	return result, nil
}

func (runner *Runner) processPhotoFoundation(ctx context.Context, asset archive.PhotoUpdateAsset, knownPlaceConfigurationSHA256 []byte) (foundationwire.PhotoFoundationOutcomeState, error) {
	captureInput, hasCaptureLocation, err := archive.LoadOptionalCaptureLocationInput(ctx, runner.options.OpenedArchiveStore, string(asset.AssetID))
	if err != nil {
		return foundationwire.PhotoFoundationOutcomeState_PHOTO_FOUNDATION_OUTCOME_STATE_UNSPECIFIED, err
	}
	captureAvailability := foundationwire.CaptureLocationAvailability_CAPTURE_LOCATION_AVAILABILITY_ABSENT
	if hasCaptureLocation {
		captureAvailability = foundationwire.CaptureLocationAvailability_CAPTURE_LOCATION_AVAILABILITY_PRESENT
	}
	if asset.MediaType != archive.PhotoMediaKindImage {
		outcome := &foundationwire.PhotoFoundationOutcome{
			AssetId: string(asset.AssetID), State: foundationwire.PhotoFoundationOutcomeState_PHOTO_FOUNDATION_OUTCOME_STATE_UNSUPPORTED_MEDIA,
			CaptureLocationAvailability: captureAvailability, CompletedAt: timestamppb.Now(),
		}
		return outcome.GetState(), archive.StoreCurrentPhotoFoundationOutcome(ctx, runner.options.OpenedArchiveStore, outcome)
	}

	currentMediaRequest := archive.CurrentRenderedStillRequestForPhotoUpdateAsset(asset)
	retainedMedia, retainedMediaFound, err := archive.LoadCurrentRenderedPhotoMediaEvidence(ctx, runner.options.OpenedArchiveStore, asset.AssetID)
	if err != nil {
		return foundationwire.PhotoFoundationOutcomeState_PHOTO_FOUNDATION_OUTCOME_STATE_UNSPECIFIED, err
	}
	currentMediaReady := retainedMediaFound && archive.CurrentRenderedPhotoMediaEvidenceMatchesRequest(retainedMedia, currentMediaRequest)

	originalRequest := archive.ImmutableOriginalImageFactsRequestForPhotoUpdateAsset(asset)
	_, originalReady, err := archive.LoadCurrentImmutableOriginalImageFactsOutcomeForRequest(ctx, runner.options.OpenedArchiveStore, asset.AssetID, originalRequest)
	if err != nil {
		return foundationwire.PhotoFoundationOutcomeState_PHOTO_FOUNDATION_OUTCOME_STATE_UNSPECIFIED, err
	}

	var mediaUnavailable *mediawire.PhotosMediaUnavailable
	var mediaErr, originalErr, locationErr error
	var wait sync.WaitGroup
	if currentMediaReady {
		runner.observations.record(asset.AssetID, ProductionNodeCurrentMedia, WorkReused, nil, nil)
	} else {
		wait.Add(1)
		go func() {
			defer wait.Done()
			runner.observations.startNode(asset.AssetID, ProductionNodeCurrentMedia)
			mediaUnavailable, mediaErr = runner.acquireAndStoreCurrentRenderedPhoto(ctx, asset, currentMediaRequest)
			runner.finishObservedNode(asset.AssetID, ProductionNodeCurrentMedia, mediaErr, true)
		}()
	}
	if originalReady {
		runner.observations.record(asset.AssetID, ProductionNodeImmutableOriginalImageFacts, WorkReused, nil, nil)
	} else {
		wait.Add(1)
		go func() {
			defer wait.Done()
			runner.observations.startNode(asset.AssetID, ProductionNodeImmutableOriginalImageFacts)
			originalErr = runner.inspectAndStoreImmutableOriginalImageFacts(ctx, asset, originalRequest)
			runner.finishObservedNode(asset.AssetID, ProductionNodeImmutableOriginalImageFacts, originalErr, true)
		}()
	}
	if hasCaptureLocation {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, locationErr = runner.acquireLocationEvidenceForInput(ctx, asset, captureInput, knownPlaceConfigurationSHA256)
		}()
	} else {
		runner.observations.record(asset.AssetID, ProductionNodeComposeLocationEvidence, WorkSkipped, nil, nil)
	}
	wait.Wait()
	if err := errors.Join(mediaErr, originalErr, locationErr); err != nil {
		return foundationwire.PhotoFoundationOutcomeState_PHOTO_FOUNDATION_OUTCOME_STATE_UNSPECIFIED, err
	}
	state := foundationwire.PhotoFoundationOutcomeState_PHOTO_FOUNDATION_OUTCOME_STATE_READY
	if mediaUnavailable != nil {
		state = foundationwire.PhotoFoundationOutcomeState_PHOTO_FOUNDATION_OUTCOME_STATE_CURRENT_MEDIA_UNAVAILABLE
	}
	outcome := &foundationwire.PhotoFoundationOutcome{
		AssetId: string(asset.AssetID), State: state, CaptureLocationAvailability: captureAvailability,
		CurrentMediaRequest: currentMediaRequest, CurrentMediaUnavailable: mediaUnavailable, CompletedAt: timestamppb.Now(),
	}
	return state, archive.StoreCurrentPhotoFoundationOutcome(ctx, runner.options.OpenedArchiveStore, outcome)
}

func (runner *Runner) acquireAndStoreCurrentRenderedPhoto(ctx context.Context, asset archive.PhotoUpdateAsset, request *mediawire.AcquireCurrentRenderedStillRequest) (*mediawire.PhotosMediaUnavailable, error) {
	client := photosmedia.NewInstalledOpenTrawlClient(runner.photosMediaWorkingRoot())
	currentStill, err := client.AcquireCurrentRenderedStill(ctx, request)
	if err != nil {
		var outcomeError *photosmedia.PhotosMediaOutcomeError
		if errors.As(err, &outcomeError) && outcomeError.Unavailable != nil {
			return outcomeError.Unavailable, nil
		}
		return nil, err
	}
	runner.observations.acquireMediaLease(asset.AssetID, currentStill.Outcome.GetByteCount())
	defer runner.closeCurrentRenderedStill(asset.AssetID, currentStill)
	if err := archive.StoreCurrentRenderedPhotoMediaEvidence(ctx, runner.options.OpenedArchiveStore, asset.AssetID, currentStill.Outcome); err != nil {
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

func (runner *Runner) inspectAndStoreImmutableOriginalImageFacts(ctx context.Context, asset archive.PhotoUpdateAsset, request *mediawire.InspectImmutableOriginalImageFactsRequest) error {
	client := photosmedia.NewInstalledOpenTrawlClient(runner.photosMediaWorkingRoot())
	outcome, err := client.InspectImmutableOriginalImageFacts(ctx, request)
	if err != nil {
		return err
	}
	return archive.StoreCurrentImmutableOriginalImageFactsOutcome(ctx, runner.options.OpenedArchiveStore, asset.AssetID, outcome)
}

func (runner *Runner) acquireLocationEvidenceForInput(ctx context.Context, asset archive.PhotoUpdateAsset, input *locationwire.CaptureLocationInput, knownPlaceConfigurationSHA256 []byte) (*locationwire.ComposePhotoLocationEvidenceOutcome, error) {
	if retained, found, err := archive.LoadCurrentPhotoLocationEvidence(ctx, runner.options.OpenedArchiveStore, asset, knownPlaceConfigurationSHA256); err != nil {
		return nil, err
	} else if found && archive.CurrentPhotoLocationEvidenceMatchesInput(retained, input) {
		runner.observations.record(asset.AssetID, ProductionNodeComposeLocationEvidence, WorkReused, nil, nil)
		return retained, nil
	}
	runner.observations.startNode(asset.AssetID, ProductionNodeKnownPlace)
	known, knownDisposition, err := runner.matchConfiguredKnownPlace(ctx, input, knownPlaceConfigurationSHA256)
	runner.observations.finishNode(asset.AssetID, ProductionNodeKnownPlace, knownDisposition, nil, nil)
	if err != nil {
		return nil, err
	}
	appleReverse, appleNearby, geoapify, err := runner.acquireProviderLocationEvidence(ctx, input, known)
	if err != nil {
		return nil, err
	}
	runner.observations.startNode(asset.AssetID, ProductionNodeComposeLocationEvidence)
	composed, err := runner.composePhotoLocationEvidence(ctx, asset, knownPlaceConfigurationSHA256, known, appleReverse, appleNearby, geoapify)
	runner.finishObservedNode(asset.AssetID, ProductionNodeComposeLocationEvidence, err, true)
	return composed, err
}
