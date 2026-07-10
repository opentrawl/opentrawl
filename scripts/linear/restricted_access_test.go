package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRestrictedCLIReadsContinueWithoutRequestLog(t *testing.T) {
	withUnavailableRequestLog(t)
	t.Setenv("LINEAR_CLIENT_ID", "synthetic-client")
	t.Setenv("LINEAR_CLIENT_SECRET", "synthetic-secret")
	graph := &restrictedReadGraph{}
	oldFactory := newLinearAPI
	newLinearAPI = func(stderr io.Writer, verbosity int) (*LinearAPI, error) {
		api, err := NewLinearAPI(stderr, verbosity)
		if err != nil {
			return nil, err
		}
		api.graph = graph
		return api, nil
	}
	t.Cleanup(func() { newLinearAPI = oldFactory })

	for _, args := range [][]string{{"issue", "TRAWL-1"}, {"issues", "--team", "TRAWL"}} {
		var stdout, stderr bytes.Buffer
		if err := execute(args, bytes.NewReader(nil), &stdout, &stderr); err != nil {
			t.Fatalf("linear %s returned error: %v", strings.Join(args, " "), err)
		}
		if !strings.Contains(stderr.String(), "request logging is unavailable for this read") {
			t.Fatalf("linear %s stderr = %q, want unavailable-log warning", strings.Join(args, " "), stderr.String())
		}
	}
	if got, want := graph.queries, []string{issueByIdentifierQuery, listIssuesQuery}; !reflect.DeepEqual(got, want) {
		t.Fatalf("GraphQL queries = %#v, want %#v", got, want)
	}
}

func TestEveryCLIWriteRequiresAuditBeforeAPIConstruction(t *testing.T) {
	withUnavailableRequestLog(t)
	t.Setenv("LINEAR_CLIENT_ID", "synthetic-client")
	t.Setenv("LINEAR_CLIENT_SECRET", "synthetic-secret")
	oldFactory := newLinearWriteAPI
	newLinearWriteAPI = NewLinearWriteAPI
	t.Cleanup(func() { newLinearWriteAPI = oldFactory })

	cases := [][]string{
		{"ack", "00000000-0000-4000-8000-000000000001"},
		{"comment", "TRAWL-1", "Synthetic comment", "--as", "test actor"},
		{"issue", "new", "--team", "TRAWL", "--title", "Synthetic issue", "--as", "test actor"},
		{"issue", "state", "TRAWL-1", "--state", "Done", "--as", "test actor"},
		{"issue", "update", "TRAWL-1", "--priority", "high", "--as", "test actor"},
		{"project", "update", "Synthetic project", "--summary", "Synthetic summary", "--as", "test actor"},
		{"project", "milestone", "ensure", "Synthetic project", "--name", "Synthetic milestone", "--as", "test actor"},
	}
	for _, args := range cases {
		var stdout, stderr bytes.Buffer
		err := execute(args, bytes.NewReader(nil), &stdout, &stderr)
		if err == nil || !strings.Contains(err.Error(), "open required Linear write audit") {
			t.Fatalf("linear %s error = %v, want required-audit error", strings.Join(args, " "), err)
		}
	}
}

func TestMCPReadThenWriteUpgradesToRequiredAudit(t *testing.T) {
	graph := &restrictedReadGraph{}
	var accesses []toolAccess
	server := &MCPServer{
		stderr: &bytes.Buffer{},
		newAPI: func(_ io.Writer, _ int, access toolAccess) (*LinearAPI, error) {
			accesses = append(accesses, access)
			if access == toolWrite {
				return nil, errors.New("open required Linear write audit: synthetic denial")
			}
			return &LinearAPI{graph: graph}, nil
		},
	}
	if _, err := server.runTool("get_issue", rawArguments(map[string]string{"issue": "TRAWL-1"})); err != nil {
		t.Fatalf("get_issue returned error: %v", err)
	}
	_, err := server.runTool("create_comment", rawArguments(map[string]string{
		"issue": "TRAWL-1", "actor": "test actor", "body": "Synthetic comment",
	}))
	if err == nil || !strings.Contains(err.Error(), "required Linear write audit") {
		t.Fatalf("create_comment error = %v, want required-audit error", err)
	}
	if got, want := accesses, []toolAccess{toolRead, toolWrite}; !reflect.DeepEqual(got, want) {
		t.Fatalf("API accesses = %#v, want %#v", got, want)
	}
	if got, want := graph.queries, []string{issueByIdentifierQuery}; !reflect.DeepEqual(got, want) {
		t.Fatalf("GraphQL queries = %#v, want no mutation after read: %#v", got, want)
	}
}

