package archive

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	locationwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/location"
)

type OpenResult struct {
	Ref        string         `json:"ref"`
	Stale      *OpenStale     `json:"stale,omitempty"`
	Mechanical OpenMechanical `json:"mechanical"`
}

type OpenStale struct {
	Since  string `json:"since"`
	Reason string `json:"reason"`
	Banner string `json:"banner"`
}

type OpenMechanical struct {
	Source                  OpenSource                          `json:"source"`
	Captured                *OpenCaptured                       `json:"captured,omitempty"`
	Media                   *OpenMedia                          `json:"media,omitempty"`
	GPS                     *OpenGPS                            `json:"gps,omitempty"`
	CaptureLocationEvidence *locationwire.PhotoLocationBriefing `json:"capture_location_evidence,omitempty"`
	Camera                  *OpenCamera                         `json:"camera,omitempty"`
	Albums                  []OpenAlbum                         `json:"albums,omitempty"`
	Original                *OpenOriginal                       `json:"original,omitempty"`
	Filenames               []string                            `json:"-"`
	Flags                   []string                            `json:"flags,omitempty"`
}

type OpenSource struct {
	State           string `json:"state"`
	FirstMissingAt  string `json:"first_missing_at,omitempty"`
	SourceDeletedAt string `json:"source_deleted_at,omitempty"`
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
}

type OpenOriginal struct {
	Filename     string `json:"filename,omitempty"`
	Bytes        int64  `json:"bytes,omitempty"`
	Availability string `json:"availability,omitempty"`
}

func newOpenResult(asset map[string]any, resources, locations, albums []map[string]any) OpenResult {
	return OpenResult{
		Ref: AssetRef(rowString(asset, "id")),
		Mechanical: OpenMechanical{
			Source:    openSource(asset),
			Captured:  openCaptured(asset),
			Media:     openMedia(asset),
			GPS:       openGPS(locations),
			Camera:    openCamera(asset),
			Albums:    openAlbums(albums),
			Original:  openOriginal(resources),
			Filenames: openResourceNames(resources),
			Flags:     openFlags(asset),
		},
	}
}

func openResourceNames(rows []map[string]any) []string {
	values := make([]string, 0, len(rows))
	for _, row := range rows {
		if name := strings.TrimSpace(rowString(row, "original_filename")); name != "" {
			values = append(values, name)
		}
	}
	return values
}

func openSource(asset map[string]any) OpenSource {
	state := strings.TrimSpace(rowString(asset, "source_state"))
	if state == "" {
		state = sourceStateCurrent
	}
	return OpenSource{
		State:           state,
		FirstMissingAt:  strings.TrimSpace(rowString(asset, "first_missing_at")),
		SourceDeletedAt: strings.TrimSpace(rowString(asset, "source_deleted_at")),
	}
}

func openStale(groups ...[]map[string]any) *OpenStale {
	since := ""
	reason := ""
	for _, rows := range groups {
		for _, row := range rows {
			rowSince := strings.TrimSpace(rowString(row, "stale_since"))
			if rowSince == "" {
				continue
			}
			if since != "" && rowSince >= since {
				continue
			}
			since = rowSince
			reason = strings.TrimSpace(rowString(row, "stale_reason"))
		}
	}
	if since == "" {
		return nil
	}
	return &OpenStale{
		Since:  since,
		Reason: staleCardReason(reason),
		Banner: StaleCardBanner(since, reason),
	}
}

func StaleCardBanner(since, reason string) string {
	return "Card status: Stale · " + staleCardReason(reason) + " · since " + staleCardSince(since)
}

func staleCardReason(string) string {
	return "source details changed after this card was created"
}

func staleCardSince(value string) string {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return strings.TrimSpace(value)
	}
	return parsed.Format("2 January 2006")
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
	if captureLocation(value) == nil {
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
			Latitude:                 roundPhotoPresentationNumber(lat, 5),
			Longitude:                roundPhotoPresentationNumber(lon, 5),
			HorizontalAccuracyMeters: roundPhotoPresentationNumber(rowFloat(row, "horizontal_accuracy"), 0),
		}
	}
	return nil
}

