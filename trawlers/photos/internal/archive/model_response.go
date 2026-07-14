package archive

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit/model"
)

const (
	modelObservationCardSummary     = "card_summary"
	modelObservationCardDescription = "card_description"
	modelObservationCardUncertainty = "card_uncertainty"
	modelObservationCardOCR         = "card_ocr"

	photoCardToolName        = "submit_photo_card"
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
      "description": "One sentence that states the main visible subject."
    },
    "description": {
      "type": "string",
      "description": "A grounded description of the visible evidence."
    },
    "venue_plausibility": {
      "type": "object",
      "additionalProperties": false,
      "properties": {
        "candidate_id": {"type": "string", "description": "An exact supplied candidate id or none."},
        "verdict": {"type": "string", "enum": ["corroborated", "plausible", "inconsistent", "none"]},
        "reason": {"type": "string", "description": "A short explanation of the venue verdict."}
      },
      "required": ["candidate_id", "verdict", "reason"]
    },
    "ocr_text": {
      "type": "string",
      "description": "Readable text from the image, or an empty string."
    },
    "uncertainties": {
      "type": "array",
      "items": {"type": "string"},
      "description": "Only uncertainties that affect interpretation."
    }
  },
  "required": ["summary", "description", "venue_plausibility", "ocr_text", "uncertainties"]
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
	VenuePlausibility venuePlausibility
	Observations      []contentObservation
}

type photoCard struct {
	Summary           string
	Description       string
	VenuePlausibility venuePlausibility
	OCRText           string
	Uncertainties     []string
}

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
	if err := requireOnlyFields(fields, "photo card", "summary", "description", "venue_plausibility", "ocr_text", "uncertainties"); err != nil {
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
	ocrText, err := requiredString(fields, "ocr_text", "photo card")
	if err != nil {
		return photoCard{}, err
	}
	uncertainties, err := requiredStrings(fields, "uncertainties", "photo card")
	if err != nil {
		return photoCard{}, err
	}
	venueFields, err := requiredObject(fields, "venue_plausibility", "photo card")
	if err != nil {
		return photoCard{}, err
	}
	if err := requireOnlyFields(venueFields, "venue plausibility", "candidate_id", "verdict", "reason"); err != nil {
		return photoCard{}, err
	}
	candidateID, err := requiredString(venueFields, "candidate_id", "venue plausibility")
	if err != nil {
		return photoCard{}, err
	}
	verdict, err := requiredString(venueFields, "verdict", "venue plausibility")
	if err != nil {
		return photoCard{}, err
	}
	reason, err := requiredString(venueFields, "reason", "venue plausibility")
	if err != nil {
		return photoCard{}, err
	}
	if strings.TrimSpace(summary) == "" || strings.TrimSpace(description) == "" || strings.TrimSpace(reason) == "" {
		return photoCard{}, fmt.Errorf("%w: summary, description and venue reason must not be empty", errModelCardParse)
	}
	if verdict != venueVerdictCorroborated && verdict != venueVerdictPlausible && verdict != venueVerdictInconsistent && verdict != venueVerdictNone {
		return photoCard{}, fmt.Errorf("%w: unknown venue verdict %q", errModelCardParse, verdict)
	}
	if (candidateID == venueVerdictNone) != (verdict == venueVerdictNone) {
		return photoCard{}, fmt.Errorf("%w: candidate_id and verdict must both be none or both name a candidate", errModelCardParse)
	}
	card := photoCard{
		Summary:       summary,
		Description:   description,
		OCRText:       ocrText,
		Uncertainties: uncertainties,
		VenuePlausibility: venuePlausibility{
			CandidateID: candidateID,
			Verdict:     verdict,
			Reason:      reason,
		},
	}
	if err := validateVenueCandidate(prepared, &card.VenuePlausibility); err != nil {
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

func validateVenueCandidate(prepared preparedCardRequest, plausibility *venuePlausibility) error {
	if plausibility == nil {
		return fmt.Errorf("%w: missing venue_plausibility", errUnknownCardCandidate)
	}
	if plausibility.CandidateID == venueVerdictNone {
		return nil
	}
	if _, ok := prepared.CandidateByID[plausibility.CandidateID]; !ok {
		return fmt.Errorf("%w: %s", errUnknownCardCandidate, plausibility.CandidateID)
	}
	return nil
}

func observationsFromCard(card photoCard) []contentObservation {
	observations := []contentObservation{
		cardObservation(modelObservationCardSummary, card.Summary),
		cardObservation(modelObservationCardDescription, card.Description),
	}
	if card.OCRText != "" {
		observations = append(observations, cardObservation(modelObservationCardOCR, card.OCRText))
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
		"summary":            card.Summary,
		"description":        card.Description,
		"venue_plausibility": card.VenuePlausibility,
		"ocr_text":           card.OCRText,
		"uncertainties":      card.Uncertainties,
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
