// Package updatephotos composes the one Photos update dependency graph.
// Components keep their own typed boundaries; this package owns their order
// and the small amount of useful concurrency between independent operations.
package updatephotos

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	photosmedia "github.com/opentrawl/opentrawl/trawlers/photos/internal/media"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/media/mediawire"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/place"
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
	maximumGeoapifyTransmissionsPerRollingDay      = 3000
	minimumGeoapifyTransmissionStartInterval       = 200 * time.Millisecond
)

// Model hypothesis: this provider-native query may return useful photographed-place candidates.
// Final candidate relevance remains model judgement, not a code taxonomy.
var geoapifyPhotographedPlaceProviderCategoryHypothesis = []string{
	"tourism",
	"natural",
	"leisure",
	"entertainment",
	"populated_place",
	"public_transport",
	"national_park",
	"beach",
}

type CurrentRenderedImageInspectionFilePath string

type Options struct {
	OpenedArchiveStore             *store.Store
	GeoapifyAPIKeyFilePath         string
	PhotosWorkingRoot              string
	CurrentMediaInspectionFilePath CurrentRenderedImageInspectionFilePath
	MaximumAssetsToProcess         int
	Observe                        func(Observation)
}

type Result struct {
	PendingAssets                        int
	SelectedAssets                       int
	GeoapifyTransmissionAllowanceAtStart int
	FoundationsStored                    int
	MediaUnavailable                     int
	UnsupportedMedia                     int
	DeferredOrFailed                     int
	Duration                             time.Duration
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
	appleLocationMainThreadOperations chan *appleLocationMainThreadOperation
	appleMapKitPaused                 bool
	observations                      *observationAccumulator
	providerRequestFlights            providerRequestFlights
	geoapifyAdmissionMutex            sync.Mutex
	geoapifyAttemptsInRollingDay      int
	geoapifyAttemptsInRollingDayKnown bool
	nextGeoapifyTransmissionStart     time.Time
}

type appleLocationMainThreadOperation struct {
	context   context.Context
	execute   func() *locationwire.OperationFailure
	completed chan struct{}
	err       error
}

const appleMapKitPausedReason = "Apple Maps is throttling location requests. OpenTrawl paused Apple location work for this update."
const geoapifyAllowanceExhaustedReason = "Geoapify's free request allowance is exhausted. OpenTrawl will continue on a later update."

type appleReverseGeocodingOperationResult struct {
	outcome *locationwire.AcquireAppleReverseGeocodingEvidenceOutcome
	err     error
}

type appleNearbyPlacesOperationResult struct {
	outcome *locationwire.AcquireAppleNearbyPlaceEvidenceOutcome
	err     error
}

type geoapifyPhotographedPlaceCandidatesOperationResult struct {
	outcome *locationwire.AcquireGeoapifyPhotographedPlaceCandidateEvidenceOutcome
	err     error
}

