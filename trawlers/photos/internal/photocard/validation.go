package photocard

import (
	"errors"
	"fmt"
	"strings"

	cardwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/card"
	"google.golang.org/protobuf/proto"
)

type SuppliedPhotographedPlaceCandidate struct {
	Identifier string
	HumanName  string
}

func ValidateModelResult(card *cardwire.PhotoCard, suppliedCandidates []SuppliedPhotographedPlaceCandidate) error {
	if card == nil {
		return errors.New("PhotoCard is required")
	}
	if err := validateDescriptions(card.Descriptions); err != nil {
		return err
	}
	return validateNonDescriptionSections(card, suppliedCandidates)
}

func ValidateExtractedPhotoText(recognition *cardwire.PhotoOpticalCharacterRecognition) error {
	if recognition == nil {
		return errors.New("extracted photo text is required")
	}
	return validateOpticalCharacterRecognition(recognition)
}

func ApplyPhotoTextVerification(extractedPhotoText *cardwire.PhotoOpticalCharacterRecognition, verification *cardwire.PhotoOpticalCharacterRecognitionVerification) (*cardwire.PhotoOpticalCharacterRecognition, error) {
	if err := ValidateExtractedPhotoText(extractedPhotoText); err != nil {
		return nil, err
	}
	return applyPhotoOpticalCharacterRecognitionVerification(extractedPhotoText, verification)
}

func ComposePhotoCard(verifiedPhotoText *cardwire.PhotoOpticalCharacterRecognition, semanticSections *cardwire.PhotoCardSemanticSections, suppliedCandidates []SuppliedPhotographedPlaceCandidate) (*cardwire.PhotoCard, error) {
	if err := ValidateExtractedPhotoText(verifiedPhotoText); err != nil {
		return nil, err
	}
	if semanticSections == nil {
		return nil, errors.New("PhotoCard semantic sections are required")
	}
	photographedPlace, err := composePhotographedPlaceJudgement(semanticSections.PhotographedPlace, suppliedCandidates)
	if err != nil {
		return nil, err
	}
	card := &cardwire.PhotoCard{
		Descriptions:                semanticSections.Descriptions,
		PrimaryDepictedSubject:      semanticSections.PrimaryDepictedSubject,
		VisibleContent:              semanticSections.VisibleContent,
		OpticalCharacterRecognition: verifiedPhotoText,
		PhotographedPlace:           photographedPlace,
		SearchableFacts:             append([]string(nil), semanticSections.SearchableFacts...),
		Uncertainties:               clonePhotoCardUncertainties(semanticSections.Uncertainties),
	}
	return card, nil
}

type retainedOpticalCharacterRecognitionLinePosition struct {
	regionIndex uint32
	lineIndex   uint32
}

