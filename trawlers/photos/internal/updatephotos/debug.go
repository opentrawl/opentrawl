package updatephotos

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/locationbriefing"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/media/mediawire"
	locationwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/location"
	"github.com/opentrawl/opentrawl/trawlkit/store"
)

type DebugNodeResult struct {
	NodeName ProductionNodeName
	Input    string
	Output   string
}

func DebugProductionNode(ctx context.Context, options Options, nodeName ProductionNodeName, asset archive.PhotoUpdateAsset) (DebugNodeResult, error) {
	if options.OpenedArchiveStore == nil {
		return DebugNodeResult{}, errors.New("the Photos archive is not available")
	}
	node, found := productionNodeForName(nodeName)
	if !found || !node.RequiresPhoto {
		return DebugNodeResult{}, fmt.Errorf("Photos production node %q does not have a retained per-photo output", nodeName)
	}
	if err := runProductionNode(ctx, options, nodeName, asset); err != nil {
		return DebugNodeResult{}, err
	}
	input, output, err := inspectRetainedProductionNode(ctx, options.OpenedArchiveStore, nodeName, asset)
	return DebugNodeResult{NodeName: nodeName, Input: input, Output: output}, err
}

func inspectRetainedProductionNode(ctx context.Context, openedArchiveStore *store.Store, nodeName ProductionNodeName, asset archive.PhotoUpdateAsset) (string, string, error) {
	switch nodeName {
	case ProductionNodeCurrentMedia:
		retained, found, err := archive.LoadCurrentRenderedPhotoMediaEvidence(ctx, openedArchiveStore, asset.AssetID)
		if err != nil || !found || !archive.CurrentRenderedPhotoMediaEvidenceMatchesRequest(retained, archive.CurrentRenderedStillRequestForPhotoUpdateAsset(asset)) {
			return "", "", missingRetainedProductionOutput(nodeName, err)
		}
		return humanReadableSourceAsset(asset), humanReadableCurrentRenderedPhoto(retained), nil
	case ProductionNodeImmutableOriginalImageFacts:
		request := archive.ImmutableOriginalImageFactsRequestForPhotoUpdateAsset(asset)
		outcome, found, err := archive.LoadCurrentImmutableOriginalImageFactsOutcomeForRequest(ctx, openedArchiveStore, asset.AssetID, request)
		if err != nil || !found {
			return "", "", missingRetainedProductionOutput(nodeName, err)
		}
		return humanReadableSourceAsset(asset), humanReadableImmutableOriginalImageFacts(outcome), nil
	case ProductionNodeKnownPlace, ProductionNodeAppleReverseGeocoding, ProductionNodeAppleNearbyPlaces, ProductionNodeGeoapifyPhotographedPlaceCandidates, ProductionNodeComposeLocationEvidence:
		return inspectRetainedLocationNode(ctx, openedArchiveStore, nodeName, asset)
	default:
		return "", "", fmt.Errorf("unknown Photos production node %q", nodeName)
	}
}

func humanReadableCurrentRenderedPhoto(retained archive.RetainedCurrentPhotoMediaEvidence) string {
	return fmt.Sprintf("Current rendered image: %d × %d pixels, %d bytes, SHA-256 %s", retained.CurrentRenderedStillPixelWidth, retained.CurrentRenderedStillPixelHeight, retained.CurrentRenderedStillByteCount, hex.EncodeToString(retained.CurrentRenderedStillSHA256))
}

func humanReadableImmutableOriginalImageFacts(outcome *mediawire.ImmutableOriginalImageFactsOutcome) string {
	if facts := outcome.GetFacts(); facts != nil {
		return fmt.Sprintf("Immutable original: %d × %d pixels, %d bytes, SHA-256 %s", facts.GetPixelWidth(), facts.GetPixelHeight(), facts.GetByteCount(), hex.EncodeToString(facts.GetSha256()))
	}
	if unavailable := outcome.GetUnavailable(); unavailable != nil {
		return strings.TrimSpace(unavailable.GetHumanDescription())
	}
	if failure := outcome.GetFailure(); failure != nil {
		return strings.TrimSpace(failure.GetHumanDescription())
	}
	return "Immutable original facts are unavailable."
}

