package updatephotos

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/luna"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photocard"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/place"
	cardwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/card"
	locationwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/location"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type DebugNodeResult struct {
	NodeName ProductionNodeName
	Input    string
	Output   string
}

func DebugProductionNode(ctx context.Context, options Options, nodeName ProductionNodeName, asset archive.PhotoUpdateAsset, currentImageInspectionPath string) (DebugNodeResult, error) {
	if options.OpenedArchiveStore == nil {
		return DebugNodeResult{}, errors.New("Photos update archive store is required")
	}
	runner := &Runner{options: options, appleLocationMainThreadOperations: make(chan appleLocationMainThreadOperation)}
	worker := &photoAssetWorker{runner: runner}
	defer worker.closeLunaClient()
	node, found := productionNodeForName(nodeName)
	if !found || node.debugOperation == nil {
		return DebugNodeResult{}, fmt.Errorf("Photos production node %q cannot run through the per-photo dispatcher", nodeName)
	}
	result := DebugNodeResult{NodeName: nodeName}
	err := runDebugOperationOnAppleMainThreadDispatcher(ctx, runner, func() error {
		var operationErr error
		result.Input, result.Output, operationErr = node.debugOperation(ctx, runner, worker, asset, currentImageInspectionPath)
		return operationErr
	})
	return result, err
}

func debugMediaAccessNode(ctx context.Context, runner *Runner, _ *photoAssetWorker, _ archive.PhotoUpdateAsset, _ string) (string, string, error) {
	err := runner.ensurePhotoLibraryAccess(ctx)
	return "Installed OpenTrawl application and the current Apple Photos permission identity", "Apple Photos access is available through the installed OpenTrawl application.", err
}

func debugCurrentMediaNode(ctx context.Context, runner *Runner, _ *photoAssetWorker, asset archive.PhotoUpdateAsset, inspectionPath string) (string, string, error) {
	mediaEvidence, err := runner.acquireMediaEvidence(ctx, asset)
	if err != nil {
		return humanReadableSourceAsset(asset), "", err
	}
	defer func() { _ = mediaEvidence.CurrentRenderedStill.Close() }()
	if strings.TrimSpace(inspectionPath) != "" {
		if err := retainDebugCurrentImage(mediaEvidence, inspectionPath); err != nil {
			return humanReadableSourceAsset(asset), "", err
		}
	}
	return humanReadableSourceAsset(asset), humanReadableMediaEvidence(mediaEvidence, inspectionPath), nil
}

func debugLocationNodeOperation(nodeName ProductionNodeName) productionNodeDebugOperation {
	return func(ctx context.Context, runner *Runner, _ *photoAssetWorker, asset archive.PhotoUpdateAsset, _ string) (string, string, error) {
		return debugLocationNode(ctx, runner, nodeName, asset)
	}
}

func debugPhotoTextExtractionNode(ctx context.Context, runner *Runner, worker *photoAssetWorker, asset archive.PhotoUpdateAsset, inspectionPath string) (string, string, error) {
	return debugExtractPhotoText(ctx, runner, worker, asset, inspectionPath)
}

func debugPhotoTextVerificationNode(ctx context.Context, runner *Runner, worker *photoAssetWorker, asset archive.PhotoUpdateAsset, inspectionPath string) (string, string, error) {
	return debugVerifyPhotoText(ctx, runner, worker, asset, inspectionPath)
}

func debugPhotoCardNode(ctx context.Context, runner *Runner, worker *photoAssetWorker, asset archive.PhotoUpdateAsset, inspectionPath string) (string, string, error) {
	return debugBuildPhotoCard(ctx, runner, worker, asset, inspectionPath)
}