func applyPhotoOpticalCharacterRecognitionVerification(recognition *cardwire.PhotoOpticalCharacterRecognition, verification *cardwire.PhotoOpticalCharacterRecognitionVerification) (*cardwire.PhotoOpticalCharacterRecognition, error) {
	if verification == nil {
		return nil, errors.New("PhotoCard OCR verification is required")
	}
	editCount := len(verification.LineReplacements) + len(verification.LineRemovals) + len(verification.LineInsertions) + len(verification.RegionInsertions)
	switch verification.State {
	case cardwire.PhotoOpticalCharacterRecognitionVerificationState_PHOTO_OPTICAL_CHARACTER_RECOGNITION_VERIFICATION_STATE_VERIFIED:
		if editCount != 0 {
			return nil, errors.New("verified PhotoCard OCR cannot include corrections")
		}
	case cardwire.PhotoOpticalCharacterRecognitionVerificationState_PHOTO_OPTICAL_CHARACTER_RECOGNITION_VERIFICATION_STATE_CORRECTED:
		if editCount == 0 {
			return nil, errors.New("corrected PhotoCard OCR requires at least one correction")
		}
	default:
		return nil, errors.New("PhotoCard OCR verification requires VERIFIED or CORRECTED state")
	}

	replacements := make(map[retainedOpticalCharacterRecognitionLinePosition]*cardwire.OpticalCharacterRecognitionLineReplacement, len(verification.LineReplacements))
	removed := make(map[retainedOpticalCharacterRecognitionLinePosition]struct{}, len(verification.LineRemovals))
	claimedLines := make(map[retainedOpticalCharacterRecognitionLinePosition]struct{}, len(verification.LineReplacements)+len(verification.LineRemovals))
	for index, replacement := range verification.LineReplacements {
		if replacement == nil {
			return nil, fmt.Errorf("PhotoCard OCR line replacement %d is missing", index+1)
		}
		position, retainedLine, err := resolveRetainedOpticalCharacterRecognitionLine(recognition, replacement.RetainedRegionIndex, replacement.RetainedLineIndex)
		if err != nil {
			return nil, fmt.Errorf("PhotoCard OCR line replacement %d: %w", index+1, err)
		}
		if _, duplicate := claimedLines[position]; duplicate {
			return nil, fmt.Errorf("PhotoCard OCR line replacement %d repeats an edited retained line", index+1)
		}
		if replacement.ExpectedRetainedText != retainedLine.TranscribedText {
			return nil, fmt.Errorf("PhotoCard OCR line replacement %d expected retained text does not match", index+1)
		}
		if err := validateOpticalCharacterRecognitionLine(replacement.ReplacementLine, "PhotoCard OCR line replacement", index+1); err != nil {
			return nil, err
		}
		claimedLines[position] = struct{}{}
		replacements[position] = replacement
	}
	for index, removal := range verification.LineRemovals {
		if removal == nil {
			return nil, fmt.Errorf("PhotoCard OCR line removal %d is missing", index+1)
		}
		position, retainedLine, err := resolveRetainedOpticalCharacterRecognitionLine(recognition, removal.RetainedRegionIndex, removal.RetainedLineIndex)
		if err != nil {
			return nil, fmt.Errorf("PhotoCard OCR line removal %d: %w", index+1, err)
		}
		if _, duplicate := claimedLines[position]; duplicate {
			return nil, fmt.Errorf("PhotoCard OCR line removal %d repeats an edited retained line", index+1)
		}
		if removal.ExpectedRetainedText != retainedLine.TranscribedText {
			return nil, fmt.Errorf("PhotoCard OCR line removal %d expected retained text does not match", index+1)
		}
		claimedLines[position] = struct{}{}
		removed[position] = struct{}{}
	}

	lineInsertions := make(map[retainedOpticalCharacterRecognitionLinePosition][]*cardwire.OpticalCharacterRecognitionLine, len(verification.LineInsertions))
	for index, insertion := range verification.LineInsertions {
		if insertion == nil || insertion.RetainedRegionIndex == 0 || int(insertion.RetainedRegionIndex) > len(recognition.RegionsInReadingOrder) {
			return nil, fmt.Errorf("PhotoCard OCR line insertion %d has an invalid retained region index", index+1)
		}
		retainedLineCount := len(recognition.RegionsInReadingOrder[insertion.RetainedRegionIndex-1].LinesInReadingOrder)
		if int(insertion.InsertAfterRetainedLineIndex) > retainedLineCount {
			return nil, fmt.Errorf("PhotoCard OCR line insertion %d has an invalid reading-order position", index+1)
		}
		position := retainedOpticalCharacterRecognitionLinePosition{regionIndex: insertion.RetainedRegionIndex, lineIndex: insertion.InsertAfterRetainedLineIndex}
		if _, duplicate := lineInsertions[position]; duplicate {
			return nil, fmt.Errorf("PhotoCard OCR line insertion %d repeats an insertion position", index+1)
		}
		if len(insertion.InsertedLinesInReadingOrder) == 0 {
			return nil, fmt.Errorf("PhotoCard OCR line insertion %d requires at least one inserted line", index+1)
		}
		for insertedLineIndex, insertedLine := range insertion.InsertedLinesInReadingOrder {
			if err := validateOpticalCharacterRecognitionLine(insertedLine, fmt.Sprintf("PhotoCard OCR line insertion %d inserted line", index+1), insertedLineIndex+1); err != nil {
				return nil, err
			}
		}
		lineInsertions[position] = insertion.InsertedLinesInReadingOrder
	}

	regionInsertions := make(map[uint32][]*cardwire.OpticalCharacterRecognitionRegion, len(verification.RegionInsertions))
	for index, insertion := range verification.RegionInsertions {
		if insertion == nil || int(insertion.InsertAfterRetainedRegionIndex) > len(recognition.RegionsInReadingOrder) {
			return nil, fmt.Errorf("PhotoCard OCR region insertion %d has an invalid reading-order position", index+1)
		}
		if _, duplicate := regionInsertions[insertion.InsertAfterRetainedRegionIndex]; duplicate {
			return nil, fmt.Errorf("PhotoCard OCR region insertion %d repeats an insertion position", index+1)
		}
		if len(insertion.InsertedRegionsInReadingOrder) == 0 {
			return nil, fmt.Errorf("PhotoCard OCR region insertion %d requires at least one inserted region", index+1)
		}
		for insertedRegionIndex, insertedRegion := range insertion.InsertedRegionsInReadingOrder {
			if err := validateOpticalCharacterRecognitionRegion(insertedRegion, fmt.Sprintf("PhotoCard OCR region insertion %d inserted region", index+1), insertedRegionIndex+1); err != nil {
				return nil, err
			}
		}
		regionInsertions[insertion.InsertAfterRetainedRegionIndex] = insertion.InsertedRegionsInReadingOrder
	}

	corrected := proto.Clone(recognition).(*cardwire.PhotoOpticalCharacterRecognition)
	corrected.RegionsInReadingOrder = make([]*cardwire.OpticalCharacterRecognitionRegion, 0, len(recognition.RegionsInReadingOrder)+len(regionInsertions))
	appendInsertedRegions := func(position uint32) {
		for _, inserted := range regionInsertions[position] {
			corrected.RegionsInReadingOrder = append(corrected.RegionsInReadingOrder, proto.Clone(inserted).(*cardwire.OpticalCharacterRecognitionRegion))
		}
	}
	appendInsertedRegions(0)
	for regionOffset, retainedRegion := range recognition.RegionsInReadingOrder {
		regionPosition := uint32(regionOffset + 1)
		correctedRegion := proto.Clone(retainedRegion).(*cardwire.OpticalCharacterRecognitionRegion)
		correctedRegion.LinesInReadingOrder = make([]*cardwire.OpticalCharacterRecognitionLine, 0, len(retainedRegion.LinesInReadingOrder)+len(verification.LineInsertions))
		for _, inserted := range lineInsertions[retainedOpticalCharacterRecognitionLinePosition{regionIndex: regionPosition, lineIndex: 0}] {
			correctedRegion.LinesInReadingOrder = append(correctedRegion.LinesInReadingOrder, proto.Clone(inserted).(*cardwire.OpticalCharacterRecognitionLine))
		}
		for lineOffset, retainedLine := range retainedRegion.LinesInReadingOrder {
			linePosition := retainedOpticalCharacterRecognitionLinePosition{regionIndex: regionPosition, lineIndex: uint32(lineOffset + 1)}
			if replacement := replacements[linePosition]; replacement != nil {
				correctedRegion.LinesInReadingOrder = append(correctedRegion.LinesInReadingOrder, proto.Clone(replacement.ReplacementLine).(*cardwire.OpticalCharacterRecognitionLine))
			} else if _, remove := removed[linePosition]; !remove {
				correctedRegion.LinesInReadingOrder = append(correctedRegion.LinesInReadingOrder, proto.Clone(retainedLine).(*cardwire.OpticalCharacterRecognitionLine))
			}
			for _, inserted := range lineInsertions[linePosition] {
				correctedRegion.LinesInReadingOrder = append(correctedRegion.LinesInReadingOrder, proto.Clone(inserted).(*cardwire.OpticalCharacterRecognitionLine))
			}
		}
		corrected.RegionsInReadingOrder = append(corrected.RegionsInReadingOrder, correctedRegion)
		appendInsertedRegions(regionPosition)
	}
	if err := validateOpticalCharacterRecognition(corrected); err != nil {
		return nil, fmt.Errorf("validate corrected PhotoCard OCR: %w", err)
	}
	return corrected, nil
}

