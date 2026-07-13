package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const restrictedAccessEvidenceEnv = "TRAWL253_EVIDENCE_DIR"

func TestTRAWL253BoundaryEvidence(t *testing.T) {
	dir := os.Getenv(restrictedAccessEvidenceEnv)
	if dir == "" {
		t.Skip("set TRAWL253_EVIDENCE_DIR to capture synthetic boundary evidence")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	withUnavailableRequestLog(t)
	t.Setenv("LINEAR_CLIENT_ID", "synthetic-client")
	t.Setenv("LINEAR_CLIENT_SECRET", "synthetic-secret")

	captureRestrictedCLIRead(t, dir)
	captureRestrictedMCPSession(t, dir)
	captureRestrictedTokenRefresh(t, dir)
}

func captureRestrictedCLIRead(t *testing.T, dir string) {
	t.Helper()
	graph := &rawRestrictedGraph{}
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
	args := []string{"issue", "TRAWL-1"}
	var stdout, stderr bytes.Buffer
	if err := execute(args, bytes.NewReader(nil), &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	writeJSONEvidence(t, filepath.Join(dir, "cli-read.json"), map[string]any{
		"input":              map[string]any{"argv": append([]string{"linear"}, args...)},
		"output":             map[string]any{"stdout": stdout.String(), "stderr": stderr.String()},
		"graphql_boundaries": graph.calls,
	})
}

func captureRestrictedMCPSession(t *testing.T, dir string) {
	t.Helper()
	graph := &rawRestrictedGraph{}
	frames := []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "get_issue", "arguments": map[string]any{"issue": "TRAWL-1"}}},
		{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "create_comment", "arguments": map[string]any{"issue": "TRAWL-1", "actor": "test actor", "body": "Synthetic comment"}}},
	}
	var input bytes.Buffer
	for _, frame := range frames {
		if err := json.NewEncoder(&input).Encode(frame); err != nil {
			t.Fatal(err)
		}
	}
	var output, stderr bytes.Buffer
	var accesses []string
	server := &MCPServer{
		stdin:  &input,
		stdout: &output,
		stderr: &stderr,
		newAPI: func(stderr io.Writer, verbosity int, access toolAccess) (*LinearAPI, error) {
			if access == toolWrite {
				accesses = append(accesses, "write: refused before GraphQL")
				return NewLinearWriteAPI(stderr, verbosity)
			}
			accesses = append(accesses, "read: optional log")
			api, err := NewLinearAPI(stderr, verbosity)
			if err != nil {
				return nil, err
			}
			api.graph = graph
			return api, nil
		},
	}
	if err := server.Serve(); err != nil {
		t.Fatal(err)
	}
	writeJSONEvidence(t, filepath.Join(dir, "mcp-read-then-write.json"), map[string]any{
		"input_frames":       frames,
		"raw_output_frames":  output.String(),
		"stderr":             stderr.String(),
		"access_transitions": accesses,
		"graphql_boundaries": graph.calls,
	})
}

func captureRestrictedTokenRefresh(t *testing.T, dir string) {
	t.Helper()
	blockedParent := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blockedParent, []byte("block"), 0o600); err != nil {
		t.Fatal(err)
	}
	logger, stderr := testRequestLogger(t)
	var requestEvidence map[string]any
	rawResponse := `{"access_token":"synthetic-access-token","expires_in":3600,"scope":"read write initiative:read initiative:write","token_type":"Bearer"}`
	store := &TokenStore{
		path:         filepath.Join(blockedParent, "token.json"),
		clientID:     "synthetic-client",
		clientSecret: "synthetic-secret",
		httpClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
			form, err := url.ParseQuery(string(body))
			if err != nil {
				t.Fatal(err)
			}
			form.Set("client_secret", "[synthetic secret redacted]")
			requestEvidence = map[string]any{
				"method": request.Method,
				"url":    request.URL.String(),
				"form":   form,
			}
			response := syntheticTokenResponse()
			return response, nil
		})},
		logger: logger,
		now:    func() time.Time { return time.Unix(1000, 0) },
	}
	token, err := store.Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cached, err := store.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	writeJSONEvidence(t, filepath.Join(dir, "token-refresh.json"), map[string]any{
		"http_input":      requestEvidence,
		"raw_http_output": rawResponse,
		"process_output": map[string]any{
			"refresh_token": token,
			"cached_token":  cached,
			"stderr":        stderr.String(),
		},
	})
}

type rawBoundaryCall struct {
	Input  string `json:"raw_input"`
	Output string `json:"raw_output"`
}

type rawRestrictedGraph struct {
	calls []rawBoundaryCall
}

func (graph *rawRestrictedGraph) Do(_ context.Context, query string, variables map[string]any, out any) error {
	input, err := json.Marshal(graphRequest{Query: query, Variables: variables})
	if err != nil {
		return err
	}
	data := map[string]any{"issues": map[string]any{"nodes": []Issue{{ID: "issue-1", Identifier: "TRAWL-1", Title: "Synthetic issue", State: IssueState{Name: "Todo"}}}}}
	output, err := json.Marshal(map[string]any{"data": data})
	if err != nil {
		return err
	}
	graph.calls = append(graph.calls, rawBoundaryCall{Input: string(input), Output: string(output)})
	return setGraphOut(out, data)
}

func writeJSONEvidence(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestTRAWL253EvidenceContainsNoPrivatePaths(t *testing.T) {
	dir := os.Getenv(restrictedAccessEvidenceEnv)
	if dir == "" {
		t.Skip("set TRAWL253_EVIDENCE_DIR to inspect synthetic boundary evidence")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"/Users/", "@gmail.com", "+31"} {
			if strings.Contains(string(data), forbidden) {
				t.Errorf("%s contains forbidden private marker %q", entry.Name(), forbidden)
			}
		}
	}
}
