// Package updatephotos composes the one Photos update dependency graph.
// Components keep their own typed boundaries; this package owns their order
// and the small amount of useful concurrency between independent operations.
package updatephotos

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/luna"
	photosmedia "github.com/opentrawl/opentrawl/trawlers/photos/internal/media"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/media/mediawire"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photocard"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/place"
	cardwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/card"
	locationwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/location"
	"github.com/opentrawl/opentrawl/trawlkit/store"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	appleNearbyPlaceRadiusMetres                   = 500
	maximumAppleNearbyPlaceCandidates              = 100
	geoapifyPhotographedPlaceCandidateRadiusMetres = 5000
	maximumGeoapifyPhotographedPlaceCandidates     = 20
	maximumAssetsInFlight                          = 8
)

type Options struct {
	OpenedArchiveStore     *store.Store
	GeoapifyAPIKeyFilePath string
	CodexExecutablePath    string
	WorkingDirectory       string
	MaximumAssetsToProcess int
	ReportProgress         func(completed, total int, message string)
	ReportComponent        func(component, outcome string, duration time.Duration)
}

type Result struct {
	PendingAssets    int
	SelectedAssets   int
	CardsStored      int
	MediaUnavailable int
	UnsupportedMedia int
	DeferredOrFailed int
}

type PhotoLibraryAccessUnavailableError struct {
	State mediawire.PhotoLibraryAccessState
}

type AssetDeferredError struct{ Reason string }

func (e *AssetDeferredError) Error() string { return e.Reason }

func (e *PhotoLibraryAccessUnavailableError) Error() string {
	switch e.State {
	case mediawire.PhotoLibraryAccessState_PHOTO_LIBRARY_ACCESS_STATE_DENIED:
		return "OpenTrawl does not have access to Apple Photos"
	case mediawire.PhotoLibraryAccessState_PHOTO_LIBRARY_ACCESS_STATE_RESTRICTED:
		return "this Mac does not allow OpenTrawl to access Apple Photos"
	default:
		return "Apple Photos access is unavailable"
	}
}

type Runner struct {
	options                           Options
	chatGPTSignInMutex                sync.Mutex
	appleLocationMainThreadOperations chan appleLocationMainThreadOperation
}

type appleLocationMainThreadOperation struct {
	context   context.Context
	execute   func()
	completed chan struct{}
}

type photoAssetWorker struct {
	runner     *Runner
	lunaClient *luna.Client
}

type locationProviderOperationResult struct {
	name    string
	outcome any
	err     error
}

