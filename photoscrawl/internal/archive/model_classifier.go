package archive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/openclaw/photoscrawl/internal/cardformat"
	"github.com/openclaw/photoscrawl/internal/modelclient"
	repoPrompts "github.com/openclaw/photoscrawl/prompts"
)

const (
	modelClassifierSource = "photo_card"
	modelPromptVersion    = repoPrompts.PhotoCardVersion
)

type modelClassifier struct {
	modelID       string
	promptVersion string
	baseURL       string
	client        *modelclient.Client
}

func newModelClassifier(modelID, baseURL, bearerKeyEnv string) modelClassifier {
	return modelClassifier{
		modelID:       strings.TrimSpace(modelID),
		promptVersion: modelPromptVersion,
		baseURL:       modelclient.NormalizeBaseURL(baseURL),
		client: modelclient.New(modelclient.Config{
			BaseURL:      baseURL,
			Model:        modelID,
			BearerKeyEnv: bearerKeyEnv,
		}),
	}
}

func (c modelClassifier) classify(ctx context.Context, input classifyInput, imagePath string) (modelResult, error) {
	data, err := os.ReadFile(imagePath)
	if err != nil {
		return modelResult{}, fmt.Errorf("read image: %w", err)
	}
	sum := sha256.Sum256(data)
	prompt, err := renderPhotoCardPrompt(repoPrompts.PhotoCardV3, input)
	if err != nil {
		return modelResult{}, fmt.Errorf("render photo card prompt: %w", err)
	}
	response, err := c.client.Generate(ctx, modelclient.Request{
		Prompt: prompt,
		Images: []modelclient.Image{{
			Data:     data,
			MIMEType: mimeTypeForPath(imagePath),
		}},
		Temperature: 0.1,
	})
	if err != nil {
		return modelResult{}, err
	}
	card, err := parsePhotoCard(response.Text)
	if err != nil {
		return modelResult{}, err
	}
	payload := photoCardPayload(card)
	return modelResult{
		Payload:           payload,
		RawResponse:       response.Text,
		ImageBytes:        int64(len(data)),
		ImageSHA256:       hex.EncodeToString(sum[:]),
		VenuePlausibility: card.VenuePlausibility,
		Observations:      observationsFromCard(card),
		SearchTerms:       photoCardSearchTerms(card),
	}, nil
}

func renderPhotoCardPrompt(promptText string, input classifyInput) (string, error) {
	metadataJSON, err := photoCardMetadataJSON(input)
	if err != nil {
		return "", err
	}
	tmpl, err := template.New("photo-card").Option("missingkey=error").Parse(promptText)
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, map[string]string{"MetadataJSON": string(metadataJSON)}); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.String()), nil
}

func photoCardMetadataJSON(input classifyInput) ([]byte, error) {
	albums := make([]map[string]any, 0, len(input.Albums))
	for _, album := range input.Albums {
		albums = append(albums, map[string]any{
			"title": album.AlbumTitle,
			"kind":  album.AlbumKind,
		})
	}
	media := map[string]any{
		"kind":   openMediaType(input.MediaType),
		"width":  input.Width,
		"height": input.Height,
	}
	if subtypes := splitSubtypes(input.MediaSubtypes); len(subtypes) > 0 {
		media["subtypes"] = subtypes
	}
	if input.DurationSeconds > 0 {
		media["duration_seconds"] = input.DurationSeconds
	}
	library := map[string]any{
		"original": input.originalContext(),
	}
	if len(albums) > 0 {
		library["albums"] = albums
	}
	if input.Favorite {
		library["favorite"] = true
	}
	if input.Hidden {
		library["hidden"] = true
	}
	if strings.TrimSpace(input.BurstIdentifier) != "" {
		library["burst_member"] = true
	}
	payload := map[string]any{
		"capture":         captureContext(input),
		"media":           media,
		"library_context": library,
	}
	if input.HasLocation {
		location := map[string]any{
			"gps": map[string]any{
				"latitude":                   cardformat.Coordinate(input.Latitude),
				"longitude":                  cardformat.Coordinate(input.Longitude),
				"horizontal_accuracy_meters": cardformat.Meters(input.AccuracyMeters),
			},
		}
		if input.Place != nil {
			location["place_context"] = input.placeContextForPrompt()
		}
		if input.KnownPlace != nil {
			location["known_place"] = map[string]any{
				"name":         input.KnownPlace.Name,
				"relationship": knownPlaceRelationship(*input.KnownPlace),
			}
		}
		payload["location"] = location
	}
	if camera := input.cameraContext(); len(camera) > 0 {
		payload["camera"] = camera
	}
	return json.MarshalIndent(payload, "", "  ")
}

func captureContext(input classifyInput) map[string]any {
	capture := map[string]any{
		"local_time": localCaptureTime(input.CreationDate, input.timezoneName()),
	}
	if zone := input.timezoneName(); zone != "local" {
		capture["timezone"] = zone
	}
	return capture
}

func (input classifyInput) timezoneName() string {
	name := strings.TrimSpace(input.TimezoneName)
	if name == "" || strings.HasPrefix(name, "GMT") || strings.HasPrefix(name, "UTC") {
		return "local"
	}
	return name
}

func localCaptureTime(value, timezoneName string) string {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return strings.TrimSpace(value)
	}
	if timezoneName != "" && timezoneName != "local" {
		if loc, err := time.LoadLocation(timezoneName); err == nil {
			return parsed.In(loc).Format(time.RFC3339)
		}
	}
	return parsed.Local().Format(time.RFC3339)
}

