package updatephotos

import (
	"context"
	"errors"
	"fmt"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	locationwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/location"
	"google.golang.org/protobuf/proto"
)

func runProductionNode(ctx context.Context, options Options, nodeName ProductionNodeName, asset archive.PhotoUpdateAsset) error {
	runner := &Runner{
		options:                           options,
		appleLocationMainThreadOperations: make(chan appleLocationMainThreadOperation),
		observations:                      newObservationAccumulator(options.Observe),
	}
	knownPlaceConfigurationSHA256, err := archive.KnownPlaceConfigurationSHA256(ctx, options.OpenedArchiveStore)
	if err != nil {
		return err
	}
	switch nodeName {
	case ProductionNodeCurrentMedia:
		request := archive.CurrentRenderedStillRequestForPhotoUpdateAsset(asset)
		retained, found, err := archive.LoadCurrentRenderedPhotoMediaEvidence(ctx, options.OpenedArchiveStore, asset.AssetID)
		if err != nil || found && archive.CurrentRenderedPhotoMediaEvidenceMatchesRequest(retained, request) {
			return err
		}
		if err := runner.ensurePhotoLibraryAccess(ctx); err != nil {
			return err
		}
		unavailable, err := runner.acquireAndStoreCurrentRenderedPhoto(ctx, asset, request)
		if unavailable != nil {
			return fmt.Errorf("current photo media is unavailable: %s", unavailable.GetHumanDescription())
		}
		return err
	case ProductionNodeImmutableOriginalImageFacts:
		request := archive.ImmutableOriginalImageFactsRequestForPhotoUpdateAsset(asset)
		_, found, err := archive.LoadCurrentImmutableOriginalImageFactsOutcomeForRequest(ctx, options.OpenedArchiveStore, asset.AssetID, request)
		if err != nil || found {
			return err
		}
		if err := runner.ensurePhotoLibraryAccess(ctx); err != nil {
			return err
		}
		return runner.inspectAndStoreImmutableOriginalImageFacts(ctx, asset, request)
	case ProductionNodeKnownPlace:
		input, found, err := archive.LoadOptionalCaptureLocationInput(ctx, options.OpenedArchiveStore, string(asset.AssetID))
		if err != nil || !found {
			return err
		}
		_, _, err = runner.matchConfiguredKnownPlace(ctx, input, knownPlaceConfigurationSHA256)
		return err
	case ProductionNodeAppleReverseGeocoding, ProductionNodeAppleNearbyPlaces, ProductionNodeGeoapifyPhotographedPlaceCandidates, ProductionNodeComposeLocationEvidence:
		return runLocationProductionNode(ctx, runner, nodeName, asset, knownPlaceConfigurationSHA256)
	default:
		return fmt.Errorf("Photos production node %q cannot run for one photo", nodeName)
	}
}

func runLocationProductionNode(ctx context.Context, runner *Runner, nodeName ProductionNodeName, asset archive.PhotoUpdateAsset, knownPlaceConfigurationSHA256 []byte) error {
	input, found, err := archive.LoadOptionalCaptureLocationInput(ctx, runner.options.OpenedArchiveStore, string(asset.AssetID))
	if err != nil || !found {
		return err
	}
	knownRequest := &locationwire.MatchConfiguredKnownPlaceRequest{Input: input, KnownPlaceConfigurationSha256: knownPlaceConfigurationSHA256}
	known, knownFound, err := archive.LoadMatchConfiguredKnownPlaceOutcome(ctx, runner.options.OpenedArchiveStore, input.GetAssetId())
	if err != nil {
		return err
	}
	if !knownFound || !proto.Equal(known.GetRequest(), knownRequest) {
		return missingUpstreamProductionNode(ProductionNodeKnownPlace)
	}
	switch nodeName {
	case ProductionNodeAppleReverseGeocoding:
		return runWithAppleMainThreadOperations(ctx, runner, func() error {
			_, operationErr := runner.acquireAppleReverseGeocodingEvidence(ctx, input)
			return operationErr
		})
	case ProductionNodeAppleNearbyPlaces:
		if len(known.GetMatches()) > 0 {
			appleNearby, _ := suppressedNearbyProviderOutcomes(input)
			return archive.StoreAppleNearbyPlaceEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, appleNearby)
		}
		return runWithAppleMainThreadOperations(ctx, runner, func() error {
			_, operationErr := runner.acquireAppleNearbyPlaceEvidence(ctx, input)
			return operationErr
		})
	case ProductionNodeGeoapifyPhotographedPlaceCandidates:
		if len(known.GetMatches()) > 0 {
			_, geoapify := suppressedNearbyProviderOutcomes(input)
			return archive.StoreGeoapifyPhotographedPlaceCandidateEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, geoapify)
		}
		_, err := runner.acquireGeoapifyPhotographedPlaceCandidateEvidence(ctx, input)
		return err
	case ProductionNodeComposeLocationEvidence:
		appleReverseRequest := &locationwire.AcquireAppleReverseGeocodingEvidenceRequest{
			Input: input, ProviderRequest: &locationwire.AppleReverseGeocodingProviderRequest{Coordinate: copyLocationCoordinate(input.GetCoordinate())},
		}
		appleReverse, found, err := archive.LoadAppleReverseGeocodingEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, input.GetAssetId())
		if err != nil || !found || !proto.Equal(appleReverse.GetRequest(), appleReverseRequest) {
			return errors.Join(missingUpstreamProductionNode(ProductionNodeAppleReverseGeocoding), err)
		}
		appleNearbyRequest := appleNearbyPlaceEvidenceRequest(input)
		appleNearby, found, err := archive.LoadAppleNearbyPlaceEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, input.GetAssetId())
		if err != nil || !found || !proto.Equal(appleNearby.GetRequest(), appleNearbyRequest) {
			return errors.Join(missingUpstreamProductionNode(ProductionNodeAppleNearbyPlaces), err)
		}
		geoapifyRequest := geoapifyPhotographedPlaceCandidateEvidenceRequest(input)
		geoapify, found, err := archive.LoadGeoapifyPhotographedPlaceCandidateEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, input.GetAssetId())
		if err != nil || !found || !proto.Equal(geoapify.GetRequest(), geoapifyRequest) {
			return errors.Join(missingUpstreamProductionNode(ProductionNodeGeoapifyPhotographedPlaceCandidates), err)
		}
		_, err = runner.composePhotoLocationEvidence(ctx, asset, knownPlaceConfigurationSHA256, known, appleReverse, appleNearby, geoapify)
		return err
	default:
		return fmt.Errorf("unknown Photos location production node %q", nodeName)
	}
}

func runWithAppleMainThreadOperations(ctx context.Context, runner *Runner, operation func() error) error {
	completed := make(chan error, 1)
	go func() { completed <- operation() }()
	for {
		select {
		case appleOperation := <-runner.appleLocationMainThreadOperations:
			if appleOperation.context.Err() == nil {
				appleOperation.execute()
			}
			close(appleOperation.completed)
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
