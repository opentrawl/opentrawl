package densesearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
)

const (
	embeddingGemmaModelName              = "embeddinggemma:latest"
	embeddingGemmaModelDigest            = "85462619ee721b466c5927d109d4cb765861907d5417b9109caebc4e614679f1"
	embeddingGemmaModelBytes       int64 = 621875917
	embeddingGemmaContextTokens          = 2048
	embeddingGemmaNativeDimensions       = 768
	embeddingGemmaStoredDimensions       = 256
	embeddingGemmaDocumentPrefix         = "title: none | text: "
	embeddingGemmaQueryPrefix            = "task: search result | query: "
	embeddingRuntimeEndpoint             = "http://127.0.0.1:11434"
)

type ollamaEmbeddingRequest struct {
	Model      string   `json:"model"`
	Input      []string `json:"input"`
	Dimensions int      `json:"dimensions"`
	Truncate   bool     `json:"truncate"`
	KeepAlive  string   `json:"keep_alive"`
	Options    struct {
		ContextTokens int `json:"num_ctx"`
	} `json:"options"`
}

type ollamaEmbeddingResponse struct {
	Embeddings [][]float64 `json:"embeddings"`
}

type ollamaModelList struct {
	Models []struct {
		Name   string `json:"name"`
		Digest string `json:"digest"`
		Size   int64  `json:"size"`
	} `json:"models"`
}

func requestStoredEmbeddings(ctx context.Context, inputs []string) ([][]float32, error) {
	requestBody := ollamaEmbeddingRequest{
		Model:      embeddingGemmaModelName,
		Input:      inputs,
		Dimensions: embeddingGemmaNativeDimensions,
		Truncate:   false,
		KeepAlive:  "30m",
	}
	requestBody.Options.ContextTokens = embeddingGemmaContextTokens
	encodedRequestBody, err := json.Marshal(requestBody)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		embeddingRuntimeEndpoint+"/api/embed",
		bytes.NewReader(encodedRequestBody),
	)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		responseBody, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return nil, fmt.Errorf(
			"embedding runtime rejected an untruncated input with HTTP %d: %s",
			response.StatusCode,
			strings.TrimSpace(string(responseBody)),
		)
	}
	var embeddingResponse ollamaEmbeddingResponse
	if err := json.NewDecoder(response.Body).Decode(&embeddingResponse); err != nil {
		return nil, err
	}
	if len(embeddingResponse.Embeddings) != len(inputs) {
		return nil, fmt.Errorf(
			"embedding runtime returned %d vectors for %d inputs",
			len(embeddingResponse.Embeddings),
			len(inputs),
		)
	}
	storedEmbeddings := make([][]float32, len(embeddingResponse.Embeddings))
	for embeddingIndex, runtimeEmbedding := range embeddingResponse.Embeddings {
		if err := validateRuntimeEmbedding(runtimeEmbedding); err != nil {
			return nil, err
		}
		storedEmbeddings[embeddingIndex], err = normalizeAndTruncateRuntimeEmbedding(runtimeEmbedding)
		if err != nil {
			return nil, err
		}
	}
	return storedEmbeddings, nil
}

func verifyEmbeddingGemmaModel(ctx context.Context) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, embeddingRuntimeEndpoint+"/api/tags", nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("embedding runtime model list returned HTTP %d", response.StatusCode)
	}
	var modelList ollamaModelList
	if err := json.NewDecoder(response.Body).Decode(&modelList); err != nil {
		return err
	}
	for _, model := range modelList.Models {
		if model.Name != embeddingGemmaModelName && strings.TrimSuffix(model.Name, ":latest") != strings.TrimSuffix(embeddingGemmaModelName, ":latest") {
			continue
		}
		if model.Digest != embeddingGemmaModelDigest || model.Size != embeddingGemmaModelBytes {
			return errors.New("installed EmbeddingGemma artifact does not match the released dense search model")
		}
		return nil
	}
	return errors.New("released EmbeddingGemma model is not installed")
}

func validateRuntimeEmbedding(embedding []float64) error {
	if len(embedding) != embeddingGemmaNativeDimensions {
		return fmt.Errorf("embedding runtime returned %d dimensions; expected %d", len(embedding), embeddingGemmaNativeDimensions)
	}
	var squaredNorm float64
	for _, component := range embedding {
		if math.IsNaN(component) || math.IsInf(component, 0) {
			return errors.New("embedding runtime returned a non-finite vector")
		}
		squaredNorm += component * component
	}
	if squaredNorm == 0 {
		return errors.New("embedding runtime returned a zero vector")
	}
	return nil
}

func normalizeAndTruncateRuntimeEmbedding(runtimeEmbedding []float64) ([]float32, error) {
	var squaredNorm float64
	for _, component := range runtimeEmbedding[:embeddingGemmaStoredDimensions] {
		squaredNorm += component * component
	}
	norm := math.Sqrt(squaredNorm)
	if norm == 0 || math.IsNaN(norm) || math.IsInf(norm, 0) {
		return nil, errors.New("stored embedding prefix has a zero or non-finite norm")
	}
	storedEmbedding := make([]float32, embeddingGemmaStoredDimensions)
	for componentIndex, component := range runtimeEmbedding[:embeddingGemmaStoredDimensions] {
		storedEmbedding[componentIndex] = float32(component / norm)
	}
	return storedEmbedding, nil
}