func debugLocationNode(ctx context.Context, runner *Runner, nodeName ProductionNodeName, asset archive.PhotoUpdateAsset) (string, string, error) {
	input, found, err := archive.LoadOptionalCaptureLocationInput(ctx, runner.options.OpenedArchiveStore, string(asset.AssetID))
	if err != nil {
		return "", "", err
	}
	if !found {
		return "Capture location is absent.", "No provider location work is required for this photo.", nil
	}
	knownPlaceConfigurationSHA256, err := archive.KnownPlaceConfigurationSHA256(ctx, runner.options.OpenedArchiveStore)
	if err != nil {
		return "", "", err
	}
	if nodeName == ProductionNodeKnownPlace {
		request := &locationwire.MatchConfiguredKnownPlaceRequest{Input: input, KnownPlaceConfigurationSha256: knownPlaceConfigurationSHA256}
		retainedBefore, retainedBeforeFound, loadErr := archive.LoadMatchConfiguredKnownPlaceOutcome(ctx, runner.options.OpenedArchiveStore, input.GetAssetId())
		if loadErr != nil {
			return "", "", loadErr
		}
		outcome, err := runner.matchConfiguredKnownPlace(ctx, input, knownPlaceConfigurationSHA256)
		return humanReadableCaptureLocation(input), reuseDescription(retainedBeforeFound && proto.Equal(retainedBefore.GetRequest(), request)) + "\n" + humanReadableKnownPlaceOutcome(outcome), err
	}
	if nodeName == ProductionNodeAppleReverseGeocoding {
		request := &locationwire.AcquireAppleReverseGeocodingEvidenceRequest{Input: input}
		retainedBefore, retainedBeforeFound, loadErr := archive.LoadAppleReverseGeocodingEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, input.GetAssetId())
		if loadErr != nil {
			return "", "", loadErr
		}
		outcome, err := runner.acquireAppleReverseGeocodingEvidence(ctx, input)
		reused := retainedBeforeFound && proto.Equal(retainedBefore.GetRequest(), request) && place.ProviderExchangeSatisfiesCurrentLocationEvidence(retainedBefore.GetExchange(), false)
		return humanReadableCaptureLocation(input), reuseDescription(reused) + "\n" + humanReadableReverseGeocodingOutcome(outcome.GetExchange(), outcome.GetAddress()), err
	}
	known, knownFound, err := archive.LoadMatchConfiguredKnownPlaceOutcome(ctx, runner.options.OpenedArchiveStore, string(asset.AssetID))
	if err != nil {
		return "", "", err
	}
	expectedKnownRequest := &locationwire.MatchConfiguredKnownPlaceRequest{Input: input, KnownPlaceConfigurationSha256: knownPlaceConfigurationSHA256}
	if !knownFound || !proto.Equal(known.GetRequest(), expectedKnownRequest) {
		return "", "", errors.New("location provider node needs retained known-place output for the current input")
	}
	switch nodeName {
	case ProductionNodeAppleNearbyPlaces:
		request := &locationwire.AcquireAppleNearbyPlaceEvidenceRequest{Input: input, RadiusMeters: appleNearbyPlaceRadiusMetres, MaximumCandidates: maximumAppleNearbyPlaceCandidates, KnownPlaceOutcome: known}
		retainedBefore, retainedBeforeFound, loadErr := archive.LoadAppleNearbyPlaceEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, input.GetAssetId())
		if loadErr != nil {
			return "", "", loadErr
		}
		outcome, err := runner.acquireAppleNearbyPlaceEvidence(ctx, input, known)
		reused := retainedBeforeFound && proto.Equal(retainedBefore.GetRequest(), request) && place.ProviderExchangeSatisfiesCurrentLocationEvidence(retainedBefore.GetExchange(), true)
		return humanReadableNearbyPlacesRequest(input, request.GetRadiusMeters(), request.GetMaximumCandidates(), known), reuseDescription(reused) + "\n" + humanReadableNearbyPlacesOutcome(outcome.GetExchange(), outcome.GetCandidates()), err
	case ProductionNodeGeoapifyPhotographedPlaceCandidates:
		request := geoapifyPhotographedPlaceCandidateEvidenceRequest(input, known)
		retainedBefore, retainedBeforeFound, loadErr := archive.LoadGeoapifyPhotographedPlaceCandidateEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, input.GetAssetId())
		if loadErr != nil {
			return "", "", loadErr
		}
		outcome, err := runner.acquireGeoapifyPhotographedPlaceCandidateEvidence(ctx, input, known)
		reused := retainedBeforeFound && proto.Equal(retainedBefore.GetRequest(), request) && place.ProviderExchangeSatisfiesCurrentLocationEvidence(retainedBefore.GetExchange(), true)
		return humanReadableGeoapifyPhotographedPlaceCandidateRequest(request), reuseDescription(reused) + "\n" + humanReadableNearbyPlacesOutcome(outcome.GetExchange(), outcome.GetCandidates()), err
	case ProductionNodeComposeLocationEvidence:
		appleReverse, appleReverseFound, err := archive.LoadAppleReverseGeocodingEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, string(asset.AssetID))
		if err != nil || !appleReverseFound {
			return "", "", errors.Join(errors.New("compose-location-evidence needs retained apple-reverse-geocoding output"), err)
		}
		appleNearby, appleNearbyFound, err := archive.LoadAppleNearbyPlaceEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, string(asset.AssetID))
		if err != nil || !appleNearbyFound {
			return "", "", errors.Join(errors.New("compose-location-evidence needs retained apple-nearby-places output"), err)
		}
		geoapifyPhotographedPlaceCandidates, geoapifyPhotographedPlaceCandidatesFound, err := archive.LoadGeoapifyPhotographedPlaceCandidateEvidenceOutcome(ctx, runner.options.OpenedArchiveStore, string(asset.AssetID))
		if err != nil || !geoapifyPhotographedPlaceCandidatesFound {
			return "", "", errors.Join(errors.New("compose-location-evidence needs retained geoapify-photographed-place-candidates output"), err)
		}
		if !place.ProviderExchangeSatisfiesCurrentLocationEvidence(appleReverse.GetExchange(), false) || !place.ProviderExchangeSatisfiesCurrentLocationEvidence(appleNearby.GetExchange(), true) || !place.ProviderExchangeSatisfiesCurrentLocationEvidence(geoapifyPhotographedPlaceCandidates.GetExchange(), true) {
			return "", "", errors.New("compose-location-evidence needs successful retained provider outputs")
		}
		composed, err := runner.composePhotoLocationEvidence(ctx, asset, knownPlaceConfigurationSHA256, known, appleReverse, appleNearby, geoapifyPhotographedPlaceCandidates)
		if err != nil {
			return "", "", err
		}
		readable, err := photocard.BuildHumanReadableLocationEvidence(composed)
		inputs := strings.Join([]string{
			"Known place\n" + humanReadableKnownPlaceOutcome(known),
			"Apple address\n" + humanReadableReverseGeocodingOutcome(appleReverse.GetExchange(), appleReverse.GetAddress()),
			"Apple nearby places\n" + humanReadableNearbyPlacesOutcome(appleNearby.GetExchange(), appleNearby.GetCandidates()),
			"Geoapify photographed-place candidate evidence\n" + humanReadableNearbyPlacesOutcome(geoapifyPhotographedPlaceCandidates.GetExchange(), geoapifyPhotographedPlaceCandidates.GetCandidates()),
		}, "\n\n")
		return inputs, readable.Text, err
	default:
		return "", "", fmt.Errorf("unknown location node %q", nodeName)
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

