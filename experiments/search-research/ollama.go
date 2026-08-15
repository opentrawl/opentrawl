package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
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
	Embeddings       [][]float64 `json:"embeddings"`
	PromptTokenCount int64       `json:"prompt_eval_count"`
}

type ollamaModelList struct {
	Models []struct {
		Name   string `json:"name"`
		Digest string `json:"digest"`
		Size   int64  `json:"size"`
	} `json:"models"`
}

type ollamaVersionResponse struct {
	Version string `json:"version"`
}

func requestOllamaEmbeddings(
	ctx context.Context,
	configuration embeddingDeploymentConfiguration,
	inputs []string,
) (ollamaEmbeddingResponse, time.Duration, error) {
	requestBody := ollamaEmbeddingRequest{
		Model:      configuration.modelArtifactName,
		Input:      inputs,
		Dimensions: configuration.nativeEmbeddingDimensions,
		Truncate:   false,
		KeepAlive:  "30m",
	}
	requestBody.Options.ContextTokens = configuration.maximumInputTokens
	encodedRequestBody, err := json.Marshal(requestBody)
	if err != nil {
		return ollamaEmbeddingResponse{}, 0, err
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		strings.TrimRight(configuration.runtimeEndpoint, "/")+"/api/embed",
		bytes.NewReader(encodedRequestBody),
	)
	if err != nil {
		return ollamaEmbeddingResponse{}, 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	startedAt := time.Now()
	response, err := http.DefaultClient.Do(request)
	inferenceElapsed := time.Since(startedAt)
	if err != nil {
		return ollamaEmbeddingResponse{}, inferenceElapsed, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		responseBody, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return ollamaEmbeddingResponse{}, inferenceElapsed, fmt.Errorf(
			"ollama rejected an untruncated embedding input with HTTP %d: %s",
			response.StatusCode,
			strings.TrimSpace(string(responseBody)),
		)
	}
	var embeddingResponse ollamaEmbeddingResponse
	if err := json.NewDecoder(response.Body).Decode(&embeddingResponse); err != nil {
		return ollamaEmbeddingResponse{}, inferenceElapsed, err
	}
	return embeddingResponse, inferenceElapsed, nil
}

func verifyOllamaEmbeddingDeployment(
	ctx context.Context,
	configuration embeddingDeploymentConfiguration,
) error {
	if configuration.runtimeName != "ollama" {
		return fmt.Errorf("runtime %q is unsupported", configuration.runtimeName)
	}
	versionRequest, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		strings.TrimRight(configuration.runtimeEndpoint, "/")+"/api/version",
		nil,
	)
	if err != nil {
		return err
	}
	versionResponse, err := http.DefaultClient.Do(versionRequest)
	if err != nil {
		return err
	}
	defer func() { _ = versionResponse.Body.Close() }()
	if versionResponse.StatusCode < 200 || versionResponse.StatusCode >= 300 {
		return fmt.Errorf("ollama version returned HTTP %d", versionResponse.StatusCode)
	}
	var observedVersion ollamaVersionResponse
	if err := json.NewDecoder(versionResponse.Body).Decode(&observedVersion); err != nil {
		return err
	}
	if observedVersion.Version != configuration.runtimeVersion {
		return fmt.Errorf("ollama runtime version %q does not match the embedding deployment", observedVersion.Version)
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		strings.TrimRight(configuration.runtimeEndpoint, "/")+"/api/tags",
		nil,
	)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("ollama model list returned HTTP %d", response.StatusCode)
	}
	var modelList ollamaModelList
	if err := json.NewDecoder(response.Body).Decode(&modelList); err != nil {
		return err
	}
	for _, model := range modelList.Models {
		modelNameWithoutLatest := strings.TrimSuffix(model.Name, ":latest")
		configuredNameWithoutLatest := strings.TrimSuffix(configuration.modelArtifactName, ":latest")
		if model.Name != configuration.modelArtifactName && modelNameWithoutLatest != configuredNameWithoutLatest {
			continue
		}
		if model.Digest != configuration.runtimeModelDigest {
			return fmt.Errorf("ollama model artifact digest does not match the deployment configuration")
		}
		if configuration.runtimeLoadedModelBytes != model.Size {
			return fmt.Errorf("ollama model artifact size does not match the deployment configuration")
		}
		return nil
	}
	return errors.New("configured ollama model artifact is not installed")
}
