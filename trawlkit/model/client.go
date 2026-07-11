package model

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
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

// ProviderRequest is the complete credential-free request identity. Body is
// the exact JSON sent on the wire; Render creates it once and Send does not
// marshal it again.
type ProviderRequest struct {
	route string
	model string
	body  []byte
}

func RestoreProviderRequest(route, model string, body []byte) (ProviderRequest, error) {
	if err := validateProviderRoute(route); err != nil {
		return ProviderRequest{}, err
	}
	if strings.TrimSpace(model) == "" {
		return ProviderRequest{}, errors.New("model request model is required")
	}
	if len(body) == 0 || !json.Valid(body) {
		return ProviderRequest{}, errors.New("model request body must be valid JSON")
	}
	return ProviderRequest{route: route, model: model, body: bytes.Clone(body)}, nil
}

func (r ProviderRequest) Route() string { return r.route }
func (r ProviderRequest) Model() string { return r.model }
func (r ProviderRequest) Body() []byte  { return bytes.Clone(r.body) }

// Digest covers the route, model and exact body without relying on delimiter
// characters that may also occur in the body.
func (r ProviderRequest) Digest() [sha256.Size]byte {
	hash := sha256.New()
	for _, part := range [][]byte{[]byte(r.route), []byte(r.model), r.body} {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(part)))
		_, _ = hash.Write(size[:])
		_, _ = hash.Write(part)
	}
	var digest [sha256.Size]byte
	copy(digest[:], hash.Sum(nil))
	return digest
}

// RawResult retains the provider boundary before any response decoding.
type RawResult struct {
	Response            []byte
	Failure             []byte
	Status              string
	StatusCode          int
	ProviderRequestID   string
	TransmissionStarted bool
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

func New(cfg Config) (*Client, error) {
	baseURL, err := NormalizeBaseURL(cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	apiKey := ""
	if envName := strings.TrimSpace(cfg.BearerKeyEnv); envName != "" {
		apiKey = strings.TrimSpace(os.Getenv(envName))
	}
	return &Client{
		baseURL: baseURL,
		model:   strings.TrimSpace(cfg.Model),
		apiKey:  apiKey,
		client:  &http.Client{Timeout: requestTimeout},
	}, nil
}

func NormalizeBaseURL(raw string) (string, error) {
	value := strings.TrimRight(strings.TrimSpace(raw), "/")
	if value == "" {
		value = DefaultBaseURL
	}
	for _, suffix := range []string{"/api/generate", "/v1/chat/completions"} {
		if strings.HasSuffix(value, suffix) {
			value = strings.TrimSuffix(value, suffix)
			break
		}
	}
	if err := validateBaseURL(value); err != nil {
		return "", err
	}
	return value, nil
}

func GenerateEndpoint(raw string) (string, error) {
	baseURL, err := NormalizeBaseURL(raw)
	if err != nil {
		return "", err
	}
	switch {
	case strings.HasSuffix(baseURL, "/api"):
		return baseURL + "/generate", nil
	case strings.HasSuffix(baseURL, "/v1"):
		return baseURL + "/chat/completions", nil
	default:
		return baseURL + "/api/generate", nil
	}
}

func validateBaseURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("model base URL %q is invalid: %w", value, err)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("model base URL %q must use http or https", value)
	}
	if parsed.Host == "" || parsed.Hostname() == "" {
		return fmt.Errorf("model base URL %q must include a host", value)
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("model base URL %q must not contain credentials, a query or a fragment", value)
	}
	return nil
}

