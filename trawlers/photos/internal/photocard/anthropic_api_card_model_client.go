package photocard

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

	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
)

const (
	anthropicMessagesURL      = "https://api.anthropic.com/v1/messages"
	anthropicAPIVersionHeader = "2023-06-01"
	anthropicMaxOutputTokens  = 16384
)

// AnthropicAPICardModelClient calls Anthropic's Messages API directly with an
// API key (for example from a Claude Ultra Console org's included monthly API
// credits, which hard-stop at $0 with HTTP 400/402 when no card is attached).
type AnthropicAPICardModelClient struct {
	cardModelName   string
	cardModelEffort string
	anthropicAPIKey string
	httpClient      *http.Client
}

func NewAnthropicAPICardModelClient(cardModelName, cardModelEffort, anthropicAPIKey string) *AnthropicAPICardModelClient {
	return &AnthropicAPICardModelClient{
		cardModelName:   cardModelName,
		cardModelEffort: strings.ToLower(strings.TrimSpace(cardModelEffort)),
		anthropicAPIKey: anthropicAPIKey,
		httpClient:      &http.Client{Timeout: cardModelCallTimeout},
	}
}

func (*AnthropicAPICardModelClient) CardModelProvider() photo.CardModelProvider {
	return photo.CardModelProvider_CARD_MODEL_PROVIDER_ANTHROPIC_API
}

func (client *AnthropicAPICardModelClient) CardModelName() string   { return client.cardModelName }
func (client *AnthropicAPICardModelClient) CardModelEffort() string { return client.cardModelEffort }

type anthropicMessagesRequestBody struct {
	Model        string                    `json:"model"`
	MaxTokens    int                       `json:"max_tokens"`
	Thinking     *anthropicThinkingConfig  `json:"thinking,omitempty"`
	OutputConfig *anthropicOutputConfig    `json:"output_config,omitempty"`
	Messages     []anthropicRequestMessage `json:"messages"`
}

type anthropicThinkingConfig struct {
	Type string `json:"type"`
}

type anthropicOutputConfig struct {
	Effort string `json:"effort"`
}

type anthropicRequestMessage struct {
	Role    string                  `json:"role"`
	Content []anthropicRequestBlock `json:"content"`
}

type anthropicRequestBlock struct {
	Type   string                `json:"type"`
	Text   string                `json:"text,omitempty"`
	Source *anthropicImageSource `json:"source,omitempty"`
}

type anthropicImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type anthropicMessagesResponseBody struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage struct {
		InputTokens  *int64 `json:"input_tokens"`
		OutputTokens *int64 `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

func (client *AnthropicAPICardModelClient) AnswerPhotoCardModelInput(ctx context.Context, photoCardModelInputText string, pictureJPEGFilePath string) (CardModelAnswer, error) {
	pictureJPEGBytes, err := os.ReadFile(pictureJPEGFilePath)
	if err != nil {
		return CardModelAnswer{}, fmt.Errorf("read picture for the card model: %w", err)
	}
	payload := anthropicMessagesRequestBody{
		Model:     client.cardModelName,
		MaxTokens: anthropicMaxOutputTokens,
		Messages: []anthropicRequestMessage{{
			Role: "user",
			Content: []anthropicRequestBlock{
				{
					Type: "image",
					Source: &anthropicImageSource{
						Type:      "base64",
						MediaType: "image/jpeg",
						Data:      base64.StdEncoding.EncodeToString(pictureJPEGBytes),
					},
				},
				{
					Type: "text",
					Text: photoCardModelInputText,
				},
			},
		}},
	}
	if client.cardModelEffort != "" {
		payload.Thinking = &anthropicThinkingConfig{Type: "adaptive"}
		payload.OutputConfig = &anthropicOutputConfig{Effort: client.cardModelEffort}
	}
	requestBody, err := json.Marshal(payload)
	if err != nil {
		return CardModelAnswer{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicMessagesURL, bytes.NewReader(requestBody))
	if err != nil {
		return CardModelAnswer{}, err
	}
	request.Header.Set("x-api-key", client.anthropicAPIKey)
	request.Header.Set("anthropic-version", anthropicAPIVersionHeader)
	request.Header.Set("content-type", "application/json")
	response, err := client.httpClient.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return CardModelAnswer{}, ctx.Err()
		}
		return CardModelAnswer{}, &CardModelCallFailedError{CardModelCallFailureMessage: "Anthropic API did not answer: " + err.Error()}
	}
	defer func() { _ = response.Body.Close() }()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		if ctx.Err() != nil {
			return CardModelAnswer{}, ctx.Err()
		}
		return CardModelAnswer{}, &CardModelCallFailedError{CardModelCallFailureMessage: "Anthropic API answer was cut off: " + err.Error()}
	}
	var messagesResponse anthropicMessagesResponseBody
	decodeErr := json.Unmarshal(responseBody, &messagesResponse)
	errText := anthropicErrorText(messagesResponse, responseBody)
	switch {
	case response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusPaymentRequired ||
		response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusNotFound ||
		response.StatusCode == http.StatusTooManyRequests ||
		(response.StatusCode == http.StatusBadRequest && anthropicErrorRefusesAccess(errText)):
		return CardModelAnswer{}, &CardModelRefusedAccessError{
			CardModelRefusalMessage: fmt.Sprintf("Anthropic API answered HTTP %d: %s", response.StatusCode, errText),
		}
	case response.StatusCode != http.StatusOK:
		return CardModelAnswer{}, &CardModelCallFailedError{
			CardModelCallFailureMessage: fmt.Sprintf("Anthropic API answered HTTP %d: %s", response.StatusCode, errText),
		}
	case decodeErr != nil:
		return CardModelAnswer{}, &CardModelCallFailedError{
			CardModelCallFailureMessage: "Anthropic API answer is not a messages response: " + decodeErr.Error(),
		}
	}
	var textParts []string
	for _, block := range messagesResponse.Content {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			textParts = append(textParts, block.Text)
		}
	}
	rawAnswer := strings.TrimSpace(strings.Join(textParts, "\n"))
	if rawAnswer == "" {
		return CardModelAnswer{}, &CardModelCallFailedError{CardModelCallFailureMessage: "Anthropic API returned an empty answer."}
	}
	return CardModelAnswer{
		RawModelAnswer: rawAnswer,
		InputTokens:    messagesResponse.Usage.InputTokens,
		OutputTokens:   messagesResponse.Usage.OutputTokens,
	}, nil
}

func anthropicErrorText(messagesResponse anthropicMessagesResponseBody, responseBody []byte) string {
	if messagesResponse.Error != nil && messagesResponse.Error.Message != "" {
		return messagesResponse.Error.Message
	}
	return strings.TrimSpace(string(responseBody))
}

func anthropicErrorRefusesAccess(errorText string) bool {
	lower := strings.ToLower(errorText)
	for _, marker := range []string{"credit balance", "billing", "insufficient", "invalid_api_key", "not found"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