func Run(ctx context.Context, options Options) (Result, error) {
	if options.OpenedArchiveStore == nil {
		return Result{}, errors.New("Photos update archive store is required")
	}
	runner := &Runner{
		options:                           options,
		appleLocationMainThreadOperations: make(chan appleLocationMainThreadOperation),
	}
	accessStartedAt := time.Now()
	if err := runner.ensurePhotoLibraryAccess(ctx); err != nil {
		runner.reportCompletedComponent(string(ProductionNodeMediaAccess), err, time.Since(accessStartedAt))
		return Result{}, err
	}
	runner.reportCompletedComponent(string(ProductionNodeMediaAccess), nil, time.Since(accessStartedAt))
	knownPlaceConfigurationSHA256, err := archive.KnownPlaceConfigurationSHA256(ctx, options.OpenedArchiveStore)
	if err != nil {
		return Result{}, err
	}
	if _, err := archive.InvalidatePhotoCardsWithInsufficientLocationEvidence(ctx, options.OpenedArchiveStore, knownPlaceConfigurationSHA256); err != nil {
		return Result{}, err
	}
	assets, err := archive.SelectPhotoUpdateAssets(ctx, options.OpenedArchiveStore, knownPlaceConfigurationSHA256)
	if err != nil {
		return Result{}, err
	}
	pendingAssetCount := len(assets)
	if options.MaximumAssetsToProcess > 0 && len(assets) > options.MaximumAssetsToProcess {
		assets = assets[:options.MaximumAssetsToProcess]
	}
	result := Result{PendingAssets: pendingAssetCount, SelectedAssets: len(assets)}
	workerContext, cancelWorkers := context.WithCancel(ctx)
	defer cancelWorkers()
	type assetResult struct {
		outcome archive.PhotoUpdateResultKind
		err     error
	}
	jobs := make(chan archive.PhotoUpdateAsset)
	completedAssets := make(chan assetResult)
	workerCount := min(maximumAssetsInFlight, len(assets))
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for workerIndex := 0; workerIndex < workerCount; workerIndex++ {
		go func() {
			defer workers.Done()
			worker := &photoAssetWorker{runner: runner}
			defer worker.closeLunaClient()
			for asset := range jobs {
				assetStartedAt := time.Now()
				outcome, operationErr := worker.processAsset(workerContext, asset)
				componentOutcome := string(outcome)
				if operationErr != nil {
					var assetDeferredError *AssetDeferredError
					if errors.As(operationErr, &assetDeferredError) {
						componentOutcome = "deferred"
					} else {
						componentOutcome = "failed"
					}
				}
				runner.reportComponent(string(ProductionNodePhotoCard), componentOutcome, assetStartedAt)
				completedAssets <- assetResult{outcome: outcome, err: operationErr}
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
	for completedAssets != nil {
		var completed assetResult
		select {
		case appleLocationOperation := <-runner.appleLocationMainThreadOperations:
			if appleLocationOperation.context.Err() == nil {
				appleLocationOperation.execute()
			}
			close(appleLocationOperation.completed)
			continue
		case received, open := <-completedAssets:
			if !open {
				completedAssets = nil
				continue
			}
			completed = received
		}
		completedCount++
		if completed.err != nil {
			var mediaOutcomeError *photosmedia.PhotosMediaOutcomeError
			var assetDeferredError *AssetDeferredError
			if errors.As(completed.err, &assetDeferredError) || errors.As(completed.err, &mediaOutcomeError) && (mediaOutcomeError.AdmissionDeferred != nil || mediaOutcomeError.OperationFailure != nil) {
				result.DeferredOrFailed++
			} else if fatalErr == nil {
				fatalErr = completed.err
				cancelWorkers()
			}
		} else {
			switch completed.outcome {
			case archive.PhotoUpdateResultCardStored:
				result.CardsStored++
			case archive.PhotoUpdateResultMediaUnavailable:
				result.MediaUnavailable++
			case archive.PhotoUpdateResultUnsupportedMedia:
				result.UnsupportedMedia++
			}
		}
		if options.ReportProgress != nil {
			options.ReportProgress(completedCount, len(assets), "enriching and describing photos")
		}
	}
	if fatalErr != nil {
		return result, fmt.Errorf("Photos update stopped before all cards were stored: %w", fatalErr)
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if options.ReportProgress != nil {
		options.ReportProgress(len(assets), len(assets), "photo update complete")
	}
	return result, nil
}

func (runner *Runner) executeAppleLocationOperationOnMainThread(ctx context.Context, execute func()) error {
	operation := appleLocationMainThreadOperation{
		context:   ctx,
		execute:   execute,
		completed: make(chan struct{}),
	}
	select {
	case runner.appleLocationMainThreadOperations <- operation:
	case <-ctx.Done():
		return ctx.Err()
	}
	<-operation.completed
	return ctx.Err()
}

func (runner *Runner) ensurePhotoLibraryAccess(ctx context.Context) error {
	client := photosmedia.NewInstalledOpenTrawlClient()
	access, err := client.ReadPhotoLibraryAccess(ctx)
	if err != nil {
		return err
	}
	if access.GetState() == mediawire.PhotoLibraryAccessState_PHOTO_LIBRARY_ACCESS_STATE_NOT_DETERMINED {
		access, err = client.RequestPhotoLibraryAccess(ctx)
		if err != nil {
			return err
		}
	}
	switch access.GetState() {
	case mediawire.PhotoLibraryAccessState_PHOTO_LIBRARY_ACCESS_STATE_AUTHORIZED,
		mediawire.PhotoLibraryAccessState_PHOTO_LIBRARY_ACCESS_STATE_LIMITED:
		return nil
	case mediawire.PhotoLibraryAccessState_PHOTO_LIBRARY_ACCESS_STATE_DENIED,
		mediawire.PhotoLibraryAccessState_PHOTO_LIBRARY_ACCESS_STATE_RESTRICTED:
		return &PhotoLibraryAccessUnavailableError{State: access.GetState()}
	default:
		return fmt.Errorf("OpenTrawl returned unknown Apple Photos access state %q", access.GetState())
	}
}

func (runner *Runner) reportComponent(component, outcome string, startedAt time.Time) {
	if runner.options.ReportComponent != nil {
		runner.options.ReportComponent(component, outcome, time.Since(startedAt))
	}
}

func (worker *photoAssetWorker) processAsset(ctx context.Context, asset archive.PhotoUpdateAsset) (archive.PhotoUpdateResultKind, error) {
	runner := worker.runner
	if asset.MediaType != archive.PhotoMediaKindImage {
		err := archive.StorePhotoUpdateOutcome(ctx, runner.options.OpenedArchiveStore, asset, archive.PhotoUpdateResultUnsupportedMedia, "This Photos item is not a still image", time.Now())
		return archive.PhotoUpdateResultUnsupportedMedia, err
	}

	mediaOutcome, verifiedPhotoText, locationOutcome, err := worker.acquirePhotoCardDependencies(ctx, asset)
	if err != nil {
		var unavailable *photosmedia.PhotosMediaOutcomeError
		if errors.As(err, &unavailable) && unavailable.Unavailable != nil {
			storeErr := archive.StorePhotoUpdateOutcome(ctx, runner.options.OpenedArchiveStore, asset, archive.PhotoUpdateResultMediaUnavailable, unavailable.Error(), time.Now())
			return archive.PhotoUpdateResultMediaUnavailable, storeErr
		}
		return "", err
	}
	defer func() { _ = mediaOutcome.CurrentRenderedStill.Close() }()

	locationEvidence, err := photocard.BuildHumanReadableLocationEvidence(locationOutcome)
	if err != nil {
		return "", err
	}
	checkedEvidence := buildHumanReadablePhotoEvidence(asset, mediaOutcome.ImmutableOriginalFacts, mediaOutcome.CurrentRenderedStill.Outcome, locationEvidence.Text)
	card, inputSHA256, locationSHA256, err := worker.generatePhotoCard(ctx, asset, mediaOutcome, verifiedPhotoText, locationOutcome, locationEvidence, checkedEvidence)
	if err != nil {
		return "", err
	}
	if err := archive.StoreCurrentPhotoCard(ctx, runner.options.OpenedArchiveStore, asset, inputSHA256, mediaOutcome.CurrentRenderedStill.Outcome.GetSha256(), locationSHA256, locationOutcome, card, time.Now()); err != nil {
		return "", err
	}
	return archive.PhotoUpdateResultCardStored, nil
}

type acquiredMediaEvidence struct {
	CurrentRenderedStill   *photosmedia.CurrentRenderedStillLease
	ImmutableOriginalFacts *mediawire.ImmutableOriginalImageFacts
}

func (worker *photoAssetWorker) acquirePhotoCardDependencies(ctx context.Context, asset archive.PhotoUpdateAsset) (acquiredMediaEvidence, *cardwire.PhotoOpticalCharacterRecognition, *locationwire.ComposePhotoLocationEvidenceOutcome, error) {
	runner := worker.runner
	var mediaEvidence acquiredMediaEvidence
	var extractedPhotoText *cardwire.PhotoOpticalCharacterRecognition
	var verifiedPhotoText *cardwire.PhotoOpticalCharacterRecognition
	var locationEvidence *locationwire.ComposePhotoLocationEvidenceOutcome
	var mediaErr, textExtractionErr, textVerificationErr, locationErr error
	var mediaDuration, textExtractionDuration, textVerificationDuration time.Duration
	var textExtractionAttempted, textVerificationAttempted bool
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		startedAt := time.Now()
		mediaEvidence, mediaErr = runner.acquireMediaEvidence(ctx, asset)
		mediaDuration = time.Since(startedAt)
		if mediaErr != nil {
			return
		}
		textExtractionAttempted = true
		startedAt = time.Now()
		extractedPhotoText, textExtractionErr = worker.extractPhotoText(ctx, asset, mediaEvidence)
		textExtractionDuration = time.Since(startedAt)
		if textExtractionErr != nil {
			return
		}
		textVerificationAttempted = true
		startedAt = time.Now()
		verifiedPhotoText, textVerificationErr = worker.verifyPhotoText(ctx, asset, mediaEvidence, extractedPhotoText)
		textVerificationDuration = time.Since(startedAt)
	}()
	go func() {
		defer wait.Done()
		locationEvidence, locationErr = runner.acquireLocationEvidence(ctx, asset)
	}()
	wait.Wait()
	runner.reportCompletedComponent(string(ProductionNodeCurrentMedia), mediaErr, mediaDuration)
	if textExtractionAttempted {
		runner.reportCompletedComponent(string(ProductionNodePhotoTextExtraction), textExtractionErr, textExtractionDuration)
	}
	if textVerificationAttempted {
		runner.reportCompletedComponent(string(ProductionNodePhotoTextVerification), textVerificationErr, textVerificationDuration)
	}
	if mediaErr != nil || textExtractionErr != nil || textVerificationErr != nil || locationErr != nil {
		if mediaEvidence.CurrentRenderedStill != nil {
			_ = mediaEvidence.CurrentRenderedStill.Close()
		}
		return acquiredMediaEvidence{}, nil, nil, errors.Join(mediaErr, textExtractionErr, textVerificationErr, locationErr)
	}
	return mediaEvidence, verifiedPhotoText, locationEvidence, nil
}

func (runner *Runner) reportCompletedComponent(component string, operationErr error, duration time.Duration) {
	if runner.options.ReportComponent == nil {
		return
	}
	outcome := "succeeded"
	if operationErr != nil {
		outcome = "failed"
	}
	runner.options.ReportComponent(component, outcome, duration)
}

func (runner *Runner) acquireMediaEvidence(ctx context.Context, asset archive.PhotoUpdateAsset) (acquiredMediaEvidence, error) {
	client := photosmedia.NewInstalledOpenTrawlClient()
	readiness, err := client.InspectPhotoAssetReadiness(ctx, string(asset.LocalIdentifier))
	if err != nil {
		return acquiredMediaEvidence{}, err
	}
	if readiness.GetPhotoAssetLocalIdentifier() != string(asset.LocalIdentifier) {
		return acquiredMediaEvidence{}, errors.New("installed OpenTrawl returned media readiness for a different Photos asset")
	}
	if (readiness.GetModificationTime() != nil) != asset.ModificationTime.Present {
		return acquiredMediaEvidence{}, errors.New("PhotoKit modification time does not match the indexed Photos asset")
	}
	originalFilename := readiness.GetImmutableOriginalFilename()
	originalUTI := readiness.GetImmutableOriginalUniformTypeIdentifier()
	expectedOriginalByteCount, matched := indexedOriginalByteCount(asset.OriginalResources, originalFilename, originalUTI)
	if !matched {
		return acquiredMediaEvidence{}, errors.New("PhotoKit immutable original does not match the indexed Photos resource")
	}
	var expectedModificationTime *timestamppb.Timestamp
	if asset.ModificationTime.Present {
		expectedModificationTime = timestamppb.New(asset.ModificationTime.Value)
	}
	originalFacts, originalFactsRetained, err := archive.LoadCurrentImmutableOriginalFacts(ctx, runner.options.OpenedArchiveStore, asset)
	if err != nil {
		return acquiredMediaEvidence{}, err
	}
	var currentStill *photosmedia.CurrentRenderedStillLease
	var originalErr, currentErr error
	var wait sync.WaitGroup
	wait.Add(1)
	if !originalFactsRetained {
		wait.Add(1)
		go func() {
			defer wait.Done()
			originalFacts, originalErr = client.InspectImmutableOriginalImageFacts(ctx, &mediawire.InspectImmutableOriginalImageFactsRequest{
				PhotoAssetLocalIdentifier:                      string(asset.LocalIdentifier),
				ExpectedImmutableOriginalFilename:              originalFilename,
				ExpectedImmutableOriginalUniformTypeIdentifier: originalUTI,
				ExpectedImmutableOriginalByteCount:             expectedOriginalByteCount,
				AllowIcloudNetworkAccess:                       true,
			})
		}()
	}
	go func() {
		defer wait.Done()
		currentStill, currentErr = client.AcquireCurrentRenderedStill(ctx, &mediawire.AcquireCurrentRenderedStillRequest{
			PhotoAssetLocalIdentifier:     string(asset.LocalIdentifier),
			ExpectedPhotoModificationTime: expectedModificationTime,
			AllowIcloudNetworkAccess:      true,
		})
	}()
	wait.Wait()
	if originalErr != nil || currentErr != nil {
		if currentStill != nil {
			_ = currentStill.Close()
		}
		return acquiredMediaEvidence{}, errors.Join(originalErr, currentErr)
	}
	if err := archive.StoreCurrentPhotoMediaEvidence(ctx, runner.options.OpenedArchiveStore, asset, originalFacts, currentStill.Outcome); err != nil {
		_ = currentStill.Close()
		return acquiredMediaEvidence{}, err
	}
	return acquiredMediaEvidence{CurrentRenderedStill: currentStill, ImmutableOriginalFacts: originalFacts}, nil
}

func indexedOriginalByteCount(resources []archive.PhotoUpdateOriginalResource, filename, uniformTypeIdentifier string) (uint64, bool) {
	matched := false
	var agreedPositiveByteCount int64
	allPositiveByteCountsAgree := true
	for _, resource := range resources {
		if !strings.EqualFold(strings.TrimSpace(resource.Filename), strings.TrimSpace(filename)) || !strings.EqualFold(strings.TrimSpace(resource.UniformTypeIdentifier), strings.TrimSpace(uniformTypeIdentifier)) {
			continue
		}
		matched = true
		if resource.IndexedByteCount <= 0 {
			continue
		}
		if agreedPositiveByteCount == 0 {
			agreedPositiveByteCount = resource.IndexedByteCount
		} else if agreedPositiveByteCount != resource.IndexedByteCount {
			allPositiveByteCountsAgree = false
		}
	}
	if matched && allPositiveByteCountsAgree && agreedPositiveByteCount > 0 {
		return uint64(agreedPositiveByteCount), true
	}
	return 0, matched
}

func (runner *Runner) acquireLocationEvidence(ctx context.Context, asset archive.PhotoUpdateAsset) (*locationwire.ComposePhotoLocationEvidenceOutcome, error) {
	knownPlaceConfigurationSHA256, err := archive.KnownPlaceConfigurationSHA256(ctx, runner.options.OpenedArchiveStore)
	if err != nil {
		return nil, err
	}
	if retained, found, err := archive.LoadCurrentPhotoLocationEvidence(ctx, runner.options.OpenedArchiveStore, asset, knownPlaceConfigurationSHA256); err != nil || found {
		return retained, err
	}
	input, hasCaptureLocation, err := archive.LoadOptionalCaptureLocationInput(ctx, runner.options.OpenedArchiveStore, string(asset.AssetID))
	if err != nil || !hasCaptureLocation {
		return nil, err
	}
	knownStartedAt := time.Now()
	known, err := runner.matchConfiguredKnownPlace(ctx, input, knownPlaceConfigurationSHA256)
	runner.reportCompletedComponent(string(ProductionNodeKnownPlace), err, time.Since(knownStartedAt))
	if err != nil {
		return nil, err
	}

	appleReverse, appleNearby, geoapifyPhotographedPlaceCandidateEvidence, err := runner.acquireProviderLocationEvidence(ctx, input, known)
	if err != nil {
		return nil, err
	}
	composeStartedAt := time.Now()
	composed, err := runner.composePhotoLocationEvidence(ctx, asset, knownPlaceConfigurationSHA256, known, appleReverse, appleNearby, geoapifyPhotographedPlaceCandidateEvidence)
	runner.reportCompletedComponent(string(ProductionNodeComposeLocationEvidence), err, time.Since(composeStartedAt))
	return composed, err
}

func (runner *Runner) composePhotoLocationEvidence(ctx context.Context, asset archive.PhotoUpdateAsset, knownPlaceConfigurationSHA256 []byte, known *locationwire.MatchConfiguredKnownPlaceOutcome, appleReverse *locationwire.AcquireAppleReverseGeocodingEvidenceOutcome, appleNearby *locationwire.AcquireAppleNearbyPlaceEvidenceOutcome, geoapifyPhotographedPlaceCandidateEvidence *locationwire.AcquireGeoapifyPhotographedPlaceCandidateEvidenceOutcome) (*locationwire.ComposePhotoLocationEvidenceOutcome, error) {
	composed, err := place.ComposePhotoLocationEvidence(known, appleReverse, appleNearby, geoapifyPhotographedPlaceCandidateEvidence)
	if err != nil {
		return nil, err
	}
	if err := archive.StoreCurrentPhotoLocationEvidence(ctx, runner.options.OpenedArchiveStore, asset, knownPlaceConfigurationSHA256, composed); err != nil {
		return nil, err
	}
	return composed, nil
}

func (runner *Runner) matchConfiguredKnownPlace(ctx context.Context, input *locationwire.CaptureLocationInput, knownPlaceConfigurationSHA256 []byte) (*locationwire.MatchConfiguredKnownPlaceOutcome, error) {
	request := &locationwire.MatchConfiguredKnownPlaceRequest{Input: input, KnownPlaceConfigurationSha256: knownPlaceConfigurationSHA256}
	retained, found, err := archive.LoadMatchConfiguredKnownPlaceOutcome(ctx, runner.options.OpenedArchiveStore, input.GetAssetId())
	if err != nil {
		return nil, err
	}
	if found && proto.Equal(retained.GetRequest(), request) {
		return retained, nil
	}
	outcome, err := archive.MatchConfiguredKnownPlace(ctx, runner.options.OpenedArchiveStore, request)
	if err != nil {
		return nil, err
	}
	if err := archive.StoreMatchConfiguredKnownPlaceOutcome(ctx, runner.options.OpenedArchiveStore, outcome); err != nil {
		return nil, err
	}
	return outcome, nil
}

func (runner *Runner) acquireProviderLocationEvidence(ctx context.Context, input *locationwire.CaptureLocationInput, known *locationwire.MatchConfiguredKnownPlaceOutcome) (*locationwire.AcquireAppleReverseGeocodingEvidenceOutcome, *locationwire.AcquireAppleNearbyPlaceEvidenceOutcome, *locationwire.AcquireGeoapifyPhotographedPlaceCandidateEvidenceOutcome, error) {
	results := make(chan locationProviderOperationResult, 3)
	go runner.reportLocationProviderOperation(ProductionNodeAppleReverseGeocoding, "Apple reverse geocoding", func() (any, error) { return runner.acquireAppleReverseGeocodingEvidence(ctx, input) }, results)
	go runner.reportLocationProviderOperation(ProductionNodeAppleNearbyPlaces, "Apple nearby places", func() (any, error) { return runner.acquireAppleNearbyPlaceEvidence(ctx, input, known) }, results)
	go runner.reportLocationProviderOperation(ProductionNodeGeoapifyPhotographedPlaceCandidates, "Geoapify photographed-place candidates", func() (any, error) {
		return runner.acquireGeoapifyPhotographedPlaceCandidateEvidence(ctx, input, known)
	}, results)
	var appleReverse *locationwire.AcquireAppleReverseGeocodingEvidenceOutcome
	var appleNearby *locationwire.AcquireAppleNearbyPlaceEvidenceOutcome
	var geoapifyPhotographedPlaceCandidateEvidence *locationwire.AcquireGeoapifyPhotographedPlaceCandidateEvidenceOutcome
	var operationErrors []error
	for index := 0; index < 3; index++ {
		result := <-results
		if result.err != nil {
			operationErrors = append(operationErrors, fmt.Errorf("%s: %w", result.name, result.err))
			continue
		}
		switch value := result.outcome.(type) {
		case *locationwire.AcquireAppleReverseGeocodingEvidenceOutcome:
			appleReverse = value
		case *locationwire.AcquireAppleNearbyPlaceEvidenceOutcome:
			appleNearby = value
		case *locationwire.AcquireGeoapifyPhotographedPlaceCandidateEvidenceOutcome:
			geoapifyPhotographedPlaceCandidateEvidence = value
		}
	}
	if len(operationErrors) != 0 {
		return nil, nil, nil, errors.Join(operationErrors...)
	}
	if !place.ProviderExchangeSatisfiesCurrentLocationEvidence(appleReverse.GetExchange(), false) || !place.ProviderExchangeSatisfiesCurrentLocationEvidence(appleNearby.GetExchange(), true) || !place.ProviderExchangeSatisfiesCurrentLocationEvidence(geoapifyPhotographedPlaceCandidateEvidence.GetExchange(), true) {
		return nil, nil, nil, &AssetDeferredError{Reason: "location provider evidence remains retryable"}
	}
	return appleReverse, appleNearby, geoapifyPhotographedPlaceCandidateEvidence, nil
}

func (runner *Runner) reportLocationProviderOperation(nodeName ProductionNodeName, displayName string, operation func() (any, error), results chan<- locationProviderOperationResult) {
	startedAt := time.Now()
	outcome, err := operation()
	runner.reportCompletedComponent(string(nodeName), err, time.Since(startedAt))
	results <- locationProviderOperationResult{displayName, outcome, err}
}

func (runner *Runner) acquireAppleReverseGeocodingEvidence(ctx context.Context, input *locationwire.CaptureLocationInput) (*locationwire.AcquireAppleReverseGeocodingEvidenceOutcome, error) {
	request := &locationwire.AcquireAppleReverseGeocodingEvidenceRequest{Input: input}
	retained, found, err := archive.LoadAppleReverseGeocodingEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, input.GetAssetId())
	if err != nil || found && proto.Equal(retained.GetRequest(), request) && (place.ProviderExchangeSatisfiesCurrentLocationEvidence(retained.GetExchange(), false) || providerRetryNotBeforeIsFuture(retained.GetExchange(), time.Now())) {
		return retained, err
	}
	retain := func(outcome *locationwire.AcquireAppleReverseGeocodingEvidenceOutcome) error {
		return archive.StoreAppleReverseGeocodingEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, outcome)
	}
	var outcome *locationwire.AcquireAppleReverseGeocodingEvidenceOutcome
	dispatchErr := runner.executeAppleLocationOperationOnMainThread(ctx, func() {
		if proto.Equal(retained.GetRequest(), request) && retained.GetExchange().GetState() == locationwire.OperationState_OPERATION_STATE_RESPONSE_RETAINED {
			outcome, err = place.ResumeAppleReverseGeocodingEvidence(retained, retain)
		} else {
			outcome, err = place.AcquireAppleReverseGeocodingEvidence(ctx, request, retain)
		}
	})
	return outcome, errors.Join(err, dispatchErr)
}

func (runner *Runner) acquireAppleNearbyPlaceEvidence(ctx context.Context, input *locationwire.CaptureLocationInput, known *locationwire.MatchConfiguredKnownPlaceOutcome) (*locationwire.AcquireAppleNearbyPlaceEvidenceOutcome, error) {
	request := &locationwire.AcquireAppleNearbyPlaceEvidenceRequest{Input: input, RadiusMeters: appleNearbyPlaceRadiusMetres, MaximumCandidates: maximumAppleNearbyPlaceCandidates, KnownPlaceOutcome: known}
	retained, found, err := archive.LoadAppleNearbyPlaceEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, input.GetAssetId())
	if err != nil || found && proto.Equal(retained.GetRequest(), request) && (place.ProviderExchangeSatisfiesCurrentLocationEvidence(retained.GetExchange(), true) || providerRetryNotBeforeIsFuture(retained.GetExchange(), time.Now())) {
		return retained, err
	}
	retain := func(outcome *locationwire.AcquireAppleNearbyPlaceEvidenceOutcome) error {
		return archive.StoreAppleNearbyPlaceEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, outcome)
	}
	var outcome *locationwire.AcquireAppleNearbyPlaceEvidenceOutcome
	dispatchErr := runner.executeAppleLocationOperationOnMainThread(ctx, func() {
		if proto.Equal(retained.GetRequest(), request) && retained.GetExchange().GetState() == locationwire.OperationState_OPERATION_STATE_RESPONSE_RETAINED {
			outcome, err = place.ResumeAppleNearbyPlaceEvidence(retained, retain)
		} else {
			outcome, err = place.AcquireAppleNearbyPlaceEvidence(ctx, request, retain)
		}
	})
	return outcome, errors.Join(err, dispatchErr)
}

