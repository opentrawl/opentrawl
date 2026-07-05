package model

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// sandboxSafeServer starts a real HTTP test server, skipping honestly in
// sandboxes that forbid listeners (several worker sandboxes do). Do NOT
// replace this with a DefaultTransport swap: global transport mutation has
// been reverted four times and hides real wire behaviour.
func sandboxSafeServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("sandbox forbids listeners: %v", err)
	}
	server := &httptest.Server{Listener: listener, Config: &http.Server{Handler: handler}}
	server.Start()
	t.Cleanup(server.Close)
	return server
}

func TestGenerateNativeSendsBearerAndImagePayload(t *testing.T) {
	t.Setenv("MODEL_TEST_KEY", "secret-token")
	var sawPath string
	var sawAuth string
	server := sandboxSafeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			t.Errorf("decode request: %v", err)
			return
		}
		if request.Model != "fixture-model" || request.Prompt != "describe this" || len(request.Images) != 1 || request.Stream {
			t.Errorf("request = %#v", request)
		}
		if len(request.Images) == 1 && request.Images[0] != "aW1hZ2UtYnl0ZXM=" || request.Options["temperature"] != 0.1 {
			t.Errorf("request payload = %#v", request)
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
	server := sandboxSafeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			t.Errorf("decode request: %v", err)
			return
		}
		if request.Model != "openai-compatible" || request.Messages[0].Content[0].Text != "describe this" {
			t.Errorf("request = %#v", request)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": "synthetic answer"},
			}},
		})
	}))

	client := New(Config{BaseURL: server.URL + "/v1", Model: "openai-compatible"})
	response, err := client.Generate(context.Background(), Request{Prompt: "describe this"})
	if err != nil {
		t.Fatal(err)
	}
	if sawPath != "/v1/chat/completions" || response.Text != "synthetic answer" {
		t.Fatalf("path/response = %q %#v", sawPath, response)
	}
}
