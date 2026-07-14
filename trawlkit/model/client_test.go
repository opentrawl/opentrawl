package model

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
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

func TestGenerateChatForcesOneFunctionToolAndReturnsArguments(t *testing.T) {
	server := sandboxSafeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Tools []struct {
				Type     string `json:"type"`
				Function struct {
					Name       string          `json:"name"`
					Parameters json.RawMessage `json:"parameters"`
				} `json:"function"`
			} `json:"tools"`
			ToolChoice struct {
				Type     string `json:"type"`
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tool_choice"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if len(request.Tools) != 1 || request.Tools[0].Type != "function" || request.Tools[0].Function.Name != "submit_fixture" || request.ToolChoice.Type != "function" || request.ToolChoice.Function.Name != "submit_fixture" {
			t.Errorf("tool request = %#v", request)
		}
		if !json.Valid(request.Tools[0].Function.Parameters) {
			t.Errorf("tool schema = %s", request.Tools[0].Function.Parameters)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{
					"tool_calls": []map[string]any{{
						"type": "function",
						"function": map[string]string{
							"name":      "submit_fixture",
							"arguments": `{"summary":"synthetic"}`,
						},
					}},
				},
			}},
		})
	}))
	client := newTestClient(t, Config{BaseURL: server.URL + "/v1", Model: "openai-compatible"})
	schema := json.RawMessage(`{"type":"object","properties":{"summary":{"type":"string"}},"required":["summary"]}`)
	response, err := client.Generate(context.Background(), Request{
		Prompt: "describe this",
		Tool:   &Tool{Name: "submit_fixture", Parameters: schema},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.ToolCalls) != 1 || response.ToolCalls[0].Name != "submit_fixture" || string(response.ToolCalls[0].Arguments) != `{"summary":"synthetic"}` {
		t.Fatalf("tool calls = %#v", response.ToolCalls)
	}
}

func TestRenderIdentityAndRawProviderBoundary(t *testing.T) {
	t.Setenv("MODEL_TEST_KEY", "synthetic-secret")
	var fixtureBody []byte
	server := sandboxSafeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		fixtureBody, err = io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read fixture request: %v", err)
		}
		w.Header().Set("X-Request-ID", "request-synthetic-1")
		_, _ = w.Write([]byte(`{"response":"synthetic answer","done":true}`))
	}))
	client := newTestClient(t, Config{BaseURL: server.URL, Model: "fixture-model", BearerKeyEnv: "MODEL_TEST_KEY"})
	logical := Request{Prompt: "describe synthetic pixels", Images: []Image{{Data: []byte("synthetic-image")}}, Temperature: 0.1}
	first, err := client.Render(logical)
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.Render(logical)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := client.Render(Request{Prompt: "describe changed synthetic pixels"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest() != second.Digest() || first.Digest() == changed.Digest() {
		t.Fatal("provider request identity was not stable and content-sensitive")
	}
	bodyCopy := first.Body()
	bodyCopy[0] = 'x'
	if bytes.Equal(bodyCopy, first.Body()) {
		t.Fatal("provider request body was mutable through Body")
	}
	if bytes.Contains(first.Body(), []byte("synthetic-secret")) {
		t.Fatal("credential entered persisted provider bytes")
	}
	raw, err := client.Send(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(fixtureBody, first.Body()) {
		t.Fatalf("fixture body differs from rendered body\nrendered: %s\nfixture: %s", first.Body(), fixtureBody)
	}
	if string(raw.Response) != `{"response":"synthetic answer","done":true}` || raw.ProviderRequestID != "request-synthetic-1" {
		t.Fatalf("raw result = %#v", raw)
	}
	parsed, err := Parse(first, raw)
	if err != nil || parsed.Text != "synthetic answer" {
		t.Fatalf("parsed = %#v, err = %v", parsed, err)
	}
	t.Logf("RAW rendered provider request: route=%s model=%s body=%s", first.Route(), first.Model(), first.Body())
	t.Logf("RAW fixture request body: %s", fixtureBody)
	t.Logf("RAW provider response before parsing: status=%s request_id=%s body=%s", raw.Status, raw.ProviderRequestID, raw.Response)
}

func TestSendReturnsRawHTTPErrorBeforeParsing(t *testing.T) {
	server := sandboxSafeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Request-ID", "request-synthetic-503")
		http.Error(w, "synthetic overload", http.StatusServiceUnavailable)
	}))
	client := newTestClient(t, Config{BaseURL: server.URL, Model: "fixture-model"})
	request, err := client.Render(Request{Prompt: "synthetic failure"})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := client.Send(context.Background(), request)
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("error = %T %v", err, err)
	}
	if string(raw.Response) != "synthetic overload\n" || raw.ProviderRequestID != "request-synthetic-503" {
		t.Fatalf("raw result = %#v", raw)
	}
}

func TestValidateRequestBindsConfiguredEndpointAndModel(t *testing.T) {
	client := newTestClient(t, Config{BaseURL: "https://models.example.com/api", Model: "fixture-model"})
	request, err := client.Render(Request{Prompt: "synthetic request"})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.ValidateRequest(request); err != nil {
		t.Fatal(err)
	}
	cases := []ProviderRequest{}
	changedRoute, err := RestoreProviderRequest("https://other.example.com/api/generate", request.Model(), request.Body())
	if err != nil {
		t.Fatal(err)
	}
	cases = append(cases, changedRoute)
	changedModel, err := RestoreProviderRequest(request.Route(), "other-model", request.Body())
	if err != nil {
		t.Fatal(err)
	}
	cases = append(cases, changedModel)
	changedBody, err := RestoreProviderRequest(request.Route(), request.Model(), []byte(`{"model":"other-model","prompt":"synthetic request","stream":false}`))
	if err != nil {
		t.Fatal(err)
	}
	cases = append(cases, changedBody)
	for _, changed := range cases {
		if err := client.ValidateRequest(changed); err == nil {
			t.Fatalf("accepted changed request: route=%s model=%s body=%s", changed.Route(), changed.Model(), changed.Body())
		}
	}
}
