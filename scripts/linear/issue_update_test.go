package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestUpdateIssueReplacesSelectedFieldsInOneMutation(t *testing.T) {
	graph := &issueUpdateGraph{
		project: Project{ID: "project-1", Name: "OpenTrawl", SlugID: "opentrawl"},
		updated: Issue{
			ID:            "issue-1",
			Identifier:    "TRAWL-1",
			Description:   "Replacement description\n\n",
			PriorityLabel: "High",
			Project:       &Project{ID: "project-1", Name: "OpenTrawl", SlugID: "opentrawl"},
		},
	}
	api := &LinearAPI{graph: graph}
	description := "Replacement description\n\n"
	priority := "high"
	project := "OpenTrawl"
	updated, err := api.UpdateIssue(context.Background(), "TRAWL-1", "lane cli", IssueUpdateOptions{
		Description: &description,
		Priority:    &priority,
		Project:     &project,
	})
	if err != nil {
		t.Fatalf("UpdateIssue returned error: %v", err)
	}
	wantInput := map[string]any{
		"description": "Replacement description\n\n",
		"priority":    2,
		"projectId":   "project-1",
	}
	if !reflect.DeepEqual(graph.updateInput, wantInput) {
		t.Fatalf("mutation input = %#v, want %#v", graph.updateInput, wantInput)
	}
	if graph.updateCalls != 1 {
		t.Fatalf("update calls = %d, want 1", graph.updateCalls)
	}
	if updated.Issue.Description != description {
		t.Fatalf("updated description = %q, want %q", updated.Issue.Description, description)
	}
	wantChanges := []IssueChange{
		{Field: "description", Value: "replaced"},
		{Field: "priority", Value: "High"},
		{Field: "project", Value: "OpenTrawl"},
	}
	if !reflect.DeepEqual(updated.Changes, wantChanges) {
		t.Fatalf("changes = %#v, want %#v", updated.Changes, wantChanges)
	}
}

func TestUpdateIssueCanClearDescriptionProjectAndPriority(t *testing.T) {
	graph := &issueUpdateGraph{
		updated: Issue{ID: "issue-1", Identifier: "TRAWL-1", PriorityLabel: "No priority"},
	}
	api := &LinearAPI{graph: graph}
	description := ""
	priority := "none"
	project := "none"
	updated, err := api.UpdateIssue(context.Background(), "TRAWL-1", "coordinator", IssueUpdateOptions{
		Description: &description,
		Priority:    &priority,
		Project:     &project,
	})
	if err != nil {
		t.Fatalf("UpdateIssue returned error: %v", err)
	}
	wantInput := map[string]any{"description": "", "priority": 0, "projectId": nil}
	if !reflect.DeepEqual(graph.updateInput, wantInput) {
		t.Fatalf("mutation input = %#v, want %#v", graph.updateInput, wantInput)
	}
	wantChanges := []IssueChange{
		{Field: "description", Value: "cleared"},
		{Field: "priority", Value: "No priority"},
		{Field: "project", Value: "none"},
	}
	if !reflect.DeepEqual(updated.Changes, wantChanges) {
		t.Fatalf("changes = %#v, want %#v", updated.Changes, wantChanges)
	}
}

func TestUpdateIssueRefusesMissingFieldsBeforeGraphQL(t *testing.T) {
	graph := &issueUpdateGraph{}
	api := &LinearAPI{graph: graph}
	_, err := api.UpdateIssue(context.Background(), "TRAWL-1", "coordinator", IssueUpdateOptions{})
	if err == nil || err.Error() != "at least one issue field is required" {
		t.Fatalf("error = %v, want missing fields error", err)
	}
	if graph.calls != 0 {
		t.Fatalf("GraphQL calls = %d, want 0", graph.calls)
	}
}

func TestUpdateIssueRefusesInvalidFieldsBeforeGraphQL(t *testing.T) {
	graph := &issueUpdateGraph{}
	api := &LinearAPI{graph: graph}
	priority := "critical"
	_, err := api.UpdateIssue(context.Background(), "TRAWL-1", "coordinator", IssueUpdateOptions{Priority: &priority})
	if err == nil || !strings.Contains(err.Error(), "Valid priorities") {
		t.Fatalf("error = %v, want invalid priority error", err)
	}
	if graph.calls != 0 {
		t.Fatalf("GraphQL calls = %d, want 0", graph.calls)
	}
}

