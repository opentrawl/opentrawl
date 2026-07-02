package modelclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGenerateNativeSendsBearerAndImagePayload(t *testing.T) {
	t.Setenv("MODEL_TEST_KEY", "secret-token")
	var sawPath string
	var sawAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		sawAuth = r.Header.Get("Authorization")
		var request struct {
			Model   string         `json:"model"`
			Prompt  string         `json:"prompt"`
			Images  []string       `json:"images"`
			Stream  bool           `json:"stream"`
			Options map[string]any `json:"options"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request.Model != "fixture-model" || request.Prompt != "describe this" || len(request.Images) != 1 || request.Stream {
			t.Fatalf("request = %#v", request)
		}
		if request.Images[0] != "aW1hZ2UtYnl0ZXM=" || request.Options["temperature"] != 0.1 {
			t.Fatalf("request payload = %#v", request)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"response":             `{"scene_summary":"synthetic scene"}`,
			"total_duration":       10,
			"prompt_eval_count":    2,
			"prompt_eval_duration": 3,
			"eval_count":           4,
			"eval_duration":        5,
			"load_duration":        6,
			"done":                 true,
		})
	}))
	defer server.Close()

	client := New(Config{
		BaseURL:      server.URL + "/api",
		Model:        "fixture-model",
		BearerKeyEnv: "MODEL_TEST_KEY",
	})
	response, err := client.Generate(context.Background(), Request{
		Prompt: "describe this",
		Images: []Image{{
			Data: []byte("image-bytes"),
		}},
		Temperature: 0.1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if sawPath != "/api/generate" || sawAuth != "Bearer secret-token" {
		t.Fatalf("path/auth = %q %q", sawPath, sawAuth)
	}
	if response.Text != `{"scene_summary":"synthetic scene"}` || response.Telemetry["total_duration"] != int64(10) {
		t.Fatalf("response = %#v", response)
	}
}

func TestGenerateChatUsesOpenAICompatibleEndpoint(t *testing.T) {
	var sawPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		var request struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request.Model != "openai-compatible" || request.Messages[0].Content[0].Text != "describe this" {
			t.Fatalf("request = %#v", request)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": "synthetic answer"},
			}},
		})
	}))
	defer server.Close()

	client := New(Config{BaseURL: server.URL + "/v1", Model: "openai-compatible"})
	response, err := client.Generate(context.Background(), Request{Prompt: "describe this"})
	if err != nil {
		t.Fatal(err)
	}
	if sawPath != "/v1/chat/completions" || response.Text != "synthetic answer" {
		t.Fatalf("path/response = %q %#v", sawPath, response)
	}
}