func geoapifyPhotographedPlaceCandidateEvidenceRequest(input *locationwire.CaptureLocationInput, known *locationwire.MatchConfiguredKnownPlaceOutcome) *locationwire.AcquireGeoapifyPhotographedPlaceCandidateEvidenceRequest {
	return &locationwire.AcquireGeoapifyPhotographedPlaceCandidateEvidenceRequest{
		Input: input, RadiusMeters: geoapifyPhotographedPlaceCandidateRadiusMetres, MaximumCandidates: maximumGeoapifyPhotographedPlaceCandidates,
		KnownPlaceOutcome: known, Categories: place.GeoapifyPhotographedPlaceCandidateCategories(), RequireNamedCandidates: true,
	}
}

func (runner *Runner) acquireGeoapifyPhotographedPlaceCandidateEvidence(ctx context.Context, input *locationwire.CaptureLocationInput, known *locationwire.MatchConfiguredKnownPlaceOutcome) (*locationwire.AcquireGeoapifyPhotographedPlaceCandidateEvidenceOutcome, error) {
	request := geoapifyPhotographedPlaceCandidateEvidenceRequest(input, known)
	retained, found, err := archive.LoadGeoapifyPhotographedPlaceCandidateEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, input.GetAssetId())
	if err != nil || found && proto.Equal(retained.GetRequest(), request) && (place.ProviderExchangeSatisfiesCurrentLocationEvidence(retained.GetExchange(), true) || providerRetryNotBeforeIsFuture(retained.GetExchange(), time.Now())) {
		return retained, err
	}
	retain := func(outcome *locationwire.AcquireGeoapifyPhotographedPlaceCandidateEvidenceOutcome) error {
		return archive.StoreGeoapifyPhotographedPlaceCandidateEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, outcome)
	}
	if proto.Equal(retained.GetRequest(), request) && retained.GetExchange().GetState() == locationwire.OperationState_OPERATION_STATE_RESPONSE_RETAINED {
		return place.ResumeGeoapifyPhotographedPlaceCandidateEvidence(retained, retain)
	}
	return place.AcquireGeoapifyPhotographedPlaceCandidateEvidence(ctx, request, runner.options.GeoapifyAPIKeyFilePath, &http.Client{Timeout: 30 * time.Second}, retain)
}