func missingRetainedProductionOutput(nodeName ProductionNodeName, cause error) error {
	message := fmt.Errorf("%s has no retained output; run trawl update photos first", nodeName)
	return errors.Join(message, cause)
}

func inspectRetainedLocationNode(ctx context.Context, openedArchiveStore *store.Store, nodeName ProductionNodeName, asset archive.PhotoUpdateAsset) (string, string, error) {
	input, found, err := archive.LoadOptionalCaptureLocationInput(ctx, openedArchiveStore, string(asset.AssetID))
	if err != nil {
		return "", "", err
	}
	if !found {
		return "Capture location is absent.", "No location operation is required for this photo.", nil
	}
	knownPlaceConfigurationSHA256, err := archive.KnownPlaceConfigurationSHA256(ctx, openedArchiveStore)
	if err != nil {
		return "", "", err
	}
	known, knownFound, err := archive.LoadMatchConfiguredKnownPlaceOutcome(ctx, openedArchiveStore, input.GetAssetId())
	if err != nil {
		return "", "", err
	}
	if nodeName == ProductionNodeKnownPlace {
		if !knownFound {
			return "", "", missingRetainedProductionOutput(nodeName, nil)
		}
		return humanReadableCaptureLocation(input), humanReadableKnownPlaceOutcome(known), nil
	}
	if !knownFound {
		return "", "", missingRetainedProductionOutput(ProductionNodeKnownPlace, nil)
	}
	switch nodeName {
	case ProductionNodeAppleReverseGeocoding:
		outcome, retained, err := archive.LoadAppleReverseGeocodingEvidenceOutcome(ctx, openedArchiveStore, input.GetAssetId())
		if err != nil || !retained {
			return "", "", missingRetainedProductionOutput(nodeName, err)
		}
		return humanReadableCaptureLocation(input), humanReadableReverseGeocodingOutcome(outcome.GetExchange(), outcome.GetAddress()), nil
	case ProductionNodeAppleNearbyPlaces:
		outcome, retained, err := archive.LoadAppleNearbyPlaceEvidenceOutcome(ctx, openedArchiveStore, input.GetAssetId())
		if err != nil || !retained {
			return "", "", missingRetainedProductionOutput(nodeName, err)
		}
		return humanReadableKnownPlaceOutcome(known), humanReadableNearbyPlacesOutcome(outcome.GetExchange(), outcome.GetCandidates()), nil
	case ProductionNodeGeoapifyPhotographedPlaceCandidates:
		outcome, retained, err := archive.LoadGeoapifyPhotographedPlaceCandidateEvidenceOutcome(ctx, openedArchiveStore, input.GetAssetId())
		if err != nil || !retained {
			return "", "", missingRetainedProductionOutput(nodeName, err)
		}
		return humanReadableKnownPlaceOutcome(known), humanReadableNearbyPlacesOutcome(outcome.GetExchange(), outcome.GetCandidates()), nil
	case ProductionNodeComposeLocationEvidence:
		outcome, retained, err := archive.LoadCurrentPhotoLocationEvidence(ctx, openedArchiveStore, asset, knownPlaceConfigurationSHA256)
		if err != nil || !retained {
			return "", "", missingRetainedProductionOutput(nodeName, err)
		}
		readable, err := locationbriefing.Render(outcome)
		return humanReadableCaptureLocation(input), readable, err
	default:
		return "", "", fmt.Errorf("unknown Photos location production node %q", nodeName)
	}
}

func humanReadableCaptureLocation(input *locationwire.CaptureLocationInput) string {
	if input == nil || input.GetCoordinate() == nil {
		return "Capture location is absent."
	}
	parts := []string{fmt.Sprintf("Capture coordinate: %.6f, %.6f", input.GetCoordinate().GetLatitude(), input.GetCoordinate().GetLongitude())}
	if capturedAt := input.GetCaptureTime(); capturedAt != nil && capturedAt.IsValid() {
		parts = append(parts, "Captured: "+capturedAt.AsTime().Format(time.RFC3339))
	}
	return strings.Join(parts, "\n")
}

