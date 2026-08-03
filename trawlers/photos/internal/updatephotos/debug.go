package updatephotos

import (
	"context"
	"errors"
	"fmt"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/locationbriefing"
	locationwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/location"
	"github.com/opentrawl/opentrawl/trawlkit/store"
	"google.golang.org/protobuf/proto"
)

type DebugNodeResult struct {
	NodeName                       ProductionNodeName
	Work                           *WorkDisposition
	Input                          string
	Output                         string
	CurrentMediaInspectionFilePath CurrentRenderedImageInspectionFilePath
}

func DebugProductionNode(ctx context.Context, options Options, nodeName ProductionNodeName, asset archive.PhotoUpdateAsset) (DebugNodeResult, error) {
	if options.OpenedArchiveStore == nil {
		return DebugNodeResult{}, errors.New("the Photos archive is not available")
	}
	node, found := productionNodeForName(nodeName)
	if !found || !node.RequiresPhoto {
		return DebugNodeResult{}, fmt.Errorf("Photos production node %q does not have a retained per-photo output", nodeName)
	}
	input, output, err := inspectRetainedProductionNode(ctx, options.OpenedArchiveStore, nodeName, asset)
	return DebugNodeResult{NodeName: nodeName, Input: input, Output: output}, err
}

func RunAndDebugProductionNode(ctx context.Context, options Options, nodeName ProductionNodeName, asset archive.PhotoUpdateAsset) (DebugNodeResult, error) {
	disposition, err := runProductionNode(ctx, options, nodeName, asset)
	if err != nil {
		return DebugNodeResult{}, err
	}
	input, output, err := inspectRetainedProductionNode(ctx, options.OpenedArchiveStore, nodeName, asset)
	return DebugNodeResult{
		NodeName:                       nodeName,
		Work:                           &disposition,
		Input:                          input,
		Output:                         output,
		CurrentMediaInspectionFilePath: options.CurrentMediaInspectionFilePath,
	}, err
}

func inspectRetainedProductionNode(ctx context.Context, openedArchiveStore *store.Store, nodeName ProductionNodeName, asset archive.PhotoUpdateAsset) (string, string, error) {
	switch nodeName {
	case ProductionNodeCurrentMedia:
		retained, found, err := archive.LoadCurrentRenderedPhotoMediaEvidence(ctx, openedArchiveStore, asset.AssetID)
		if err != nil || !found || !archive.CurrentRenderedPhotoMediaEvidenceMatchesRequest(retained, archive.CurrentRenderedStillRequestForPhotoUpdateAsset(asset)) {
			return "", "", missingRetainedProductionOutput(nodeName, err)
		}
		return renderDebugInputAndOutput("source-asset", asset, "current-media", retained)
	case ProductionNodeImmutableOriginalImageFacts:
		outcome, found, err := archive.LoadRetainedImmutableOriginalImageFactsOutcome(ctx, openedArchiveStore, asset.AssetID)
		if err != nil || !found {
			return "", "", missingRetainedProductionOutput(nodeName, err)
		}
		return renderDebugInputAndOutput("source-asset", asset, "immutable-original", outcome)
	case ProductionNodeKnownPlace, ProductionNodeAppleReverseGeocoding, ProductionNodeAppleNearbyPlaces, ProductionNodeGeoapifyReverseGeocoding, ProductionNodeGeoapifyPhotographedPlaceCandidates, ProductionNodeComposeLocationEvidence:
		return inspectRetainedLocationNode(ctx, openedArchiveStore, nodeName, asset)
	default:
		return "", "", fmt.Errorf("unknown Photos production node %q", nodeName)
	}
}

func renderDebugInputAndOutput(inputTemplateName string, input any, outputTemplateName string, output any) (string, string, error) {
	readableInput, err := renderDebugOutput(inputTemplateName, input)
	if err != nil {
		return "", "", err
	}
	readableOutput, err := renderDebugOutput(outputTemplateName, output)
	return readableInput, readableOutput, err
}

func missingRetainedProductionOutput(nodeName ProductionNodeName, cause error) error {
	readable, renderErr := renderDebugOutput("missing-output", nodeName)
	message := errors.New(readable)
	return errors.Join(message, cause, renderErr)
}