func (runner *Runner) executeAppleLocationOperationOnMainThread(ctx context.Context, execute func() *locationwire.OperationFailure) error {
	operation := &appleLocationMainThreadOperation{
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
	return errors.Join(ctx.Err(), operation.err)
}

func (runner *Runner) completeAppleLocationMainThreadOperation(operation *appleLocationMainThreadOperation) {
	defer close(operation.completed)
	if err := operation.context.Err(); err != nil {
		operation.err = err
		return
	}
	if runner.appleMapKitPaused {
		operation.err = &AssetDeferredError{Reason: appleMapKitPausedReason}
		return
	}
	failure := operation.execute()
	if failure.GetClass() == locationwire.OperationFailureClass_OPERATION_FAILURE_CLASS_APPLE_MAPKIT_LOADING_THROTTLED {
		runner.appleMapKitPaused = true
		operation.err = &AssetDeferredError{Reason: appleMapKitPausedReason}
	}
}

func (runner *Runner) ensurePhotoLibraryAccess(ctx context.Context) error {
	client := photosmedia.NewInstalledOpenTrawlClient(runner.photosMediaWorkingRoot())
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

func (runner *Runner) photosMediaWorkingRoot() string {
	return filepath.Join(runner.options.PhotosWorkingRoot, "photos-media-ipc")
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

func (runner *Runner) matchConfiguredKnownPlace(ctx context.Context, input *locationwire.CaptureLocationInput, knownPlaceConfigurationSHA256 []byte) (*locationwire.MatchConfiguredKnownPlaceOutcome, WorkDisposition, error) {
	request := &locationwire.MatchConfiguredKnownPlaceRequest{Input: input, KnownPlaceConfigurationSha256: knownPlaceConfigurationSHA256}
	retained, found, err := archive.LoadMatchConfiguredKnownPlaceOutcome(ctx, runner.options.OpenedArchiveStore, input.GetAssetId())
	if err != nil {
		return nil, WorkFailed, err
	}
	if found && proto.Equal(retained.GetRequest(), request) {
		return retained, WorkReused, nil
	}
	outcome, err := archive.MatchConfiguredKnownPlace(ctx, runner.options.OpenedArchiveStore, request)
	if err != nil {
		return nil, WorkFailed, err
	}
	if err := archive.StoreMatchConfiguredKnownPlaceOutcome(ctx, runner.options.OpenedArchiveStore, outcome); err != nil {
		return nil, WorkFailed, err
	}
	return outcome, WorkAcquired, nil
}

func (runner *Runner) acquireProviderLocationEvidence(ctx context.Context, input *locationwire.CaptureLocationInput, known *locationwire.MatchConfiguredKnownPlaceOutcome) (*locationwire.AcquireAppleReverseGeocodingEvidenceOutcome, *locationwire.AcquireAppleNearbyPlaceEvidenceOutcome, *locationwire.AcquireGeoapifyPhotographedPlaceCandidateEvidenceOutcome, error) {
	assetID := archive.PhotoAssetID(input.GetAssetId())
	if len(known.GetMatches()) > 0 {
		runner.observations.startNode(assetID, ProductionNodeAppleReverseGeocoding)
		appleReverse, err := runner.acquireAppleReverseGeocodingEvidence(ctx, input)
		runner.finishLocationProviderNode(assetID, ProductionNodeAppleReverseGeocoding, appleReverse.GetExchange(), appleReverse.GetEvidenceUse(), err)
		if err != nil {
			return nil, nil, nil, err
		}
		appleNearby, geoapify := suppressedNearbyProviderOutcomes(input)
		runner.observations.startNode(assetID, ProductionNodeAppleNearbyPlaces)
		if err := archive.StoreAppleNearbyPlaceEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, appleNearby); err != nil {
			runner.observations.finishNode(assetID, ProductionNodeAppleNearbyPlaces, WorkFailed, nil, nil)
			return nil, nil, nil, err
		}
		runner.observations.finishNode(assetID, ProductionNodeAppleNearbyPlaces, WorkSkipped, nil, nil)
		runner.observations.startNode(assetID, ProductionNodeGeoapifyPhotographedPlaceCandidates)
		if err := archive.StoreGeoapifyPhotographedPlaceCandidateEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, geoapify); err != nil {
			runner.observations.finishNode(assetID, ProductionNodeGeoapifyPhotographedPlaceCandidates, WorkFailed, nil, nil)
			return nil, nil, nil, err
		}
		runner.observations.finishNode(assetID, ProductionNodeGeoapifyPhotographedPlaceCandidates, WorkSkipped, nil, nil)
		return appleReverse, appleNearby, geoapify, nil
	}
	appleReverseResults := make(chan appleReverseGeocodingOperationResult, 1)
	appleNearbyResults := make(chan appleNearbyPlacesOperationResult, 1)
	geoapifyResults := make(chan geoapifyPhotographedPlaceCandidatesOperationResult, 1)
	go func() {
		runner.observations.startNode(assetID, ProductionNodeAppleReverseGeocoding)
		outcome, operationErr := runner.acquireAppleReverseGeocodingEvidence(ctx, input)
		runner.finishLocationProviderNode(assetID, ProductionNodeAppleReverseGeocoding, outcome.GetExchange(), outcome.GetEvidenceUse(), operationErr)
		appleReverseResults <- appleReverseGeocodingOperationResult{outcome: outcome, err: operationErr}
	}()
	go func() {
		runner.observations.startNode(assetID, ProductionNodeAppleNearbyPlaces)
		outcome, operationErr := runner.acquireAppleNearbyPlaceEvidence(ctx, input)
		runner.finishLocationProviderNode(assetID, ProductionNodeAppleNearbyPlaces, outcome.GetExchange(), outcome.GetEvidenceUse(), operationErr)
		appleNearbyResults <- appleNearbyPlacesOperationResult{outcome: outcome, err: operationErr}
	}()
	go func() {
		runner.observations.startNode(assetID, ProductionNodeGeoapifyPhotographedPlaceCandidates)
		outcome, operationErr := runner.acquireGeoapifyPhotographedPlaceCandidateEvidence(ctx, input)
		runner.finishLocationProviderNode(assetID, ProductionNodeGeoapifyPhotographedPlaceCandidates, outcome.GetExchange(), outcome.GetEvidenceUse(), operationErr)
		geoapifyResults <- geoapifyPhotographedPlaceCandidatesOperationResult{outcome: outcome, err: operationErr}
	}()
	appleReverseResult := <-appleReverseResults
	appleNearbyResult := <-appleNearbyResults
	geoapifyResult := <-geoapifyResults
	operationErrors := make([]error, 0, 3)
	if appleReverseResult.err != nil {
		operationErrors = append(operationErrors, fmt.Errorf("Apple reverse geocoding: %w", appleReverseResult.err))
	}
	if appleNearbyResult.err != nil {
		operationErrors = append(operationErrors, fmt.Errorf("Apple nearby places: %w", appleNearbyResult.err))
	}
	if geoapifyResult.err != nil {
		operationErrors = append(operationErrors, fmt.Errorf("Geoapify photographed-place candidates: %w", geoapifyResult.err))
	}
	if len(operationErrors) != 0 {
		return nil, nil, nil, errors.Join(operationErrors...)
	}
	if !place.ProviderExchangeSatisfiesCurrentLocationEvidence(appleReverseResult.outcome.GetExchange(), false) || !place.ProviderExchangeSatisfiesCurrentLocationEvidence(appleNearbyResult.outcome.GetExchange(), true) || !place.ProviderExchangeSatisfiesCurrentLocationEvidence(geoapifyResult.outcome.GetExchange(), true) {
		return nil, nil, nil, &AssetDeferredError{Reason: "location provider evidence remains retryable"}
	}
	return appleReverseResult.outcome, appleNearbyResult.outcome, geoapifyResult.outcome, nil
}

func suppressedNearbyProviderOutcomes(input *locationwire.CaptureLocationInput) (*locationwire.AcquireAppleNearbyPlaceEvidenceOutcome, *locationwire.AcquireGeoapifyPhotographedPlaceCandidateEvidenceOutcome) {
	completedAt := timestamppb.Now()
	appleRequest := appleNearbyPlaceEvidenceRequest(input)
	geoapifyRequest := geoapifyPhotographedPlaceCandidateEvidenceRequest(input)
	return &locationwire.AcquireAppleNearbyPlaceEvidenceOutcome{
			Request:     appleRequest,
			Exchange:    &locationwire.ProviderExchange{State: locationwire.OperationState_OPERATION_STATE_SKIPPED_KNOWN_PLACE},
			Provider:    locationwire.LocationEvidenceProvider_LOCATION_EVIDENCE_PROVIDER_APPLE_NEARBY_PLACES,
			CompletedAt: completedAt,
		}, &locationwire.AcquireGeoapifyPhotographedPlaceCandidateEvidenceOutcome{
			Request:     geoapifyRequest,
			Exchange:    &locationwire.ProviderExchange{State: locationwire.OperationState_OPERATION_STATE_SKIPPED_KNOWN_PLACE},
			Provider:    locationwire.LocationEvidenceProvider_LOCATION_EVIDENCE_PROVIDER_GEOAPIFY_PLACES,
			CompletedAt: completedAt,
		}
}

func (runner *Runner) acquireAppleReverseGeocodingEvidence(ctx context.Context, input *locationwire.CaptureLocationInput) (*locationwire.AcquireAppleReverseGeocodingEvidenceOutcome, error) {
	request := appleReverseGeocodingEvidenceRequest(input)
	return runProviderRequestFlight(ctx, &runner.providerRequestFlights, "apple-reverse-geocoding", request.GetProviderRequest(), func() (*locationwire.AcquireAppleReverseGeocodingEvidenceOutcome, error) {
		return runner.acquireAppleReverseGeocodingEvidenceWithoutConcurrentDuplicate(ctx, request)
	})
}

func appleReverseGeocodingEvidenceRequest(input *locationwire.CaptureLocationInput) *locationwire.AcquireAppleReverseGeocodingEvidenceRequest {
	return &locationwire.AcquireAppleReverseGeocodingEvidenceRequest{
		Input: input,
		ProviderRequest: &locationwire.AppleReverseGeocodingProviderRequest{
			Coordinate: copyLocationCoordinate(input.GetCoordinate()),
			Method:     locationwire.AppleReverseGeocodingMethod_APPLE_REVERSE_GEOCODING_METHOD_MAP_KIT_REVERSE_GEOCODING_REQUEST,
		},
	}
}

func (runner *Runner) acquireAppleReverseGeocodingEvidenceWithoutConcurrentDuplicate(ctx context.Context, request *locationwire.AcquireAppleReverseGeocodingEvidenceRequest) (*locationwire.AcquireAppleReverseGeocodingEvidenceOutcome, error) {
	retained, found, err := archive.LoadAppleReverseGeocodingEvidenceOutcomeForRequest(ctx, runner.options.OpenedArchiveStore, request)
	if err != nil {
		return nil, err
	}
	retain := func(outcome *locationwire.AcquireAppleReverseGeocodingEvidenceOutcome) error {
		return archive.StoreAppleReverseGeocodingEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, outcome)
	}
	if found && (place.ProviderExchangeSatisfiesCurrentLocationEvidence(retained.GetExchange(), false) || providerRetryNotBeforeIsFuture(retained.GetExchange(), time.Now())) {
		return retained, retain(retained)
	}
	if found && retained.GetExchange().GetState() == locationwire.OperationState_OPERATION_STATE_RESPONSE_RETAINED {
		return place.ResumeAppleReverseGeocodingEvidence(retained, retain)
	}
	var outcome *locationwire.AcquireAppleReverseGeocodingEvidenceOutcome
	dispatchErr := runner.executeAppleLocationOperationOnMainThread(ctx, func() *locationwire.OperationFailure {
		outcome, err = place.AcquireAppleReverseGeocodingEvidence(ctx, request, retain)
		return outcome.GetExchange().GetFailure()
	})
	return outcome, errors.Join(err, dispatchErr)
}