func providerRetryNotBeforeIsFuture(exchange *locationwire.ProviderExchange, now time.Time) bool {
	retryNotBefore := exchange.GetFailure().GetRetryNotBefore()
	return exchange.GetState() == locationwire.OperationState_OPERATION_STATE_FAILED && retryNotBefore != nil && retryNotBefore.IsValid() && retryNotBefore.AsTime().After(now)
}

func (worker *photoAssetWorker) ensureLunaClient(ctx context.Context) (*luna.Client, error) {
	if worker.lunaClient != nil {
		return worker.lunaClient, nil
	}
	runner := worker.runner
	codexExecutablePath := strings.TrimSpace(runner.options.CodexExecutablePath)
	if codexExecutablePath == "" {
		resolved, err := exec.LookPath("codex")
		if err != nil {
			return nil, errors.New("Photos update needs the Codex app or CLI to call GPT-5.6 Luna")
		}
		codexExecutablePath = resolved
	}
	workingDirectory := strings.TrimSpace(runner.options.WorkingDirectory)
	if workingDirectory == "" {
		return nil, errors.New("Photos update Luna working directory is required")
	}
	if err := os.MkdirAll(workingDirectory, 0o700); err != nil {
		return nil, err
	}
	client, err := luna.Start(ctx, luna.Configuration{CodexExecutablePath: codexExecutablePath, EmptyWorkingDirectory: workingDirectory, ClientVersion: "photos-v1", PrivateWireTranscript: &boundedTranscript{maximumBytes: 32 << 20}})
	if err != nil {
		return nil, err
	}
	if err := runner.ensureChatGPTAccount(ctx, client); err != nil {
		_ = client.Close()
		return nil, err
	}
	worker.lunaClient = client
	return client, nil
}