func resolveRetainedOpticalCharacterRecognitionLine(recognition *cardwire.PhotoOpticalCharacterRecognition, regionIndex, lineIndex uint32) (retainedOpticalCharacterRecognitionLinePosition, *cardwire.OpticalCharacterRecognitionLine, error) {
	if regionIndex == 0 || int(regionIndex) > len(recognition.RegionsInReadingOrder) {
		return retainedOpticalCharacterRecognitionLinePosition{}, nil, errors.New("retained region index is invalid")
	}
	region := recognition.RegionsInReadingOrder[regionIndex-1]
	if lineIndex == 0 || int(lineIndex) > len(region.LinesInReadingOrder) {
		return retainedOpticalCharacterRecognitionLinePosition{}, nil, errors.New("retained line index is invalid")
	}
	position := retainedOpticalCharacterRecognitionLinePosition{regionIndex: regionIndex, lineIndex: lineIndex}
	return position, region.LinesInReadingOrder[lineIndex-1], nil
}

func validateOpticalCharacterRecognitionRegion(region *cardwire.OpticalCharacterRecognitionRegion, label string, index int) error {
	if region == nil || strings.TrimSpace(region.VisibleSource) == "" {
		return fmt.Errorf("%s %d requires a visible source", label, index)
	}
	if len(region.LinesInReadingOrder) == 0 {
		return fmt.Errorf("%s %d requires at least one line", label, index)
	}
	for lineIndex, line := range region.LinesInReadingOrder {
		if err := validateOpticalCharacterRecognitionLine(line, fmt.Sprintf("%s %d line", label, index), lineIndex+1); err != nil {
			return err
		}
	}
	return nil
}