func appleNearbyPlaceEvidenceRequest(input *locationwire.CaptureLocationInput) *locationwire.AcquireAppleNearbyPlaceEvidenceRequest {
	return &locationwire.AcquireAppleNearbyPlaceEvidenceRequest{
		Input: input,
		ProviderRequest: &locationwire.AppleNearbyPlaceProviderRequest{
			Coordinate: copyLocationCoordinate(input.GetCoordinate()), RadiusMeters: appleNearbyPlaceRadiusMetres, MaximumCandidates: maximumAppleNearbyPlaceCandidates,
			Method: locationwire.AppleNearbyPlaceSearchMethod_APPLE_NEARBY_PLACE_SEARCH_METHOD_MAP_KIT_LOCAL_SEARCH,
		},
	}
}

func (runner *Runner) acquireAppleNearbyPlaceEvidence(ctx context.Context, input *locationwire.CaptureLocationInput) (*locationwire.AcquireAppleNearbyPlaceEvidenceOutcome, error) {
	request := appleNearbyPlaceEvidenceRequest(input)
	return runProviderRequestFlight(ctx, &runner.providerRequestFlights, "apple-nearby-places", request.GetProviderRequest(), func() (*locationwire.AcquireAppleNearbyPlaceEvidenceOutcome, error) {
		return runner.acquireAppleNearbyPlaceEvidenceWithoutConcurrentDuplicate(ctx, request)
	})
}

