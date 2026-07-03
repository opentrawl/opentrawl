package archive

import (
	"fmt"
	"strconv"
	"strings"
)

type OpenResult struct {
	Ref           string              `json:"ref"`
	Time          string              `json:"time,omitempty"`
	MediaType     string              `json:"media_type,omitempty"`
	Where         string              `json:"where,omitempty"`
	Summary       string              `json:"summary,omitempty"`
	Description   string              `json:"description,omitempty"`
	PlacePhrase   string              `json:"place_phrase,omitempty"`
	Uncertainties []string            `json:"uncertainties,omitempty"`
	Original      *OpenOriginal       `json:"original,omitempty"`
	Evidence      OpenEvidenceSummary `json:"evidence"`
}

type OpenOriginal struct {
	Filename     string `json:"filename,omitempty"`
	Bytes        int64  `json:"bytes,omitempty"`
	Availability string `json:"availability,omitempty"`
}

type OpenEvidenceSummary struct {
	Count int    `json:"count"`
	Ref   string `json:"ref"`
}

type EvidenceResult struct {
	Ref      string              `json:"ref"`
	Evidence []EvidenceReference `json:"evidence"`
}

type EvidenceReference struct {
	Ref      string `json:"ref"`
	Kind     string `json:"kind"`
	KindID   string `json:"kind_id,omitempty"`
	Source   string `json:"source,omitempty"`
	SourceID string `json:"source_id,omitempty"`
	AssetRef string `json:"asset_ref,omitempty"`
	Summary  string `json:"summary,omitempty"`
}

func newOpenResult(asset map[string]any, resources, locations, modelObservations, evidence []map[string]any) OpenResult {
	ref := assetRef(rowString(asset, "id"))
	card := openCard(modelObservations)
	return OpenResult{
		Ref:           ref,
		Time:          localRFC3339(rowString(asset, "creation_date")),
		MediaType:     openMediaType(rowString(asset, "media_type")),
		Where:         openWhere(card.PlacePhrase, locations),
		Summary:       card.Summary,
		Description:   card.Description,
		PlacePhrase:   card.PlacePhrase,
		Uncertainties: card.Uncertainties,
		Original:      openOriginal(resources),
		Evidence: OpenEvidenceSummary{
			Count: len(evidence),
			Ref:   ref,
		},
	}
}

func openCard(rows []map[string]any) photoCard {
	card := photoCard{}
	for _, row := range rows {
		text := strings.TrimSpace(rowString(row, "value_text"))
		if text == "" {
			continue
		}
		switch rowString(row, "observation_type") {
		case modelObservationCardSummary:
			if card.Summary == "" {
				card.Summary = text
			}
		case modelObservationCardDescription:
			if card.Description == "" {
				card.Description = text
			}
		case modelObservationCardPlacePhrase:
			if card.PlacePhrase == "" {
				card.PlacePhrase = text
			}
		case modelObservationCardUncertainty:
			card.Uncertainties = append(card.Uncertainties, text)
		}
	}
	card.Uncertainties = uniqueStrings(card.Uncertainties)
	return card
}

func openOriginal(rows []map[string]any) *OpenOriginal {
	if len(rows) == 0 {
		return nil
	}
	best := rows[0]
	bestScore := originalResourceScore(best)
	for _, row := range rows[1:] {
		if score := originalResourceScore(row); score > bestScore {
			best = row
			bestScore = score
		}
	}
	filename := strings.TrimSpace(rowString(best, "original_filename"))
	if filename == "" {
		return nil
	}
	availability := "in iCloud"
	if rowBool(best, "available_locally") && !rowBool(best, "needs_download") {
		availability = "on this Mac"
	}
	return &OpenOriginal{
		Filename:     filename,
		Bytes:        rowInt(best, "file_size"),
		Availability: availability,
	}
}

func originalResourceScore(row map[string]any) int {
	text := strings.ToLower(strings.Join([]string{
		rowString(row, "resource_type"),
		rowString(row, "original_filename"),
		rowString(row, "uti"),
	}, " "))
	score := 0
	if strings.Contains(text, "original") {
		score += 4
	}
	if strings.Contains(text, "photo") || strings.Contains(text, "image") {
		score += 2
	}
	if strings.TrimSpace(rowString(row, "original_filename")) != "" {
		score++
	}
	return score
}

