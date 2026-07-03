package archive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/openclaw/photoscrawl/internal/modelclient"
	repoPrompts "github.com/openclaw/photoscrawl/prompts"
)

const (
	modelClassifierSource = "local_multimodal"
	modelPromptVersion    = repoPrompts.LocalMultimodalObservationsV1Version
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

func (c modelClassifier) classify(ctx context.Context, imagePath string) (modelResult, error) {
	data, err := os.ReadFile(imagePath)
	if err != nil {
		return modelResult{}, fmt.Errorf("read image: %w", err)
	}
	sum := sha256.Sum256(data)
	response, err := c.client.Generate(ctx, modelclient.Request{
		Prompt: repoPrompts.LocalMultimodalObservationsV1,
		Images: []modelclient.Image{{
			Data:     data,
			MIMEType: mimeTypeForPath(imagePath),
		}},
		Temperature: 0.1,
	})
	if err != nil {
		return modelResult{}, err
	}
	payload, err := parseModelPayload(response.Text)
	if err != nil {
		return modelResult{}, err
	}
	return modelResult{
		Payload:      payload,
		RawResponse:  response.Text,
		ImageBytes:   int64(len(data)),
		ImageSHA256:  hex.EncodeToString(sum[:]),
		Observations: observationsFromPayload(payload),
	}, nil
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