func (runner *Runner) acquireAppleNearbyPlaceEvidenceWithoutConcurrentDuplicate(ctx context.Context, request *locationwire.AcquireAppleNearbyPlaceEvidenceRequest) (*locationwire.AcquireAppleNearbyPlaceEvidenceOutcome, error) {
	retained, found, err := archive.LoadAppleNearbyPlaceEvidenceOutcomeForRequest(ctx, runner.options.OpenedArchiveStore, request)
	if err != nil {
		return nil, err
	}
	if found && (place.ProviderExchangeSatisfiesCurrentLocationEvidence(retained.GetExchange(), false) || providerRetryNotBeforeIsFuture(retained.GetExchange(), time.Now())) {
		return retained, archive.StoreAppleNearbyPlaceEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, retained)
	}
	retain := func(outcome *locationwire.AcquireAppleNearbyPlaceEvidenceOutcome) error {
		return archive.StoreAppleNearbyPlaceEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, outcome)
	}
	var outcome *locationwire.AcquireAppleNearbyPlaceEvidenceOutcome
	dispatchErr := runner.executeAppleLocationOperationOnMainThread(ctx, func() *locationwire.OperationFailure {
		if found && retained.GetExchange().GetState() == locationwire.OperationState_OPERATION_STATE_RESPONSE_RETAINED {
			outcome, err = place.ResumeAppleNearbyPlaceEvidence(retained, retain)
		} else {
			outcome, err = place.AcquireAppleNearbyPlaceEvidence(ctx, request, retain)
		}
		return outcome.GetExchange().GetFailure()
	})
	return outcome, errors.Join(err, dispatchErr)
}

