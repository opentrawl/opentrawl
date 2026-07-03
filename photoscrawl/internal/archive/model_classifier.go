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
	payload := map[string]any{
		"schema_version": 3,
		"capture": map[string]any{
			"local_time": localCaptureTime(input.CreationDate, input.timezoneName()),
			"timezone":   input.timezoneName(),
			"source":     "apple_photos",
		},
		"media": map[string]any{
			"kind":             openMediaType(input.MediaType),
			"subtypes":         splitSubtypes(input.MediaSubtypes),
			"width":            input.Width,
			"height":           input.Height,
			"duration_seconds": input.DurationSeconds,
		},
		"library_context": map[string]any{
			"albums":       albums,
			"favorite":     input.Favorite,
			"hidden":       input.Hidden,
			"burst_member": strings.TrimSpace(input.BurstIdentifier) != "",
			"original":     input.originalContext(),
			"source_note":  "metadata is context for reasoning, not text to echo",
		},
	}
	if input.HasLocation {
		location := map[string]any{
			"gps": map[string]any{
				"latitude":                   input.Latitude,
				"longitude":                  input.Longitude,
				"horizontal_accuracy_meters": input.AccuracyMeters,
			},
		}
		if input.Place != nil {
			location["place_context"] = input.placeContextForPrompt()
		}
		payload["location"] = location
	}
	return json.MarshalIndent(payload, "", "  ")
}

func (input classifyInput) timezoneName() string {
	if strings.TrimSpace(input.TimezoneName) != "" {
		return strings.TrimSpace(input.TimezoneName)
	}
	return "local"
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

func splitSubtypes(value string) []string {
	out := []string{}
	for _, part := range strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '|'
	}) {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
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
	return map[string]any{
		"uti":               resource.UTI,
		"availability":      resource.Availability(),
		"bytes":             resource.FileSize,
		"available_locally": resource.AvailableLocally,
		"needs_download":    resource.NeedsDownload,
	}
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
	for _, candidate := range result.POICandidates {
		candidates = append(candidates, map[string]any{
			"name":            candidate.Name,
			"category":        candidate.Category,
			"distance_meters": candidate.DistanceM,
			"tier":            candidate.Tier,
			"source":          candidate.Source,
		})
	}
	return map[string]any{
		"cache_status":     input.Place.CacheStatus,
		"address_line":     addressLine(result.Address),
		"area":             result.Area,
		"poi_status":       result.POIStatus,
		"venue_candidates": candidates,
	}
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
