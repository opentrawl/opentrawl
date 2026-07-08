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

func newTestClient(t *testing.T, cfg Config) *Client {
	t.Helper()
	client, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestNormalizeBaseURLAllowsHTTPAndHTTPSHosts(t *testing.T) {
	tests := map[string]string{
		"":                                       DefaultBaseURL,
		"http://localhost:11434":                 "http://localhost:11434",
		"http://127.0.0.1:21434/api/generate":    "http://127.0.0.1:21434",
		"http://[::1]:31434/v1/chat/completions": "http://[::1]:31434",
		"https://ollama.com/api":                 "https://ollama.com/api",
		"https://OLLAMA.COM":                     "https://OLLAMA.COM",
		"https://models.example.com/v1beta":      "https://models.example.com/v1beta",
		"https://models.example.com/v1":          "https://models.example.com/v1",
		"https://fixture.test/api":               "https://fixture.test/api",
		"https://example.com:8443/models":        "https://example.com:8443/models",
	}
	for input, want := range tests {
		got, err := NormalizeBaseURL(input)
		if err != nil {
			t.Fatalf("NormalizeBaseURL(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("NormalizeBaseURL(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNewRejectsInvalidBaseURLs(t *testing.T) {
	tests := map[string]string{
		"ftp://127.0.0.1:11434":    `model base URL "ftp://127.0.0.1:11434" must use http or https`,
		"gopher://localhost:11434": `model base URL "gopher://localhost:11434" must use http or https`,
		"https://":                 `model base URL "https:" must include a host`,
		"http:///api":              `model base URL "http:///api" must include a host`,
		"localhost:11434":          `model base URL "localhost:11434" must use http or https`,
		"://bad":                   `model base URL "://bad" is invalid: parse "://bad": missing protocol scheme`,
	}
	for input, want := range tests {
		_, err := NormalizeBaseURL(input)
		if err == nil {
			t.Fatalf("NormalizeBaseURL accepted %q", input)
		}
		if err.Error() != want {
			t.Fatalf("NormalizeBaseURL(%q) error = %q, want %q", input, err.Error(), want)
		}

		_, err = New(Config{BaseURL: input, Model: "m"})
		if err == nil {
			t.Fatalf("New accepted %q", input)
		}
		if err.Error() != want {
			t.Fatalf("New(%q) error = %q, want %q", input, err.Error(), want)
		}
	}
}

func TestGenerateEndpointAcceptsConfiguredHost(t *testing.T) {
	got, err := GenerateEndpoint("https://models.example.com/v1beta/openai")
	if err != nil {
		t.Fatalf("GenerateEndpoint returned error: %v", err)
	}
	want := "https://models.example.com/v1beta/openai/api/generate"
	if got != want {
		t.Fatalf("GenerateEndpoint = %q, want %q", got, want)
	}
}

func TestNewDefaultsEmptyBaseURL(t *testing.T) {
	client, err := New(Config{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if client.baseURL != DefaultBaseURL {
		t.Fatalf("baseURL = %q, want %q", client.baseURL, DefaultBaseURL)
	}
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

	client := newTestClient(t, Config{
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

	client := newTestClient(t, Config{BaseURL: server.URL + "/v1", Model: "openai-compatible"})
	response, err := client.Generate(context.Background(), Request{Prompt: "describe this"})
	if err != nil {
		t.Fatal(err)
	}
	if sawPath != "/v1/chat/completions" || response.Text != "synthetic answer" {
		t.Fatalf("path/response = %q %#v", sawPath, response)
	}
}
