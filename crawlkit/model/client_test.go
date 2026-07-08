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

func TestNormalizeBaseURLAllowsOllamaHosts(t *testing.T) {
	tests := map[string]string{
		"":                                       DefaultBaseURL,
		"http://localhost:11434":                 "http://localhost:11434",
		"http://127.0.0.1:21434/api/generate":    "http://127.0.0.1:21434",
		"http://[::1]:31434/v1/chat/completions": "http://[::1]:31434",
		"https://ollama.com/api":                 "https://ollama.com/api",
		"https://OLLAMA.COM":                     "https://OLLAMA.COM",
		"https://ollama.com:443/api/generate":    "https://ollama.com:443",
		"https://vision.ollama.com/v1":           "https://vision.ollama.com/v1",
		// url.URL.Hostname strips userinfo before validation, so the dial goes to ollama.com.
		"https://evil.com@ollama.com": "https://evil.com@ollama.com",
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

func TestNewRejectsNonOllamaHosts(t *testing.T) {
	tests := map[string]string{
		"https://generativelanguage.googleapis.com/v1beta/openai": `host "generativelanguage.googleapis.com" is not an Ollama endpoint`,
		"https://api.openai.com/v1":                               `host "api.openai.com" is not an Ollama endpoint`,
		"https://fixture.test/api":                                `host "fixture.test" is not an Ollama endpoint`,
		"https://ollama.com.evil.tld":                             `host "ollama.com.evil.tld" is not an Ollama endpoint`,
		"https://notollama.com":                                   `host "notollama.com" is not an Ollama endpoint`,
		"https://ollama.com@evil.com":                             `host "evil.com" is not an Ollama endpoint`,
		"http://ollama.com":                                       `endpoint "http://ollama.com" must use https on port 443 for Ollama cloud`,
		"https://ollama.com:8443":                                 `endpoint "https://ollama.com:8443" must use https on port 443 for Ollama cloud`,
		"ftp://127.0.0.1:11434":                                   `endpoint "ftp://127.0.0.1:11434" must use http or https for loopback Ollama endpoints`,
		"gopher://localhost:11434":                                `endpoint "gopher://localhost:11434" must use http or https for loopback Ollama endpoints`,
		"https://[2001:db8::1]:11434":                             `host "2001:db8::1" is not an Ollama endpoint`,
		"https://8.8.8.8:11434":                                   `host "8.8.8.8" is not an Ollama endpoint`,
		"https://xn--ollama-XXX.com-style":                        `host "xn--ollama-xxx.com-style" is not an Ollama endpoint`,
		"https://ollama.com.":                                     `host "ollama.com." is not an Ollama endpoint`,
	}
	for input, detail := range tests {
		_, err := NormalizeBaseURL(input)
		if err == nil {
			t.Fatalf("NormalizeBaseURL accepted %q", input)
		}
		want := ollamaOnlyRule + "; " + detail
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

func TestGenerateEndpointRejectsNonOllamaHosts(t *testing.T) {
	_, err := GenerateEndpoint("https://generativelanguage.googleapis.com/v1beta/openai")
	want := `model inference goes through Ollama only (Ollama-only policy, crawlkit/model doc; ruling 2026-07-08); host "generativelanguage.googleapis.com" is not an Ollama endpoint`
	if err == nil || err.Error() != want {
		t.Fatalf("GenerateEndpoint error = %v, want %q", err, want)
	}
	t.Log(err.Error())
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
