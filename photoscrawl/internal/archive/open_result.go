package archive

import (
	"fmt"
	"strconv"
	"strings"
)

const maxOpenObservations = 20

type OpenResult struct {
	Ref             string            `json:"ref"`
	Time            string            `json:"time,omitempty"`
	MediaType       string            `json:"media_type,omitempty"`
	Dimensions      *OpenDimensions   `json:"dimensions,omitempty"`
	DurationSeconds float64           `json:"duration_seconds,omitempty"`
	Favorite        bool              `json:"favorite,omitempty"`
	Hidden          bool              `json:"hidden,omitempty"`
	Where           string            `json:"where,omitempty"`
	Who             []string          `json:"who,omitempty"`
	LocationCount   int               `json:"location_count,omitempty"`
	Albums          []OpenAlbum       `json:"albums,omitempty"`
	Resources       []OpenResource    `json:"resources,omitempty"`
	Observations    []OpenObservation `json:"observations,omitempty"`
	Evidence        OpenEvidence      `json:"evidence"`
}

type OpenDimensions struct {
	Width  int64 `json:"width"`
	Height int64 `json:"height"`
}

type OpenAlbum struct {
	Title string `json:"title"`
	Kind  string `json:"kind,omitempty"`
}

type OpenResource struct {
	Type             string `json:"type,omitempty"`
	Filename         string `json:"filename,omitempty"`
	UTI              string `json:"uti,omitempty"`
	Bytes            int64  `json:"bytes,omitempty"`
	AvailableLocally bool   `json:"available_locally"`
	NeedsDownload    bool   `json:"needs_download"`
}

type OpenObservation struct {
	Kind        string  `json:"kind"`
	Text        string  `json:"text"`
	Confidence  float64 `json:"confidence,omitempty"`
	EvidenceRef string  `json:"evidence_ref,omitempty"`
}

type OpenEvidence struct {
	Refs []EvidenceReference `json:"refs,omitempty"`
}

type EvidenceResult struct {
	Ref      string              `json:"ref"`
	Evidence []EvidenceReference `json:"evidence"`
}

type EvidenceReference struct {
	Ref      string `json:"ref"`
	Kind     string `json:"kind"`
	Source   string `json:"source,omitempty"`
	AssetRef string `json:"asset_ref,omitempty"`
	Summary  string `json:"summary,omitempty"`
}

func newOpenResult(asset map[string]any, resources, albums, locations, visualObservations, textObservations, faceObservations, modelObservations, evidence []map[string]any) OpenResult {
	return OpenResult{
		Ref:             assetRef(rowString(asset, "id")),
		Time:            localRFC3339(rowString(asset, "creation_date")),
		MediaType:       rowString(asset, "media_type"),
		Dimensions:      openDimensions(asset),
		DurationSeconds: rowFloat(asset, "duration_seconds"),
		Favorite:        rowBool(asset, "favorite"),
		Hidden:          rowBool(asset, "hidden"),
		Where:           openWhere(modelObservations, locations),
		Who:             openWho(faceObservations),
		LocationCount:   len(locations),
		Albums:          openAlbums(albums),
		Resources:       openResources(resources),
		Observations:    openObservations(visualObservations, textObservations, faceObservations, modelObservations),
		Evidence:        OpenEvidence{Refs: openEvidenceRefs(evidence)},
	}
}

func openDimensions(asset map[string]any) *OpenDimensions {
	width := rowInt(asset, "width")
	height := rowInt(asset, "height")
	if width == 0 && height == 0 {
		return nil
	}
	return &OpenDimensions{Width: width, Height: height}
}

func openAlbums(rows []map[string]any) []OpenAlbum {
	out := []OpenAlbum{}
	for _, row := range rows {
		title := strings.TrimSpace(rowString(row, "album_title"))
		if title == "" {
			continue
		}
		out = append(out, OpenAlbum{
			Title: title,
			Kind:  rowString(row, "album_kind"),
		})
	}
	return out
}

