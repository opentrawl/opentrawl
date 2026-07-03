package archive

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type OpenResult struct {
	SchemaVersion int            `json:"schema_version"`
	Ref           string         `json:"ref"`
	Mechanical    OpenMechanical `json:"mechanical"`
	Model         OpenModel      `json:"model,omitempty"`
}

type OpenMechanical struct {
	Captured        *OpenCaptured        `json:"captured,omitempty"`
	Media           *OpenMedia           `json:"media,omitempty"`
	GPS             *OpenGPS             `json:"gps,omitempty"`
	Address         string               `json:"address,omitempty"`
	Venue           *OpenVenue           `json:"venue,omitempty"`
	VenueCandidates []OpenVenueCandidate `json:"venue_candidates,omitempty"`
	Albums          []OpenAlbum          `json:"albums,omitempty"`
	Original        *OpenOriginal        `json:"original,omitempty"`
	Flags           []string             `json:"flags,omitempty"`
}

type OpenCaptured struct {
	Local    string `json:"local"`
	Timezone string `json:"timezone,omitempty"`
	Source   string `json:"source"`
}

type OpenMedia struct {
	Kind            string  `json:"kind,omitempty"`
	Width           int64   `json:"width,omitempty"`
	Height          int64   `json:"height,omitempty"`
	DurationSeconds float64 `json:"duration_seconds,omitempty"`
}

type OpenGPS struct {
	Latitude                 float64 `json:"latitude"`
	Longitude                float64 `json:"longitude"`
	HorizontalAccuracyMeters float64 `json:"horizontal_accuracy_meters,omitempty"`
	Source                   string  `json:"source"`
}

type OpenVenue struct {
	Name           string  `json:"name"`
	Category       string  `json:"category,omitempty"`
	Tier           string  `json:"tier"`
	DistanceMeters float64 `json:"distance_meters,omitempty"`
	Source         string  `json:"source,omitempty"`
}

type OpenVenueCandidate struct {
	Name              string                 `json:"name"`
	Category          string                 `json:"category,omitempty"`
	Tier              string                 `json:"tier,omitempty"`
	DistanceMeters    float64                `json:"distance_meters,omitempty"`
	Source            string                 `json:"source,omitempty"`
	VenuePlausibility *OpenVenuePlausibility `json:"venue_plausibility,omitempty"`
}

type OpenVenuePlausibility struct {
	CandidateName string `json:"candidate,omitempty"`
	Verdict       string `json:"verdict"`
	Reason        string `json:"reason,omitempty"`
}

type OpenAlbum struct {
	Title string `json:"title"`
	Kind  string `json:"kind,omitempty"`
}

type OpenOriginal struct {
	Filename     string `json:"filename,omitempty"`
	Bytes        int64  `json:"bytes,omitempty"`
	Availability string `json:"availability,omitempty"`
}

type OpenModel struct {
	PromptVersion string   `json:"prompt_version,omitempty"`
	ModelID       string   `json:"model_id,omitempty"`
	Summary       string   `json:"summary,omitempty"`
	Description   string   `json:"description,omitempty"`
	OCRText       string   `json:"ocr_text,omitempty"`
	Uncertainties []string `json:"uncertainties,omitempty"`
}

func newOpenResult(asset map[string]any, resources, locations, albums, modelObservations, placeObservations []map[string]any) OpenResult {
	return OpenResult{
		SchemaVersion: 3,
		Ref:           assetRef(rowString(asset, "id")),
		Mechanical: OpenMechanical{
			Captured:        openCaptured(asset),
			Media:           openMedia(asset),
			GPS:             openGPS(locations),
			Address:         openAddress(placeObservations),
			Venue:           openVenue(placeObservations),
			VenueCandidates: openVenueCandidates(placeObservations),
			Albums:          openAlbums(albums),
			Original:        openOriginal(resources),
			Flags:           openFlags(asset),
		},
		Model: openModel(modelObservations),
	}
}

func openCaptured(asset map[string]any) *OpenCaptured {
	created := strings.TrimSpace(rowString(asset, "creation_date"))
	if created == "" {
		return nil
	}
	timezoneName := strings.TrimSpace(rowString(asset, "timezone_name"))
	return &OpenCaptured{
		Local:    localCaptureTime(created, timezoneName),
		Timezone: timezoneName,
		Source:   "apple_photos",
	}
}

func openMedia(asset map[string]any) *OpenMedia {
	return &OpenMedia{
		Kind:            openMediaType(rowString(asset, "media_type")),
		Width:           rowInt(asset, "width"),
		Height:          rowInt(asset, "height"),
		DurationSeconds: rowFloat(asset, "duration_seconds"),
	}
}

func openGPS(rows []map[string]any) *OpenGPS {
	for _, row := range rows {
		lat, lon := rowFloat(row, "latitude"), rowFloat(row, "longitude")
		if lat == 0 && lon == 0 {
			continue
		}
		return &OpenGPS{
			Latitude:                 lat,
			Longitude:                lon,
			HorizontalAccuracyMeters: rowFloat(row, "horizontal_accuracy"),
			Source:                   rowString(row, "source"),
		}
	}
	return nil
}

func openAddress(rows []map[string]any) string {
	for _, row := range rows {
		if rowString(row, "observation_type") == "address" {
			return strings.TrimSpace(rowString(row, "value_text"))
		}
	}
	return ""
}

