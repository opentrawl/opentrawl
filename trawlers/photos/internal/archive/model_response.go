package archive

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	cardwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/card/v1"
	"github.com/opentrawl/opentrawl/trawlkit/model"
)

const (
	modelObservationCardSummary     = "card_summary"
	modelObservationCardDescription = "card_description"
	modelObservationCardUncertainty = "card_uncertainty"
	modelObservationCardOCR         = "card_ocr" // v1 only
	modelObservationCardVisibleText = "card_visible_text"
	modelObservationCardLocation    = "card_location"

	photoCardToolName = "submit_photo_card"
	locationCandidate = "candidate"
	locationInferred  = "inferred"
	locationNone      = "none"

	venueVerdictCorroborated = "corroborated"
	venueVerdictPlausible    = "plausible"
	venueVerdictInconsistent = "inconsistent"
	venueVerdictNone         = "none"
)

var photoCardToolSchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "summary": {
      "type": "string",
      "minLength": 1,
      "description": "One sentence stating the main visible subject and why the image is recognisable."
    },
    "description": {
      "type": "string",
      "minLength": 1,
      "description": "A long, specific account of the visible scene. Separate observation from inference and do not copy mechanical metadata."
    },
    "location": {
      "type": "object",
      "additionalProperties": false,
      "properties": {
        "kind": {"type": "string", "enum": ["candidate", "inferred", "none"], "description": "Whether the judgement selects a supplied candidate, names a model-inferred place, or finds no useful location."},
        "candidate_id": {"type": "string", "description": "The exact supplied provider candidate id when kind is candidate. Use an empty string otherwise."},
        "inferred_name": {"type": "string", "description": "The model-inferred place name when kind is inferred. Use an empty string otherwise."},
        "confidence": {"type": "string", "enum": ["high", "medium", "low", "none"], "description": "Confidence in this location judgement. Use none only when kind is none."},
        "reason": {"type": "string", "minLength": 1, "description": "A short statement of the visible and checked evidence for the judgement, including why no place is useful."}
      },
      "required": ["kind", "candidate_id", "inferred_name", "confidence", "reason"]
    },
    "visible_text": {
      "type": "string",
      "description": "All useful readable text, preserving reading order, line breaks, repeated values and original language. Use an empty string when there is none."
    },
    "uncertainties": {
      "type": "array",
      "items": {"type": "string", "minLength": 1},
      "description": "Only unresolved ambiguities that would change how a person interprets the image."
    }
  },
  "required": ["summary", "description", "location", "visible_text", "uncertainties"]
}`)

type contentObservation struct {
	ObservationType string
	ValueText       string
	Value           any
	Confidence      *float64
	TermType        string
}

type modelResult struct {
	Payload           map[string]any
	ImageBytes        int64
	ImageSHA256       string
	Card              photoCard
	TypedCard         *cardwire.PhotoCard
	VenuePlausibility venuePlausibility
	Observations      []contentObservation
}

type photoCard struct {
	Summary       string
	Description   string
	Location      modelLocation
	VisibleText   string
	Uncertainties []string
}

type modelLocation struct {
	Kind         string `json:"kind"`
	CandidateID  string `json:"candidate_id"`
	InferredName string `json:"inferred_name"`
	Confidence   string `json:"confidence"`
	Reason       string `json:"reason"`
}

// venuePlausibility remains the mechanical projection for historical v1 cards.
// New v2 cards use modelLocation and never write model place evidence.
type venuePlausibility struct {
	CandidateID string `json:"candidate_id"`
	Verdict     string `json:"verdict"`
	Reason      string `json:"reason"`
}

// errModelCardParse marks every failure to convert a retained model response
// into a card. A failed call stays retained and is never retried automatically.
var errModelCardParse = errors.New("model card parse failure")

func photoCardTool() model.Tool {
	return model.Tool{
		Name:        photoCardToolName,
		Description: "Submit one complete, grounded photo card.",
		Parameters:  append(json.RawMessage(nil), photoCardToolSchema...),
	}
}

func parsePhotoCardToolCall(calls []model.ToolCall, prepared preparedCardRequest) (photoCard, error) {
	if len(calls) != 1 {
		return photoCard{}, fmt.Errorf("%w: expected one %s tool call, got %d", errModelCardParse, photoCardToolName, len(calls))
	}
	call := calls[0]
	if call.Name != photoCardToolName {
		return photoCard{}, fmt.Errorf("%w: expected tool %q, got %q", errModelCardParse, photoCardToolName, call.Name)
	}
	fields, err := objectFields(call.Arguments, "photo card")
	if err != nil {
		return photoCard{}, err
	}
	if err := requireOnlyFields(fields, "photo card", "summary", "description", "location", "visible_text", "uncertainties"); err != nil {
		return photoCard{}, err
	}
	summary, err := requiredString(fields, "summary", "photo card")
	if err != nil {
		return photoCard{}, err
	}
	description, err := requiredString(fields, "description", "photo card")
	if err != nil {
		return photoCard{}, err
	}
	visibleText, err := requiredString(fields, "visible_text", "photo card")
	if err != nil {
		return photoCard{}, err
	}
	uncertainties, err := requiredStrings(fields, "uncertainties", "photo card")
	if err != nil {
		return photoCard{}, err
	}
	locationFields, err := requiredObject(fields, "location", "photo card")
	if err != nil {
		return photoCard{}, err
	}
	if err := requireOnlyFields(locationFields, "location", "kind", "candidate_id", "inferred_name", "confidence", "reason"); err != nil {
		return photoCard{}, err
	}
	kind, err := requiredString(locationFields, "kind", "location")
	if err != nil {
		return photoCard{}, err
	}
	candidateID, err := requiredString(locationFields, "candidate_id", "location")
	if err != nil {
		return photoCard{}, err
	}
	inferredName, err := requiredString(locationFields, "inferred_name", "location")
	if err != nil {
		return photoCard{}, err
	}
	confidence, err := requiredString(locationFields, "confidence", "location")
	if err != nil {
		return photoCard{}, err
	}
	reason, err := requiredString(locationFields, "reason", "location")
	if err != nil {
		return photoCard{}, err
	}
	if strings.TrimSpace(summary) == "" || strings.TrimSpace(description) == "" || strings.TrimSpace(reason) == "" {
		return photoCard{}, fmt.Errorf("%w: summary, description and location reason must not be empty", errModelCardParse)
	}
	card := photoCard{
		Summary:       summary,
		Description:   description,
		VisibleText:   visibleText,
		Uncertainties: uncertainties,
		Location: modelLocation{
			Kind: kind, CandidateID: candidateID, InferredName: inferredName, Confidence: confidence, Reason: reason,
		},
	}
	if err := validateModelLocation(prepared, card.Location); err != nil {
		return photoCard{}, err
	}
	return card, nil
}

func objectFields(raw json.RawMessage, name string) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, fmt.Errorf("%w: %s must be an object", errModelCardParse, name)
	}
	return fields, nil
}

func requireOnlyFields(fields map[string]json.RawMessage, name string, required ...string) error {
	allowed := make(map[string]bool, len(required))
	for _, key := range required {
		allowed[key] = true
		if _, ok := fields[key]; !ok {
			return fmt.Errorf("%w: %s is missing %s", errModelCardParse, name, key)
		}
	}
	for key := range fields {
		if !allowed[key] {
			return fmt.Errorf("%w: %s has unknown field %s", errModelCardParse, name, key)
		}
	}
	return nil
}

func requiredString(fields map[string]json.RawMessage, key, name string) (string, error) {
	var value any
	if err := json.Unmarshal(fields[key], &value); err != nil {
		return "", fmt.Errorf("%w: decode %s.%s: %v", errModelCardParse, name, key, err)
	}
	stringValue, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%w: %s.%s must be a string", errModelCardParse, name, key)
	}
	return stringValue, nil
}

func requiredStrings(fields map[string]json.RawMessage, key, name string) ([]string, error) {
	var values []any
	if err := json.Unmarshal(fields[key], &values); err != nil {
		return nil, fmt.Errorf("%w: decode %s.%s: %v", errModelCardParse, name, key, err)
	}
	if values == nil {
		return nil, fmt.Errorf("%w: %s.%s must be an array", errModelCardParse, name, key)
	}
	result := make([]string, len(values))
	for index, value := range values {
		stringValue, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("%w: %s.%s[%d] must be a string", errModelCardParse, name, key, index)
		}
		if strings.TrimSpace(stringValue) == "" {
			return nil, fmt.Errorf("%w: %s.%s[%d] must not be empty", errModelCardParse, name, key, index)
		}
		result[index] = stringValue
	}
	return result, nil
}

func requiredObject(fields map[string]json.RawMessage, key, name string) (map[string]json.RawMessage, error) {
	object, err := objectFields(fields[key], name+"."+key)
	if err != nil {
		return nil, err
	}
	return object, nil
}

func validateModelLocation(prepared preparedCardRequest, location modelLocation) error {
	switch location.Kind {
	case locationCandidate:
		if location.CandidateID == "" || location.InferredName != "" || location.Confidence == "none" {
			return fmt.Errorf("%w: candidate location requires an exact candidate id, no inferred name and non-none confidence", errModelCardParse)
		}
		if _, ok := prepared.CandidateByID[location.CandidateID]; !ok {
			return fmt.Errorf("%w: %s", errUnknownCardCandidate, location.CandidateID)
		}
	case locationInferred:
		if location.CandidateID != "" || strings.TrimSpace(location.InferredName) == "" || location.Confidence == "none" {
			return fmt.Errorf("%w: inferred location requires a name, no candidate id and non-none confidence", errModelCardParse)
		}
	case locationNone:
		if location.CandidateID != "" || location.InferredName != "" || location.Confidence != "none" {
			return fmt.Errorf("%w: no location requires empty names and none confidence", errModelCardParse)
		}
	default:
		return fmt.Errorf("%w: unknown location kind %q", errModelCardParse, location.Kind)
	}
	if location.Confidence != "high" && location.Confidence != "medium" && location.Confidence != "low" && location.Confidence != "none" {
		return fmt.Errorf("%w: unknown location confidence %q", errModelCardParse, location.Confidence)
	}
	return nil
}

func observationsFromCard(card photoCard, prepared preparedCardRequest) []contentObservation {
	observations := []contentObservation{
		cardObservation(modelObservationCardSummary, card.Summary),
		cardObservation(modelObservationCardDescription, card.Description),
	}
	if card.VisibleText != "" {
		observations = append(observations, cardObservation(modelObservationCardVisibleText, card.VisibleText))
	}
	locationText := card.Location.Reason
	switch card.Location.Kind {
	case locationCandidate:
		locationText = prepared.CandidateByID[card.Location.CandidateID].Name + "\n" + card.Location.Reason
	case locationInferred:
		locationText = card.Location.InferredName + "\n" + card.Location.Reason
	}
	if locationText != "" {
		observations = append(observations, cardObservation(modelObservationCardLocation, locationText))
	}
	for _, uncertainty := range card.Uncertainties {
		observations = append(observations, cardObservation(modelObservationCardUncertainty, uncertainty))
	}
	return observations
}

func cardObservation(kind, text string) contentObservation {
	return contentObservation{
		ObservationType: kind,
		ValueText:       text,
		Value:           map[string]any{"text": text},
		TermType:        "photo_card",
	}
}

func photoCardPayload(card photoCard) map[string]any {
	return map[string]any{
		"summary":       card.Summary,
		"description":   card.Description,
		"location":      card.Location,
		"visible_text":  card.VisibleText,
		"uncertainties": card.Uncertainties,
	}
}

func photoCardMessage(card photoCard) *cardwire.PhotoCard {
	return &cardwire.PhotoCard{
		Summary:       card.Summary,
		Description:   card.Description,
		VisibleText:   card.VisibleText,
		Uncertainties: append([]string(nil), card.Uncertainties...),
		Location: &cardwire.ModelLocation{
			Kind: card.Location.Kind, CandidateId: card.Location.CandidateID,
			InferredName: card.Location.InferredName, Confidence: card.Location.Confidence,
			Reason: card.Location.Reason,
		},
	}
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}
