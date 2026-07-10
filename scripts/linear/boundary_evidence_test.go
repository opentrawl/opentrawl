package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

const evidenceDirEnv = "TRAWL237_EVIDENCE_DIR"

func TestTRAWL237BoundaryEvidence(t *testing.T) {
	dir := os.Getenv(evidenceDirEnv)
	if dir == "" {
		t.Skip("set TRAWL237_EVIDENCE_DIR to capture synthetic boundary evidence")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	milestonePath := filepath.Join(dir, "milestone.md")
	if err := os.WriteFile(milestonePath, []byte("Milestone brief\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	projectPath := filepath.Join(dir, "project.md")
	if err := os.WriteFile(projectPath, []byte("Updated project brief.\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range evidenceScenarios(milestonePath, projectPath) {
		t.Run(scenario.name, func(t *testing.T) {
			api, graph := evidenceAPI(scenario.newGraph())
			captureCLI(t, dir, scenario.name, scenario.cliArgs, scenario.wantOutput, api)
			captureGraphCalls(t, filepath.Join(dir, scenario.name+"-cli-graphql.txt"), graph.calls)

			api, graph = evidenceAPI(scenario.newGraph())
			captureMCP(t, dir, scenario.name, scenario.mcpTool, scenario.mcpArgs, scenario.wantOutput, api)
			captureGraphCalls(t, filepath.Join(dir, scenario.name+"-mcp-graphql.txt"), graph.calls)
		})
	}
}

type evidenceScenario struct {
	name       string
	cliArgs    []string
	mcpTool    string
	mcpArgs    map[string]any
	wantOutput string
	newGraph   func() graphDoer
}

func evidenceScenarios(milestonePath, projectPath string) []evidenceScenario {
	return []evidenceScenario{
		{
			name:     "project-read",
			cliArgs:  []string{"project", "Photos"},
			mcpTool:  "get_project",
			mcpArgs:  map[string]any{"project": "Photos"},
			newGraph: func() graphDoer { return &literalEvidenceGraph{mode: "project-read"} },
		},
		{
			name:       "project-update",
			cliArgs:    []string{"project", "update", "Photos", "--as", "lane photos", "--summary", "One clear outcome", "--description-file", projectPath, "--status", "In Progress", "--priority", "high"},
			mcpTool:    "update_project",
			mcpArgs:    map[string]any{"project": "Photos", "actor": "lane photos", "summary": "One clear outcome", "description": "Updated project brief.\n\n", "status": "In Progress", "priority": "high"},
			wantOutput: "Photos\nStatus: In Progress\nPriority: High\nHealth: Not set\nLead: Unassigned\nIssues: 0 open, 0 total\n\nSummary\nOne clear outcome\n\nMilestones\nNone\n\nDescription\nUpdated project brief.\n\n",
			newGraph:   func() graphDoer { return &literalEvidenceGraph{mode: "project-update"} },
		},
		{
			name:     "milestone-create",
			cliArgs:  []string{"project", "milestone", "ensure", "Photos", "--name", "Foundations complete", "--as", "lane photos", "--description-file", milestonePath},
			mcpTool:  "ensure_project_milestone",
			mcpArgs:  map[string]any{"project": "Photos", "name": "Foundations complete", "actor": "lane photos", "description": "Milestone brief\n\n"},
			newGraph: func() graphDoer { return &literalEvidenceGraph{mode: "milestone-create"} },
		},
		{
			name:     "milestone-update",
			cliArgs:  []string{"project", "milestone", "ensure", "Photos", "--name", "Foundations complete", "--as", "lane photos", "--description-file", milestonePath},
			mcpTool:  "ensure_project_milestone",
			mcpArgs:  map[string]any{"project": "Photos", "name": "Foundations complete", "actor": "lane photos", "description": "Milestone brief\n\n"},
			newGraph: func() graphDoer { return &literalEvidenceGraph{mode: "milestone-update"} },
		},
		{
			name:     "issue-milestone-assign",
			cliArgs:  []string{"issue", "update", "TRAWL-1", "--as", "lane photos", "--milestone", "Foundations complete"},
			mcpTool:  "update_issue",
			mcpArgs:  map[string]any{"issue": "TRAWL-1", "actor": "lane photos", "milestone": "Foundations complete"},
			newGraph: func() graphDoer { return &literalEvidenceGraph{mode: "issue-milestone-assign"} },
		},
		{
			name:     "issue-milestone-clear",
			cliArgs:  []string{"issue", "update", "TRAWL-1", "--as", "lane photos", "--milestone", "none"},
			mcpTool:  "update_issue",
			mcpArgs:  map[string]any{"issue": "TRAWL-1", "actor": "lane photos", "milestone": "none"},
			newGraph: func() graphDoer { return &literalEvidenceGraph{mode: "issue-milestone-clear"} },
		},
		{
			name:     "issue-title-update",
			cliArgs:  []string{"issue", "update", "TRAWL-1", "--as", "lane photos", "--title", "Clarify the wrapper"},
			mcpTool:  "update_issue",
			mcpArgs:  map[string]any{"issue": "TRAWL-1", "actor": "lane photos", "title": "Clarify the wrapper"},
			newGraph: func() graphDoer { return &literalEvidenceGraph{mode: "issue-title-update"} },
		},
	}
}

func evidenceAPI(graph graphDoer) (*LinearAPI, *recordingGraph) {
	recorded := &recordingGraph{graph: graph}
	return &LinearAPI{graph: recorded}, recorded
}

func captureCLI(t *testing.T, dir, name string, args []string, wantOutput string, api *LinearAPI) {
	t.Helper()
	oldFactory := newLinearAPI
	newLinearAPI = func(io.Writer, int) (*LinearAPI, error) { return api, nil }
	t.Cleanup(func() { newLinearAPI = oldFactory })
	var stdout, stderr bytes.Buffer
	err := execute(args, bytes.NewReader(nil), &stdout, &stderr)
	if err != nil {
		t.Fatalf("%s CLI returned error: %v", name, err)
	}
	if wantOutput != "" && stdout.String() != wantOutput {
		t.Fatalf("%s CLI output = %q, want %q", name, stdout.String(), wantOutput)
	}
	writeEvidence(t, filepath.Join(dir, name+"-cli.json"), map[string]any{
		"argv":   append([]string{"linear"}, args...),
		"stdout": stdout.String(),
		"stderr": stderr.String(),
	})
}

func captureMCP(t *testing.T, dir, name, tool string, args map[string]any, wantOutput string, api *LinearAPI) {
	t.Helper()
	request, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      tool,
			"arguments": args,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	request = append(request, '\n')
	var stdout, stderr bytes.Buffer
	server := &MCPServer{stdin: bytes.NewReader(request), stdout: &stdout, stderr: &stderr, api: api}
	if err := server.Serve(); err != nil {
		t.Fatalf("%s MCP returned error: %v", name, err)
	}
	if wantOutput != "" {
		var response struct {
			Result struct {
				Content []toolContent `json:"content"`
			} `json:"result"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
			t.Fatalf("decode %s MCP output: %v", name, err)
		}
		if len(response.Result.Content) != 1 || response.Result.Content[0].Text != wantOutput {
			t.Fatalf("%s MCP output = %#v, want %q", name, response.Result.Content, wantOutput)
		}
	}
	writeEvidence(t, filepath.Join(dir, name+"-mcp.json"), map[string]any{
		"request":  string(request),
		"response": stdout.String(),
		"stderr":   stderr.String(),
	})
}

type graphCall struct {
	RequestRaw  []byte
	ResponseRaw []byte
}

type recordingGraph struct {
	graph graphDoer
	calls []graphCall
}

func (graph *recordingGraph) Do(ctx context.Context, query string, variables map[string]any, out any) error {
	request, err := json.Marshal(graphRequest{Query: query, Variables: variables})
	if err != nil {
		return err
	}
	rawGraph, ok := graph.graph.(rawGraphDoer)
	if !ok {
		return fmt.Errorf("evidence graph does not expose raw responses")
	}
	response, err := rawGraph.DoRaw(ctx, query, variables)
	graph.calls = append(graph.calls, graphCall{RequestRaw: request, ResponseRaw: response})
	if err != nil {
		return err
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(response, &envelope); err != nil {
		return err
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return err
	}
	return nil
}

func captureGraphCalls(t *testing.T, path string, calls []graphCall) {
	t.Helper()
	var transcript bytes.Buffer
	for index, call := range calls {
		fmt.Fprintf(&transcript, "=== call %d request ===\n", index+1)
		transcript.Write(call.RequestRaw)
		transcript.WriteString("\n=== call ")
		fmt.Fprintf(&transcript, "%d response ===\n", index+1)
		transcript.Write(call.ResponseRaw)
		transcript.WriteByte('\n')
	}
	if err := os.WriteFile(path, transcript.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

type rawGraphDoer interface {
	DoRaw(ctx context.Context, query string, variables map[string]any) ([]byte, error)
}

type literalEvidenceGraph struct {
	mode    string
	written bool
}

func (graph *literalEvidenceGraph) Do(ctx context.Context, query string, variables map[string]any, out any) error {
	raw, err := graph.DoRaw(ctx, query, variables)
	if err != nil {
		return err
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return err
	}
	return json.Unmarshal(envelope.Data, out)
}

func (graph *literalEvidenceGraph) DoRaw(_ context.Context, query string, _ map[string]any) ([]byte, error) {
	data, err := graph.responseData(query)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"data": data})
}

func (graph *literalEvidenceGraph) responseData(query string) (any, error) {
	switch query {
	case resolveProjectQuery:
		return map[string]any{"projects": map[string]any{"nodes": []any{projectReference()}, "pageInfo": pageInfoHasNext()}}, nil
	case projectByIDQuery:
		return map[string]any{"project": graph.projectReadFixture()}, nil
	case projectStatusesQuery:
		return map[string]any{"projectStatuses": map[string]any{"nodes": []any{map[string]any{"id": "status-progress", "name": "In Progress"}}, "pageInfo": pageInfo()}}, nil
	case updateProjectMutation:
		graph.written = true
		return map[string]any{"projectUpdate": map[string]any{"success": true}}, nil
	case createProjectMilestoneMutation:
		graph.written = true
		return map[string]any{"projectMilestoneCreate": map[string]any{"success": true}}, nil
	case updateProjectMilestoneMutation:
		graph.written = true
		return map[string]any{"projectMilestoneUpdate": map[string]any{"success": true}}, nil
	case resolveIssueIDQuery:
		return map[string]any{"issues": map[string]any{"nodes": []any{map[string]any{"id": "issue-1", "identifier": "TRAWL-1", "project": projectReference()}}}}, nil
	case updateIssueMutation:
		graph.written = true
		return map[string]any{"issueUpdate": map[string]any{"success": true, "issue": graph.issueMutationFixture()}}, nil
	case issueByIdentifierQuery:
		return map[string]any{"issues": map[string]any{"nodes": []any{graph.issueReadFixture()}}}, nil
	default:
		return nil, fmt.Errorf("unexpected query in %s evidence", graph.mode)
	}
}

func projectReference() map[string]any {
	return map[string]any{"id": "project-1", "name": "Photos", "slugId": "photos"}
}

func pageInfo() map[string]any {
	return map[string]any{"hasNextPage": false, "endCursor": ""}
}

func pageInfoHasNext() map[string]any {
	return map[string]any{"hasNextPage": false}
}

func (graph *literalEvidenceGraph) projectReadFixture() map[string]any {
	description := "Current Apple Photos to one reconstructable stored card"
	content := "Initial project brief.\n\n"
	milestones := []any{}
	if graph.mode == "project-update" && graph.written {
		description = "One clear outcome"
		content = "Updated project brief.\n\n"
	}
	if graph.mode == "milestone-update" && !graph.written {
		milestones = []any{map[string]any{"id": "milestone-1", "name": "Foundations complete", "description": "Old brief", "project": projectReference()}}
	}
	if graph.mode == "issue-milestone-assign" {
		milestones = []any{map[string]any{"id": "milestone-1", "name": "Foundations complete", "description": "Milestone brief\n\n", "project": projectReference()}}
	}
	if (graph.mode == "milestone-create" || graph.mode == "milestone-update") && graph.written {
		milestones = []any{map[string]any{"id": "milestone-1", "name": "Foundations complete", "description": "Milestone brief\n\n", "project": projectReference()}}
	}
	return map[string]any{
		"id": "project-1", "name": "Photos", "slugId": "photos",
		"description": description, "content": content,
		"status":   map[string]any{"id": "status-progress", "name": "In Progress"},
		"priority": 2, "priorityLabel": "High", "health": nil, "lead": nil,
		"projectMilestones": map[string]any{"nodes": milestones, "pageInfo": pageInfo()},
		"issues":            map[string]any{"nodes": []any{}, "pageInfo": pageInfo()},
	}
}

func (graph *literalEvidenceGraph) issueMutationFixture() map[string]any {
	return graph.issueFixture(false)
}

func (graph *literalEvidenceGraph) issueReadFixture() map[string]any {
	fixture := graph.issueFixture(true)
	fixture["comments"] = map[string]any{"nodes": []any{}, "pageInfo": pageInfo()}
	return fixture
}

func (graph *literalEvidenceGraph) issueFixture(includeComments bool) map[string]any {
	_ = includeComments
	title := "Original title"
	milestone := any(nil)
	if graph.mode == "issue-title-update" && graph.written {
		title = "Clarify the wrapper"
	}
	if graph.mode == "issue-milestone-assign" && graph.written {
		milestone = map[string]any{"id": "milestone-1", "name": "Foundations complete"}
	}
	return map[string]any{
		"id": "issue-1", "identifier": "TRAWL-1", "title": title, "description": "", "url": "https://linear.test/TRAWL-1",
		"priorityLabel": "No priority", "state": map[string]any{"name": "Todo", "type": "backlog"},
		"project": projectReference(), "projectMilestone": milestone, "assignee": nil,
		"labels": map[string]any{"nodes": []any{}, "pageInfo": map[string]any{"hasNextPage": false}},
	}
}

func writeEvidence(t *testing.T, path string, value any) {
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