func openCamera(asset map[string]any) *OpenCamera {
	makeName := strings.TrimSpace(rowString(asset, "camera_make"))
	modelName := strings.TrimSpace(rowString(asset, "camera_model"))
	lensModel := strings.TrimSpace(rowString(asset, "lens_model"))
	display := strings.TrimSpace(strings.Join([]string{makeName, modelName}, " "))
	if display == "" && lensModel == "" {
		return nil
	}
	open := &OpenCamera{
		Display:         display,
		Make:            makeName,
		Model:           modelName,
		LensModel:       lensModel,
		FocalLengthMM:   roundPhotoPresentationNumber(rowFloat(asset, "focal_length_mm"), 2),
		FocalLength35MM: roundPhotoPresentationNumber(rowFloat(asset, "focal_length_35mm"), 0),
		Aperture:        roundPhotoPresentationNumber(rowFloat(asset, "aperture"), 1),
		ShutterSpeed:    formatPhotoShutterSpeed(rowFloat(asset, "shutter_speed")),
		ISO:             rowInt(asset, "iso"),
	}
	if open.Display == "" && open.Make == "" && open.Model == "" && open.LensModel == "" &&
		open.FocalLengthMM == 0 && open.FocalLength35MM == 0 && open.Aperture == 0 &&
		open.ShutterSpeed == "" && open.ISO == 0 {
		return nil
	}
	return open
}

func roundPhotoPresentationNumber(value float64, decimalPlaces int) float64 {
	if value == 0 {
		return 0
	}
	scale := math.Pow10(decimalPlaces)
	return math.Round(value*scale) / scale
}

func formatPhotoShutterSpeed(value float64) string {
	if value <= 0 {
		return ""
	}
	seconds := value
	if value >= 32 {
		seconds = 1 / value
	} else if value > 1 {
		seconds = 1 / math.Pow(2, value)
	}
	if seconds >= 1 {
		return fmt.Sprintf("%.1fs", roundPhotoPresentationNumber(seconds, 1))
	}
	return fmt.Sprintf("1/%.0fs", math.Round(1/seconds))
}

func openAlbums(rows []map[string]any) []OpenAlbum {
	seen := map[string]bool{}
	out := []OpenAlbum{}
	for _, row := range rows {
		title := strings.TrimSpace(rowString(row, "album_title"))
		if title == "" || seen[title] {
			continue
		}
		seen[title] = true
		out = append(out, OpenAlbum{Title: title})
	}
	return out
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
	return originalResourceTextScore(rowString(row, "resource_type"), rowString(row, "original_filename"), rowString(row, "uti"))
}

func originalResourceTextScore(resourceType, filename, uti string) int {
	text := strings.ToLower(strings.Join([]string{resourceType, filename, uti}, " "))
	score := 0
	if strings.Contains(text, "original") {
		score += 4
	}
	if strings.Contains(text, "photo") || strings.Contains(text, "image") {
		score += 2
	}
	if strings.TrimSpace(filename) != "" {
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
	available := map[string]bool{}
	for _, subtype := range splitSubtypes(subtypes) {
		available[subtype] = true
	}
	for _, candidate := range []struct{ subtype, display string }{
		{"live_photo", "live photo"},
		{"screenshot", "screenshot"},
		{"panorama", "panorama"},
		{"time_lapse", "time lapse"},
		{"slow_motion", "slow motion video"},
	} {
		if available[candidate.subtype] {
			return candidate.display
		}
	}
	return kind
}

func captureLocation(name string) *time.Location {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	if location, err := time.LoadLocation(name); err == nil {
		return location
	}
	prefix := ""
	if strings.HasPrefix(name, "GMT") || strings.HasPrefix(name, "UTC") {
		prefix = name[:3]
	}
	remainder := strings.TrimPrefix(name, prefix)
	if prefix == "" || len(remainder) != 5 || (remainder[0] != '+' && remainder[0] != '-') {
		return nil
	}
	hours, hoursErr := strconv.Atoi(remainder[1:3])
	minutes, minutesErr := strconv.Atoi(remainder[3:5])
	if hoursErr != nil || minutesErr != nil || hours > 14 || minutes > 59 {
		return nil
	}
	offset := hours*3600 + minutes*60
	if remainder[0] == '-' {
		offset = -offset
	}
	return time.FixedZone(name, offset)
}

func localCaptureTime(value, timezoneName string) string {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return strings.TrimSpace(value)
	}
	if location := captureLocation(timezoneName); location != nil {
		return parsed.In(location).Format(time.RFC3339)
	}
	return parsed.UTC().Format(time.RFC3339)
}

func splitSubtypes(value string) []string {
	names := map[string]string{"kind_subtype:1": "panorama", "kind_subtype:2": "live_photo", "kind_subtype:10": "screenshot", "kind_subtype:100": "video_streamed", "kind_subtype:101": "time_lapse", "kind_subtype:102": "slow_motion"}
	result := []string{}
	for _, part := range strings.FieldsFunc(value, func(character rune) bool { return character == ',' || character == ';' || character == '|' }) {
		if name, found := names[strings.TrimSpace(part)]; found {
			result = append(result, name)
		}
	}
	return result
}