func TestParsePriorityUsesLinearPriorityValues(t *testing.T) {
	tests := []struct {
		input string
		value int
	}{
		{input: "none", value: 0},
		{input: "urgent", value: 1},
		{input: "high", value: 2},
		{input: "medium", value: 3},
		{input: "low", value: 4},
	}
	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			value, err := parsePriority(test.input)
			if err != nil {
				t.Fatalf("parsePriority returned error: %v", err)
			}
			if value != test.value {
				t.Fatalf("parsePriority = %d, want %d", value, test.value)
			}
		})
	}
	if _, err := parsePriority("critical"); err == nil {
		t.Fatal("parsePriority accepted an unknown priority")
	}
}

func TestResolveProjectRefusesAmbiguousNames(t *testing.T) {
	graph := &issueUpdateGraph{projects: []Project{
		{ID: "project-1", Name: "Launch", SlugID: "launch-one"},
		{ID: "project-2", Name: "Launch", SlugID: "launch-two"},
	}}
	api := &LinearAPI{graph: graph}
	_, err := api.ResolveProject(context.Background(), "Launch")
	if err == nil {
		t.Fatal("ResolveProject accepted an ambiguous name")
	}
	want := `project "Launch" is ambiguous: Launch (launch-one), Launch (launch-two)`
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}

func TestRenderIssueIncludesPlanningAndOwnershipFields(t *testing.T) {
	description := "First paragraph.\n\n[Read the source](https://example.com/a/very/long/path/that/must/not/be/split/by/the/human/output/renderer/when/it/exceeds/the/default/terminal/width)\n\n"
	issue := Issue{
		Identifier:    "TRAWL-1",
		Title:         "Ship a clear wrapper",
		Description:   description,
		URL:           "https://linear.app/example/issue/TRAWL-1",
		PriorityLabel: "Urgent",
		State:         IssueState{Name: "In Progress"},
		Project:       &Project{Name: "OpenTrawl"},
		Assignee:      &Person{DisplayName: "Jamie"},
	}
	var output bytes.Buffer
	if err := RenderIssue(&output, issue); err != nil {
		t.Fatalf("RenderIssue returned error: %v", err)
	}
	for _, want := range []string{
		"Description\n\n" + description + "\nNo comments.",
		"Priority: Urgent",
		"Project: OpenTrawl",
		"Assignee: Jamie",
	} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("output missing %q:\n%s", want, output.String())
		}
	}
}

func TestMCPUpdateIssueUsesAppMutationPath(t *testing.T) {
	if strings.Contains(updateIssueMutation, "createAsUser") {
		t.Fatal("issue update mutation must use the app token without createAsUser")
	}
	description := "MCP replacement\n\n"
	graph := &issueUpdateGraph{
		updated: Issue{ID: "issue-1", Identifier: "TRAWL-1", Description: description, PriorityLabel: "High"},
	}
	server := &MCPServer{api: &LinearAPI{graph: graph}}
	text, err := server.runTool("update_issue", map[string]json.RawMessage{
		"issue":       json.RawMessage(`"TRAWL-1"`),
		"actor":       json.RawMessage(`"lane cli"`),
		"description": json.RawMessage(`"MCP replacement\n\n"`),
		"priority":    json.RawMessage(`"high"`),
	})
	if err != nil {
		t.Fatalf("update_issue returned error: %v", err)
	}
	if graph.updateCalls != 1 {
		t.Fatalf("update calls = %d, want 1", graph.updateCalls)
	}
	if got := graph.updateInput["description"]; got != description {
		t.Fatalf("MCP description input = %#v, want %q", got, description)
	}
	if !strings.Contains(text, "Updated TRAWL-1") || !strings.Contains(text, "Priority: High") {
		t.Fatalf("tool output = %q", text)
	}
}