func humanReadableNearbyPlacesRequest(input *locationwire.CaptureLocationInput, radiusMeters float64, maximumCandidates int32, known *locationwire.MatchConfiguredKnownPlaceOutcome) string {
	return strings.Join([]string{
		humanReadableCaptureLocation(input),
		fmt.Sprintf("Search: up to %d nearby places within %.0f m", maximumCandidates, radiusMeters),
		"Known-place dependency:\n" + humanReadableKnownPlaceOutcome(known),
	}, "\n")
}

func humanReadableGeoapifyPhotographedPlaceCandidateRequest(request *locationwire.AcquireGeoapifyPhotographedPlaceCandidateEvidenceRequest) string {
	return strings.Join([]string{
		humanReadableCaptureLocation(request.GetInput()),
		fmt.Sprintf("Search: up to %d potential photographed places within %.0f m", request.GetMaximumCandidates(), request.GetRadiusMeters()),
		fmt.Sprintf("Require a provider-supplied name: %t", request.GetRequireNamedCandidates()),
		"Geoapify categories: " + strings.Join(place.GeoapifyProviderCategoryNames(request.GetCategories()), ", "),
		"Known-place dependency:\n" + humanReadableKnownPlaceOutcome(request.GetKnownPlaceOutcome()),
	}, "\n")
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

func reuseDescription(reused bool) string {
	if reused {
		return "Execution: reused the matching retained production output; no provider or model request was sent."
	}
	return "Execution: acquired and retained the production output for this input."
}

func runDebugOperationOnAppleMainThreadDispatcher(ctx context.Context, runner *Runner, operation func() error) error {
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

func debugExtractPhotoText(ctx context.Context, runner *Runner, worker *photoAssetWorker, asset archive.PhotoUpdateAsset, inspectionPath string) (string, string, error) {
	mediaEvidence, err := runner.acquireMediaEvidence(ctx, asset)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = mediaEvidence.CurrentRenderedStill.Close() }()
	if inspectionPath != "" {
		if err := retainDebugCurrentImage(mediaEvidence, inspectionPath); err != nil {
			return "", "", err
		}
	}
	_, retainedBeforeErr := loadRetainedExtractedPhotoText(ctx, runner, asset, mediaEvidence)
	text, err := worker.extractPhotoText(ctx, asset, mediaEvidence)
	return humanReadableMediaEvidence(mediaEvidence, inspectionPath), reuseDescription(retainedBeforeErr == nil) + "\n" + humanReadablePhotoText(text), err
}

func debugVerifyPhotoText(ctx context.Context, runner *Runner, worker *photoAssetWorker, asset archive.PhotoUpdateAsset, inspectionPath string) (string, string, error) {
	mediaEvidence, err := runner.acquireMediaEvidence(ctx, asset)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = mediaEvidence.CurrentRenderedStill.Close() }()
	if inspectionPath != "" {
		if err := retainDebugCurrentImage(mediaEvidence, inspectionPath); err != nil {
			return "", "", err
		}
	}
	extracted, err := loadRetainedExtractedPhotoText(ctx, runner, asset, mediaEvidence)
	if err != nil {
		return "", "", fmt.Errorf("photo-text-verification needs retained photo-text-extraction: %w", err)
	}
	_, _, retainedBeforeErr := loadRetainedVerifiedPhotoText(ctx, runner, asset, mediaEvidence, extracted)
	verified, err := worker.verifyPhotoText(ctx, asset, mediaEvidence, extracted)
	if err != nil {
		return humanReadablePhotoText(extracted), reuseDescription(retainedBeforeErr == nil), err
	}
	_, verification, err := loadRetainedVerifiedPhotoText(ctx, runner, asset, mediaEvidence, extracted)
	return humanReadablePhotoText(extracted), reuseDescription(retainedBeforeErr == nil) + "\n" + humanReadablePhotoTextVerification(verification, verified), err
}