func validateOpticalCharacterRecognitionLine(line *cardwire.OpticalCharacterRecognitionLine, label string, index int) error {
	if line == nil || strings.TrimSpace(line.TranscribedText) == "" {
		return fmt.Errorf("%s %d requires transcribed text", label, index)
	}
	if err := validateNonblankStrings(fmt.Sprintf("%s %d language", label, index), line.Languages); err != nil {
		return err
	}
	if line.Legibility == cardwire.OpticalCharacterRecognitionLegibility_OPTICAL_CHARACTER_RECOGNITION_LEGIBILITY_UNSPECIFIED {
		return fmt.Errorf("%s %d requires nonzero legibility", label, index)
	}
	return nil
}

func composePhotographedPlaceJudgement(semanticJudgement *cardwire.SemanticPhotographedPlaceJudgement, suppliedCandidates []SuppliedPhotographedPlaceCandidate) (*cardwire.PhotographedPlaceJudgement, error) {
	if semanticJudgement == nil {
		return nil, errors.New("PhotoCard photographed-place judgement is required")
	}
	suppliedNameByIdentifier, err := suppliedPhotographedPlaceNamesByIdentifier(suppliedCandidates)
	if err != nil {
		return nil, err
	}
	suppliedIdentifiersByNormalisedHumanName := suppliedPhotographedPlaceIdentifiersByNormalisedHumanName(suppliedCandidates)
	selectedCandidates := make([]*cardwire.SuppliedPhotographedPlaceCandidate, len(semanticJudgement.SelectedSuppliedCandidates))
	selectedIdentifiers := make(map[string]struct{}, len(semanticJudgement.SelectedSuppliedCandidates))
	for index, selection := range semanticJudgement.SelectedSuppliedCandidates {
		if selection == nil {
			return nil, fmt.Errorf("PhotoCard selected supplied photographed-place candidate %d is missing", index+1)
		}
		identifier := strings.TrimSpace(selection.SuppliedCandidateIdentifier)
		canonicalHumanName, exists := suppliedNameByIdentifier[identifier]
		if !exists {
			return nil, fmt.Errorf("PhotoCard selected supplied photographed-place candidate %d has an unknown identifier", index+1)
		}
		if strings.TrimSpace(selection.Evidence) == "" {
			return nil, fmt.Errorf("PhotoCard selected supplied photographed-place candidate %d requires evidence", index+1)
		}
		if _, duplicate := selectedIdentifiers[identifier]; duplicate {
			return nil, fmt.Errorf("PhotoCard selected supplied photographed-place candidate %d repeats an earlier candidate", index+1)
		}
		selectedIdentifiers[identifier] = struct{}{}
		selectedCandidates[index] = &cardwire.SuppliedPhotographedPlaceCandidate{
			SuppliedCandidateIdentifier: identifier,
			HumanName:                   canonicalHumanName,
			Evidence:                    selection.Evidence,
		}
	}
	imageInferredPlaces := make([]*cardwire.ImageInferredPhotographedPlace, 0, len(semanticJudgement.ImageInferredPlaces))
	for index, inferredPlace := range semanticJudgement.ImageInferredPlaces {
		if inferredPlace == nil {
			imageInferredPlaces = append(imageInferredPlaces, nil)
			continue
		}
		matchingSuppliedIdentifiers := suppliedIdentifiersByNormalisedHumanName[normalisePhotographedPlaceHumanName(inferredPlace.HumanName)]
		switch len(matchingSuppliedIdentifiers) {
		case 0:
			imageInferredPlaces = append(imageInferredPlaces, proto.Clone(inferredPlace).(*cardwire.ImageInferredPhotographedPlace))
		case 1:
			matchingSuppliedIdentifier := matchingSuppliedIdentifiers[0]
			if _, alreadySelected := selectedIdentifiers[matchingSuppliedIdentifier]; alreadySelected {
				return nil, fmt.Errorf("PhotoCard image-inferred photographed place %d repeats selected supplied candidate %q", index+1, matchingSuppliedIdentifier)
			}
			selectedIdentifiers[matchingSuppliedIdentifier] = struct{}{}
			selectedCandidates = append(selectedCandidates, &cardwire.SuppliedPhotographedPlaceCandidate{
				SuppliedCandidateIdentifier: matchingSuppliedIdentifier,
				HumanName:                   suppliedNameByIdentifier[matchingSuppliedIdentifier],
				Evidence:                    inferredPlace.Evidence,
			})
		default:
			return nil, fmt.Errorf("PhotoCard image-inferred photographed place %d matches multiple supplied candidates; select the intended identifier", index+1)
		}
	}
	return &cardwire.PhotographedPlaceJudgement{
		Certainty:                  semanticJudgement.Certainty,
		SelectedSuppliedCandidates: selectedCandidates,
		ImageInferredPlaces:        imageInferredPlaces,
		Explanation:                semanticJudgement.Explanation,
	}, nil
}