func TestEveryMCPToolHasOneAccessClassification(t *testing.T) {
	var exposed []string
	for _, tool := range mcpTools() {
		name := tool["name"].(string)
		exposed = append(exposed, name)
		if _, ok := mcpToolAccess[name]; !ok {
			t.Errorf("exposed tool %q has no access classification", name)
		}
	}
	if len(exposed) != len(mcpToolAccess) {
		t.Fatalf("exposed tools = %d, classifications = %d", len(exposed), len(mcpToolAccess))
	}
	for name := range mcpToolAccess {
		if !containsString(exposed, name) {
			t.Errorf("classification exists for unexposed tool %q", name)
		}
	}
	writes := []string{"ack_comment", "create_comment", "create_issue", "ensure_project_milestone", "update_issue", "update_project"}
	for name, access := range mcpToolAccess {
		want := toolRead
		if containsString(writes, name) {
			want = toolWrite
		}
		if access != want {
			t.Errorf("tool %q access = %v, want %v", name, access, want)
		}
	}
}

func TestRefreshedTokenSurvivesCacheWriteFailure(t *testing.T) {
	logger, stderr := testRequestLogger(t)
	blockedParent := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blockedParent, []byte("block"), 0o600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	store := &TokenStore{
		path:         filepath.Join(blockedParent, "token.json"),
		clientID:     "synthetic-client",
		clientSecret: "synthetic-secret",
		httpClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			calls++
			return syntheticTokenResponse(), nil
		})},
		logger: logger,
		now:    func() time.Time { return time.Unix(1000, 0) },
	}
	token, err := store.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh returned error: %v", err)
	}
	if token != "synthetic-access-token" {
		t.Fatalf("token = %q", token)
	}
	second, err := store.Token(context.Background())
	if err != nil || second != token {
		t.Fatalf("cached Token = %q, error = %v", second, err)
	}
	if calls != 1 {
		t.Fatalf("token requests = %d, want 1", calls)
	}
	if count := strings.Count(stderr.String(), "token cache could not be saved"); count != 1 {
		t.Fatalf("save warning count = %d, want 1; stderr %q", count, stderr.String())
	}
}

func TestWritableLoggingAndTokenCacheRemainDurable(t *testing.T) {
	logger, _ := testRequestLogger(t)
	path := filepath.Join(t.TempDir(), "linear", "token.json")
	store := &TokenStore{
		path:         path,
		clientID:     "synthetic-client",
		clientSecret: "synthetic-secret",
		httpClient:   &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return syntheticTokenResponse(), nil })},
		logger:       logger,
		now:          func() time.Time { return time.Unix(1000, 0) },
	}
	if _, err := store.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh returned error: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read token cache: %v", err)
	}
	if !bytes.Contains(data, []byte(`"access_token": "synthetic-access-token"`)) {
		t.Fatalf("token cache did not contain the synthetic token: %s", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat token cache: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("token cache mode = %v, want 0600", info.Mode().Perm())
	}
	logger.LogDiagnostic("info", "synthetic audit entry")
	if err := logger.file.Sync(); err != nil {
		t.Fatal(err)
	}
	logData, err := os.ReadFile(logger.file.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(logData, []byte("synthetic audit entry")) {
		t.Fatalf("request log did not append the audit entry: %s", logData)
	}
}

func withUnavailableRequestLog(t *testing.T) {
	t.Helper()
	oldFactory := requestLoggerFactory
	requestLoggerFactory = func(io.Writer, int) (*requestLogger, error) {
		return nil, errors.New("synthetic log permission denied")
	}
	t.Cleanup(func() { requestLoggerFactory = oldFactory })
}

type restrictedReadGraph struct {
	queries []string
}

func (graph *restrictedReadGraph) Do(_ context.Context, query string, _ map[string]any, out any) error {
	graph.queries = append(graph.queries, query)
	switch query {
	case issueByIdentifierQuery:
		return setGraphOut(out, map[string]any{"issues": map[string]any{"nodes": []Issue{{ID: "issue-1", Identifier: "TRAWL-1", Title: "Synthetic issue", State: IssueState{Name: "Todo"}}}}})
	case listIssuesQuery:
		return setGraphOut(out, map[string]any{"issues": map[string]any{"nodes": []Issue{{ID: "issue-1", Identifier: "TRAWL-1", Title: "Synthetic issue", State: IssueState{Name: "Todo"}}}}})
	default:
		return errors.New("unexpected GraphQL query")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func syntheticTokenResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"access_token":"synthetic-access-token","expires_in":3600,"scope":"read,write","token_type":"Bearer"}`)),
		Header:     make(http.Header),
	}
}

func rawArguments(values map[string]string) map[string]json.RawMessage {
	result := make(map[string]json.RawMessage, len(values))
	for name, value := range values {
		encoded, _ := json.Marshal(value)
		result[name] = encoded
	}
	return result
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