func (runner *Runner) ensureChatGPTAccount(ctx context.Context, client *luna.Client) error {
	account, err := client.Account(ctx)
	if err != nil || account.Kind == luna.AccountChatGPT {
		return err
	}
	runner.chatGPTSignInMutex.Lock()
	defer runner.chatGPTSignInMutex.Unlock()
	account, err = client.Account(ctx)
	if err != nil || account.Kind == luna.AccountChatGPT {
		return err
	}
	signIn, err := client.BeginChatGPTSignIn(ctx)
	if err != nil {
		return err
	}
	if err := exec.CommandContext(ctx, "/usr/bin/open", signIn.URL.String()).Run(); err != nil {
		return fmt.Errorf("open ChatGPT sign-in: %w", err)
	}
	return client.WaitForChatGPTSignIn(ctx, signIn.LoginID)
}

func (worker *photoAssetWorker) closeLunaClient() {
	if worker.lunaClient != nil {
		_ = worker.lunaClient.Close()
		worker.lunaClient = nil
	}
}

type boundedTranscript struct {
	mu           sync.Mutex
	maximumBytes int
	bytes        []byte
}

func (writer *boundedTranscript) Write(value []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if len(value) >= writer.maximumBytes {
		writer.bytes = append(writer.bytes[:0], value[len(value)-writer.maximumBytes:]...)
		return len(value), nil
	}
	overflow := len(writer.bytes) + len(value) - writer.maximumBytes
	if overflow > 0 {
		writer.bytes = append(writer.bytes[:0], writer.bytes[overflow:]...)
	}
	writer.bytes = append(writer.bytes, value...)
	return len(value), nil
}

