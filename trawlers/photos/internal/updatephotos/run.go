// Package updatephotos composes the one Photos update dependency graph.
// Components keep their own typed boundaries; this package owns their order
// and the small amount of useful concurrency between independent operations.
package updatephotos

import (
	"context"
	"crypto/sha256"
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
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	nearbyPlaceRadiusMetres      = 500
	maximumNearbyPlaceCandidates = 100
	maximumAssetsInFlight        = 4
)

type Options struct {
	OpenedArchiveStore     *store.Store
	GeoapifyAPIKeyFilePath string
	CodexExecutablePath    string
	WorkingDirectory       string
	ReportProgress         func(completed, total int, message string)
	ReportComponent        func(component, outcome string, duration time.Duration)
}

type Result struct {
	PendingAssets    int
	CardsStored      int
	MediaUnavailable int
	UnsupportedMedia int
	DeferredOrFailed int
}

type PhotoLibraryAccessUnavailableError struct {
	State mediawire.PhotoLibraryAccessState
}

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
	options      Options
	lunaClient   *luna.Client
	lunaClientMu sync.Mutex
}

func Run(ctx context.Context, options Options) (Result, error) {
	if options.OpenedArchiveStore == nil {
		return Result{}, errors.New("Photos update archive store is required")
	}
	runner := &Runner{options: options}
	accessStartedAt := time.Now()
	if err := runner.ensurePhotoLibraryAccess(ctx); err != nil {
		runner.reportCompletedComponent("media-access", err, time.Since(accessStartedAt))
		return Result{}, err
	}
	runner.reportCompletedComponent("media-access", nil, time.Since(accessStartedAt))
	assets, err := archive.SelectPhotoUpdateAssets(ctx, options.OpenedArchiveStore)
	if err != nil {
		return Result{}, err
	}
	defer func() {
		if runner.lunaClient != nil {
			_ = runner.lunaClient.Close()
		}
	}()
	result := Result{PendingAssets: len(assets)}
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
			for asset := range jobs {
				assetStartedAt := time.Now()
				outcome, operationErr := runner.processAsset(workerContext, asset)
				componentOutcome := string(outcome)
				if operationErr != nil {
					componentOutcome = "failed"
				}
				runner.reportComponent("photo", componentOutcome, assetStartedAt)
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
	for completed := range completedAssets {
		completedCount++
		if completed.err != nil {
			var mediaOutcomeError *photosmedia.PhotosMediaOutcomeError
			if errors.As(completed.err, &mediaOutcomeError) && (mediaOutcomeError.AdmissionDeferred != nil || mediaOutcomeError.OperationFailure != nil) {
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

func (runner *Runner) processAsset(ctx context.Context, asset archive.PhotoUpdateAsset) (archive.PhotoUpdateResultKind, error) {
	if asset.MediaType != "image" {
		err := archive.StorePhotoUpdateOutcome(ctx, runner.options.OpenedArchiveStore, asset, archive.PhotoUpdateResultUnsupportedMedia, "This Photos item is not a still image", time.Now())
		return archive.PhotoUpdateResultUnsupportedMedia, err
	}

	mediaOutcome, locationOutcome, err := runner.acquireIndependentEvidence(ctx, asset)
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
	card, inputSHA256, locationSHA256, err := runner.generatePhotoCard(ctx, asset, mediaOutcome, locationOutcome, locationEvidence, checkedEvidence)
	if err != nil {
		return "", err
	}
	if err := archive.StoreCurrentPhotoCard(ctx, runner.options.OpenedArchiveStore, asset, inputSHA256, mediaOutcome.CurrentRenderedStill.Outcome.GetSha256(), locationSHA256, card, time.Now()); err != nil {
		return "", err
	}
	return archive.PhotoUpdateResultCardStored, nil
}

type acquiredMediaEvidence struct {
	CurrentRenderedStill   *photosmedia.CurrentRenderedStillLease
	ImmutableOriginalFacts *mediawire.ImmutableOriginalImageFacts
}

func (runner *Runner) acquireIndependentEvidence(ctx context.Context, asset archive.PhotoUpdateAsset) (acquiredMediaEvidence, *locationwire.ComposePhotoLocationEvidenceOutcome, error) {
	var mediaEvidence acquiredMediaEvidence
	var locationEvidence *locationwire.ComposePhotoLocationEvidenceOutcome
	var mediaErr, locationErr error
	var mediaDuration, locationDuration time.Duration
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		startedAt := time.Now()
		mediaEvidence, mediaErr = runner.acquireMediaEvidence(ctx, asset)
		mediaDuration = time.Since(startedAt)
	}()
	go func() {
		defer wait.Done()
		startedAt := time.Now()
		locationEvidence, locationErr = runner.acquireLocationEvidence(ctx, asset)
		locationDuration = time.Since(startedAt)
	}()
	wait.Wait()
	runner.reportCompletedComponent("media", mediaErr, mediaDuration)
	runner.reportCompletedComponent("location", locationErr, locationDuration)
	if mediaErr != nil || locationErr != nil {
		if mediaEvidence.CurrentRenderedStill != nil {
			_ = mediaEvidence.CurrentRenderedStill.Close()
		}
		return acquiredMediaEvidence{}, nil, errors.Join(mediaErr, locationErr)
	}
	return mediaEvidence, locationEvidence, nil
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
	readiness, err := client.InspectPhotoAssetReadiness(ctx, asset.LocalIdentifier)
	if err != nil {
		return acquiredMediaEvidence{}, err
	}
	if readiness.GetPhotoAssetLocalIdentifier() != asset.LocalIdentifier {
		return acquiredMediaEvidence{}, errors.New("installed OpenTrawl returned media readiness for a different Photos asset")
	}
	if (readiness.GetModificationTime() == nil) != (asset.ModificationTime == "") {
		return acquiredMediaEvidence{}, errors.New("PhotoKit modification time does not match the indexed Photos asset")
	}
	originalFilename := readiness.GetImmutableOriginalFilename()
	originalUTI := readiness.GetImmutableOriginalUniformTypeIdentifier()
	expectedOriginalByteCount, matched := indexedOriginalByteCount(asset.OriginalResources, originalFilename, originalUTI)
	if !matched {
		return acquiredMediaEvidence{}, errors.New("PhotoKit immutable original does not match the indexed Photos resource")
	}
	var expectedModificationTime *timestamppb.Timestamp
	if asset.ModificationTime != "" {
		modificationTime, err := time.Parse(time.RFC3339Nano, asset.ModificationTime)
		if err != nil {
			return acquiredMediaEvidence{}, fmt.Errorf("parse indexed Photos modification time: %w", err)
		}
		expectedModificationTime = timestamppb.New(modificationTime)
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
				PhotoAssetLocalIdentifier:                      asset.LocalIdentifier,
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
			PhotoAssetLocalIdentifier:     asset.LocalIdentifier,
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
	input, hasCaptureLocation, err := archive.LoadOptionalCaptureLocationInput(ctx, runner.options.OpenedArchiveStore, asset.AssetID)
	if err != nil || !hasCaptureLocation {
		return nil, err
	}
	known, found, err := archive.LoadMatchConfiguredKnownPlaceOutcome(ctx, runner.options.OpenedArchiveStore, asset.AssetID)
	if err != nil {
		return nil, err
	}
	if !found || !proto.Equal(known.GetRequest().GetInput(), input) {
		known, err = archive.MatchConfiguredKnownPlace(ctx, runner.options.OpenedArchiveStore, &locationwire.MatchConfiguredKnownPlaceRequest{Input: input})
		if err != nil {
			return nil, err
		}
		if err := archive.StoreMatchConfiguredKnownPlaceOutcome(ctx, runner.options.OpenedArchiveStore, known); err != nil {
			return nil, err
		}
	}

	appleReverse, appleNearby, geoapifyReverse, geoapifyNearby, err := runner.acquireProviderLocationEvidence(ctx, input, known)
	if err != nil {
		return nil, err
	}
	composed, err := place.ComposePhotoLocationEvidence(known, appleReverse, appleNearby, geoapifyReverse, geoapifyNearby)
	if err != nil {
		return nil, err
	}
	if err := archive.StoreComposedPhotoLocationEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, composed); err != nil {
		return nil, err
	}
	if err := archive.StoreCurrentPhotoLocationEvidence(ctx, runner.options.OpenedArchiveStore, asset, knownPlaceConfigurationSHA256, composed); err != nil {
		return nil, err
	}
	return composed, nil
}

func (runner *Runner) acquireProviderLocationEvidence(ctx context.Context, input *locationwire.CaptureLocationInput, known *locationwire.MatchConfiguredKnownPlaceOutcome) (*locationwire.AcquireAppleReverseGeocodingEvidenceOutcome, *locationwire.AcquireAppleNearbyPlaceEvidenceOutcome, *locationwire.AcquireGeoapifyReverseGeocodingEvidenceOutcome, *locationwire.AcquireGeoapifyNearbyPlaceEvidenceOutcome, error) {
	assetID := input.GetAssetId()
	appleReverse, appleReverseFound, err := archive.LoadAppleReverseGeocodingEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, assetID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	appleNearby, appleNearbyFound, err := archive.LoadAppleNearbyPlaceEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, assetID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	geoapifyReverse, geoapifyReverseFound, err := archive.LoadGeoapifyReverseGeocodingEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, assetID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	geoapifyNearby, geoapifyNearbyFound, err := archive.LoadGeoapifyNearbyPlaceEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, assetID)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	appleReverseFound = appleReverseFound && proto.Equal(appleReverse.GetRequest().GetInput(), input)
	appleNearbyFound = appleNearbyFound && proto.Equal(appleNearby.GetRequest().GetInput(), input) && proto.Equal(appleNearby.GetRequest().GetKnownPlaceOutcome(), known)
	geoapifyReverseFound = geoapifyReverseFound && proto.Equal(geoapifyReverse.GetRequest().GetInput(), input)
	geoapifyNearbyFound = geoapifyNearbyFound && proto.Equal(geoapifyNearby.GetRequest().GetInput(), input) && proto.Equal(geoapifyNearby.GetRequest().GetKnownPlaceOutcome(), known)

	type operationResult struct {
		name string
		err  error
	}
	results := make(chan operationResult, 4)
	operations := 0
	if !appleReverseFound {
		operations++
		go func() {
			var operationErr error
			appleReverse, operationErr = place.AcquireAppleReverseGeocodingEvidence(ctx, &locationwire.AcquireAppleReverseGeocodingEvidenceRequest{Input: input})
			if operationErr == nil {
				operationErr = archive.StoreAppleReverseGeocodingEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, appleReverse)
			}
			results <- operationResult{"Apple reverse geocoding", operationErr}
		}()
	}
	if !appleNearbyFound {
		operations++
		go func() {
			var operationErr error
			appleNearby, operationErr = place.AcquireAppleNearbyPlaceEvidence(ctx, &locationwire.AcquireAppleNearbyPlaceEvidenceRequest{Input: input, RadiusMeters: nearbyPlaceRadiusMetres, MaximumCandidates: maximumNearbyPlaceCandidates, KnownPlaceOutcome: known})
			if operationErr == nil {
				operationErr = archive.StoreAppleNearbyPlaceEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, appleNearby)
			}
			results <- operationResult{"Apple nearby places", operationErr}
		}()
	}
	if !geoapifyReverseFound {
		operations++
		go func() {
			var operationErr error
			geoapifyReverse, operationErr = place.AcquireGeoapifyReverseGeocodingEvidence(ctx, &locationwire.AcquireGeoapifyReverseGeocodingEvidenceRequest{Input: input}, runner.options.GeoapifyAPIKeyFilePath, &http.Client{Timeout: 30 * time.Second})
			if operationErr == nil {
				operationErr = archive.StoreGeoapifyReverseGeocodingEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, geoapifyReverse)
			}
			results <- operationResult{"Geoapify reverse geocoding", operationErr}
		}()
	}
	if !geoapifyNearbyFound {
		operations++
		go func() {
			var operationErr error
			geoapifyNearby, operationErr = place.AcquireGeoapifyNearbyPlaceEvidence(ctx, &locationwire.AcquireGeoapifyNearbyPlaceEvidenceRequest{Input: input, RadiusMeters: nearbyPlaceRadiusMetres, MaximumCandidates: maximumNearbyPlaceCandidates, KnownPlaceOutcome: known}, runner.options.GeoapifyAPIKeyFilePath, &http.Client{Timeout: 30 * time.Second})
			if operationErr == nil {
				operationErr = archive.StoreGeoapifyNearbyPlaceEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, geoapifyNearby)
			}
			results <- operationResult{"Geoapify nearby places", operationErr}
		}()
	}
	var operationErrors []error
	for index := 0; index < operations; index++ {
		result := <-results
		if result.err != nil {
			operationErrors = append(operationErrors, fmt.Errorf("%s: %w", result.name, result.err))
		}
	}
	if len(operationErrors) != 0 {
		return nil, nil, nil, nil, errors.Join(operationErrors...)
	}
	return appleReverse, appleNearby, geoapifyReverse, geoapifyNearby, nil
}

