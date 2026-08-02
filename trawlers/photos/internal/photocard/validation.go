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
	detailedDescriptionWordCount := len(strings.Fields(descriptions.DetailedDescription))
	if detailedDescriptionWordCount < 250 || detailedDescriptionWordCount > 500 {
		return fmt.Errorf("PhotoCard detailed description must contain 250 to 500 words, got %d", detailedDescriptionWordCount)
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

	suppliedNameByIdentifier := make(map[string]string, len(suppliedCandidates))
	for _, suppliedCandidate := range suppliedCandidates {
		identifier := strings.TrimSpace(suppliedCandidate.Identifier)
		humanName := strings.TrimSpace(suppliedCandidate.HumanName)
		if identifier == "" || humanName == "" {
			return errors.New("supplied photographed-place candidates require an identifier and human name")
		}
		if _, exists := suppliedNameByIdentifier[identifier]; exists {
			return fmt.Errorf("duplicate supplied photographed-place candidate identifier %q", identifier)
		}
		suppliedNameByIdentifier[identifier] = humanName
	}

	selectedIdentifiers := make(map[string]struct{}, len(place.SelectedSuppliedCandidates))
	for index, selectedCandidate := range place.SelectedSuppliedCandidates {
		if selectedCandidate == nil {
			return fmt.Errorf("PhotoCard selected supplied photographed-place candidate %d is missing", index+1)
		}
		identifier := strings.TrimSpace(selectedCandidate.SuppliedCandidateIdentifier)
		expectedName, exists := suppliedNameByIdentifier[identifier]
		if !exists {
			return fmt.Errorf("PhotoCard selected unknown supplied photographed-place candidate %q", identifier)
		}
		if strings.TrimSpace(selectedCandidate.HumanName) != expectedName {
			return fmt.Errorf("PhotoCard changed the human name of supplied photographed-place candidate %q", identifier)
		}
		if strings.TrimSpace(selectedCandidate.Evidence) == "" {
			return fmt.Errorf("PhotoCard supplied photographed-place candidate %q requires evidence", identifier)
		}
		if _, duplicate := selectedIdentifiers[identifier]; duplicate {
			return fmt.Errorf("PhotoCard selected supplied photographed-place candidate %q more than once", identifier)
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
			if len(line.Languages) == 0 {
				return fmt.Errorf("PhotoCard OCR region %d line %d requires at least one language", regionIndex+1, lineIndex+1)
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