func debugBuildPhotoCard(ctx context.Context, runner *Runner, worker *photoAssetWorker, asset archive.PhotoUpdateAsset, inspectionPath string) (string, string, error) {
	mediaEvidence, err := runner.acquireMediaEvidence(ctx, asset)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = mediaEvidence.CurrentRenderedStill.Close() }()
	if inspectionPath != "" {
		if err := retainDebugCurrentImage(mediaEvidence, inspectionPath); err != nil {
			return "", "", err
		}
	}
	extracted, err := loadRetainedExtractedPhotoText(ctx, runner, asset, mediaEvidence)
	if err != nil {
		return "", "", fmt.Errorf("photo-card needs retained photo-text-extraction: %w", err)
	}
	verified, _, err := loadRetainedVerifiedPhotoText(ctx, runner, asset, mediaEvidence, extracted)
	if err != nil {
		return "", "", fmt.Errorf("photo-card needs retained photo-text-verification: %w", err)
	}
	knownPlaceConfigurationSHA256, err := archive.KnownPlaceConfigurationSHA256(ctx, runner.options.OpenedArchiveStore)
	if err != nil {
		return "", "", err
	}
	locationOutcome, locationFound, err := archive.LoadCurrentPhotoLocationEvidence(ctx, runner.options.OpenedArchiveStore, asset, knownPlaceConfigurationSHA256)
	if err != nil {
		return "", "", err
	}
	_, hasCaptureLocation, err := archive.LoadOptionalCaptureLocationInput(ctx, runner.options.OpenedArchiveStore, string(asset.AssetID))
	if err != nil {
		return "", "", err
	}
	if hasCaptureLocation && !locationFound {
		return "", "", errors.New("photo-card needs retained location-evidence")
	}
	locationEvidence, err := photocard.BuildHumanReadableLocationEvidence(locationOutcome)
	if err != nil {
		return "", "", err
	}
	checkedEvidence := buildHumanReadablePhotoEvidence(asset, mediaEvidence.ImmutableOriginalFacts, mediaEvidence.CurrentRenderedStill.Outcome, locationEvidence.Text)
	retainedBefore := matchingRetainedPhotoCardGenerationExists(ctx, runner, asset, mediaEvidence, verified, locationOutcome, locationEvidence.SuppliedCandidates, checkedEvidence)
	card, inputSHA256, locationSHA256, err := worker.generatePhotoCard(ctx, asset, mediaEvidence, verified, locationOutcome, locationEvidence, checkedEvidence)
	if err != nil {
		return "", "", err
	}
	if err := archive.StoreCurrentPhotoCard(ctx, runner.options.OpenedArchiveStore, asset, inputSHA256, mediaEvidence.CurrentRenderedStill.Outcome.GetSha256(), locationSHA256, locationOutcome, card, time.Now()); err != nil {
		return "", "", err
	}
	input := humanReadableMediaEvidence(mediaEvidence, inspectionPath) + "\n\nVerified visible text:\n" + humanReadablePhotoText(verified) + "\n\n" + locationEvidence.Text
	return input, reuseDescription(retainedBefore) + "\n" + humanReadablePhotoCard(card), nil
}