func geoapifyPhotographedPlaceCandidateEvidenceRequest(input *locationwire.CaptureLocationInput) *locationwire.AcquireGeoapifyPhotographedPlaceCandidateEvidenceRequest {
	return &locationwire.AcquireGeoapifyPhotographedPlaceCandidateEvidenceRequest{
		Input: input,
		ProviderRequest: &locationwire.GeoapifyPlacesProviderRequest{
			Coordinate: copyLocationCoordinate(input.GetCoordinate()), RadiusMeters: geoapifyPhotographedPlaceCandidateRadiusMetres,
			MaximumCandidates:  maximumGeoapifyPhotographedPlaceCandidates,
			ProviderCategories: append([]string(nil), geoapifyPhotographedPlaceProviderCategoryHypothesis...), RequireNamedCandidates: true,
		},
	}
}

func (runner *Runner) acquireGeoapifyPhotographedPlaceCandidateEvidence(ctx context.Context, input *locationwire.CaptureLocationInput) (*locationwire.AcquireGeoapifyPhotographedPlaceCandidateEvidenceOutcome, error) {
	request := geoapifyPhotographedPlaceCandidateEvidenceRequest(input)
	return runProviderRequestFlight(ctx, &runner.providerRequestFlights, "geoapify-photographed-place-candidates", request.GetProviderRequest(), func() (*locationwire.AcquireGeoapifyPhotographedPlaceCandidateEvidenceOutcome, error) {
		return runner.acquireGeoapifyPhotographedPlaceCandidateEvidenceWithoutConcurrentDuplicate(ctx, request)
	})
}