// splitSubtypes turns Photos' numeric kind subtypes into words a reader (and
// the model) can use; unknown codes carry no meaning and are dropped.
func splitSubtypes(value string) []string {
	names := map[string]string{
		"kind_subtype:1":   "panorama",
		"kind_subtype:2":   "live_photo",
		"kind_subtype:10":  "screenshot",
		"kind_subtype:100": "video_streamed",
		"kind_subtype:101": "time_lapse",
		"kind_subtype:102": "slow_motion",
	}
	out := []string{}
	for _, part := range strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '|'
	}) {
		part = strings.TrimSpace(part)
		if name, ok := names[part]; ok {
			out = append(out, name)
		}
	}
	return out
}

func (input classifyInput) originalContext() map[string]any {
	best := input.Resources
	if len(best) == 0 {
		return nil
	}
	resource := best[0]
	for _, candidate := range input.Resources[1:] {
		if originalResourceScore(map[string]any{
			"resource_type":     candidate.ResourceType,
			"original_filename": candidate.OriginalFilename,
			"uti":               candidate.UTI,
		}) > originalResourceScore(map[string]any{
			"resource_type":     resource.ResourceType,
			"original_filename": resource.OriginalFilename,
			"uti":               resource.UTI,
		}) {
			resource = candidate
		}
	}
	original := map[string]any{
		"availability": resource.Availability(),
		"bytes":        resource.FileSize,
	}
	if filename := strings.TrimSpace(resource.OriginalFilename); filename != "" {
		original["filename"] = filename
	}
	return original
}

func (resource classifyResource) Availability() string {
	if resource.AvailableLocally && !resource.NeedsDownload {
		return "local"
	}
	if resource.NeedsDownload {
		return "in_icloud"
	}
	return ""
}

func (input classifyInput) placeContextForPrompt() map[string]any {
	result := input.Place.Result
	candidates := []map[string]any{}
	if input.KnownPlace == nil {
		for _, candidate := range topPOICandidates(applyVenuePlausibility(result.POICandidates, venuePlausibility{})) {
			candidates = append(candidates, promptVenueCandidate(candidate))
		}
	}
	area := []map[string]string{}
	for _, level := range result.Area {
		area = append(area, map[string]string{"level": level.Level, "name": level.Name})
	}
	return map[string]any{
		"address_line":     addressLine(result.Address),
		"area":             area,
		"poi_status":       result.POIStatus,
		"venue_candidates": candidates,
	}
}

func (input classifyInput) cameraContext() map[string]any {
	camera := cardformat.Camera{
		Make:            input.CameraMake,
		Model:           input.CameraModel,
		LensModel:       input.LensModel,
		FocalLengthMM:   cardformat.FocalLength(input.FocalLengthMM),
		FocalLength35MM: cardformat.Meters(input.FocalLength35MM),
		Aperture:        cardformat.Aperture(input.Aperture),
		ShutterSpeed:    input.ShutterSpeed,
		ISO:             input.ISO,
	}
	out := map[string]any{}
	if display := cardformat.CameraDisplay(camera); display != "" {
		out["display"] = display
	}
	if value := strings.TrimSpace(camera.Make); value != "" {
		out["make"] = value
	}
	if value := strings.TrimSpace(camera.Model); value != "" {
		out["model"] = value
	}
	if value := strings.TrimSpace(camera.LensModel); value != "" {
		out["lens_model"] = value
	}
	if camera.FocalLengthMM > 0 {
		out["focal_length_mm"] = camera.FocalLengthMM
	}
	if camera.FocalLength35MM > 0 {
		out["focal_length_35mm"] = camera.FocalLength35MM
	}
	if camera.Aperture > 0 {
		out["aperture"] = camera.Aperture
	}
	if shutter := cardformat.ShutterSpeedLabel(camera.ShutterSpeed); shutter != "" {
		out["shutter_speed"] = shutter
	}
	if camera.ISO > 0 {
		out["iso"] = camera.ISO
	}
	return out
}

func (c modelClassifier) remote() bool {
	parsed, err := url.Parse(strings.TrimSpace(c.baseURL))
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" || host == "localhost" {
		return false
	}
	ip := net.ParseIP(host)
	return ip == nil || !ip.IsLoopback()
}

func (input classifyInput) contentImagePath() (string, bool) {
	if input.MediaType != "image" {
		return "", false
	}
	for _, resource := range input.Resources {
		path := strings.TrimSpace(resource.LocalPath)
		if path == "" || !classifiableImagePath(path) {
			continue
		}
		return path, true
	}
	return "", false
}

func (input classifyInput) localPathClass(path string) string {
	for _, resource := range input.Resources {
		if resource.LocalPath != path {
			continue
		}
		value := strings.ToLower(strings.Join([]string{resource.ResourceType, resource.LocalPath}, " "))
		switch {
		case strings.Contains(value, "derivative"):
			return "derivative"
		case strings.Contains(value, "render"):
			return "render"
		case strings.Contains(value, "original"):
			return "original"
		default:
			return "local_media"
		}
	}
	return "unknown"
}

func classifiableImagePath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg", ".png", ".heic":
		return true
	default:
		return false
	}
}

func mimeTypeForPath(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".heic":
		return "image/heic"
	default:
		return "image/jpeg"
	}
}

// knownPlaceRelationship phrases the match for the model in plain words —
// one sentence fragment, no kind enums.
func knownPlaceRelationship(match KnownPlaceMatch) string {
	switch {
	case match.After && match.Kind == KnownPlaceKindWork:
		return "the user's former workplace"
	case match.After:
		return "the user's former home"
	case match.Kind == KnownPlaceKindWork:
		return "the user's workplace"
	case match.Kind == KnownPlaceKindFormerHome:
		return "the user's home at the time this photo was taken"
	default:
		return "the user's home"
	}
}
