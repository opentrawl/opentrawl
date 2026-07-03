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
	prompt, err := renderPhotoCardPrompt(repoPrompts.PhotoCardV2, input)
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
		Payload:      payload,
		RawResponse:  response.Text,
		ImageBytes:   int64(len(data)),
		ImageSHA256:  hex.EncodeToString(sum[:]),
		Observations: observationsFromCard(card, input.HasLocation),
		SearchTerms:  photoCardSearchTerms(card),
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
	resources := make([]map[string]any, 0, len(input.Resources))
	for _, resource := range input.Resources {
		resources = append(resources, map[string]any{
			"type":              resource.ResourceType,
			"uti":               resource.UTI,
			"original_filename": resource.OriginalFilename,
			"file_size":         resource.FileSize,
			"available_locally": resource.AvailableLocally,
			"needs_download":    resource.NeedsDownload,
		})
	}
	albums := make([]map[string]any, 0, len(input.Albums))
	for _, album := range input.Albums {
		albums = append(albums, map[string]any{
			"title": album.AlbumTitle,
			"kind":  album.AlbumKind,
		})
	}
	payload := map[string]any{
		"media_type":       input.MediaType,
		"media_subtypes":   input.MediaSubtypes,
		"creation_date":    input.CreationDate,
		"width":            input.Width,
		"height":           input.Height,
		"favorite":         input.Favorite,
		"hidden":           input.Hidden,
		"burst_identifier": input.BurstIdentifier,
		"asset_metadata":   input.MetadataJSON,
		"resources":        resources,
		"albums":           albums,
	}
	if input.HasLocation {
		payload["location"] = map[string]any{
			"latitude":                   input.Latitude,
			"longitude":                  input.Longitude,
			"horizontal_accuracy_meters": input.AccuracyMeters,
		}
	}
	return json.MarshalIndent(payload, "", "  ")
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