func (runner *Runner) acquireGeoapifyPhotographedPlaceCandidateEvidenceWithoutConcurrentDuplicate(ctx context.Context, request *locationwire.AcquireGeoapifyPhotographedPlaceCandidateEvidenceRequest) (*locationwire.AcquireGeoapifyPhotographedPlaceCandidateEvidenceOutcome, error) {
	retained, found, err := archive.LoadGeoapifyPhotographedPlaceCandidateEvidenceOutcomeForRequest(ctx, runner.options.OpenedArchiveStore, request)
	if err != nil {
		return nil, err
	}
	if found && (place.ProviderExchangeSatisfiesCurrentLocationEvidence(retained.GetExchange(), false) || providerRetryNotBeforeIsFuture(retained.GetExchange(), time.Now())) {
		return retained, archive.StoreGeoapifyPhotographedPlaceCandidateEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, retained)
	}
	retain := func(outcome *locationwire.AcquireGeoapifyPhotographedPlaceCandidateEvidenceOutcome) error {
		return archive.StoreGeoapifyPhotographedPlaceCandidateEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, outcome)
	}
	if found && retained.GetExchange().GetState() == locationwire.OperationState_OPERATION_STATE_RESPONSE_RETAINED {
		return place.ResumeGeoapifyPhotographedPlaceCandidateEvidence(retained, retain)
	}
	if err := runner.admitGeoapifyTransmission(ctx); err != nil {
		return nil, err
	}
	return place.AcquireGeoapifyPhotographedPlaceCandidateEvidence(ctx, request, runner.options.GeoapifyAPIKeyFilePath, &http.Client{Timeout: 30 * time.Second}, retain)
}

func (runner *Runner) admitGeoapifyTransmission(ctx context.Context) error {
	runner.geoapifyAdmissionMutex.Lock()
	if !runner.geoapifyAttemptsInRollingDayKnown {
		attempts, err := archive.CountLocationProviderTransmissionAttemptsSince(
			ctx,
			runner.options.OpenedArchiveStore,
			archive.ProviderLocationOperationGeoapifyPhotographedPlaceCandidateEvidence,
			time.Now().Add(-24*time.Hour),
		)
		if err != nil {
			runner.geoapifyAdmissionMutex.Unlock()
			return err
		}
		runner.geoapifyAttemptsInRollingDay = attempts
		runner.geoapifyAttemptsInRollingDayKnown = true
	}
	if runner.geoapifyAttemptsInRollingDay >= maximumGeoapifyTransmissionsPerRollingDay {
		runner.geoapifyAdmissionMutex.Unlock()
		return &AssetDeferredError{Reason: geoapifyAllowanceExhaustedReason}
	}
	now := time.Now()
	transmissionStart := now
	if runner.nextGeoapifyTransmissionStart.After(now) {
		transmissionStart = runner.nextGeoapifyTransmissionStart
	}
	runner.nextGeoapifyTransmissionStart = transmissionStart.Add(minimumGeoapifyTransmissionStartInterval)
	runner.geoapifyAttemptsInRollingDay++
	runner.geoapifyAdmissionMutex.Unlock()

	wait := time.Until(transmissionStart)
	if wait <= 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func copyLocationCoordinate(coordinate *locationwire.Coordinate) *locationwire.Coordinate {
	if coordinate == nil {
		return nil
	}
	return &locationwire.Coordinate{Latitude: coordinate.GetLatitude(), Longitude: coordinate.GetLongitude()}
}

func providerRetryNotBeforeIsFuture(exchange *locationwire.ProviderExchange, now time.Time) bool {
	retryNotBefore := exchange.GetFailure().GetRetryNotBefore()
	return exchange.GetState() == locationwire.OperationState_OPERATION_STATE_FAILED && retryNotBefore != nil && retryNotBefore.IsValid() && retryNotBefore.AsTime().After(now)
}
