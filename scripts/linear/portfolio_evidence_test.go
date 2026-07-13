package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const portfolioEvidenceEnv = "TRAWL305_EVIDENCE_DIR"

func TestTRAWL305BoundaryEvidence(t *testing.T) {
	dir := os.Getenv(portfolioEvidenceEnv)
	if dir == "" {
		t.Skip("set TRAWL305_EVIDENCE_DIR to capture synthetic portfolio boundary evidence")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	descriptionPath := filepath.Join(dir, "project.md")
	if err := os.WriteFile(descriptionPath, []byte("# Synthetic project\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		name  string
		cli   []string
		tool  string
		args  map[string]any
		graph func() *portfolioGraph
	}{
		{
			name:  "project-read",
			cli:   []string{"project", "Process & tooling"},
			tool:  "get_project",
			args:  map[string]any{"project": "Process & tooling"},
			graph: newPortfolioGraph,
		},
		{
			name:  "project-create-with-initiative",
			cli:   []string{"project", "create", "--team", "TRAWL", "--name", "Engineering delivery system", "--summary", "One clear outcome", "--description-file", descriptionPath, "--status", "Triage", "--priority", "high", "--as", "portfolio bot", "--initiative", "OpenTrawl"},
			tool:  "create_project",
			args:  map[string]any{"team": "TRAWL", "name": "Engineering delivery system", "summary": "One clear outcome", "description": "# Synthetic project\n", "status": "Triage", "priority": "high", "actor": "portfolio bot", "initiative": "OpenTrawl"},
			graph: newPortfolioGraph,
		},
		{
			name: "project-update-preserves-initiative-membership",
			cli:  []string{"project", "update", "Process & tooling", "--summary", "Updated summary", "--as", "portfolio bot"},
			tool: "update_project",
			args: map[string]any{"project": "Process & tooling", "summary": "Updated summary", "actor": "portfolio bot"},
			graph: func() *portfolioGraph {
				graph := newPortfolioGraph()
				graph.project.Initiatives.Nodes = []Initiative{{ID: "initiative-existing", Name: "Existing"}}
				graph.updatedProject = graph.project
				return graph
			},
		},
		{
			name:  "initiative-read",
			cli:   []string{"initiative", "OpenTrawl"},
			tool:  "get_initiative",
			args:  map[string]any{"initiative": "OpenTrawl"},
			graph: newPortfolioGraph,
		},
		{
			name: "project-rename-and-attach-initiative",
			cli:  []string{"project", "update", "Process & tooling", "--name", "Engineering delivery system", "--initiative", "OpenTrawl", "--as", "portfolio bot"},
			tool: "update_project",
			args: map[string]any{"project": "Process & tooling", "name": "Engineering delivery system", "initiative": "OpenTrawl", "actor": "portfolio bot"},
			graph: func() *portfolioGraph {
				graph := newPortfolioGraph()
				graph.project.Initiatives.Nodes = []Initiative{{ID: "initiative-existing", Name: "Existing"}}
				graph.updatedProject = graph.project
				return graph
			},
		},
		{
			name:  "initiative-update",
			cli:   []string{"initiative", "update", "OpenTrawl", "--as", "portfolio bot", "--summary", "Updated summary"},
			tool:  "update_initiative",
			args:  map[string]any{"initiative": "OpenTrawl", "actor": "portfolio bot", "summary": "Updated summary"},
			graph: newPortfolioGraph,
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			capturePortfolioCLI(t, filepath.Join(dir, scenario.name+"-cli.json"), scenario.cli, scenario.graph())
			capturePortfolioMCP(t, filepath.Join(dir, scenario.name+"-mcp.json"), scenario.tool, scenario.args, scenario.graph())
		})
	}
	for _, mode := range []string{"missing", "ambiguous"} {
		args := []string{"project", "update", "Process & tooling", "--name", "Renamed project", "--initiative", "OpenTrawl", "--as", "portfolio bot"}
		mcpArgs := map[string]any{"project": "Process & tooling", "name": "Renamed project", "initiative": "OpenTrawl", "actor": "portfolio bot"}
		capturePortfolioCLIError(t, filepath.Join(dir, "project-update-"+mode+"-initiative-cli.json"), args, newPortfolioGraphWithInitiativeResolution(mode))
		capturePortfolioMCPError(t, filepath.Join(dir, "project-update-"+mode+"-initiative-mcp.json"), "update_project", mcpArgs, newPortfolioGraphWithInitiativeResolution(mode))
	}
	createArgs := []string{"project", "create", "--team", "TRAWL", "--name", "Engineering delivery system", "--summary", "One clear outcome", "--description-file", descriptionPath, "--status", "Triage", "--priority", "high", "--as", "portfolio bot", "--initiative", "OpenTrawl"}
	createMCPArgs := map[string]any{"team": "TRAWL", "name": "Engineering delivery system", "summary": "One clear outcome", "description": "# Synthetic project\n", "status": "Triage", "priority": "high", "actor": "portfolio bot", "initiative": "OpenTrawl"}
	capturePortfolioCLIError(t, filepath.Join(dir, "project-create-attach-failure-cli.json"), createArgs, newPortfolioGraphWithAttachError())
	capturePortfolioMCPError(t, filepath.Join(dir, "project-create-attach-failure-mcp.json"), "create_project", createMCPArgs, newPortfolioGraphWithAttachError())
	duplicateArgs := []string{"project", "create", "--team", "TRAWL", "--name", "Engineering delivery system", "--summary", "Summary", "--description-file", descriptionPath, "--status", "Triage", "--priority", "high", "--as", "portfolio bot"}
	duplicateMCPArgs := map[string]any{"team": "TRAWL", "name": "Engineering delivery system", "summary": "Summary", "description": "# Synthetic project\n", "status": "Triage", "priority": "high", "actor": "portfolio bot"}
	duplicateGraph := func() *portfolioGraph { graph := newPortfolioGraph(); graph.duplicateOnSecondPage = true; return graph }
	capturePortfolioCLIError(t, filepath.Join(dir, "project-create-duplicate-page-two-cli.json"), duplicateArgs, duplicateGraph())
	capturePortfolioMCPError(t, filepath.Join(dir, "project-create-duplicate-page-two-mcp.json"), "create_project", duplicateMCPArgs, duplicateGraph())
	capturePortfolioCLIError(t, filepath.Join(dir, "project-update-readback-mismatch-cli.json"), []string{"project", "update", "Process & tooling", "--summary", "Requested summary", "--initiative", "OpenTrawl", "--as", "portfolio bot"}, newPortfolioGraphWithUpdateMismatch())
	capturePortfolioMCPError(t, filepath.Join(dir, "project-update-readback-mismatch-mcp.json"), "update_project", map[string]any{"project": "Process & tooling", "summary": "Requested summary", "initiative": "OpenTrawl", "actor": "portfolio bot"}, newPortfolioGraphWithUpdateMismatch())
	partialUpdate := func() *portfolioGraph { graph := newPortfolioGraph(); graph.projectReadErrorAt = 3; return graph }
	partialArgs := []string{"project", "update", "Process & tooling", "--name", "Engineering delivery system", "--initiative", "OpenTrawl", "--as", "portfolio bot"}
	partialMCPArgs := map[string]any{"project": "Process & tooling", "name": "Engineering delivery system", "initiative": "OpenTrawl", "actor": "portfolio bot"}
	capturePortfolioCLIError(t, filepath.Join(dir, "project-update-attached-readback-failure-cli.json"), partialArgs, partialUpdate())
	capturePortfolioMCPError(t, filepath.Join(dir, "project-update-attached-readback-failure-mcp.json"), "update_project", partialMCPArgs, partialUpdate())
	capturePortfolioRestrictedWrites(t, dir, createArgs, createMCPArgs)
	initiativeArgs := []string{"initiative", "update", "OpenTrawl", "--as", "portfolio bot", "--summary", "Updated summary"}
	initiativeMCPArgs := map[string]any{"initiative": "OpenTrawl", "actor": "portfolio bot", "summary": "Updated summary"}
	initiativeReadFailure := func() *portfolioGraph { graph := newPortfolioGraph(); graph.initiativeReadErrorAt = 1; return graph }
	capturePortfolioCLIError(t, filepath.Join(dir, "initiative-update-readback-failure-cli.json"), initiativeArgs, initiativeReadFailure())
	capturePortfolioMCPError(t, filepath.Join(dir, "initiative-update-readback-failure-mcp.json"), "update_initiative", initiativeMCPArgs, initiativeReadFailure())
	capturePortfolioTopLevelGraphQLError(t, filepath.Join(dir, "top-level-graphql-error.json"))
	capturePortfolioScopeRotation(t, filepath.Join(dir, "cached-scope-rotation.json"))
}

func capturePortfolioRestrictedWrites(t *testing.T, dir string, createCLIArgs []string, createMCPArgs map[string]any) {
	t.Helper()
	denial := errors.New("open required Linear write audit: synthetic denial")
	oldWrite := newLinearWriteAPI
	newLinearWriteAPI = func(io.Writer, int) (*LinearAPI, error) { return nil, denial }
	t.Cleanup(func() { newLinearWriteAPI = oldWrite })
	cases := []struct {
		name string
		cli  []string
		tool string
		args map[string]any
	}{
		{"create-project", createCLIArgs, "create_project", createMCPArgs},
		{"update-project", []string{"project", "update", "Process & tooling", "--summary", "Updated summary", "--as", "portfolio bot"}, "update_project", map[string]any{"project": "Process & tooling", "summary": "Updated summary", "actor": "portfolio bot"}},
		{"update-initiative", []string{"initiative", "update", "OpenTrawl", "--summary", "Updated summary", "--as", "portfolio bot"}, "update_initiative", map[string]any{"initiative": "OpenTrawl", "summary": "Updated summary", "actor": "portfolio bot"}},
	}
	for _, scenario := range cases {
		var stdout, stderr bytes.Buffer
		err := execute(scenario.cli, bytes.NewReader(nil), &stdout, &stderr)
		if err == nil {
			t.Fatalf("CLI accepted restricted %s write", scenario.name)
		}
		writeJSONEvidence(t, filepath.Join(dir, "restricted-"+scenario.name+"-cli.json"), map[string]any{"input": map[string]any{"argv": append([]string{"linear"}, scenario.cli...)}, "output": map[string]string{"stdout": stdout.String(), "stderr": stderr.String(), "error": err.Error()}, "graphql_boundaries": []portfolioGraphCall{}})
		server := &MCPServer{stderr: &bytes.Buffer{}, newAPI: func(io.Writer, int, toolAccess) (*LinearAPI, error) { return nil, denial }}
		_, err = server.runTool(scenario.tool, rawArguments(anyStrings(scenario.args)))
		if err == nil {
			t.Fatalf("MCP accepted restricted %s write", scenario.name)
		}
		writeJSONEvidence(t, filepath.Join(dir, "restricted-"+scenario.name+"-mcp.json"), map[string]any{"input": map[string]any{"tool": scenario.tool, "arguments": scenario.args}, "output": map[string]string{"error": err.Error()}, "graphql_boundaries": []portfolioGraphCall{}})
	}
}

func capturePortfolioTopLevelGraphQLError(t *testing.T, path string) {
	t.Helper()
	response := `{"data":{"projectCreate":{"success":true}},"errors":[{"message":"synthetic top-level refusal"}]}`
	logger, _ := testRequestLogger(t)
	err := decodeGraphResponse([]byte(response), &struct{}{}, logger)
	if err == nil {
		t.Fatal("top-level GraphQL error was accepted")
	}
	writeJSONEvidence(t, path, map[string]any{"input": graphRequest{Query: createProjectMutation, Variables: map[string]any{"input": map[string]any{"name": "Synthetic project"}}}, "response": response, "output": map[string]string{"error": err.Error()}})
}

func anyStrings(values map[string]any) map[string]string {
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value.(string)
	}
	return result
}