func openResources(rows []map[string]any) []OpenResource {
	out := []OpenResource{}
	for _, row := range rows {
		out = append(out, OpenResource{
			Type:             rowString(row, "resource_type"),
			Filename:         rowString(row, "original_filename"),
			UTI:              rowString(row, "uti"),
			Bytes:            rowInt(row, "file_size"),
			AvailableLocally: rowBool(row, "available_locally"),
			NeedsDownload:    rowBool(row, "needs_download"),
		})
	}
	return out
}

func openObservations(visualRows, textRows, faceRows, modelRows []map[string]any) []OpenObservation {
	out := []OpenObservation{}
	add := func(kind, text string, confidence float64, evidenceRef string) {
		kind = strings.TrimSpace(kind)
		text = strings.TrimSpace(text)
		if kind == "" || text == "" || len(out) >= maxOpenObservations {
			return
		}
		out = append(out, OpenObservation{
			Kind:        kind,
			Text:        text,
			Confidence:  confidence,
			EvidenceRef: photoscrawlRef(evidenceRef),
		})
	}
	for _, row := range visualRows {
		add(rowString(row, "observation_type"), rowString(row, "label"), rowFloat(row, "confidence"), rowString(row, "evidence_id"))
	}
	for _, row := range textRows {
		add("text", rowString(row, "text"), rowFloat(row, "confidence"), rowString(row, "evidence_id"))
	}
	for _, row := range faceRows {
		add("face", rowString(row, "person_label"), rowFloat(row, "confidence"), rowString(row, "evidence_id"))
	}
	for _, row := range modelRows {
		add(rowString(row, "observation_type"), rowString(row, "value_text"), rowFloat(row, "confidence"), rowString(row, "evidence_id"))
	}
	return out
}

func openWho(faceRows []map[string]any) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, row := range faceRows {
		name := strings.TrimSpace(rowString(row, "person_label"))
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

func openWhere(modelRows, locationRows []map[string]any) string {
	for _, kind := range []string{"merchant_or_venue_name_candidate", "landmark_or_place_name_candidate", "place_type_candidate"} {
		if value := bestModelObservation(modelRows, kind); value != "" {
			return value
		}
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

func bestModelObservation(rows []map[string]any, kind string) string {
	bestText := ""
	bestConfidence := -1.0
	for _, row := range rows {
		if rowString(row, "observation_type") != kind {
			continue
		}
		text := strings.TrimSpace(rowString(row, "value_text"))
		if text == "" {
			continue
		}
		confidence := rowFloat(row, "confidence")
		if confidence > bestConfidence {
			bestText = text
			bestConfidence = confidence
		}
	}
	return bestText
}

func openEvidenceRefs(rows []map[string]any) []EvidenceReference {
	out := []EvidenceReference{}
	for _, row := range rows {
		ref := photoscrawlRef(rowString(row, "id"))
		kind := strings.TrimSpace(rowString(row, "evidence_kind"))
		if ref == "" || kind == "" {
			continue
		}
		source := strings.TrimSpace(rowString(row, "source"))
		out = append(out, EvidenceReference{
			Ref:      ref,
			Kind:     kind,
			Source:   source,
			AssetRef: photoscrawlRef(rowString(row, "asset_id")),
			Summary:  evidenceSummary(kind, source),
		})
	}
	return out
}

func evidenceSummary(kind, source string) string {
	kind = strings.ReplaceAll(strings.TrimSpace(kind), "_", " ")
	source = strings.TrimSpace(source)
	if kind == "" {
		return ""
	}
	if source == "" {
		return kind
	}
	return kind + " from " + source
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
	switch value := row[key].(type) {
	case float32:
		return float64(value)
	case float64:
		return value
	case int:
		return float64(value)
	case int64:
		return float64(value)
	case string:
		parsed, _ := strconv.ParseFloat(strings.TrimSpace(value), 64)
		return parsed
	default:
		return 0
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
		trimmed := strings.TrimSpace(value)
		parsed, _ := strconv.ParseBool(trimmed)
		return parsed || trimmed == "1"
	default:
		return false
	}
}