func openWhere(placePhrase string, locationRows []map[string]any) string {
	if cleaned := cleanPlacePhrase(placePhrase); cleaned != "" {
		return cleaned
	}
	for _, row := range locationRows {
		lat, lon := rowFloat(row, "latitude"), rowFloat(row, "longitude")
		if lat == 0 && lon == 0 {
			continue
		}
		label := fmt.Sprintf("GPS %.4f, %.4f", lat, lon)
		if accuracy := rowFloat(row, "horizontal_accuracy"); accuracy > 0 {
			label += fmt.Sprintf(" +/-%.0fm", accuracy)
		}
		return label
	}
	return ""
}

func openMediaType(value string) string {
	switch strings.TrimSpace(value) {
	case "image":
		return "photo"
	default:
		return strings.TrimSpace(value)
	}
}

func openEvidenceRefs(rows []map[string]any) []EvidenceReference {
	out := []EvidenceReference{}
	for _, row := range rows {
		ref := photoscrawlRef(rowString(row, "id"))
		kindID := strings.TrimSpace(rowString(row, "evidence_kind"))
		if ref == "" || kindID == "" {
			continue
		}
		sourceID := strings.TrimSpace(rowString(row, "source"))
		out = append(out, EvidenceReference{
			Ref:      ref,
			Kind:     evidenceKindLabel(kindID),
			KindID:   kindID,
			Source:   evidenceSourceLabel(sourceID),
			SourceID: sourceID,
			AssetRef: photoscrawlRef(rowString(row, "asset_id")),
			Summary:  evidenceSummary(kindID, sourceID),
		})
	}
	return out
}

func evidenceKindLabel(kind string) string {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return ""
	}
	switch kind {
	case "asset_metadata":
		return "asset metadata"
	case "asset_resource":
		return "asset resource"
	case "album_membership":
		return "album membership"
	case "classification_input":
		return "classification input"
	case "content_classification":
		return "content classification"
	default:
		return strings.ReplaceAll(kind, "_", " ")
	}
}

func evidenceSourceLabel(source string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return ""
	}
	switch source {
	case "photos_sqlite_snapshot":
		return "Photos library database"
	case metadataClassifierSource:
		return "Photo metadata"
	case modelClassifierSource:
		return "Photo card"
	default:
		return strings.ReplaceAll(source, "_", " ")
	}
}

func evidenceSummary(kind, source string) string {
	switch strings.TrimSpace(kind) {
	case "asset_metadata":
		return "details from the Photos library database"
	case "asset_resource":
		return "file resource details from the Photos library database"
	case "album_membership":
		return "album membership from the Photos library database"
	case "classification_input":
		return "derived from photo metadata"
	case "content_classification":
		return "derived from model analysis"
	default:
		kindLabel := evidenceKindLabel(kind)
		sourceLabel := evidenceSourceLabel(source)
		if kindLabel == "" {
			return ""
		}
		if sourceLabel == "" {
			return kindLabel
		}
		return kindLabel + " from " + sourceLabel
	}
}

func rowString(row map[string]any, key string) string {
	if row == nil {
		return ""
	}
	switch value := row[key].(type) {
	case string:
		return value
	case fmt.Stringer:
		return value.String()
	case nil:
		return ""
	default:
		return fmt.Sprint(value)
	}
}

func rowInt(row map[string]any, key string) int64 {
	switch value := row[key].(type) {
	case int:
		return int64(value)
	case int64:
		return value
	case float64:
		return int64(value)
	case string:
		parsed, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		return parsed
	default:
		return 0
	}
}

func rowFloat(row map[string]any, key string) float64 {
	value := rowOptionalFloat(row, key)
	if value == nil {
		return 0
	}
	return *value
}

func rowOptionalFloat(row map[string]any, key string) *float64 {
	switch value := row[key].(type) {
	case float32:
		parsed := float64(value)
		return &parsed
	case float64:
		return &value
	case int:
		parsed := float64(value)
		return &parsed
	case int64:
		parsed := float64(value)
		return &parsed
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil {
			return nil
		}
		return &parsed
	default:
		return nil
	}
}

func rowBool(row map[string]any, key string) bool {
	switch value := row[key].(type) {
	case bool:
		return value
	case int:
		return value != 0
	case int64:
		return value != 0
	case float64:
		return value != 0
	case string:
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "1", "true", "yes":
			return true
		default:
			return false
		}
	default:
		return false
	}
}