func openVenue(rows []map[string]any) *OpenVenue {
	candidates := []OpenVenue{}
	for _, row := range rows {
		if rowString(row, "observation_type") != "venue" {
			continue
		}
		tier := rowString(row, "tier")
		if tier != "confirmed_venue" && tier != "venue_candidate" {
			continue
		}
		venue := OpenVenue{
			Name:           rowString(row, "value_text"),
			Tier:           tier,
			DistanceMeters: rowFloat(row, "distance_meters"),
			Source:         rowString(row, "provider"),
		}
		var value map[string]any
		if json.Unmarshal([]byte(rowString(row, "value_json")), &value) == nil {
			venue.Category = mapText(value, "category")
		}
		candidates = append(candidates, venue)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Tier != candidates[j].Tier {
			return candidates[i].Tier == "confirmed_venue"
		}
		return candidates[i].DistanceMeters < candidates[j].DistanceMeters
	})
	if len(candidates) == 0 {
		return nil
	}
	return &candidates[0]
}

func openVenueCandidates(rows []map[string]any) []OpenVenueCandidate {
	candidates := []OpenVenueCandidate{}
	for _, row := range rows {
		if rowString(row, "observation_type") != "poi_candidate" {
			continue
		}
		candidate := OpenVenueCandidate{
			Name:           rowString(row, "value_text"),
			Tier:           rowString(row, "tier"),
			DistanceMeters: rowFloat(row, "distance_meters"),
			Source:         rowString(row, "provider"),
		}
		var value map[string]any
		if json.Unmarshal([]byte(rowString(row, "value_json")), &value) == nil {
			candidate.Category = mapText(value, "category")
			if source := mapText(value, "source"); source != "" {
				candidate.Source = source
			}
			if distance := mapFloat(value, "distance_m"); distance > 0 {
				candidate.DistanceMeters = distance
			}
			if raw, ok := value["venue_plausibility"].(map[string]any); ok {
				candidate.VenuePlausibility = &OpenVenuePlausibility{
					CandidateName: mapText(raw, "candidate"),
					Verdict:       mapText(raw, "verdict"),
					Reason:        mapText(raw, "reason"),
				}
			}
		}
		if candidate.Name != "" {
			candidates = append(candidates, candidate)
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Tier != candidates[j].Tier {
			return venueTierRank(candidates[i].Tier) < venueTierRank(candidates[j].Tier)
		}
		return candidates[i].DistanceMeters < candidates[j].DistanceMeters
	})
	return candidates
}

func venueTierRank(tier string) int {
	switch tier {
	case "confirmed_venue":
		return 0
	case "venue_candidate":
		return 1
	case "nearby_poi":
		return 2
	default:
		return 3
	}
}

func openAlbums(rows []map[string]any) []OpenAlbum {
	out := []OpenAlbum{}
	for _, row := range rows {
		title := strings.TrimSpace(rowString(row, "album_title"))
		if title == "" {
			continue
		}
		out = append(out, OpenAlbum{Title: title, Kind: rowString(row, "album_kind")})
	}
	return out
}

func openModel(rows []map[string]any) OpenModel {
	model := OpenModel{}
	for _, row := range rows {
		text := strings.TrimSpace(rowString(row, "value_text"))
		if text == "" {
			continue
		}
		if model.ModelID == "" {
			model.ModelID = rowString(row, "model_id")
		}
		if model.PromptVersion == "" {
			model.PromptVersion = rowString(row, "prompt_version")
		}
		switch rowString(row, "observation_type") {
		case modelObservationCardSummary:
			if model.Summary == "" {
				model.Summary = text
			}
		case modelObservationCardDescription:
			if model.Description == "" {
				model.Description = text
			}
		case modelObservationCardOCR:
			if model.OCRText == "" {
				model.OCRText = text
			}
		case modelObservationCardUncertainty:
			model.Uncertainties = append(model.Uncertainties, text)
		}
	}
	model.Uncertainties = uniqueStrings(model.Uncertainties)
	return model
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

func openFlags(asset map[string]any) []string {
	flags := []string{}
	if rowBool(asset, "favorite") {
		flags = append(flags, "favourite")
	}
	if rowBool(asset, "hidden") {
		flags = append(flags, "hidden")
	}
	if strings.TrimSpace(rowString(asset, "burst_identifier")) != "" {
		flags = append(flags, "burst member")
	}
	return flags
}

func openMediaType(value string) string {
	switch strings.TrimSpace(value) {
	case "image":
		return "photo"
	default:
		return strings.TrimSpace(value)
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
	default:
		if value == nil {
			return ""
		}
		return fmt.Sprint(value)
	}
}

func rowInt(row map[string]any, key string) int64 {
	if row == nil {
		return 0
	}
	switch value := row[key].(type) {
	case int64:
		return value
	case int:
		return int64(value)
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
	if row == nil {
		return 0
	}
	switch value := row[key].(type) {
	case float64:
		return value
	case float32:
		return float64(value)
	case int64:
		return float64(value)
	case int:
		return float64(value)
	case string:
		parsed, _ := strconv.ParseFloat(strings.TrimSpace(value), 64)
		return parsed
	default:
		return 0
	}
}

func rowBool(row map[string]any, key string) bool {
	if row == nil {
		return false
	}
	switch value := row[key].(type) {
	case bool:
		return value
	case int64:
		return value != 0
	case int:
		return value != 0
	case float64:
		return value != 0
	case string:
		return value == "1" || strings.EqualFold(value, "true")
	default:
		return false
	}
}

func mapText(row map[string]any, key string) string {
	if value, ok := row[key].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func mapFloat(row map[string]any, key string) float64 {
	if row == nil {
		return 0
	}
	switch value := row[key].(type) {
	case float64:
		return value
	case float32:
		return float64(value)
	case int64:
		return float64(value)
	case int:
		return float64(value)
	case string:
		parsed, _ := strconv.ParseFloat(strings.TrimSpace(value), 64)
		return parsed
	default:
		return 0
	}
}
