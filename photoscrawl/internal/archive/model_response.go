package archive

import (
	"errors"
	"fmt"
	"strings"
)

const (
	modelObservationCardSummary     = "card_summary"
	modelObservationCardDescription = "card_description"
	modelObservationCardPlacePhrase = "card_place_phrase"
	modelObservationCardUncertainty = "card_uncertainty"
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
	SearchTerms  []string
}

type photoCard struct {
	Summary       string
	Description   string
	PlacePhrase   string
	OCRText       string
	Uncertainties []string
}

func parsePhotoCard(raw string) (photoCard, error) {
	sections, err := splitPhotoCardSections(raw)
	if err != nil {
		return photoCard{}, err
	}
	required := []string{"summary", "description", "location", "ocr", "uncertainty"}
	for _, key := range required {
		if _, ok := sections[key]; !ok {
			return photoCard{}, fmt.Errorf("model card missing %s section", key)
		}
	}
	card := photoCard{
		Summary:       cleanSingleLine(sections["summary"]),
		Description:   cleanMultiline(sections["description"]),
		PlacePhrase:   cleanPlacePhrase(sections["location"]),
		OCRText:       cleanOptionalField(sections["ocr"]),
		Uncertainties: parseUncertainties(sections["uncertainty"]),
	}
	if card.Summary == "" {
		return photoCard{}, errors.New("model card summary is empty")
	}
	if card.Description == "" {
		return photoCard{}, errors.New("model card description is empty")
	}
	return card, nil
}

func splitPhotoCardSections(raw string) (map[string]string, error) {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")
	lines := strings.Split(raw, "\n")
	parts := map[string][]string{}
	current := ""
	for _, line := range lines {
		if key, ok := photoCardSectionKey(line); ok {
			current = key
			if _, exists := parts[current]; !exists {
				parts[current] = nil
			}
			continue
		}
		if current == "" {
			if strings.TrimSpace(line) != "" {
				return nil, errors.New("model card has text before first section heading")
			}
			continue
		}
		parts[current] = append(parts[current], line)
	}
	if len(parts) == 0 {
		return nil, errors.New("model card did not use required section headings")
	}
	sections := make(map[string]string, len(parts))
	for key, lines := range parts {
		sections[key] = strings.TrimSpace(strings.Join(lines, "\n"))
	}
	return sections, nil
}

func photoCardSectionKey(line string) (string, bool) {
	heading := strings.TrimSpace(line)
	for strings.HasPrefix(heading, "#") {
		heading = strings.TrimSpace(strings.TrimPrefix(heading, "#"))
	}
	heading = strings.TrimSpace(strings.TrimSuffix(heading, ":"))
	heading = strings.TrimSpace(strings.Trim(heading, "*"))
	heading = strings.ToLower(heading)
	switch heading {
	case "one-line summary", "one line summary", "summary":
		return "summary", true
	case "detailed description", "description":
		return "description", true
	case "location", "place":
		return "location", true
	case "ocr and machine-readable text", "ocr and machine readable text", "ocr", "machine-readable text", "machine readable text":
		return "ocr", true
	case "uncertainty", "uncertainties":
		return "uncertainty", true
	default:
		return "", false
	}
}

func cleanSingleLine(value string) string {
	for _, line := range strings.Split(value, "\n") {
		line = stripListMarker(line)
		if strings.TrimSpace(line) != "" {
			return strings.Join(strings.Fields(line), " ")
		}
	}
	return ""
}

