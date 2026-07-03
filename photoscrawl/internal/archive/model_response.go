package archive

import (
	"encoding/json"
	"fmt"
	"strings"
)

type contentObservation struct {
	ObservationType string
	ValueText       string
	Value           any
	Confidence      *float64
	TermType        string
}

type modelResult struct {
	Payload      map[string]any
	RawResponse  string
	ImageBytes   int64
	ImageSHA256  string
	Observations []contentObservation
}

type observationValue struct {
	Text       string
	Value      any
	Confidence *float64
}

func parseModelPayload(raw string) (map[string]any, error) {
	raw = strings.TrimSpace(raw)
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end < start {
		return nil, fmt.Errorf("model did not return a JSON object")
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw[start:end+1]), &payload); err != nil {
		return nil, fmt.Errorf("parse model JSON: %w", err)
	}
	return payload, nil
}

func observationsFromPayload(payload map[string]any) []contentObservation {
	out := []contentObservation{}
	add := func(kind string, value any, termType string) {
		for _, item := range observationValues(value) {
			out = append(out, contentObservation{
				ObservationType: kind,
				ValueText:       item.Text,
				Value:           item.Value,
				Confidence:      item.Confidence,
				TermType:        termType,
			})
		}
	}
	add("scene_summary", payload["scene_summary"], "scene")
	add("visible_text_summary", payload["visible_text_summary"], "visible_text")
	add("place_type_candidate", payload["place_candidates"], "place_type_candidate")
	add("landmark_or_place_name_candidate", payload["landmark_candidates"], "landmark_or_place_name_candidate")
	add("merchant_or_venue_name_candidate", payload["merchant_or_venue_candidates"], "merchant_or_venue_name_candidate")
	add("object_or_food", payload["food_or_objects"], "object_or_food")
	add("people_presence", payload["people_presence"], "people_presence")
	add("privacy_sensitivity", payload["privacy_sensitivity"], "privacy_sensitivity")
	add("cluster_feature", payload["cluster_terms"], "cluster_feature")
	add("model_uncertainty", payload["uncertainties"], "model_uncertainty")
	for _, leakage := range promptLeakageObservations(payload) {
		out = append(out, leakage)
	}
	return dedupeContentObservations(out)
}

func promptLeakageObservations(payload map[string]any) []contentObservation {
	promptFragments := []string{
		"return only valid compact",
		"do not use markdown fences",
		"use candidates, not truth",
		"keys:",
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	lower := strings.ToLower(string(data))
	for _, fragment := range promptFragments {
		if strings.Contains(lower, fragment) {
			return []contentObservation{{
				ObservationType: "quality_issue",
				ValueText:       "model_prompt_leakage",
				Value:           map[string]any{"text": "model_prompt_leakage", "fragment": fragment},
				TermType:        "quality_issue",
			}}
		}
	}
	return nil
}

func observationValues(value any) []observationValue {
	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		return textObservationValues(typed, nil)
	case []any:
		out := []observationValue{}
		for _, item := range typed {
			out = append(out, observationValues(item)...)
		}
		return out
	case map[string]any:
		confidence := mapConfidence(typed)
		for _, key := range []string{"name", "label", "text", "value", "candidate"} {
			if text, ok := typed[key].(string); ok {
				return textObservationValues(text, confidence)
			}
		}
		data, err := json.Marshal(typed)
		if err != nil {
			return nil
		}
		return textObservationValues(string(data), confidence)
	case bool:
		if typed {
			return textObservationValues("true", nil)
		}
		return textObservationValues("false", nil)
	case float64:
		return textObservationValues(fmt.Sprintf("%g", typed), nil)
	default:
		return textObservationValues(fmt.Sprint(typed), nil)
	}
}

func textObservationValues(value string, confidence *float64) []observationValue {
	texts := nonEmpty(truncateObservationText(value))
	out := make([]observationValue, 0, len(texts))
	for _, text := range texts {
		payload := map[string]any{"text": text}
		if confidence != nil {
			payload["confidence"] = *confidence
		}
		out = append(out, observationValue{Text: text, Value: payload, Confidence: confidence})
	}
	return out
}

func mapConfidence(value map[string]any) *float64 {
	raw, ok := value["confidence"]
	if !ok {
		return nil
	}
	var parsed float64
	switch typed := raw.(type) {
	case float64:
		parsed = typed
	case int:
		parsed = float64(typed)
	case int64:
		parsed = float64(typed)
	default:
		return nil
	}
	if parsed < 0 || parsed > 1 {
		return nil
	}
	return &parsed
}

func dedupeContentObservations(observations []contentObservation) []contentObservation {
	seen := map[string]bool{}
	out := make([]contentObservation, 0, len(observations))
	for _, observation := range observations {
		key := observation.ObservationType + "\x00" + observation.ValueText
		if seen[key] || strings.TrimSpace(observation.ValueText) == "" {
			continue
		}
		seen[key] = true
		out = append(out, observation)
	}
	return out
}

func observationTerms(observation contentObservation) []string {
	terms := []string{}
	for _, part := range strings.Fields(observation.ValueText) {
		if term := normalizeTerm(part); term != "" {
			terms = append(terms, term)
		}
	}
	if term := normalizeTerm(observation.ValueText); term != "" {
		terms = append(terms, term)
	}
	seen := map[string]bool{}
	out := []string{}
	for _, term := range terms {
		if seen[term] {
			continue
		}
		seen[term] = true
		out = append(out, term)
	}
	return out
}

func normalizeTerm(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	lastUnderscore := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
			lastUnderscore = false
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
			lastUnderscore = false
		default:
			if !lastUnderscore && builder.Len() > 0 {
				builder.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	out := strings.Trim(builder.String(), "_")
	if len(out) < 2 || len(out) > 80 {
		return ""
	}
	return out
}

func truncateObservationText(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) <= 500 {
		return value
	}
	return strings.TrimSpace(value[:500])
}

func truncateReason(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) <= 200 {
		return value
	}
	return strings.TrimSpace(value[:200])
}