func capturePortfolioScopeRotation(t *testing.T, path string) {
	t.Helper()
	cachePath := filepath.Join(t.TempDir(), "token.json")
	oldToken := tokenCache{AccessToken: "synthetic-old-token", ExpiresAt: time.Unix(4_000_000_000, 0), Scope: "read write"}
	data, err := json.Marshal(oldToken)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachePath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	var request url.Values
	logger, _ := testRequestLogger(t)
	store := &TokenStore{
		path: cachePath, clientID: "synthetic-client", clientSecret: "synthetic-secret", logger: logger,
		now: func() time.Time { return time.Unix(1_000, 0) },
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(req.Body)
			if err != nil {
				return nil, err
			}
			request, err = url.ParseQuery(string(body))
			if err != nil {
				return nil, err
			}
			request.Set("client_id", "[redacted]")
			request.Del("client_secret")
			return syntheticTokenResponse(), nil
		})},
	}
	if _, err := store.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	writeJSONEvidence(t, path, map[string]any{
		"cached_scope":      oldToken.Scope,
		"token_request":     request,
		"replacement_scope": linearTokenScopes,
	})
}

func capturePortfolioCLI(t *testing.T, path string, args []string, graph graphDoer) {
	t.Helper()
	oldRead, oldWrite := newLinearAPI, newLinearWriteAPI
	recorded := &portfolioRecordingGraph{graph: graph}
	api := &LinearAPI{graph: recorded}
	newLinearAPI = func(io.Writer, int) (*LinearAPI, error) { return api, nil }
	newLinearWriteAPI = func(io.Writer, int) (*LinearAPI, error) { return api, nil }
	t.Cleanup(func() { newLinearAPI, newLinearWriteAPI = oldRead, oldWrite })
	var stdout, stderr bytes.Buffer
	if err := execute(args, bytes.NewReader(nil), &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	writeJSONEvidence(t, path, map[string]any{"input": map[string]any{"argv": append([]string{"linear"}, args...)}, "output": map[string]string{"stdout": stdout.String(), "stderr": stderr.String()}, "graphql_boundaries": recorded.calls})
}

func capturePortfolioMCP(t *testing.T, path, tool string, args map[string]any, graph graphDoer) {
	t.Helper()
	request, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}})
	if err != nil {
		t.Fatal(err)
	}
	request = append(request, '\n')
	var stdout, stderr bytes.Buffer
	recorded := &portfolioRecordingGraph{graph: graph}
	server := &MCPServer{stdin: bytes.NewReader(request), stdout: &stdout, stderr: &stderr, api: &LinearAPI{graph: recorded}}
	if err := server.Serve(); err != nil {
		t.Fatal(err)
	}
	writeJSONEvidence(t, path, map[string]any{"input": string(request), "output": stdout.String(), "stderr": stderr.String(), "graphql_boundaries": recorded.calls})
}