func matchingRetainedPhotoCardGenerationExists(ctx context.Context, runner *Runner, asset archive.PhotoUpdateAsset, mediaEvidence acquiredMediaEvidence, verified *cardwire.PhotoOpticalCharacterRecognition, locationOutcome *locationwire.ComposePhotoLocationEvidenceOutcome, suppliedCandidates []photocard.SuppliedPhotographedPlaceCandidate, checkedEvidence string) bool {
	locationBytes, err := proto.Marshal(locationOutcome)
	if err != nil {
		return false
	}
	locationDigest := sha256.Sum256(locationBytes)
	instructions, err := photocard.BuildPhotoCardInstructions(checkedEvidence, verified)
	if err != nil {
		return false
	}
	schemaJSON, err := photocard.PhotoCardSemanticSectionsStructuredOutputSchemaJSON(suppliedCandidates)
	if err != nil {
		return false
	}
	verifiedBytes, err := proto.Marshal(verified)
	if err != nil {
		return false
	}
	verifiedDigest := sha256.Sum256(verifiedBytes)
	inputSHA256 := photoCardDerivationInputs{
		SourceFingerprint:                 asset.SourceFingerprint,
		CurrentRenderedStillSHA256:        mediaEvidence.CurrentRenderedStill.Outcome.GetSha256(),
		ImmutableOriginalImageFactsSHA256: mediaEvidence.ImmutableOriginalFacts.GetSha256(),
		VerifiedPhotoTextSHA256:           verifiedDigest[:],
		LocationEvidenceSHA256:            locationDigest[:],
		HumanReadableInstructions:         instructions,
		StructuredOutputSchemaJSON:        schemaJSON,
		ModelIdentifier:                   luna.ModelGPT56Luna,
	}.SHA256()
	retained, found, err := archive.LoadRetainedPhotoCardGeneration(ctx, runner.options.OpenedArchiveStore, asset.AssetID, inputSHA256)
	return err == nil && found && len(retained.ResponseBody) > 0 && !retained.ResponseRejected
}