func clonePhotoCardUncertainties(uncertainties []*cardwire.PhotoCardUncertainty) []*cardwire.PhotoCardUncertainty {
	cloned := make([]*cardwire.PhotoCardUncertainty, len(uncertainties))
	for index, uncertainty := range uncertainties {
		if uncertainty != nil {
			cloned[index] = proto.Clone(uncertainty).(*cardwire.PhotoCardUncertainty)
		}
	}
	return cloned
}

func NeedsDescriptionsOnlyRepair(card *cardwire.PhotoCard, suppliedCandidates []SuppliedPhotographedPlaceCandidate) bool {
	if card == nil || validateDescriptions(card.Descriptions) == nil {
		return false
	}
	return validateNonDescriptionSections(card, suppliedCandidates) == nil
}

func MergeDescriptionsRepair(retainedCard *cardwire.PhotoCard, repairedDescriptions *cardwire.PhotoDescriptions, suppliedCandidates []SuppliedPhotographedPlaceCandidate) (*cardwire.PhotoCard, error) {
	if !NeedsDescriptionsOnlyRepair(retainedCard, suppliedCandidates) {
		return nil, errors.New("retained PhotoCard is not eligible for a descriptions-only repair")
	}
	if repairedDescriptions == nil {
		return nil, errors.New("repaired PhotoCard descriptions are required")
	}
	mergedCard := proto.Clone(retainedCard).(*cardwire.PhotoCard)
	mergedCard.Descriptions = proto.Clone(repairedDescriptions).(*cardwire.PhotoDescriptions)
	if err := ValidateModelResult(mergedCard, suppliedCandidates); err != nil {
		return nil, fmt.Errorf("validate PhotoCard after descriptions-only repair: %w", err)
	}
	return mergedCard, nil
}

