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
)

// Model hypothesis: this provider-native query may return useful photographed-place candidates.
// Final candidate relevance remains model judgement, not a code taxonomy.
var geoapifyPhotographedPlaceProviderCategoryHypothesis = []string{
	"entertainment.museum", "national_park", "natural.coastal", "natural.desert", "natural.forest",
	"leisure.park.nature_reserve", "beach", "natural.protected_area", "natural.sand.dune", "natural.water.bay",
	"natural.water.geyser", "natural.water.hot_spring", "natural.water.reef", "natural.water.spring", "natural.water.whitewater",
	"natural.wetland", "natural.mountain.cave_entrance", "natural.mountain.cliff", "natural.mountain.glacier", "natural.mountain.peak",
	"natural.mountain.rock", "natural.mountain.volcano", "populated_place.city", "populated_place.hamlet", "populated_place.neighbourhood",
	"populated_place.suburb", "populated_place.town", "populated_place.village", "public_transport.ferry", "public_transport.train",
	"tourism.attraction.viewpoint", "tourism.sights.bridge", "tourism.sights.building", "tourism.sights.castle", "tourism.sights.city_gate",
	"tourism.sights.city_hall", "tourism.sights.fort", "tourism.sights.lighthouse", "tourism.sights.manor", "tourism.sights.mine",
	"tourism.sights.monastery", "tourism.sights.place_of_worship", "tourism.sights.ruines", "tourism.sights.tower", "tourism.sights.windmill",
}

type Options struct {
	OpenedArchiveStore     *store.Store
	GeoapifyAPIKeyFilePath string
	PhotosWorkingRoot      string
	MaximumAssetsToProcess int
	Observe                func(Observation)
}

type Result struct {
	PendingAssets     int
	SelectedAssets    int
	FoundationsStored int
	MediaUnavailable  int
	UnsupportedMedia  int
	DeferredOrFailed  int
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
	appleLocationMainThreadOperations chan appleLocationMainThreadOperation
	observations                      *observationAccumulator
	providerRequestFlights            providerRequestFlights
}

type appleLocationMainThreadOperation struct {
	context   context.Context
	execute   func()
	completed chan struct{}
}

