package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const tokenEndpoint = "https://api.linear.app/oauth/token"
const tokenExpiryMargin = 60 * time.Second

type TokenStore struct {
	path         string
	clientID     string
	clientSecret string
	httpClient   *http.Client
	logger       *requestLogger
	now          func() time.Time
	mu           sync.Mutex
	loaded       bool
	cached       tokenCache
}

type tokenCache struct {
	AccessToken string    `json:"access_token"`
	ExpiresAt   time.Time `json:"expires_at"`
	Scope       string    `json:"scope,omitempty"`
	TokenType   string    `json:"token_type,omitempty"`
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int64  `json:"expires_in"`
	Scope       string `json:"scope"`
	TokenType   string `json:"token_type"`
}

func NewTokenStore(httpClient *http.Client, logger *requestLogger) (*TokenStore, error) {
	clientID := strings.TrimSpace(os.Getenv("LINEAR_CLIENT_ID"))
	clientSecret := strings.TrimSpace(os.Getenv("LINEAR_CLIENT_SECRET"))
	if clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf("LINEAR_CLIENT_ID and LINEAR_CLIENT_SECRET are required")
	}
	path, err := linearTokenPath()
	if err != nil {
		return nil, err
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &TokenStore{
		path:         path,
		clientID:     clientID,
		clientSecret: clientSecret,
		httpClient:   httpClient,
		logger:       logger,
		now:          time.Now,
	}, nil
}

func (s *TokenStore) Token(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cached, ok, err := s.loadOnce()
	if err != nil {
		return "", err
	}
	if ok && s.valid(cached) {
		return cached.AccessToken, nil
	}
	return s.refreshLocked(ctx)
}

func (s *TokenStore) Refresh(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refreshLocked(ctx)
}

func (s *TokenStore) refreshLocked(ctx context.Context) (string, error) {
	token, err := s.mint(ctx)
	if err != nil {
		return "", err
	}
	if err := s.save(token); err != nil {
		return "", err
	}
	s.cached = token
	s.loaded = true
	return token.AccessToken, nil
}

func (s *TokenStore) loadOnce() (tokenCache, bool, error) {
	if s.loaded {
		return s.cached, s.cached.AccessToken != "", nil
	}
	cached, ok, err := s.load()
	if err != nil {
		return tokenCache{}, false, err
	}
	s.loaded = true
	if ok {
		s.cached = cached
	}
	return cached, ok, nil
}

func (s *TokenStore) load() (tokenCache, bool, error) {
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return tokenCache{}, false, nil
	}
	if err != nil {
		return tokenCache{}, false, fmt.Errorf("read token cache: %w", err)
	}
	var cached tokenCache
	if err := json.Unmarshal(data, &cached); err != nil {
		s.logger.Warn("token cache was invalid; minting a new token")
		return tokenCache{}, false, nil
	}
	if cached.AccessToken == "" || cached.ExpiresAt.IsZero() {
		s.logger.Warn("token cache was incomplete; minting a new token")
		return tokenCache{}, false, nil
	}
	return cached, true, nil
}

func (s *TokenStore) save(token tokenCache) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create token cache directory: %w", err)
	}
	data, err := json.MarshalIndent(token, "", "  ")
	if err != nil {
		return fmt.Errorf("encode token cache: %w", err)
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, ".token-*.tmp")
	if err != nil {
		return fmt.Errorf("create token cache temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write token cache: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close token cache temp file: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("replace token cache: %w", err)
	}
	return nil
}

func (s *TokenStore) mint(ctx context.Context) (tokenCache, error) {
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", s.clientID)
	form.Set("client_secret", s.clientSecret)
	form.Set("scope", "read,write")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return tokenCache{}, fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	start := time.Now()
	resp, err := s.httpClient.Do(req)
	if err != nil {
		s.logger.LogAPICall(apiLogEntry{
			Method:      http.MethodPost,
			Path:        "/oauth/token",
			Duration:    time.Since(start),
			Summary:     err.Error(),
			RequestBody: tokenRequestLogBody(),
		})
		return tokenCache{}, fmt.Errorf("request Linear token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		s.logger.LogAPICall(apiLogEntry{
			Method:      http.MethodPost,
			Path:        "/oauth/token",
			Status:      resp.StatusCode,
			Duration:    time.Since(start),
			Summary:     err.Error(),
			RequestBody: tokenRequestLogBody(),
		})
		return tokenCache{}, fmt.Errorf("read token response: %w", err)
	}
	summary := ""
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		summary = tokenErrorMessage(body)
	}
	s.logger.LogAPICall(apiLogEntry{
		Method:       http.MethodPost,
		Path:         "/oauth/token",
		Status:       resp.StatusCode,
		Duration:     time.Since(start),
		Summary:      summary,
		RequestBody:  tokenRequestLogBody(),
		ResponseBody: redactTokenBody(body),
	})
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return tokenCache{}, fmt.Errorf("linear rejected the token request (HTTP %d): %s", resp.StatusCode, summary)
	}
	var decoded tokenResponse
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&decoded); err != nil {
		s.logger.LogDiagnostic("error", "Linear token response could not be decoded: "+err.Error())
		return tokenCache{}, fmt.Errorf("decode token response: %w", err)
	}
	if decoded.AccessToken == "" || decoded.ExpiresIn <= 0 {
		s.logger.LogDiagnostic("error", "Linear token response was missing access_token or expires_in")
		return tokenCache{}, fmt.Errorf("token response is missing access_token or expires_in")
	}
	return tokenCache{
		AccessToken: decoded.AccessToken,
		ExpiresAt:   s.now().Add(time.Duration(decoded.ExpiresIn) * time.Second),
		Scope:       decoded.Scope,
		TokenType:   decoded.TokenType,
	}, nil
}

func (s *TokenStore) valid(cached tokenCache) bool {
	return cached.AccessToken != "" && !cached.ExpiresAt.IsZero() && s.now().Add(tokenExpiryMargin).Before(cached.ExpiresAt)
}

func tokenRequestLogBody() []byte {
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", "<redacted>")
	form.Set("client_secret", "<redacted>")
	form.Set("scope", "read,write")
	return []byte(form.Encode())
}

func redactTokenBody(body []byte) []byte {
	var fields map[string]any
	if err := json.Unmarshal(body, &fields); err != nil {
		return body
	}
	for _, key := range []string{"access_token", "refresh_token", "id_token"} {
		if _, ok := fields[key]; ok {
			fields[key] = "<redacted>"
		}
	}
	out, err := json.Marshal(fields)
	if err != nil {
		return body
	}
	return out
}

func tokenErrorMessage(body []byte) string {
	var fields map[string]any
	if err := json.Unmarshal(body, &fields); err == nil {
		for _, key := range []string{"error_description", "error", "message"} {
			if text, ok := fields[key].(string); ok && strings.TrimSpace(text) != "" {
				return strings.TrimSpace(text)
			}
		}
	}
	if strings.TrimSpace(string(body)) == "" {
		return "Linear returned an empty response body"
	}
	return "Linear did not return a readable error message"
}