func validateDescriptions(descriptions *cardwire.PhotoDescriptions) error {
	if descriptions == nil {
		return errors.New("PhotoCard descriptions are required")
	}
	if strings.TrimSpace(descriptions.ConciseDescription) == "" {
		return errors.New("PhotoCard concise description is required")
	}
	if strings.TrimSpace(descriptions.DetailedDescription) == "" {
		return errors.New("PhotoCard detailed description is required")
	}
	detailedDescriptionWordCount := len(strings.Fields(descriptions.DetailedDescription))
	if detailedDescriptionWordCount > 500 {
		return fmt.Errorf("PhotoCard detailed description must contain no more than 500 words, got %d", detailedDescriptionWordCount)
	}
	return nil
}

func validateNonDescriptionSections(card *cardwire.PhotoCard, suppliedCandidates []SuppliedPhotographedPlaceCandidate) error {
	if card.PrimaryDepictedSubject == nil || strings.TrimSpace(card.PrimaryDepictedSubject.HumanName) == "" || strings.TrimSpace(card.PrimaryDepictedSubject.VisualEvidence) == "" {
		return errors.New("PhotoCard primary depicted subject requires a human name and visual evidence")
	}
	if card.VisibleContent == nil || strings.TrimSpace(card.VisibleContent.Scene) == "" {
		return errors.New("PhotoCard visible content requires a scene")
	}
	for index, person := range card.VisibleContent.People {
		if person == nil || strings.TrimSpace(person.VisiblePositionOrRole) == "" || strings.TrimSpace(person.VisibleAppearance) == "" || strings.TrimSpace(person.VisibleActionOrPose) == "" {
			return fmt.Errorf("PhotoCard visible person %d requires position or role, appearance, and action or pose", index+1)
		}
	}
	if err := validateNonblankStrings("PhotoCard important object", card.VisibleContent.ImportantObjects); err != nil {
		return err
	}
	if err := validateNonblankStrings("PhotoCard visible action", card.VisibleContent.VisibleActions); err != nil {
		return err
	}
	if card.OpticalCharacterRecognition == nil {
		return errors.New("PhotoCard optical-character-recognition section is required")
	}
	if err := validateOpticalCharacterRecognition(card.OpticalCharacterRecognition); err != nil {
		return err
	}
	if len(card.SearchableFacts) == 0 {
		return errors.New("PhotoCard requires at least one searchable fact")
	}
	if err := validateNonblankStrings("PhotoCard searchable fact", card.SearchableFacts); err != nil {
		return err
	}
	for index, uncertainty := range card.Uncertainties {
		if uncertainty == nil || uncertainty.Scope == cardwire.PhotoCardUncertaintyScope_PHOTO_CARD_UNCERTAINTY_SCOPE_UNSPECIFIED || strings.TrimSpace(uncertainty.Subject) == "" || strings.TrimSpace(uncertainty.Explanation) == "" {
			return fmt.Errorf("PhotoCard uncertainty %d requires a nonzero scope, subject, and explanation", index+1)
		}
	}
	place := card.PhotographedPlace
	if place == nil {
		return errors.New("PhotoCard photographed-place judgement is required")
	}
	if strings.TrimSpace(place.Explanation) == "" {
		return errors.New("PhotoCard photographed-place judgement requires an explanation")
	}

	suppliedNameByIdentifier, err := suppliedPhotographedPlaceNamesByIdentifier(suppliedCandidates)
	if err != nil {
		return err
	}
	suppliedIdentifiersByNormalisedHumanName := suppliedPhotographedPlaceIdentifiersByNormalisedHumanName(suppliedCandidates)

	selectedIdentifiers := make(map[string]struct{}, len(place.SelectedSuppliedCandidates))
	for index, selectedCandidate := range place.SelectedSuppliedCandidates {
		if selectedCandidate == nil {
			return fmt.Errorf("PhotoCard selected supplied photographed-place candidate %d is missing", index+1)
		}
		identifier := strings.TrimSpace(selectedCandidate.SuppliedCandidateIdentifier)
		expectedName, exists := suppliedNameByIdentifier[identifier]
		if !exists {
			return fmt.Errorf("PhotoCard selected supplied photographed-place candidate %d has an unknown identifier", index+1)
		}
		if strings.TrimSpace(selectedCandidate.HumanName) != expectedName {
			return fmt.Errorf("PhotoCard selected supplied photographed-place candidate %d changed its human name", index+1)
		}
		if strings.TrimSpace(selectedCandidate.Evidence) == "" {
			return fmt.Errorf("PhotoCard selected supplied photographed-place candidate %d requires evidence", index+1)
		}
		if _, duplicate := selectedIdentifiers[identifier]; duplicate {
			return fmt.Errorf("PhotoCard selected supplied photographed-place candidate %d repeats an earlier candidate", index+1)
		}
		selectedIdentifiers[identifier] = struct{}{}
	}
	for index, inferredPlace := range place.ImageInferredPlaces {
		if inferredPlace == nil || strings.TrimSpace(inferredPlace.HumanName) == "" || strings.TrimSpace(inferredPlace.Evidence) == "" {
			if inferredPlace == nil {
				return fmt.Errorf("PhotoCard image-inferred photographed place %d is missing", index+1)
			}
			return errors.New("PhotoCard image-inferred photographed places require a human name and evidence")
		}
		if suppliedIdentifiers := suppliedIdentifiersByNormalisedHumanName[normalisePhotographedPlaceHumanName(inferredPlace.HumanName)]; len(suppliedIdentifiers) > 0 {
			return fmt.Errorf("PhotoCard image-inferred photographed place %d duplicates a supplied candidate", index+1)
		}
	}

	placeCount := len(place.SelectedSuppliedCandidates) + len(place.ImageInferredPlaces)
	switch place.Certainty {
	case cardwire.PhotographedPlaceCertainty_PHOTOGRAPHED_PLACE_CERTAINTY_IDENTIFIED:
		if placeCount != 1 {
			return fmt.Errorf("identified photographed place requires exactly one place, got %d", placeCount)
		}
	case cardwire.PhotographedPlaceCertainty_PHOTOGRAPHED_PLACE_CERTAINTY_POSSIBLE:
		if placeCount == 0 {
			return errors.New("possible photographed place requires at least one possible place")
		}
	case cardwire.PhotographedPlaceCertainty_PHOTOGRAPHED_PLACE_CERTAINTY_UNKNOWN:
		if placeCount != 0 {
			return fmt.Errorf("unknown photographed place cannot include selected or inferred places, got %d", placeCount)
		}
	default:
		return fmt.Errorf("PhotoCard has invalid photographed-place certainty %s", place.Certainty)
	}
	return nil
}