func (c *Client) Generate(ctx context.Context, request Request) (Response, error) {
	providerRequest, err := c.Render(request)
	if err != nil {
		return Response{}, err
	}
	raw, err := c.Send(ctx, providerRequest)
	if err != nil {
		return Response{}, err
	}
	return Parse(providerRequest, raw)
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

func (c *Client) renderNative(request Request) (ProviderRequest, error) {
	payload := nativeGenerateRequest{
		Model:  c.model,
		Prompt: request.Prompt,
		Images: encodedImages(request.Images),
		Stream: false,
		Options: map[string]any{
			"temperature": request.Temperature,
		},
	}
	endpoint, err := GenerateEndpoint(c.baseURL)
	if err != nil {
		return ProviderRequest{}, err
	}
	return marshalProviderRequest(endpoint, c.model, payload)
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

func (c *Client) renderChat(request Request) (ProviderRequest, error) {
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
	endpoint, err := GenerateEndpoint(c.baseURL)
	if err != nil {
		return ProviderRequest{}, err
	}
	return marshalProviderRequest(endpoint, c.model, payload)
}

func (c *Client) Render(request Request) (ProviderRequest, error) {
	if strings.HasSuffix(c.baseURL, "/v1") {
		return c.renderChat(request)
	}
	return c.renderNative(request)
}

func marshalProviderRequest(route, model string, payload any) (ProviderRequest, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return ProviderRequest{}, fmt.Errorf("marshal model request: %w", err)
	}
	return RestoreProviderRequest(route, model, body)
}

func validateProviderRoute(route string) error {
	parsed, err := url.Parse(strings.TrimSpace(route))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("model request route %q is invalid", route)
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("model request route %q must not contain credentials, a query or a fragment", route)
	}
	return nil
}

// Send transmits the exact body held by request. It returns response or
// failure bytes before provider JSON or card prose is parsed.
func (c *Client) Send(ctx context.Context, request ProviderRequest) (RawResult, error) {
	if err := validateProviderRoute(request.route); err != nil {
		return RawResult{}, err
	}
	var started atomic.Bool
	trace := &httptrace.ClientTrace{
		WroteHeaders: func() { started.Store(true) },
		WroteRequest: func(httptrace.WroteRequestInfo) { started.Store(true) },
	}
	ctx = httptrace.WithClientTrace(ctx, trace)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, request.route, bytes.NewReader(request.body))
	if err != nil {
		return RawResult{Failure: []byte(err.Error())}, fmt.Errorf("build model request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return RawResult{Failure: []byte(err.Error()), TransmissionStarted: started.Load()}, fmt.Errorf("model request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return RawResult{
			Failure:             []byte(err.Error()),
			Status:              resp.Status,
			StatusCode:          resp.StatusCode,
			ProviderRequestID:   providerRequestID(resp.Header),
			TransmissionStarted: true,
		}, fmt.Errorf("read model response: %w", err)
	}
	raw := RawResult{
		Response:            bodyBytes,
		Status:              resp.Status,
		StatusCode:          resp.StatusCode,
		ProviderRequestID:   providerRequestID(resp.Header),
		TransmissionStarted: true,
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return raw, &HTTPError{Status: resp.Status, StatusCode: resp.StatusCode, Body: string(bodyBytes)}
	}
	return raw, nil
}

func providerRequestID(header http.Header) string {
	for _, name := range []string{"X-Request-ID", "Request-ID"} {
		if value := strings.TrimSpace(header.Get(name)); value != "" {
			return value
		}
	}
	return ""
}

func Parse(request ProviderRequest, raw RawResult) (Response, error) {
	if len(raw.Failure) > 0 {
		return Response{}, fmt.Errorf("model request failed: %s", raw.Failure)
	}
	if raw.StatusCode < 200 || raw.StatusCode >= 300 {
		return Response{}, &HTTPError{Status: raw.Status, StatusCode: raw.StatusCode, Body: string(raw.Response)}
	}
	if strings.HasSuffix(request.route, "/chat/completions") {
		var response chatResponse
		if err := json.Unmarshal(raw.Response, &response); err != nil {
			return Response{}, fmt.Errorf("decode model response: %w", err)
		}
		if response.Error != nil && strings.TrimSpace(response.Error.Message) != "" {
			return Response{}, fmt.Errorf("%s", response.Error.Message)
		}
		if len(response.Choices) == 0 {
			return Response{}, fmt.Errorf("model response contained no choices")
		}
		return Response{Text: strings.TrimSpace(response.Choices[0].Message.Content)}, nil
	}
	var response nativeGenerateResponse
	if err := json.Unmarshal(raw.Response, &response); err != nil {
		return Response{}, fmt.Errorf("decode model response: %w", err)
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

func encodedImages(images []Image) []string {
	out := make([]string, 0, len(images))
	for _, image := range images {
		out = append(out, base64.StdEncoding.EncodeToString(image.Data))
	}
	return out
}