func loadRetainedExtractedPhotoText(ctx context.Context, runner *Runner, asset archive.PhotoUpdateAsset, mediaEvidence acquiredMediaEvidence) (*cardwire.PhotoOpticalCharacterRecognition, error) {
	instructions := photocard.BuildPhotoTextExtractionInstructions()
	schemaJSON, err := photocard.PhotoTextStructuredOutputSchemaJSON()
	if err != nil {
		return nil, err
	}
	inputSHA256 := photoTextDerivationInputs{
		CurrentRenderedStillSHA256: mediaEvidence.CurrentRenderedStill.Outcome.GetSha256(),
		HumanReadableInstructions:  instructions,
		StructuredOutputSchemaJSON: schemaJSON,
		ModelIdentifier:            luna.ModelGPT56Luna,
	}.SHA256()
	retained, found, err := archive.LoadRetainedPhotoTextExtraction(ctx, runner.options.OpenedArchiveStore, asset.AssetID, inputSHA256)
	if err != nil {
		return nil, err
	}
	if !found || len(retained.ResponseBody) == 0 || retained.ResponseRejected {
		return nil, errors.New("a valid retained extraction for the current image is absent")
	}
	extracted := new(cardwire.PhotoOpticalCharacterRecognition)
	if err := protojson.Unmarshal(retained.ResponseBody, extracted); err != nil {
		return nil, err
	}
	if err := photocard.ValidateExtractedPhotoText(extracted); err != nil {
		return nil, err
	}
	return extracted, nil
}

func loadRetainedVerifiedPhotoText(ctx context.Context, runner *Runner, asset archive.PhotoUpdateAsset, mediaEvidence acquiredMediaEvidence, extracted *cardwire.PhotoOpticalCharacterRecognition) (*cardwire.PhotoOpticalCharacterRecognition, *cardwire.PhotoOpticalCharacterRecognitionVerification, error) {
	instructions, err := photocard.BuildPhotoTextVerificationInstructions(extracted)
	if err != nil {
		return nil, nil, err
	}
	schemaJSON, err := photocard.PhotoTextVerificationStructuredOutputSchemaJSON()
	if err != nil {
		return nil, nil, err
	}
	extractedBytes, err := proto.Marshal(extracted)
	if err != nil {
		return nil, nil, err
	}
	extractedDigest := sha256.Sum256(extractedBytes)
	inputSHA256 := photoTextVerificationDerivationInputs{
		CurrentRenderedStillSHA256: mediaEvidence.CurrentRenderedStill.Outcome.GetSha256(),
		ExtractedPhotoTextSHA256:   extractedDigest[:],
		HumanReadableInstructions:  instructions,
		StructuredOutputSchemaJSON: schemaJSON,
		ModelIdentifier:            luna.ModelGPT56Luna,
	}.SHA256()
	retained, found, err := archive.LoadRetainedPhotoTextVerification(ctx, runner.options.OpenedArchiveStore, asset.AssetID, inputSHA256)
	if err != nil {
		return nil, nil, err
	}
	if !found || len(retained.ResponseBody) == 0 || retained.ResponseRejected {
		return nil, nil, errors.New("a valid retained verification for the current extraction is absent")
	}
	verification := new(cardwire.PhotoOpticalCharacterRecognitionVerification)
	if err := protojson.Unmarshal(retained.ResponseBody, verification); err != nil {
		return nil, nil, err
	}
	verified, err := photocard.ApplyPhotoTextVerification(extracted, verification)
	return verified, verification, err
}

func retainDebugCurrentImage(mediaEvidence acquiredMediaEvidence, destination string) error {
	imageBytes, err := mediaEvidence.CurrentRenderedStill.Read()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return fmt.Errorf("create Photos debug image directory: %w", err)
	}
	if err := os.WriteFile(destination, imageBytes, 0o600); err != nil {
		return fmt.Errorf("retain current image for inspection: %w", err)
	}
	return nil
}