func suppliedPhotographedPlaceNamesByIdentifier(suppliedCandidates []SuppliedPhotographedPlaceCandidate) (map[string]string, error) {
	suppliedNameByIdentifier := make(map[string]string, len(suppliedCandidates))
	for index, suppliedCandidate := range suppliedCandidates {
		identifier := strings.TrimSpace(suppliedCandidate.Identifier)
		humanName := strings.TrimSpace(suppliedCandidate.HumanName)
		if identifier == "" || humanName == "" {
			return nil, errors.New("supplied photographed-place candidates require an identifier and human name")
		}
		if _, exists := suppliedNameByIdentifier[identifier]; exists {
			return nil, fmt.Errorf("supplied photographed-place candidate %d repeats an earlier identifier", index+1)
		}
		suppliedNameByIdentifier[identifier] = humanName
	}
	return suppliedNameByIdentifier, nil
}

func suppliedPhotographedPlaceIdentifiersByNormalisedHumanName(suppliedCandidates []SuppliedPhotographedPlaceCandidate) map[string][]string {
	suppliedIdentifiersByNormalisedHumanName := make(map[string][]string, len(suppliedCandidates))
	for _, suppliedCandidate := range suppliedCandidates {
		suppliedIdentifier := strings.TrimSpace(suppliedCandidate.Identifier)
		humanName := strings.TrimSpace(suppliedCandidate.HumanName)
		normalisedHumanName := normalisePhotographedPlaceHumanName(humanName)
		suppliedIdentifiersByNormalisedHumanName[normalisedHumanName] = append(suppliedIdentifiersByNormalisedHumanName[normalisedHumanName], suppliedIdentifier)
	}
	return suppliedIdentifiersByNormalisedHumanName
}

