package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const graphqlEndpoint = "https://api.linear.app/graphql"

type GraphQLClient struct {
	httpClient *http.Client
	tokens     *TokenStore
	logger     *requestLogger
}

type graphRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables,omitempty"`
}

type graphResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []graphError    `json:"errors"`
}

type graphError struct {
	Message string         `json:"message"`
	Path    []any          `json:"path,omitempty"`
	Extra   map[string]any `json:"extensions,omitempty"`
}

func NewGraphQLClient(httpClient *http.Client, tokens *TokenStore, logger *requestLogger) *GraphQLClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &GraphQLClient{httpClient: httpClient, tokens: tokens, logger: logger}
}

func (c *GraphQLClient) Do(ctx context.Context, query string, variables map[string]any, out any) error {
	token, err := c.tokens.Token(ctx)
	if err != nil {
		return err
	}
	status, body, err := c.post(ctx, token, query, variables)
	if err != nil {
		return err
	}
	if c.needsAuthRetry(status, body) {
		token, err = c.tokens.Refresh(ctx)
		if err != nil {
			return err
		}
		status, body, err = c.post(ctx, token, query, variables)
		if err != nil {
			return err
		}
	}
	if status < 200 || status > 299 {
		return fmt.Errorf("Linear GraphQL request failed (HTTP %d): %s", status, graphHTTPErrorMessage(body))
	}
	return decodeGraphResponse(body, out, c.logger)
}

func (c *GraphQLClient) post(ctx context.Context, token, query string, variables map[string]any) (int, []byte, error) {
	payload, err := json.Marshal(graphRequest{Query: query, Variables: variables})
	if err != nil {
		return 0, nil, fmt.Errorf("encode GraphQL request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, graphqlEndpoint, bytes.NewReader(payload))
	if err != nil {
		return 0, nil, fmt.Errorf("build GraphQL request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	start := time.Now()
	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.logger.LogAPICall(apiLogEntry{
			Method:      http.MethodPost,
			Path:        "/graphql",
			Duration:    time.Since(start),
			Summary:     err.Error(),
			RequestBody: payload,
		})
		return 0, nil, fmt.Errorf("request Linear GraphQL: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		c.logger.LogAPICall(apiLogEntry{
			Method:      http.MethodPost,
			Path:        "/graphql",
			Status:      resp.StatusCode,
			Duration:    time.Since(start),
			Summary:     err.Error(),
			RequestBody: payload,
		})
		return 0, nil, fmt.Errorf("read GraphQL response: %w", err)
	}
	summary := ""
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		summary = graphHTTPErrorMessage(body)
	}
	c.logger.LogAPICall(apiLogEntry{
		Method:       http.MethodPost,
		Path:         "/graphql",
		Status:       resp.StatusCode,
		Duration:     time.Since(start),
		Summary:      summary,
		RequestBody:  payload,
		ResponseBody: body,
	})
	return resp.StatusCode, body, nil
}

func decodeGraphResponse(body []byte, out any, logger *requestLogger) error {
	var response graphResponse
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&response); err != nil {
		return fmt.Errorf("decode GraphQL response: %w", err)
	}
	hasData := graphResponseHasData(response.Data)
	if len(response.Errors) > 0 && !hasData {
		summary := graphErrorText(response.Errors)
		logger.LogDiagnostic("error", "Linear GraphQL returned errors without data: "+summary)
		return fmt.Errorf("Linear GraphQL: %s", summary)
	}
	if len(response.Errors) > 0 {
		logger.Warn("Linear GraphQL returned data with errors: " + graphErrorText(response.Errors))
	}
	if out == nil {
		return nil
	}
	if !hasData {
		return fmt.Errorf("Linear GraphQL response did not include data")
	}
	if err := json.Unmarshal(response.Data, out); err != nil {
		logger.LogDiagnostic("error", "Linear GraphQL returned data that could not be decoded: "+err.Error())
		return fmt.Errorf("decode GraphQL data: %w", err)
	}
	return nil
}

func (c *GraphQLClient) needsAuthRetry(status int, body []byte) bool {
	if status == http.StatusUnauthorized {
		return true
	}
	if status < 200 || status > 299 {
		return false
	}
	var response graphResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return false
	}
	for _, err := range response.Errors {
		if graphErrorIsAuth(err) {
			return true
		}
	}
	return false
}

func graphErrorIsAuth(err graphError) bool {
	code := strings.ToUpper(strings.TrimSpace(fmt.Sprint(err.Extra["code"])))
	if code == "AUTHENTICATION_ERROR" || code == "UNAUTHENTICATED" {
		return true
	}
	return strings.Contains(strings.ToLower(err.Message), "authentication")
}

func graphResponseHasData(data json.RawMessage) bool {
	trimmed := bytes.TrimSpace(data)
	return len(trimmed) > 0 && string(trimmed) != "null"
}

func graphHTTPErrorMessage(body []byte) string {
	var response graphResponse
	if err := json.Unmarshal(body, &response); err == nil && len(response.Errors) > 0 {
		return graphErrorText(response.Errors)
	}
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

func graphErrorText(errors []graphError) string {
	parts := make([]string, 0, len(errors))
	for _, err := range errors {
		message := strings.TrimSpace(err.Message)
		if message == "" {
			message = "unknown GraphQL error"
		}
		parts = append(parts, message)
	}
	return strings.Join(parts, "; ")
}