func TestMCPToolsExposeTheSameIssueUpdateFields(t *testing.T) {
	var update map[string]any
	for _, tool := range mcpTools() {
		if tool["name"] == "update_issue" {
			update = tool
			break
		}
	}
	if update == nil {
		t.Fatal("tools/list is missing update_issue")
	}
	schema, ok := update["inputSchema"].(map[string]any)
	if !ok {
		t.Fatalf("inputSchema = %#v", update["inputSchema"])
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v", schema["properties"])
	}
	for _, name := range []string{"issue", "actor", "description", "priority", "project"} {
		if _, ok := properties[name]; !ok {
			t.Errorf("tools/list is missing update field %q", name)
		}
	}
	if got, want := schema["required"], []string{"issue", "actor"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("required = %#v, want %#v", got, want)
	}
}

func TestIssueUpdateCLIRequiresAFieldBeforeAPI(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := execute([]string{"issue", "update", "TRAWL-1", "--as", "coordinator"}, strings.NewReader(""), &stdout, &stderr)
	if err == nil || err.Error() != "set at least one of --description-file, --priority or --project" {
		t.Fatalf("error = %v, want missing fields error", err)
	}
}

func TestGetIssueReadsAllCommentPagesAndPlanningFields(t *testing.T) {
	graph := &issueReadGraph{}
	api := &LinearAPI{graph: graph}
	issue, err := api.GetIssue(context.Background(), "TRAWL-1")
	if err != nil {
		t.Fatalf("GetIssue returned error: %v", err)
	}
	if graph.calls != 2 {
		t.Fatalf("issue page calls = %d, want 2", graph.calls)
	}
	if issue.Description != "Complete description" || issue.PriorityLabel != "High" || issue.Project == nil || issue.Project.Name != "OpenTrawl" {
		t.Fatalf("planning fields = %#v", issue)
	}
	if got := []string{issue.Comments.Nodes[0].ID, issue.Comments.Nodes[1].ID}; !reflect.DeepEqual(got, []string{"comment-1", "comment-2"}) {
		t.Fatalf("comment IDs = %#v", got)
	}
}

type issueUpdateGraph struct {
	calls       int
	project     Project
	projects    []Project
	updated     Issue
	updateInput map[string]any
	updateCalls int
}

type issueReadGraph struct {
	calls int
}

func (graph *issueReadGraph) Do(_ context.Context, query string, variables map[string]any, out any) error {
	if query != issueByIdentifierQuery {
		return errors.New("unexpected query")
	}
	graph.calls++
	page := map[string]any{
		"id":            "issue-1",
		"identifier":    "TRAWL-1",
		"title":         "Complete issue read",
		"description":   "Complete description",
		"priorityLabel": "High",
		"project":       map[string]any{"id": "project-1", "name": "OpenTrawl", "slugId": "opentrawl"},
		"comments": map[string]any{
			"nodes":    []Comment{{ID: "comment-1"}},
			"pageInfo": map[string]any{"hasNextPage": true, "endCursor": "cursor-1"},
		},
	}
	if _, ok := variables["commentsAfter"]; ok {
		page["comments"] = map[string]any{
			"nodes":    []Comment{{ID: "comment-2"}},
			"pageInfo": PageInfo{},
		}
	}
	return setGraphOut(out, map[string]any{"issues": map[string]any{"nodes": []any{page}}})
}

func (graph *issueUpdateGraph) Do(_ context.Context, query string, variables map[string]any, out any) error {
	graph.calls++
	switch query {
	case resolveIssueIDQuery:
		return setGraphOut(out, map[string]any{
			"issues": map[string]any{"nodes": []Issue{{ID: "issue-1", Identifier: "TRAWL-1"}}},
		})
	case resolveProjectQuery:
		projects := graph.projects
		if len(projects) == 0 && graph.project.ID != "" {
			projects = []Project{graph.project}
		}
		return setGraphOut(out, map[string]any{
			"projects": map[string]any{"nodes": projects, "pageInfo": PageInfo{}},
		})
	case updateIssueMutation:
		input, ok := variables["input"].(map[string]any)
		if !ok {
			return errors.New("update input was not a map")
		}
		graph.updateInput = input
		graph.updateCalls++
		return setGraphOut(out, map[string]any{
			"issueUpdate": map[string]any{"success": true, "issue": graph.updated},
		})
	default:
		return errors.New("unexpected query")
	}
}