func inspectRetainedLocationNode(ctx context.Context, openedArchiveStore *store.Store, nodeName ProductionNodeName, asset archive.PhotoUpdateAsset) (string, string, error) {
	input, found, err := archive.LoadOptionalCaptureLocationInput(ctx, openedArchiveStore, string(asset.AssetID))
	if err != nil {
		return "", "", err
	}
	if !found {
		return renderDebugInputAndOutput("capture-location-absent", nil, "location-not-required", nil)
	}
	knownPlaceConfigurationSHA256, err := archive.KnownPlaceConfigurationSHA256(ctx, openedArchiveStore)
	if err != nil {
		return "", "", err
	}
	known, knownFound, err := archive.LoadMatchConfiguredKnownPlaceOutcome(ctx, openedArchiveStore, input.GetAssetId())
	if err != nil {
		return "", "", err
	}
	knownRequest := &locationwire.MatchConfiguredKnownPlaceRequest{Input: input, KnownPlaceConfigurationSha256: knownPlaceConfigurationSHA256}
	if knownFound && !proto.Equal(known.GetRequest(), knownRequest) {
		knownFound = false
	}
	if nodeName == ProductionNodeKnownPlace {
		if !knownFound {
			return "", "", missingRetainedProductionOutput(nodeName, nil)
		}
		return renderDebugInputAndOutput("capture-location", input, "known-place", known)
	}
	if !knownFound {
		return "", "", missingRetainedProductionOutput(ProductionNodeKnownPlace, nil)
	}
	switch nodeName {
	case ProductionNodeAppleReverseGeocoding:
		outcome, retained, err := archive.LoadAppleReverseGeocodingEvidenceOutcome(ctx, openedArchiveStore, input.GetAssetId())
		if err != nil || !retained || !proto.Equal(outcome.GetRequest(), appleReverseGeocodingEvidenceRequest(input)) {
			return "", "", missingRetainedProductionOutput(nodeName, err)
		}
		return renderDebugInputAndOutput("capture-location", input, "reverse-geocoding", debugReverseGeocodingTemplateData{Outcome: outcome})
	case ProductionNodeAppleNearbyPlaces:
		outcome, retained, err := archive.LoadAppleNearbyPlaceEvidenceOutcome(ctx, openedArchiveStore, input.GetAssetId())
		if err != nil || !retained || !proto.Equal(outcome.GetRequest(), appleNearbyPlaceEvidenceRequest(input)) {
			return "", "", missingRetainedProductionOutput(nodeName, err)
		}
		return renderDebugInputAndOutput("known-place", known, "apple-nearby-places", debugAppleNearbyPlacesTemplateData{Outcome: outcome})
	case ProductionNodeGeoapifyReverseGeocoding:
		outcome, retained, err := archive.LoadGeoapifyReverseGeocodingEvidenceOutcome(ctx, openedArchiveStore, input.GetAssetId())
		if err != nil || !retained || !proto.Equal(outcome.GetRequest(), geoapifyReverseGeocodingEvidenceRequest(input)) {
			return "", "", missingRetainedProductionOutput(nodeName, err)
		}
		return renderDebugInputAndOutput("capture-location", input, "geoapify-reverse-geocoding", debugGeoapifyReverseGeocodingTemplateData{Outcome: outcome})
	case ProductionNodeGeoapifyPhotographedPlaceCandidates:
		outcome, retained, err := archive.LoadGeoapifyPhotographedPlaceCandidateEvidenceOutcome(ctx, openedArchiveStore, input.GetAssetId())
		if err != nil || !retained || !proto.Equal(outcome.GetRequest(), geoapifyPhotographedPlaceCandidateEvidenceRequest(input)) {
			return "", "", missingRetainedProductionOutput(nodeName, err)
		}
		return renderDebugInputAndOutput("known-place", known, "geoapify-places", debugGeoapifyPlacesTemplateData{Outcome: outcome})
	case ProductionNodeComposeLocationEvidence:
		outcome, retained, err := archive.LoadCurrentPhotoLocationEvidence(ctx, openedArchiveStore, asset, knownPlaceConfigurationSHA256)
		if err != nil || !retained {
			return "", "", missingRetainedProductionOutput(nodeName, err)
		}
		appleReverse, appleReverseFound, err := archive.LoadAppleReverseGeocodingEvidenceOutcome(ctx, openedArchiveStore, input.GetAssetId())
		if err != nil || !appleReverseFound || !proto.Equal(appleReverse.GetRequest(), appleReverseGeocodingEvidenceRequest(input)) {
			return "", "", missingRetainedProductionOutput(ProductionNodeAppleReverseGeocoding, err)
		}
		appleNearby, appleNearbyFound, err := archive.LoadAppleNearbyPlaceEvidenceOutcome(ctx, openedArchiveStore, input.GetAssetId())
		if err != nil || !appleNearbyFound || !proto.Equal(appleNearby.GetRequest(), appleNearbyPlaceEvidenceRequest(input)) {
			return "", "", missingRetainedProductionOutput(ProductionNodeAppleNearbyPlaces, err)
		}
		geoapifyReverse, geoapifyReverseFound, err := archive.LoadGeoapifyReverseGeocodingEvidenceOutcome(ctx, openedArchiveStore, input.GetAssetId())
		if err != nil || !geoapifyReverseFound || !proto.Equal(geoapifyReverse.GetRequest(), geoapifyReverseGeocodingEvidenceRequest(input)) {
			return "", "", missingRetainedProductionOutput(ProductionNodeGeoapifyReverseGeocoding, err)
		}
		geoapify, geoapifyFound, err := archive.LoadGeoapifyPhotographedPlaceCandidateEvidenceOutcome(ctx, openedArchiveStore, input.GetAssetId())
		if err != nil || !geoapifyFound || !proto.Equal(geoapify.GetRequest(), geoapifyPhotographedPlaceCandidateEvidenceRequest(input)) {
			return "", "", missingRetainedProductionOutput(ProductionNodeGeoapifyPhotographedPlaceCandidates, err)
		}
		if !composePhotoLocationEvidenceRequestMatchesDependencies(outcome, known, appleReverse, appleNearby, geoapifyReverse, geoapify) {
			return "", "", missingRetainedProductionOutput(nodeName, nil)
		}
		readableInput, err := renderDebugOutput("capture-location", input)
		if err != nil {
			return "", "", err
		}
		readableOutput, err := locationbriefing.Render(outcome)
		return readableInput, readableOutput, err
	default:
		return "", "", fmt.Errorf("unknown Photos location production node %q", nodeName)
	}
}