func cleanMultiline(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func cleanOptionalField(value string) string {
	value = cleanMultiline(value)
	if emptyCardField(value) {
		return ""
	}
	return value
}

func cleanPlacePhrase(value string) string {
	value = cleanOptionalField(value)
	if value == "" {
		return ""
	}
	sentences := splitSentences(value)
	if len(sentences) == 0 {
		return ""
	}
	return shortenPlacePhrase(sentences[0])
}

func splitSentences(value string) []string {
	raw := strings.FieldsFunc(value, func(r rune) bool {
		return r == '.' || r == '\n'
	})
	out := []string{}
	for _, sentence := range raw {
		sentence = strings.Join(strings.Fields(sentence), " ")
		if sentence != "" {
			out = append(out, sentence)
		}
	}
	return out
}

func shortenPlacePhrase(value string) string {
	value = strings.TrimSpace(value)
	lower := strings.ToLower(value)
	prefixes := []string{
		"the image was taken in an ",
		"the image was taken in a ",
		"the image was taken in ",
		"this image was taken in an ",
		"this image was taken in a ",
		"this image was taken in ",
		"the photo was taken in an ",
		"the photo was taken in a ",
		"the photo was taken in ",
		"this appears to be an ",
		"this appears to be a ",
		"it appears to be an ",
		"it appears to be a ",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(lower, prefix) {
			value = strings.TrimSpace(value[len(prefix):])
			break
		}
	}
	if len(value) <= 90 {
		return value
	}
	cut := strings.LastIndexAny(value[:90], ",;")
	if cut < 30 {
		cut = strings.LastIndex(value[:90], " ")
	}
	if cut < 30 {
		return strings.TrimSpace(value[:90])
	}
	return strings.TrimSpace(value[:cut])
}

func parseUncertainties(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" || emptyCardField(value) {
		return nil
	}
	items := []string{}
	for _, line := range strings.Split(value, "\n") {
		line = stripListMarker(line)
		line = strings.TrimPrefix(strings.TrimSpace(line), "Uncertain:")
		line = strings.TrimPrefix(strings.TrimSpace(line), "Uncertainty:")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		for _, part := range strings.Split(line, ";") {
			part = strings.Trim(strings.Join(strings.Fields(part), " "), ". ")
			if part == "" || emptyCardField(part) {
				continue
			}
			items = append(items, part)
		}
	}
	return uniqueStrings(items)
}

func stripListMarker(value string) string {
	value = strings.TrimSpace(value)
	for _, marker := range []string{"- ", "* ", "• "} {
		if strings.HasPrefix(value, marker) {
			return strings.TrimSpace(strings.TrimPrefix(value, marker))
		}
	}
	for i, r := range value {
		if r < '0' || r > '9' {
			if i > 0 && (strings.HasPrefix(value[i:], ". ") || strings.HasPrefix(value[i:], ") ")) {
				return strings.TrimSpace(value[i+2:])
			}
			break
		}
	}
	return value
}

func emptyCardField(value string) bool {
	value = strings.ToLower(strings.Trim(value, " ."))
	switch value {
	case "", "none", "no", "n/a", "na", "unknown", "not applicable", "not visible", "not enough information", "no readable text", "no visible text":
		return true
	default:
		return false
	}
}

// observationsFromCard stores the card. The place phrase is kept only when
// the asset actually had location data to inform it: without GPS the model's
// Location section can only say it does not know, which is not a place.
func observationsFromCard(card photoCard, hasLocation bool) []contentObservation {
	observations := []contentObservation{
		cardObservation(modelObservationCardSummary, card.Summary),
		cardObservation(modelObservationCardDescription, card.Description),
	}
	if hasLocation && card.PlacePhrase != "" {
		observations = append(observations, cardObservation(modelObservationCardPlacePhrase, card.PlacePhrase))
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
		"place_phrase":  card.PlacePhrase,
		"ocr_text":      card.OCRText,
		"uncertainties": card.Uncertainties,
	}
}

func photoCardSearchTerms(card photoCard) []string {
	return observationTermsFromText(strings.Join([]string{
		card.Summary,
		card.Description,
		card.PlacePhrase,
		card.OCRText,
		strings.Join(card.Uncertainties, " "),
	}, " "))
}

func observationTermsFromText(value string) []string {
	terms := []string{}
	for _, part := range strings.Fields(value) {
		if term := normalizeTerm(part); term != "" {
			terms = append(terms, term)
		}
	}
	if term := normalizeTerm(value); term != "" {
		terms = append(terms, term)
	}
	return uniqueStrings(terms)
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

func truncateReason(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) <= 200 {
		return value
	}
	return strings.TrimSpace(value[:200])
}
