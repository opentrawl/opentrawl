package model

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	DefaultBaseURL     = "http://127.0.0.1:11434"
	DefaultGenerateURL = DefaultBaseURL + "/api/generate"
	requestTimeout     = 20 * time.Minute
)

type Config struct {
	BaseURL      string
	Model        string
	BearerKeyEnv string
}

type Client struct {
	baseURL string
	model   string
	apiKey  string
	client  *http.Client
}

type Request struct {
	Prompt      string
	Images      []Image
	Temperature float64
}

type Image struct {
	Data     []byte
	MIMEType string
}

type Response struct {
	Text      string
	Telemetry map[string]any
}

type HTTPError struct {
	Status     string
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	body := strings.TrimSpace(e.Body)
	if body == "" {
		return fmt.Sprintf("model request returned %s", e.Status)
	}
	return fmt.Sprintf("model request returned %s: %s", e.Status, body)
}

func New(cfg Config) *Client {
	apiKey := ""
	if envName := strings.TrimSpace(cfg.BearerKeyEnv); envName != "" {
		apiKey = strings.TrimSpace(os.Getenv(envName))
	}
	return &Client{
		baseURL: NormalizeBaseURL(cfg.BaseURL),
		model:   strings.TrimSpace(cfg.Model),
		apiKey:  apiKey,
		client:  &http.Client{Timeout: requestTimeout},
	}
}

func NormalizeBaseURL(raw string) string {
	value := strings.TrimRight(strings.TrimSpace(raw), "/")
	if value == "" {
		return DefaultBaseURL
	}
	for _, suffix := range []string{"/api/generate", "/v1/chat/completions"} {
		if strings.HasSuffix(value, suffix) {
			return strings.TrimSuffix(value, suffix)
		}
	}
	return value
}

func GenerateEndpoint(raw string) string {
	baseURL := NormalizeBaseURL(raw)
	switch {
	case strings.HasSuffix(baseURL, "/api"):
		return baseURL + "/generate"
	case strings.HasSuffix(baseURL, "/v1"):
		return baseURL + "/chat/completions"
	default:
		return baseURL + "/api/generate"
	}
}

func (c *Client) Generate(ctx context.Context, request Request) (Response, error) {
	if strings.HasSuffix(c.baseURL, "/v1") {
		return c.generateChat(ctx, request)
	}
	return c.generateNative(ctx, request)
}

type nativeGenerateRequest struct {
	Model   string         `json:"model"`
	Prompt  string         `json:"prompt"`
	Images  []string       `json:"images"`
	Stream  bool           `json:"stream"`
	Options map[string]any `json:"options,omitempty"`
}

type nativeGenerateResponse struct {
	Model              string `json:"model,omitempty"`
	CreatedAt          string `json:"created_at,omitempty"`
	Response           string `json:"response"`
	Done               bool   `json:"done"`
	Error              string `json:"error,omitempty"`
	TotalDuration      int64  `json:"total_duration,omitempty"`
	LoadDuration       int64  `json:"load_duration,omitempty"`
	PromptEvalCount    int64  `json:"prompt_eval_count,omitempty"`
	PromptEvalDuration int64  `json:"prompt_eval_duration,omitempty"`
	EvalCount          int64  `json:"eval_count,omitempty"`
	EvalDuration       int64  `json:"eval_duration,omitempty"`
}

func (c *Client) generateNative(ctx context.Context, request Request) (Response, error) {
	payload := nativeGenerateRequest{
		Model:  c.model,
		Prompt: request.Prompt,
		Images: encodedImages(request.Images),
		Stream: false,
		Options: map[string]any{
			"temperature": request.Temperature,
		},
	}
	var response nativeGenerateResponse
	if err := c.post(ctx, GenerateEndpoint(c.baseURL), payload, &response); err != nil {
		return Response{}, err
	}
	if strings.TrimSpace(response.Error) != "" {
		return Response{}, fmt.Errorf("%s", response.Error)
	}
	return Response{
		Text: strings.TrimSpace(response.Response),
		Telemetry: map[string]any{
			"total_duration":       response.TotalDuration,
			"load_duration":        response.LoadDuration,
			"prompt_eval_count":    response.PromptEvalCount,
			"prompt_eval_duration": response.PromptEvalDuration,
			"eval_count":           response.EvalCount,
			"eval_duration":        response.EvalDuration,
		},
	}, nil
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature,omitempty"`
	Stream      bool          `json:"stream"`
}

type chatMessage struct {
	Role    string            `json:"role"`
	Content []chatContentPart `json:"content"`
}

type chatContentPart struct {
	Type     string        `json:"type"`
	Text     string        `json:"text,omitempty"`
	ImageURL *chatImageURL `json:"image_url,omitempty"`
}

type chatImageURL struct {
	URL string `json:"url"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (c *Client) generateChat(ctx context.Context, request Request) (Response, error) {
	content := []chatContentPart{{Type: "text", Text: request.Prompt}}
	for _, image := range request.Images {
		mimeType := strings.TrimSpace(image.MIMEType)
		if mimeType == "" {
			mimeType = "image/jpeg"
		}
		content = append(content, chatContentPart{
			Type:     "image_url",
			ImageURL: &chatImageURL{URL: "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(image.Data)},
		})
	}
	payload := chatRequest{
		Model:       c.model,
		Messages:    []chatMessage{{Role: "user", Content: content}},
		Temperature: request.Temperature,
		Stream:      false,
	}
	var response chatResponse
	if err := c.post(ctx, GenerateEndpoint(c.baseURL), payload, &response); err != nil {
		return Response{}, err
	}
	if response.Error != nil && strings.TrimSpace(response.Error.Message) != "" {
		return Response{}, fmt.Errorf("%s", response.Error.Message)
	}
	if len(response.Choices) == 0 {
		return Response{}, fmt.Errorf("model response contained no choices")
	}
	return Response{Text: strings.TrimSpace(response.Choices[0].Message.Content)}, nil
}

func (c *Client) post(ctx context.Context, endpoint string, payload any, target any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal model request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build model request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("model request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read model response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &HTTPError{Status: resp.Status, StatusCode: resp.StatusCode, Body: string(bodyBytes)}
	}
	if err := json.Unmarshal(bodyBytes, target); err != nil {
		return fmt.Errorf("decode model response: %w", err)
	}
	return nil
}

func encodedImages(images []Image) []string {
	out := make([]string, 0, len(images))
	for _, image := range images {
		out = append(out, base64.StdEncoding.EncodeToString(image.Data))
	}
	return out
}