type locationProviderOperationResult struct {
	name    string
	outcome any
	err     error
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
		runner.finishLocationProviderNode(assetID, ProductionNodeAppleReverseGeocoding, appleReverse, err)
		if err != nil {
			return nil, nil, nil, err
		}
		appleNearby, geoapify := suppressedNearbyProviderOutcomes(input)
		runner.observations.record(assetID, ProductionNodeAppleNearbyPlaces, WorkSkipped, nil, nil)
		runner.observations.record(assetID, ProductionNodeGeoapifyPhotographedPlaceCandidates, WorkSkipped, nil, nil)
		if err := archive.StoreAppleNearbyPlaceEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, appleNearby); err != nil {
			return nil, nil, nil, err
		}
		if err := archive.StoreGeoapifyPhotographedPlaceCandidateEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, geoapify); err != nil {
			return nil, nil, nil, err
		}
		return appleReverse, appleNearby, geoapify, nil
	}
	results := make(chan locationProviderOperationResult, 3)
	go runner.reportLocationProviderOperation(assetID, ProductionNodeAppleReverseGeocoding, "Apple reverse geocoding", func() (any, error) { return runner.acquireAppleReverseGeocodingEvidence(ctx, input) }, results)
	go runner.reportLocationProviderOperation(assetID, ProductionNodeAppleNearbyPlaces, "Apple nearby places", func() (any, error) { return runner.acquireAppleNearbyPlaceEvidence(ctx, input) }, results)
	go runner.reportLocationProviderOperation(assetID, ProductionNodeGeoapifyPhotographedPlaceCandidates, "Geoapify photographed-place candidates", func() (any, error) {
		return runner.acquireGeoapifyPhotographedPlaceCandidateEvidence(ctx, input)
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

func suppressedNearbyProviderOutcomes(input *locationwire.CaptureLocationInput) (*locationwire.AcquireAppleNearbyPlaceEvidenceOutcome, *locationwire.AcquireGeoapifyPhotographedPlaceCandidateEvidenceOutcome) {
	completedAt := timestamppb.Now()
	appleRequest := appleNearbyPlaceEvidenceRequest(input)
	geoapifyRequest := geoapifyPhotographedPlaceCandidateEvidenceRequest(input)
	return &locationwire.AcquireAppleNearbyPlaceEvidenceOutcome{
			Request:     appleRequest,
			Exchange:    &locationwire.ProviderExchange{State: locationwire.OperationState_OPERATION_STATE_SKIPPED_KNOWN_PLACE},
			Provider:    locationwire.LocationEvidenceProvider_LOCATION_EVIDENCE_PROVIDER_APPLE_MAP_KIT,
			CompletedAt: completedAt,
		}, &locationwire.AcquireGeoapifyPhotographedPlaceCandidateEvidenceOutcome{
			Request:     geoapifyRequest,
			Exchange:    &locationwire.ProviderExchange{State: locationwire.OperationState_OPERATION_STATE_SKIPPED_KNOWN_PLACE},
			Provider:    locationwire.LocationEvidenceProvider_LOCATION_EVIDENCE_PROVIDER_GEOAPIFY_PLACES,
			CompletedAt: completedAt,
		}
}

func (runner *Runner) reportLocationProviderOperation(assetID archive.PhotoAssetID, nodeName ProductionNodeName, displayName string, operation func() (any, error), results chan<- locationProviderOperationResult) {
	runner.observations.startNode(assetID, nodeName)
	outcome, err := operation()
	runner.finishLocationProviderNode(assetID, nodeName, outcome, err)
	results <- locationProviderOperationResult{displayName, outcome, err}
}

func (runner *Runner) acquireAppleReverseGeocodingEvidence(ctx context.Context, input *locationwire.CaptureLocationInput) (*locationwire.AcquireAppleReverseGeocodingEvidenceOutcome, error) {
	request := &locationwire.AcquireAppleReverseGeocodingEvidenceRequest{
		Input:           input,
		ProviderRequest: &locationwire.AppleReverseGeocodingProviderRequest{Coordinate: copyLocationCoordinate(input.GetCoordinate())},
	}
	return runProviderRequestFlight(ctx, &runner.providerRequestFlights, "apple-reverse-geocoding", request.GetProviderRequest(), func() (*locationwire.AcquireAppleReverseGeocodingEvidenceOutcome, error) {
		return runner.acquireAppleReverseGeocodingEvidenceWithoutConcurrentDuplicate(ctx, request)
	})
}

func (runner *Runner) acquireAppleReverseGeocodingEvidenceWithoutConcurrentDuplicate(ctx context.Context, request *locationwire.AcquireAppleReverseGeocodingEvidenceRequest) (*locationwire.AcquireAppleReverseGeocodingEvidenceOutcome, error) {
	input := request.GetInput()
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
	if found {
		runner.observations.record(archive.PhotoAssetID(input.GetAssetId()), ProductionNodeAppleReverseGeocoding, WorkRetried, nil, nil)
	}
	var outcome *locationwire.AcquireAppleReverseGeocodingEvidenceOutcome
	dispatchErr := runner.executeAppleLocationOperationOnMainThread(ctx, func() {
		outcome, err = place.AcquireAppleReverseGeocodingEvidence(ctx, request, retain)
	})
	return outcome, errors.Join(err, dispatchErr)
}

func appleNearbyPlaceEvidenceRequest(input *locationwire.CaptureLocationInput) *locationwire.AcquireAppleNearbyPlaceEvidenceRequest {
	return &locationwire.AcquireAppleNearbyPlaceEvidenceRequest{
		Input: input,
		ProviderRequest: &locationwire.AppleNearbyPlaceProviderRequest{
			Coordinate: copyLocationCoordinate(input.GetCoordinate()), RadiusMeters: appleNearbyPlaceRadiusMetres, MaximumCandidates: maximumAppleNearbyPlaceCandidates,
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
	input := request.GetInput()
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
	if found && retained.GetExchange().GetState() != locationwire.OperationState_OPERATION_STATE_RESPONSE_RETAINED {
		runner.observations.record(archive.PhotoAssetID(input.GetAssetId()), ProductionNodeAppleNearbyPlaces, WorkRetried, nil, nil)
	}
	dispatchErr := runner.executeAppleLocationOperationOnMainThread(ctx, func() {
		if found && retained.GetExchange().GetState() == locationwire.OperationState_OPERATION_STATE_RESPONSE_RETAINED {
			outcome, err = place.ResumeAppleNearbyPlaceEvidence(retained, retain)
		} else {
			outcome, err = place.AcquireAppleNearbyPlaceEvidence(ctx, request, retain)
		}
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
	input := request.GetInput()
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
	if found {
		runner.observations.record(archive.PhotoAssetID(input.GetAssetId()), ProductionNodeGeoapifyPhotographedPlaceCandidates, WorkRetried, nil, nil)
	}
	return place.AcquireGeoapifyPhotographedPlaceCandidateEvidence(ctx, request, runner.options.GeoapifyAPIKeyFilePath, &http.Client{Timeout: 30 * time.Second}, retain)
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
