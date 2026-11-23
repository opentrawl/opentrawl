package beeperapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultBeeperAPIBaseURL is the loopback address served by Beeper Desktop's
	// local read-only HTTP API.
	DefaultBeeperAPIBaseURL = "http://127.0.0.1:23373"
	// DefaultBeeperRequestTimeout bounds each read-only HTTP GET request so an
	// App-Napped or unreachable Beeper Desktop process fails promptly without
	// hanging OpenTrawl.
	DefaultBeeperRequestTimeout = 15 * time.Second
	maxBeeperAPIResponseBytes   = 32 << 20
	maxBeeperChatsPageLimit     = 200
)

// BeeperDesktopClient performs strictly read-only HTTP GET calls against
// Beeper Desktop's local HTTP API.
type BeeperDesktopClient struct {
	baseURL     string
	bearerToken string
	httpClient  *http.Client
}

// BeeperAPIAuthError reports that Beeper Desktop rejected the Bearer token.
type BeeperAPIAuthError struct {
	StatusCode int
}

func (e *BeeperAPIAuthError) Error() string {
	return fmt.Sprintf("Beeper Desktop API rejected credentials (HTTP %d)", e.StatusCode)
}

// BeeperAPIUnreachableError reports that Beeper Desktop did not respond (for
// example, the app is closed, asleep in macOS App Nap, or timed out).
type BeeperAPIUnreachableError struct {
	Endpoint string
	Cause    error
}

func (e *BeeperAPIUnreachableError) Error() string {
	return fmt.Sprintf("Beeper Desktop API unreachable at %s: %v", e.Endpoint, e.Cause)
}

func (e *BeeperAPIUnreachableError) Unwrap() error {
	return e.Cause
}

// BeeperAPIStatusError reports an unexpected non-200 HTTP status from Beeper Desktop.
type BeeperAPIStatusError struct {
	Endpoint   string
	StatusCode int
}

func (e *BeeperAPIStatusError) Error() string {
	return fmt.Sprintf("Beeper Desktop API %s returned HTTP %d", e.Endpoint, e.StatusCode)
}

// NewBeeperDesktopClient constructs a read-only client for Beeper Desktop.
func NewBeeperDesktopClient(baseURL string, bearerToken string, baseHTTPClient *http.Client, requestTimeout time.Duration) (*BeeperDesktopClient, error) {
	trimmedURL := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if trimmedURL == "" {
		trimmedURL = DefaultBeeperAPIBaseURL
	}
	parsed, err := url.Parse(trimmedURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || strings.TrimSpace(parsed.Host) == "" {
		return nil, fmt.Errorf("invalid Beeper Desktop API base URL %q", baseURL)
	}
	trimmedToken := strings.TrimSpace(bearerToken)
	if trimmedToken == "" {
		return nil, errors.New("beeper desktop access token is empty")
	}
	return &BeeperDesktopClient{
		baseURL:     trimmedURL,
		bearerToken: trimmedToken,
		httpClient:  NewProxyConnectTunnelHTTPClient(baseHTTPClient, requestTimeout),
	}, nil
}

// ListAccounts fetches GET /v1/accounts.
func (client *BeeperDesktopClient) ListAccounts(ctx context.Context) ([]BeeperAccount, error) {
	body, err := client.getJSONBytes(ctx, "/v1/accounts", nil)
	if err != nil {
		return nil, err
	}
	var accounts []BeeperAccount
	if err := json.Unmarshal(body, &accounts); err != nil {
		return nil, fmt.Errorf("decode GET /v1/accounts: %w", err)
	}
	return accounts, nil
}

// ListChatsPage fetches one page from GET /v1/chats.
func (client *BeeperDesktopClient) ListChatsPage(ctx context.Context, cursor string, limit int) (BeeperChatsPage, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > maxBeeperChatsPageLimit {
		limit = maxBeeperChatsPageLimit
	}
	query := url.Values{}
	query.Set("limit", strconv.Itoa(limit))
	if trimmedCursor := strings.TrimSpace(cursor); trimmedCursor != "" {
		query.Set("cursor", trimmedCursor)
		query.Set("direction", "before")
	}
	body, err := client.getJSONBytes(ctx, "/v1/chats", query)
	if err != nil {
		return BeeperChatsPage{}, err
	}
	var page BeeperChatsPage
	if err := json.Unmarshal(body, &page); err != nil {
		return BeeperChatsPage{}, fmt.Errorf("decode GET /v1/chats: %w", err)
	}
	return page, nil
}

// ListChatMessagesPage fetches one page from GET /v1/chats/{chatID}/messages.
func (client *BeeperDesktopClient) ListChatMessagesPage(ctx context.Context, chatID string, cursor string, direction string) (BeeperMessagesPage, error) {
	trimmedChatID := strings.TrimSpace(chatID)
	if trimmedChatID == "" {
		return BeeperMessagesPage{}, errors.New("chatID is required")
	}
	query := url.Values{}
	if trimmedCursor := strings.TrimSpace(cursor); trimmedCursor != "" {
		query.Set("cursor", trimmedCursor)
		dir := strings.TrimSpace(direction)
		if dir == "" {
			dir = "before"
		}
		query.Set("direction", dir)
	}
	endpoint := "/v1/chats/" + url.PathEscape(trimmedChatID) + "/messages"
	body, err := client.getJSONBytes(ctx, endpoint, query)
	if err != nil {
		return BeeperMessagesPage{}, err
	}
	var page BeeperMessagesPage
	if err := json.Unmarshal(body, &page); err != nil {
		return BeeperMessagesPage{}, fmt.Errorf("decode GET %s: %w", endpoint, err)
	}
	return page, nil
}

func (client *BeeperDesktopClient) getJSONBytes(ctx context.Context, endpoint string, query url.Values) ([]byte, error) {
	fullURL := client.baseURL + endpoint
	if len(query) > 0 {
		fullURL += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build GET %s request: %w", endpoint, err)
	}
	req.Header.Set("Authorization", "Bearer "+client.bearerToken)
	req.Header.Set("Accept", "application/json")

	resp, err := client.httpClient.Do(req)
	if err != nil {
		if isLikelyUnreachableError(err) {
			return nil, &BeeperAPIUnreachableError{Endpoint: endpoint, Cause: err}
		}
		return nil, fmt.Errorf("GET %s: %w", endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, &BeeperAPIAuthError{StatusCode: resp.StatusCode}
	}
	if resp.StatusCode == http.StatusBadGateway || resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusGatewayTimeout {
		return nil, &BeeperAPIUnreachableError{
			Endpoint: endpoint,
			Cause:    fmt.Errorf("HTTP %d", resp.StatusCode),
		}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &BeeperAPIStatusError{Endpoint: endpoint, StatusCode: resp.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBeeperAPIResponseBytes))
	if err != nil {
		if isLikelyUnreachableError(err) {
			return nil, &BeeperAPIUnreachableError{Endpoint: endpoint, Cause: err}
		}
		return nil, fmt.Errorf("read GET %s response: %w", endpoint, err)
	}
	return body, nil
}

func isLikelyUnreachableError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "connection refused") ||
		strings.Contains(text, "upstream dial failed") ||
		strings.Contains(text, "context deadline exceeded") ||
		strings.Contains(text, "client.timeout") ||
		strings.Contains(text, "eof")
}