func lunaImageMediaType(uniformTypeIdentifier string) luna.ImageMediaType {
	switch strings.ToLower(strings.TrimSpace(uniformTypeIdentifier)) {
	case "public.png":
		return luna.ImagePNG
	case "org.webmproject.webp", "public.webp":
		return luna.ImageWebP
	default:
		return luna.ImageJPEG
	}
}

func buildHumanReadablePhotoEvidence(asset archive.PhotoUpdateAsset, original *mediawire.ImmutableOriginalImageFacts, current *mediawire.CurrentRenderedStillLease, locationText string) string {
	var evidence strings.Builder
	evidence.WriteString("Photo source facts:\n")
	if asset.CreationTime.Present {
		fmt.Fprintf(&evidence, "- Captured: %s\n", asset.CreationTime.Value.Format(time.RFC3339Nano))
	}
	fmt.Fprintf(&evidence, "- Source image dimensions: %d × %d pixels\n- Current rendered still: %d × %d pixels; orientation %s\n", asset.PixelWidth, asset.PixelHeight, current.GetPixelWidth(), current.GetPixelHeight(), current.GetImageOrientation())
	if asset.CameraMake != "" || asset.CameraModel != "" {
		fmt.Fprintf(&evidence, "- Camera: %s %s\n", asset.CameraMake, asset.CameraModel)
	}
	if asset.LensModel != "" {
		fmt.Fprintf(&evidence, "- Lens: %s\n", asset.LensModel)
	}
	if asset.FocalLengthMM.Valid {
		fmt.Fprintf(&evidence, "- Focal length: %.2f mm\n", asset.FocalLengthMM.Float64)
	}
	if asset.Aperture.Valid {
		fmt.Fprintf(&evidence, "- Aperture: f/%.2f\n", asset.Aperture.Float64)
	}
	if asset.ExposureSeconds.Valid {
		fmt.Fprintf(&evidence, "- Exposure time: %.8f seconds\n", asset.ExposureSeconds.Float64)
	}
	if asset.ISO.Valid {
		fmt.Fprintf(&evidence, "- ISO: %d\n", asset.ISO.Int64)
	}
	if original != nil {
		fmt.Fprintf(&evidence, "- Immutable original: %d × %d pixels; %s; orientation %s\n", original.GetPixelWidth(), original.GetPixelHeight(), original.GetUniformTypeIdentifier(), original.GetImageOrientation())
	}
	if strings.TrimSpace(locationText) != "" {
		fmt.Fprintf(&evidence, "\n%s\n", strings.TrimSpace(locationText))
	}
	return strings.TrimSpace(evidence.String())
}