func humanReadableSourceAsset(asset archive.PhotoUpdateAsset) string {
	dimensions := fmt.Sprintf("%d × %d pixels", asset.PixelWidth, asset.PixelHeight)
	return fmt.Sprintf("Current indexed %s; %s; source fingerprint %s", asset.MediaType, dimensions, asset.SourceFingerprint)
}

func humanReadableMediaEvidence(mediaEvidence acquiredMediaEvidence, inspectionPath string) string {
	current := mediaEvidence.CurrentRenderedStill.Outcome
	parts := []string{
		fmt.Sprintf("Current rendered image: %d × %d pixels, %d bytes, SHA-256 %s", current.GetPixelWidth(), current.GetPixelHeight(), current.GetByteCount(), hex.EncodeToString(current.GetSha256())),
		fmt.Sprintf("Immutable original: %d × %d pixels, %d bytes, SHA-256 %s", mediaEvidence.ImmutableOriginalFacts.GetPixelWidth(), mediaEvidence.ImmutableOriginalFacts.GetPixelHeight(), mediaEvidence.ImmutableOriginalFacts.GetByteCount(), hex.EncodeToString(mediaEvidence.ImmutableOriginalFacts.GetSha256())),
	}
	if inspectionPath != "" {
		parts = append(parts, "Inspectable current image: "+inspectionPath)
	}
	return strings.Join(parts, "\n")
}

func humanReadablePhotoText(recognition *cardwire.PhotoOpticalCharacterRecognition) string {
	if recognition == nil || len(recognition.GetRegionsInReadingOrder()) == 0 {
		return "No useful visible text was found."
	}
	var rendered strings.Builder
	for regionIndex, region := range recognition.GetRegionsInReadingOrder() {
		fmt.Fprintf(&rendered, "Region %d — %s\n", regionIndex+1, strings.TrimSpace(region.GetVisibleSource()))
		for lineIndex, line := range region.GetLinesInReadingOrder() {
			legibility := humanReadableEnumName(line.GetLegibility().String(), "OPTICAL_CHARACTER_RECOGNITION_LEGIBILITY_")
			fmt.Fprintf(&rendered, "  Line %d: %s [%s]", lineIndex+1, strings.TrimSpace(line.GetTranscribedText()), legibility)
			if len(line.GetLanguages()) > 0 {
				fmt.Fprintf(&rendered, " — languages: %s", strings.Join(line.GetLanguages(), ", "))
			}
			rendered.WriteByte('\n')
		}
	}
	return strings.TrimSpace(rendered.String())
}

func humanReadablePhotoTextVerification(verification *cardwire.PhotoOpticalCharacterRecognitionVerification, verified *cardwire.PhotoOpticalCharacterRecognition) string {
	if verification == nil {
		return "No typed verification outcome was retained."
	}
	var rendered strings.Builder
	state := humanReadableEnumName(verification.GetState().String(), "PHOTO_OPTICAL_CHARACTER_RECOGNITION_VERIFICATION_STATE_")
	fmt.Fprintf(&rendered, "Decision: %s", state)
	if verification.GetState() == cardwire.PhotoOpticalCharacterRecognitionVerificationState_PHOTO_OPTICAL_CHARACTER_RECOGNITION_VERIFICATION_STATE_VERIFIED {
		rendered.WriteString(" without changes.\n")
	} else {
		rendered.WriteString(".\n")
	}
	for _, replacement := range verification.GetLineReplacements() {
		fmt.Fprintf(&rendered, "- Replaced region %d, line %d: %q → %q\n", replacement.GetRetainedRegionIndex(), replacement.GetRetainedLineIndex(), strings.TrimSpace(replacement.GetExpectedRetainedText()), strings.TrimSpace(replacement.GetReplacementLine().GetTranscribedText()))
	}
	for _, removal := range verification.GetLineRemovals() {
		fmt.Fprintf(&rendered, "- Removed region %d, line %d: %q\n", removal.GetRetainedRegionIndex(), removal.GetRetainedLineIndex(), strings.TrimSpace(removal.GetExpectedRetainedText()))
	}
	for _, insertion := range verification.GetLineInsertions() {
		insertedText := make([]string, 0, len(insertion.GetInsertedLinesInReadingOrder()))
		for _, line := range insertion.GetInsertedLinesInReadingOrder() {
			insertedText = append(insertedText, strings.TrimSpace(line.GetTranscribedText()))
		}
		fmt.Fprintf(&rendered, "- Inserted in region %d after retained line %d: %s\n", insertion.GetRetainedRegionIndex(), insertion.GetInsertAfterRetainedLineIndex(), strings.Join(insertedText, " | "))
	}
	for _, insertion := range verification.GetRegionInsertions() {
		fmt.Fprintf(&rendered, "- Inserted %d region(s) after retained region %d.\n", len(insertion.GetInsertedRegionsInReadingOrder()), insertion.GetInsertAfterRetainedRegionIndex())
	}
	fmt.Fprintf(&rendered, "\nVerified visible text\n%s", humanReadablePhotoText(verified))
	return strings.TrimSpace(rendered.String())
}

