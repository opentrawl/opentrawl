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

const ollamaCloudChatURL = "https://ollama.com/api/chat"

// OllamaCloudCardModelClient calls one Ollama Cloud vision model.
type OllamaCloudCardModelClient struct {
	cardModelName     string
	ollamaCloudAPIKey string
	httpClient        *http.Client
}

func NewOllamaCloudCardModelClient(cardModelName, ollamaCloudAPIKey string) *OllamaCloudCardModelClient {
	return &OllamaCloudCardModelClient{
		cardModelName:     cardModelName,
		ollamaCloudAPIKey: ollamaCloudAPIKey,
		httpClient:        &http.Client{Timeout: cardModelCallTimeout},
	}
}

func (*OllamaCloudCardModelClient) CardModelProvider() photo.CardModelProvider {
	return photo.CardModelProvider_CARD_MODEL_PROVIDER_OLLAMA_CLOUD
}

func (client *OllamaCloudCardModelClient) CardModelName() string   { return client.cardModelName }
func (client *OllamaCloudCardModelClient) CardModelEffort() string { return "" }

// The request and response bodies of Ollama's /api/chat, at its boundary.
type ollamaChatRequestBody struct {
	Model    string              `json:"model"`
	Stream   bool                `json:"stream"`
	Messages []ollamaChatMessage `json:"messages"`
}

type ollamaChatMessage struct {
	Role    string   `json:"role"`
	Content string   `json:"content"`
	Images  []string `json:"images,omitempty"`
}

type ollamaChatResponseBody struct {
	Message struct {
		Content  string `json:"content"`
		Thinking string `json:"thinking"`
	} `json:"message"`
	Done            bool   `json:"done"`
	PromptEvalCount *int64 `json:"prompt_eval_count"`
	EvalCount       *int64 `json:"eval_count"`
	Error           string `json:"error"`
}

func (client *OllamaCloudCardModelClient) AnswerPhotoCardModelInput(ctx context.Context, photoCardModelInputText string, pictureJPEGFilePath string) (CardModelAnswer, error) {
	pictureJPEGBytes, err := os.ReadFile(pictureJPEGFilePath)
	if err != nil {
		return CardModelAnswer{}, fmt.Errorf("read picture for the card model: %w", err)
	}
	requestBody, err := json.Marshal(ollamaChatRequestBody{
		Model: client.cardModelName,
		Messages: []ollamaChatMessage{{
			Role:    "user",
			Content: photoCardModelInputText,
			Images:  []string{base64.StdEncoding.EncodeToString(pictureJPEGBytes)},
		}},
	})
	if err != nil {
		return CardModelAnswer{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, ollamaCloudChatURL, bytes.NewReader(requestBody))
	if err != nil {
		return CardModelAnswer{}, err
	}
	request.Header.Set("Authorization", "Bearer "+client.ollamaCloudAPIKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.httpClient.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return CardModelAnswer{}, ctx.Err()
		}
		return CardModelAnswer{}, &CardModelCallFailedError{CardModelCallFailureMessage: "Ollama Cloud did not answer: " + err.Error()}
	}
	defer func() { _ = response.Body.Close() }()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		if ctx.Err() != nil {
			return CardModelAnswer{}, ctx.Err()
		}
		return CardModelAnswer{}, &CardModelCallFailedError{CardModelCallFailureMessage: "Ollama Cloud's answer was cut off: " + err.Error()}
	}
	var chatResponse ollamaChatResponseBody
	decodeErr := json.Unmarshal(responseBody, &chatResponse)
	switch {
	case response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden ||
		response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusTooManyRequests:
		return CardModelAnswer{}, &CardModelRefusedAccessError{CardModelRefusalMessage: fmt.Sprintf("Ollama Cloud answered HTTP %d: %s", response.StatusCode, ollamaErrorText(chatResponse, responseBody))}
	case response.StatusCode != http.StatusOK:
		return CardModelAnswer{}, &CardModelCallFailedError{CardModelCallFailureMessage: fmt.Sprintf("Ollama Cloud answered HTTP %d: %s", response.StatusCode, ollamaErrorText(chatResponse, responseBody))}
	case decodeErr != nil:
		return CardModelAnswer{}, &CardModelCallFailedError{CardModelCallFailureMessage: "Ollama Cloud's answer is not a chat response: " + decodeErr.Error()}
	case chatResponse.Error != "":
		return CardModelAnswer{}, &CardModelCallFailedError{CardModelCallFailureMessage: "Ollama Cloud returned an error: " + chatResponse.Error}
	case !chatResponse.Done || strings.TrimSpace(chatResponse.Message.Content) == "":
		return CardModelAnswer{}, &CardModelCallFailedError{CardModelCallFailureMessage: "Ollama Cloud returned an incomplete or empty answer."}
	}
	return CardModelAnswer{
		RawModelAnswer: chatResponse.Message.Content,
		InputTokens:    chatResponse.PromptEvalCount,
		OutputTokens:   chatResponse.EvalCount,
	}, nil
}

func ollamaErrorText(chatResponse ollamaChatResponseBody, responseBody []byte) string {
	if chatResponse.Error != "" {
		return chatResponse.Error
	}
	return strings.TrimSpace(string(responseBody))
}