func humanReadableKnownPlaceOutcome(outcome *locationwire.MatchConfiguredKnownPlaceOutcome) string {
	if outcome == nil {
		return "No known-place outcome was produced."
	}
	lines := []string{"Outcome: " + humanReadableOperationState(outcome.GetState())}
	if len(outcome.GetMatches()) == 0 {
		return strings.Join(append(lines, "No configured known place matched the capture coordinate."), "\n")
	}
	for _, match := range outcome.GetMatches() {
		if match == nil {
			continue
		}
		kind := humanReadableEnumName(match.GetKind().String(), "CONFIGURED_KNOWN_PLACE_KIND_")
		relationship := humanReadableEnumName(match.GetRelationshipAtCapture().String(), "CONFIGURED_KNOWN_PLACE_RELATIONSHIP_AT_CAPTURE_")
		line := fmt.Sprintf("- %s (%s), %.0f m from capture", strings.TrimSpace(match.GetDisplayName()), kind, match.GetDistanceMeters())
		if relationship != "" {
			line += "; " + relationship
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func humanReadableReverseGeocodingOutcome(exchange *locationwire.ProviderExchange, address *locationwire.AddressHierarchy) string {
	lines := []string{"Outcome: " + humanReadableOperationState(exchange.GetState())}
	if failure := exchange.GetFailure(); failure != nil {
		lines = append(lines, "Failure: "+strings.TrimSpace(failure.GetDetail()))
	}
	if address == nil {
		return strings.Join(append(lines, "No address was returned."), "\n")
	}
	if hierarchy := humanReadableAddressHierarchy(address); hierarchy != "" {
		lines = append(lines, hierarchy)
	}
	return strings.Join(lines, "\n")
}

func humanReadableNearbyPlacesOutcome(exchange *locationwire.ProviderExchange, candidates []*locationwire.PlaceCandidate) string {
	lines := []string{"Outcome: " + humanReadableOperationState(exchange.GetState())}
	if failure := exchange.GetFailure(); failure != nil {
		lines = append(lines, "Failure: "+strings.TrimSpace(failure.GetDetail()))
	}
	if len(candidates) == 0 {
		return strings.Join(append(lines, "No nearby-place candidates were returned."), "\n")
	}
	lines = append(lines, fmt.Sprintf("Candidates: %d", len(candidates)))
	for position, candidate := range candidates {
		if candidate == nil {
			continue
		}
		name := strings.TrimSpace(candidate.GetName())
		if name == "" {
			name = "Unnamed place"
		}
		line := fmt.Sprintf("%d. %s — %.0f m from capture", position+1, name, candidate.GetDistanceMeters())
		if categories := compactHumanReadableValues(candidate.GetCategories()); len(categories) > 0 {
			line += " — " + strings.Join(categories, ", ")
		}
		if address := humanReadableAddressHierarchy(candidate.GetAddress()); address != "" {
			line += "\n   " + strings.ReplaceAll(address, "\n", "\n   ")
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func humanReadableAddressHierarchy(address *locationwire.AddressHierarchy) string {
	if address == nil {
		return ""
	}
	if formatted := strings.TrimSpace(address.GetFormatted()); formatted != "" {
		return formatted
	}
	streetAddress := strings.TrimSpace(strings.Join(compactHumanReadableValues([]string{address.GetHouseNumber(), address.GetStreet()}), " "))
	return strings.Join(compactHumanReadableValues([]string{
		address.GetName(), streetAddress, address.GetNeighbourhood(), address.GetDistrict(), address.GetCity(),
		address.GetCounty(), address.GetRegion(), address.GetPostcode(), address.GetCountry(),
	}), ", ")
}

func humanReadableOperationState(state locationwire.OperationState) string {
	return humanReadableEnumName(state.String(), "OPERATION_STATE_")
}

func humanReadableEnumName(value, prefix string) string {
	value = strings.TrimPrefix(value, prefix)
	value = strings.ReplaceAll(strings.ToLower(value), "_", " ")
	if value == "unspecified" {
		return ""
	}
	if value == "" {
		return ""
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

func compactHumanReadableValues(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func humanReadableSourceAsset(asset archive.PhotoUpdateAsset) string {
	dimensions := fmt.Sprintf("%d × %d pixels", asset.PixelWidth, asset.PixelHeight)
	return fmt.Sprintf("Current indexed %s; %s; source fingerprint %s", asset.MediaType, dimensions, asset.SourceFingerprint)
}
