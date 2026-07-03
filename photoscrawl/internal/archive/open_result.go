package archive

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/openclaw/photoscrawl/internal/cardformat"
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
	KnownPlace      *OpenKnownPlace      `json:"known_place,omitempty"`
	Venue           *OpenVenue           `json:"venue,omitempty"`
	VenueCandidates []OpenVenueCandidate `json:"venue_candidates,omitempty"`
	Camera          *OpenCamera          `json:"camera,omitempty"`
	Albums          []OpenAlbum          `json:"albums,omitempty"`
	Original        *OpenOriginal        `json:"original,omitempty"`
	Flags           []string             `json:"flags,omitempty"`
}

type OpenCaptured struct {
	Local    string `json:"local"`
	Timezone string `json:"timezone,omitempty"`
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
}

type OpenVenue struct {
	Name           string  `json:"name"`
	Category       string  `json:"category,omitempty"`
	Tier           string  `json:"tier"`
	DistanceMeters float64 `json:"distance_meters,omitempty"`
}

type OpenKnownPlace struct {
	Kind  string `json:"kind"`
	Name  string `json:"name"`
	After bool   `json:"after,omitempty"`
}

type OpenVenueCandidate struct {
	Name           string  `json:"name"`
	Category       string  `json:"category,omitempty"`
	Tier           string  `json:"tier,omitempty"`
	DistanceMeters float64 `json:"distance_meters,omitempty"`
}

type OpenCamera struct {
	Display         string  `json:"display,omitempty"`
	Make            string  `json:"make,omitempty"`
	Model           string  `json:"model,omitempty"`
	LensModel       string  `json:"lens_model,omitempty"`
	FocalLengthMM   float64 `json:"focal_length_mm,omitempty"`
	FocalLength35MM float64 `json:"focal_length_35mm,omitempty"`
	Aperture        float64 `json:"aperture,omitempty"`
	ShutterSpeed    string  `json:"shutter_speed,omitempty"`
	ISO             int64   `json:"iso,omitempty"`
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
	knownPlace := openKnownPlace(placeObservations)
	venue := openVenue(placeObservations)
	venueCandidates := openVenueCandidates(placeObservations)
	if knownPlace != nil {
		venue = nil
		venueCandidates = nil
	}
	return OpenResult{
		SchemaVersion: 3,
		Ref:           assetRef(rowString(asset, "id")),
		Mechanical: OpenMechanical{
			Captured:        openCaptured(asset),
			Media:           openMedia(asset),
			GPS:             openGPS(locations),
			Address:         openAddress(placeObservations),
			KnownPlace:      knownPlace,
			Venue:           venue,
			VenueCandidates: venueCandidates,
			Camera:          openCamera(asset),
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
		Timezone: displayTimezoneName(timezoneName),
	}
}

func displayTimezoneName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if _, err := time.LoadLocation(value); err != nil {
		return ""
	}
	return value
}

func openMedia(asset map[string]any) *OpenMedia {
	return &OpenMedia{
		Kind:            openMediaKind(rowString(asset, "media_type"), rowString(asset, "media_subtypes")),
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
			Latitude:                 cardformat.Coordinate(lat),
			Longitude:                cardformat.Coordinate(lon),
			HorizontalAccuracyMeters: cardformat.Meters(rowFloat(row, "horizontal_accuracy")),
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

func openKnownPlace(rows []map[string]any) *OpenKnownPlace {
	for _, row := range rows {
		if rowString(row, "observation_type") != knownPlaceObservationType {
			continue
		}
		var value map[string]any
		if json.Unmarshal([]byte(rowString(row, "value_json")), &value) != nil {
			continue
		}
		knownPlace := &OpenKnownPlace{
			Kind: mapText(value, "kind"),
			Name: mapText(value, "name"),
		}
		if after, ok := value["after"].(bool); ok {
			knownPlace.After = after
		}
		if knownPlace.Kind != "" && knownPlace.Name != "" {
			return knownPlace
		}
	}
	return nil
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
			DistanceMeters: cardformat.Meters(rowFloat(row, "distance_meters")),
		}
		var value map[string]any
		if json.Unmarshal([]byte(rowString(row, "value_json")), &value) == nil {
			venue.Category = cardformat.NormalizePOICategory(mapText(value, "category"))
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
	for _, candidate := range topPOICandidates(placeCandidateRows(rows)) {
		candidates = append(candidates, openVenueCandidate(candidate))
	}
	return candidates
}

func openCamera(asset map[string]any) *OpenCamera {
	camera := cardformat.Camera{
		Make:            rowString(asset, "camera_make"),
		Model:           rowString(asset, "camera_model"),
		LensModel:       rowString(asset, "lens_model"),
		FocalLengthMM:   cardformat.FocalLength(rowFloat(asset, "focal_length_mm")),
		FocalLength35MM: cardformat.Meters(rowFloat(asset, "focal_length_35mm")),
		Aperture:        cardformat.Aperture(rowFloat(asset, "aperture")),
		ShutterSpeed:    rowFloat(asset, "shutter_speed"),
		ISO:             rowInt(asset, "iso"),
	}
	display := cardformat.CameraDisplay(camera)
	if display == "" && strings.TrimSpace(camera.LensModel) == "" {
		return nil
	}
	open := &OpenCamera{
		Display:         display,
		Make:            strings.TrimSpace(camera.Make),
		Model:           strings.TrimSpace(camera.Model),
		LensModel:       strings.TrimSpace(camera.LensModel),
		FocalLengthMM:   camera.FocalLengthMM,
		FocalLength35MM: camera.FocalLength35MM,
		Aperture:        camera.Aperture,
		ShutterSpeed:    cardformat.ShutterSpeedLabel(camera.ShutterSpeed),
		ISO:             camera.ISO,
	}
	if open.Display == "" && open.Make == "" && open.Model == "" && open.LensModel == "" &&
		open.FocalLengthMM == 0 && open.FocalLength35MM == 0 && open.Aperture == 0 &&
		open.ShutterSpeed == "" && open.ISO == 0 {
		return nil
	}
	return open
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

// openMediaKind names what the file is in words: "live photo", "screenshot",
// "panorama" say more than "photo" when Apple recorded the distinction.
func openMediaKind(mediaType, subtypes string) string {
	kind := openMediaType(mediaType)
	for _, subtype := range splitSubtypes(subtypes) {
		switch subtype {
		case "live_photo":
			return "live photo"
		case "screenshot":
			return "screenshot"
		case "panorama":
			return "panorama"
		case "time_lapse":
			return "time lapse"
		case "slow_motion":
			return "slow motion video"
		}
	}
	return kind
}