func capturePortfolioCLIError(t *testing.T, path string, args []string, graph graphDoer) {
	t.Helper()
	oldRead, oldWrite := newLinearAPI, newLinearWriteAPI
	recorded := &portfolioRecordingGraph{graph: graph}
	api := &LinearAPI{graph: recorded}
	newLinearAPI = func(io.Writer, int) (*LinearAPI, error) { return api, nil }
	newLinearWriteAPI = func(io.Writer, int) (*LinearAPI, error) { return api, nil }
	t.Cleanup(func() { newLinearAPI, newLinearWriteAPI = oldRead, oldWrite })
	var stdout, stderr bytes.Buffer
	err := execute(args, bytes.NewReader(nil), &stdout, &stderr)
	if err == nil {
		t.Fatal("CLI accepted a synthetic refusal")
	}
	writeJSONEvidence(t, path, map[string]any{"input": map[string]any{"argv": append([]string{"linear"}, args...)}, "output": map[string]string{"stdout": stdout.String(), "stderr": stderr.String(), "error": err.Error()}, "graphql_boundaries": recorded.calls})
}

func capturePortfolioMCPError(t *testing.T, path, tool string, args map[string]any, graph graphDoer) {
	t.Helper()
	request, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}})
	if err != nil {
		t.Fatal(err)
	}
	request = append(request, '\n')
	var stdout, stderr bytes.Buffer
	recorded := &portfolioRecordingGraph{graph: graph}
	server := &MCPServer{stdin: bytes.NewReader(request), stdout: &stdout, stderr: &stderr, api: &LinearAPI{graph: recorded}}
	if err := server.Serve(); err != nil {
		t.Fatal(err)
	}
	var response struct {
		Result toolResult `json:"result"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Result.IsError {
		t.Fatal("MCP accepted a synthetic refusal")
	}
	writeJSONEvidence(t, path, map[string]any{"input": string(request), "output": stdout.String(), "stderr": stderr.String(), "graphql_boundaries": recorded.calls})
}

type portfolioGraphCall struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
	Response  string         `json:"response"`
	Error     string         `json:"error,omitempty"`
}

type portfolioRecordingGraph struct {
	graph graphDoer
	calls []portfolioGraphCall
}

func (graph *portfolioRecordingGraph) Do(ctx context.Context, query string, variables map[string]any, out any) error {
	err := graph.graph.Do(ctx, query, variables, out)
	response, marshalErr := json.Marshal(out)
	if marshalErr != nil {
		response = []byte(`{"error":"synthetic response could not be encoded"}`)
	}
	call := portfolioGraphCall{Query: query, Variables: variables, Response: string(response)}
	if err != nil {
		call.Error = err.Error()
	}
	graph.calls = append(graph.calls, call)
	return err
}