func normalisePhotographedPlaceHumanName(humanName string) string {
	return strings.ToLower(strings.Join(strings.Fields(humanName), " "))
}

func validateOpticalCharacterRecognition(recognition *cardwire.PhotoOpticalCharacterRecognition) error {
	for regionIndex, region := range recognition.RegionsInReadingOrder {
		if region == nil || strings.TrimSpace(region.VisibleSource) == "" {
			return fmt.Errorf("PhotoCard OCR region %d requires a visible source", regionIndex+1)
		}
		if len(region.LinesInReadingOrder) == 0 {
			return fmt.Errorf("PhotoCard OCR region %d requires at least one line", regionIndex+1)
		}
		for lineIndex, line := range region.LinesInReadingOrder {
			if line == nil || strings.TrimSpace(line.TranscribedText) == "" {
				return fmt.Errorf("PhotoCard OCR region %d line %d requires transcribed text", regionIndex+1, lineIndex+1)
			}
			if err := validateNonblankStrings(fmt.Sprintf("PhotoCard OCR region %d line %d language", regionIndex+1, lineIndex+1), line.Languages); err != nil {
				return err
			}
			if line.Legibility == cardwire.OpticalCharacterRecognitionLegibility_OPTICAL_CHARACTER_RECOGNITION_LEGIBILITY_UNSPECIFIED {
				return fmt.Errorf("PhotoCard OCR region %d line %d requires nonzero legibility", regionIndex+1, lineIndex+1)
			}
		}
	}
	for index, field := range recognition.KeyValueFields {
		if field == nil || strings.TrimSpace(field.Key) == "" || strings.TrimSpace(field.Value) == "" || strings.TrimSpace(field.VisibleSource) == "" {
			return fmt.Errorf("PhotoCard OCR key-value field %d requires key, value, and visible source", index+1)
		}
	}
	for tableIndex, table := range recognition.Tables {
		if table == nil || strings.TrimSpace(table.VisibleSource) == "" {
			return fmt.Errorf("PhotoCard OCR table %d requires a visible source", tableIndex+1)
		}
		if len(table.RowsInReadingOrder) == 0 {
			return fmt.Errorf("PhotoCard OCR table %d requires at least one row", tableIndex+1)
		}
		for rowIndex, row := range table.RowsInReadingOrder {
			if row == nil || len(row.CellsInReadingOrder) == 0 {
				return fmt.Errorf("PhotoCard OCR table %d row %d requires at least one cell", tableIndex+1, rowIndex+1)
			}
			if err := validateNonblankStrings(fmt.Sprintf("PhotoCard OCR table %d row %d cell", tableIndex+1, rowIndex+1), row.CellsInReadingOrder); err != nil {
				return err
			}
		}
	}
	for index, uncertainty := range recognition.Uncertainties {
		if uncertainty == nil || strings.TrimSpace(uncertainty.VisibleSource) == "" || strings.TrimSpace(uncertainty.Explanation) == "" {
			return fmt.Errorf("PhotoCard OCR uncertainty %d requires visible source and explanation", index+1)
		}
	}
	return nil
}

func validateNonblankStrings(label string, values []string) error {
	for index, value := range values {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s %d must not be blank", label, index+1)
		}
	}
	return nil
}