func humanReadablePhotoCard(card *cardwire.PhotoCard) string {
	if card == nil {
		return "No PhotoCard was produced."
	}
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "Concise description\n%s\n\nDetailed description\n%s\n", strings.TrimSpace(card.GetDescriptions().GetConciseDescription()), strings.TrimSpace(card.GetDescriptions().GetDetailedDescription()))
	if subject := card.GetPrimaryDepictedSubject(); subject != nil {
		fmt.Fprintf(&rendered, "\nPrimary depicted subject\n%s\nEvidence: %s\n", strings.TrimSpace(subject.GetHumanName()), strings.TrimSpace(subject.GetVisualEvidence()))
	}
	if visible := card.GetVisibleContent(); visible != nil {
		fmt.Fprintf(&rendered, "\nVisible content\nScene: %s\n", strings.TrimSpace(visible.GetScene()))
		if objects := visible.GetImportantObjects(); len(objects) > 0 {
			fmt.Fprintf(&rendered, "Important objects: %s\n", strings.Join(objects, "; "))
		}
		if actions := visible.GetVisibleActions(); len(actions) > 0 {
			fmt.Fprintf(&rendered, "Visible actions: %s\n", strings.Join(actions, "; "))
		}
	}
	fmt.Fprintf(&rendered, "\nVisible text\n%s\n", humanReadablePhotoText(card.GetOpticalCharacterRecognition()))
	if photographedPlace := card.GetPhotographedPlace(); photographedPlace != nil {
		certainty := humanReadableEnumName(photographedPlace.GetCertainty().String(), "PHOTOGRAPHED_PLACE_CERTAINTY_")
		fmt.Fprintf(&rendered, "\nPhotographed place\nCertainty: %s\n", certainty)
		for _, selected := range photographedPlace.GetSelectedSuppliedCandidates() {
			fmt.Fprintf(&rendered, "Selected supplied place: %s\nEvidence: %s\n", strings.TrimSpace(selected.GetHumanName()), strings.TrimSpace(selected.GetEvidence()))
		}
		for _, inferred := range photographedPlace.GetImageInferredPlaces() {
			fmt.Fprintf(&rendered, "Image-inferred place: %s\nEvidence: %s\n", strings.TrimSpace(inferred.GetHumanName()), strings.TrimSpace(inferred.GetEvidence()))
		}
		if explanation := strings.TrimSpace(photographedPlace.GetExplanation()); explanation != "" {
			fmt.Fprintf(&rendered, "Explanation: %s\n", explanation)
		}
	}
	if facts := card.GetSearchableFacts(); len(facts) > 0 {
		fmt.Fprintf(&rendered, "\nSearchable facts\n- %s\n", strings.Join(facts, "\n- "))
	}
	if uncertainties := card.GetUncertainties(); len(uncertainties) > 0 {
		rendered.WriteString("\nMaterial uncertainties\n")
		for _, uncertainty := range uncertainties {
			fmt.Fprintf(&rendered, "- %s: %s\n", strings.TrimSpace(uncertainty.GetSubject()), strings.TrimSpace(uncertainty.GetExplanation()))
		}
	}
	return strings.TrimSpace(rendered.String())
}
