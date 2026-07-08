package evalcard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"text/template"
	"time"

	ckmodel "github.com/openclaw/crawlkit/model"
)

type storedModelOutput struct {
	EvalID          string                 `json:"eval_id"`
	Provider        string                 `json:"provider"`
	Model           string                 `json:"model"`
	PromptVersion   string                 `json:"prompt_version"`
	ImagePath       string                 `json:"image_path"`
	MetadataPath    string                 `json:"metadata_path"`
	StartedAt       string                 `json:"started_at"`
	CompletedAt     string                 `json:"completed_at"`
	DurationMillis  int64                  `json:"duration_millis"`
	Response        string                 `json:"response,omitempty"`
	Error           string                 `json:"error,omitempty"`
	OllamaTelemetry map[string]interface{} `json:"ollama_telemetry,omitempty"`
}

func runModelCalls(ctx context.Context, outputDir, promptText string, inputs []preparedInput, models []string, baseURL, apiKeyEnv string, concurrency int) (int, int) {
	type job struct {
		input preparedInput
		model string
	}
	jobs := make(chan job)
	var wg sync.WaitGroup
	var mu sync.Mutex
	succeeded := 0
	failed := 0

	worker := func() {
		defer wg.Done()
		for job := range jobs {
			if err := runOneModelCall(ctx, outputDir, promptText, job.input, job.model, baseURL, apiKeyEnv); err != nil {
				mu.Lock()
				failed++
				mu.Unlock()
				continue
			}
			mu.Lock()
			succeeded++
			mu.Unlock()
		}
	}
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go worker()
	}
	for _, input := range inputs {
		for _, model := range models {
			jobs <- job{input: input, model: model}
		}
	}
	close(jobs)
	wg.Wait()
	return succeeded, failed
}

func runOneModelCall(ctx context.Context, outputDir, promptText string, input preparedInput, model, baseURL, apiKeyEnv string) error {
	started := time.Now().UTC()
	out := storedModelOutput{
		EvalID:        input.ID,
		Provider:      "ollama",
		Model:         model,
		PromptVersion: PromptVersion,
		ImagePath:     input.ImagePath,
		MetadataPath:  input.MetadataPath,
		StartedAt:     started.Format(time.RFC3339Nano),
	}
	defer func() {
		out.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
		out.DurationMillis = time.Since(started).Milliseconds()
		_ = writeModelOutput(outputDir, out)
	}()

	imageBytes, err := os.ReadFile(input.ImagePath)
	if err != nil {
		out.Error = err.Error()
		return err
	}
	renderedPrompt, err := promptWithMetadata(promptText, input.MetadataJSON)
	if err != nil {
		out.Error = err.Error()
		return err
	}
	client, err := ckmodel.New(ckmodel.Config{
		BaseURL:      baseURL,
		Model:        model,
		BearerKeyEnv: apiKeyEnv,
	})
	if err != nil {
		out.Error = err.Error()
		return err
	}
	response, err := client.Generate(ctx, ckmodel.Request{
		Prompt: renderedPrompt,
		Images: []ckmodel.Image{{
			Data:     imageBytes,
			MIMEType: "image/jpeg",
		}},
		Temperature: 0.1,
	})
	if err != nil {
		var httpErr *ckmodel.HTTPError
		if errors.As(err, &httpErr) {
			out.Error = fmt.Sprintf("ollama returned %s: %s", httpErr.Status, strings.TrimSpace(httpErr.Body))
			return errors.New(out.Error)
		}
		out.Error = err.Error()
		return err
	}
	out.Response = response.Text
	out.OllamaTelemetry = response.Telemetry
	return nil
}

func writeModelOutput(outputDir string, out storedModelOutput) error {
	path := filepath.Join(outputDir, "raw", out.EvalID+"__ollama__"+safeName(out.Model)+"__"+PromptVersion+".json")
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

func promptWithMetadata(promptText string, metadataJSON []byte) (string, error) {
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