func (runner *Runner) generatePhotoCard(ctx context.Context, asset archive.PhotoUpdateAsset, mediaEvidence acquiredMediaEvidence, locationOutcome *locationwire.ComposePhotoLocationEvidenceOutcome, locationEvidence photocard.HumanReadableLocationEvidence, checkedEvidence string) (*cardwire.PhotoCard, []byte, []byte, error) {
	locationBytes := []byte(nil)
	if locationOutcome != nil {
		encodedLocation, err := proto.Marshal(locationOutcome)
		if err != nil {
			return nil, nil, nil, err
		}
		locationBytes = encodedLocation
	}
	locationDigest := sha256.Sum256(locationBytes)
	inputDigest := sha256.New()
	_, _ = inputDigest.Write([]byte(asset.SourceFingerprint))
	_, _ = inputDigest.Write(mediaEvidence.CurrentRenderedStill.Outcome.GetSha256())
	_, _ = inputDigest.Write(mediaEvidence.ImmutableOriginalFacts.GetSha256())
	_, _ = inputDigest.Write(locationDigest[:])
	inputSHA256 := inputDigest.Sum(nil)
	instructions, err := photocard.BuildHumanReadableInstructions(checkedEvidence)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := archive.RetainPhotoCardGenerationRequest(ctx, runner.options.OpenedArchiveStore, asset.AssetID, inputSHA256, instructions); err != nil {
		return nil, nil, nil, err
	}
	retained, found, err := archive.LoadRetainedPhotoCardGeneration(ctx, runner.options.OpenedArchiveStore, asset.AssetID, inputSHA256)
	if err != nil {
		return nil, nil, nil, err
	}
	response := retained.ResponseBody
	if !found || len(response) == 0 {
		client, err := runner.ensureLunaClient(ctx)
		if err != nil {
			return nil, nil, nil, err
		}
		schema, err := photocard.StructuredOutputSchema()
		if err != nil {
			return nil, nil, nil, err
		}
		imageBytes, err := mediaEvidence.CurrentRenderedStill.Read()
		if err != nil {
			return nil, nil, nil, err
		}
		generation, err := client.Generate(ctx, luna.GenerationRequest{Instructions: instructions, Image: imageBytes, ImageMediaType: lunaImageMediaType(mediaEvidence.CurrentRenderedStill.Outcome.GetUniformTypeIdentifier()), OutputSchema: schema})
		if err != nil {
			return nil, nil, nil, err
		}
		response = generation.RawStructuredOutputJSON
		if err := archive.RetainPhotoCardGenerationResponse(ctx, runner.options.OpenedArchiveStore, asset.AssetID, inputSHA256, generation.ThreadID, generation.TurnID, response, time.Now()); err != nil {
			return nil, nil, nil, err
		}
	}
	card := new(cardwire.PhotoCard)
	if err := protojson.Unmarshal(response, card); err != nil {
		return nil, nil, nil, fmt.Errorf("decode Luna PhotoCard: %w", err)
	}
	if err := photocard.ValidateModelResult(card, locationEvidence.SuppliedCandidates); err == nil {
		if locationOutcome == nil {
			return card, inputSHA256, nil, nil
		}
		return card, inputSHA256, locationDigest[:], nil
	}
	if !photocard.NeedsDescriptionsOnlyRepair(card, locationEvidence.SuppliedCandidates) {
		return nil, nil, nil, photocard.ValidateModelResult(card, locationEvidence.SuppliedCandidates)
	}
	repairInstructions, err := photocard.BuildDescriptionsRepairInstructions(checkedEvidence, card)
	if err != nil {
		return nil, nil, nil, err
	}
	repairSchema, err := photocard.DescriptionsRepairStructuredOutputSchema()
	if err != nil {
		return nil, nil, nil, err
	}
	imageBytes, err := mediaEvidence.CurrentRenderedStill.Read()
	if err != nil {
		return nil, nil, nil, err
	}
	client, err := runner.ensureLunaClient(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	repairResponse := retained.DescriptionsRepairResponseBody
	if len(repairResponse) == 0 {
		repairGeneration, err := client.Generate(ctx, luna.GenerationRequest{Instructions: repairInstructions, Image: imageBytes, ImageMediaType: lunaImageMediaType(mediaEvidence.CurrentRenderedStill.Outcome.GetUniformTypeIdentifier()), OutputSchema: repairSchema})
		if err != nil {
			return nil, nil, nil, err
		}
		repairResponse = repairGeneration.RawStructuredOutputJSON
		if err := archive.RetainPhotoCardDescriptionsRepair(ctx, runner.options.OpenedArchiveStore, asset.AssetID, inputSHA256, repairInstructions, repairGeneration.ThreadID, repairGeneration.TurnID, repairResponse, time.Now()); err != nil {
			return nil, nil, nil, err
		}
	}
	repairedDescriptions := new(cardwire.PhotoDescriptions)
	if err := protojson.Unmarshal(repairResponse, repairedDescriptions); err != nil {
		return nil, nil, nil, fmt.Errorf("decode Luna PhotoCard description repair: %w", err)
	}
	merged, err := photocard.MergeDescriptionsRepair(card, repairedDescriptions, locationEvidence.SuppliedCandidates)
	if locationOutcome == nil {
		return merged, inputSHA256, nil, err
	}
	return merged, inputSHA256, locationDigest[:], err
}

func (runner *Runner) ensureLunaClient(ctx context.Context) (*luna.Client, error) {
	runner.lunaClientMu.Lock()
	defer runner.lunaClientMu.Unlock()
	if runner.lunaClient != nil {
		return runner.lunaClient, nil
	}
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
	account, err := client.Account(ctx)
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	if account.Kind != luna.AccountChatGPT {
		signIn, err := client.BeginChatGPTSignIn(ctx)
		if err != nil {
			_ = client.Close()
			return nil, err
		}
		if err := exec.CommandContext(ctx, "/usr/bin/open", signIn.URL.String()).Run(); err != nil {
			_ = client.Close()
			return nil, fmt.Errorf("open ChatGPT sign-in: %w", err)
		}
		if err := client.WaitForChatGPTSignIn(ctx, signIn.LoginID); err != nil {
			_ = client.Close()
			return nil, err
		}
	}
	runner.lunaClient = client
	return client, nil
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
	fmt.Fprintf(&evidence, "Photo source facts:\n- Captured: %s\n- Source image dimensions: %d × %d pixels\n- Current rendered still: %d × %d pixels; orientation %s\n", asset.CreationTime, asset.PixelWidth, asset.PixelHeight, current.GetPixelWidth(), current.GetPixelHeight(), current.GetImageOrientation())
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
		for _, property := range original.GetProperties() {
			if property == nil || property.GetValue() == nil {
				continue
			}
			fmt.Fprintf(&evidence, "- Original metadata %s.%s: %s\n", property.GetImageIoNamespace(), property.GetPropertyName(), humanReadableMetadataValue(property.GetValue()))
		}
	}
	if strings.TrimSpace(locationText) != "" {
		fmt.Fprintf(&evidence, "\n%s\n", strings.TrimSpace(locationText))
	}
	return strings.TrimSpace(evidence.String())
}

func humanReadableMetadataValue(value *mediawire.ImageMetadataValue) string {
	switch typed := value.GetValue().(type) {
	case *mediawire.ImageMetadataValue_Text:
		return fmt.Sprintf("%q", typed.Text)
	case *mediawire.ImageMetadataValue_Integer:
		return fmt.Sprintf("%d", typed.Integer)
	case *mediawire.ImageMetadataValue_Decimal:
		return fmt.Sprintf("%g", typed.Decimal)
	case *mediawire.ImageMetadataValue_Boolean:
		return fmt.Sprintf("%t", typed.Boolean)
	case *mediawire.ImageMetadataValue_Time:
		if typed.Time == nil {
			return "unknown time"
		}
		return typed.Time.AsTime().Format(time.RFC3339Nano)
	case *mediawire.ImageMetadataValue_TextList:
		quoted := make([]string, 0, len(typed.TextList.GetValues()))
		for _, item := range typed.TextList.GetValues() {
			quoted = append(quoted, fmt.Sprintf("%q", item))
		}
		return "[" + strings.Join(quoted, ", ") + "]"
	case *mediawire.ImageMetadataValue_IntegerList:
		items := make([]string, 0, len(typed.IntegerList.GetValues()))
		for _, item := range typed.IntegerList.GetValues() {
			items = append(items, fmt.Sprintf("%d", item))
		}
		return "[" + strings.Join(items, ", ") + "]"
	case *mediawire.ImageMetadataValue_DecimalList:
		items := make([]string, 0, len(typed.DecimalList.GetValues()))
		for _, item := range typed.DecimalList.GetValues() {
			items = append(items, fmt.Sprintf("%g", item))
		}
		return "[" + strings.Join(items, ", ") + "]"
	default:
		return "unknown value"
	}
}
