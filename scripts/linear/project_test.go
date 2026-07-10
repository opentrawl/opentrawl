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

func TestGetProjectReadsEveryMilestoneAndIssuePage(t *testing.T) {
	graph := &projectPaginationGraph{}
	api := &LinearAPI{graph: graph}
	project, err := api.GetProject(context.Background(), "Photos")
	if err != nil {
		t.Fatalf("GetProject returned error: %v", err)
	}
	if graph.reads != 2 {
		t.Fatalf("project reads = %d, want 2", graph.reads)
	}
	if got := []string{project.Milestones.Nodes[0].Name, project.Milestones.Nodes[1].Name}; !reflect.DeepEqual(got, []string{"Foundations complete", "Deterministic input integrity"}) {
		t.Fatalf("milestones = %#v", got)
	}
	if got := len(project.Issues.Nodes); got != 3 {
		t.Fatalf("issues = %d, want 3", got)
	}
	var output bytes.Buffer
	if err := RenderProject(&output, project); err != nil {
		t.Fatalf("RenderProject returned error: %v", err)
	}
	want := "Photos\nStatus: In Progress\nPriority: High\nHealth: Not set\nLead: Unassigned\nIssues: 2 open, 3 total\n\nSummary\nCurrent Apple Photos to one reconstructable stored card\n\nMilestones\n- Foundations complete\n- Deterministic input integrity\n\nDescription\nBuild the simplest proven Photos pipeline.\n"
	if output.String() != want {
		t.Fatalf("project output =\n%s\nwant:\n%s", output.String(), want)
	}
}

func TestUpdateProjectUsesExactInputAndReadsBack(t *testing.T) {
	project := sampleProject()
	graph := &projectGraph{project: project, readBack: project, statuses: []ProjectStatus{{ID: "status-progress", Name: "In Progress"}}}
	api := &LinearAPI{graph: graph}
	summary, description, status, priority := "none", "Brief\n\n", "In Progress", "high"
	updated, err := api.UpdateProject(context.Background(), "Photos", "lane photos", ProjectUpdateOptions{Summary: &summary, Description: &description, Status: &status, Priority: &priority})
	if err != nil {
		t.Fatalf("UpdateProject returned error: %v", err)
	}
	wantInput := map[string]any{"description": "", "content": description, "statusId": "status-progress", "priority": 2}
	if !reflect.DeepEqual(graph.projectUpdateInput, wantInput) {
		t.Fatalf("project update input = %#v, want %#v", graph.projectUpdateInput, wantInput)
	}
	if graph.projectReads != 1 || updated.Name != "Photos" {
		t.Fatalf("read-back = %#v, project reads = %d", updated, graph.projectReads)
	}
}

func TestUpdateProjectClearsSummaryContentAndPriority(t *testing.T) {
	project := sampleProject()
	graph := &projectGraph{project: project, readBack: project}
	summary, content, priority := "none", "", "none"
	_, err := (&LinearAPI{graph: graph}).UpdateProject(context.Background(), "Photos", "lane photos", ProjectUpdateOptions{Summary: &summary, Description: &content, Priority: &priority})
	if err != nil {
		t.Fatalf("UpdateProject returned error: %v", err)
	}
	want := map[string]any{"description": "", "content": "", "priority": 0}
	if !reflect.DeepEqual(graph.projectUpdateInput, want) {
		t.Fatalf("project update input = %#v, want %#v", graph.projectUpdateInput, want)
	}
}

func TestUpdateProjectRejectsUnknownAndAmbiguousStatusBeforeMutation(t *testing.T) {
	status := "Started"
	for _, statuses := range [][]ProjectStatus{{{ID: "one", Name: "Backlog"}}, {{ID: "one", Name: "Started"}, {ID: "two", Name: "started"}}} {
		graph := &projectGraph{project: sampleProject(), statuses: statuses}
		api := &LinearAPI{graph: graph}
		_, err := api.UpdateProject(context.Background(), "Photos", "lane photos", ProjectUpdateOptions{Status: &status})
		if err == nil {
			t.Fatal("UpdateProject accepted an unresolved status")
		}
		if graph.projectUpdateCalls != 0 {
			t.Fatalf("project mutations = %d, want 0", graph.projectUpdateCalls)
		}
	}
}

func TestEnsureProjectMilestoneCreatesOrUpdatesOnlyDescription(t *testing.T) {
	description := "Milestone brief\n\n"
	createdProject := sampleProject()
	createdProject.Milestones.Nodes = []ProjectMilestone{{ID: "milestone-1", Name: "Foundations complete", Description: description}}
	createGraph := &projectGraph{project: sampleProject(), readBack: createdProject}
	created, err := (&LinearAPI{graph: createGraph}).EnsureProjectMilestone(context.Background(), "Photos", "lane photos", ProjectMilestoneOptions{Name: "Foundations complete", Description: &description})
	if err != nil {
		t.Fatalf("EnsureProjectMilestone create returned error: %v", err)
	}
	if !created.Created || !reflect.DeepEqual(createGraph.milestoneCreateInput, map[string]any{"projectId": "project-1", "name": "Foundations complete", "description": description}) {
		t.Fatalf("create = %#v, input = %#v", created, createGraph.milestoneCreateInput)
	}
	existing := sampleProject()
	existing.Milestones.Nodes = []ProjectMilestone{{ID: "milestone-1", Name: "Foundations complete", Description: "Old"}}
	updateGraph := &projectGraph{project: existing, readBack: createdProject}
	updated, err := (&LinearAPI{graph: updateGraph}).EnsureProjectMilestone(context.Background(), "Photos", "lane photos", ProjectMilestoneOptions{Name: "Foundations complete", Description: &description})
	if err != nil {
		t.Fatalf("EnsureProjectMilestone update returned error: %v", err)
	}
	if updated.Created || !reflect.DeepEqual(updateGraph.milestoneUpdateInput, map[string]any{"description": description}) {
		t.Fatalf("update = %#v, input = %#v", updated, updateGraph.milestoneUpdateInput)
	}
}

func TestEnsureProjectMilestoneRefusesDuplicatesAndMissingActor(t *testing.T) {
	project := sampleProject()
	project.Milestones.Nodes = []ProjectMilestone{{ID: "one", Name: "Foundations complete"}, {ID: "two", Name: "Foundations complete"}}
	graph := &projectGraph{project: project}
	api := &LinearAPI{graph: graph}
	_, err := api.EnsureProjectMilestone(context.Background(), "Photos", "lane photos", ProjectMilestoneOptions{Name: "Foundations complete"})
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("duplicate milestone error = %v", err)
	}
	if graph.milestoneCreateCalls != 0 || graph.milestoneUpdateCalls != 0 {
		t.Fatal("duplicate milestone caused a mutation")
	}
	_, err = api.EnsureProjectMilestone(context.Background(), "Photos", "", ProjectMilestoneOptions{Name: "Foundations complete"})
	if err == nil || err.Error() != "--as is required for write commands" {
		t.Fatalf("missing actor error = %v", err)
	}
}

func TestIssueUpdateAssignsAndClearsMilestonesAndTitles(t *testing.T) {
	issue := Issue{ID: "issue-1", Identifier: "TRAWL-1", Project: &Project{ID: "project-1", Name: "Photos", SlugID: "photos"}}
	project := sampleProject()
	project.Milestones.Nodes = []ProjectMilestone{{ID: "milestone-1", Name: "Foundations complete", Project: &Project{ID: "project-1", Name: "Photos"}}}
	graph := &milestoneIssueGraph{issue: issue, project: project, readBack: Issue{ID: "issue-1", Identifier: "TRAWL-1", Title: "Clear wrapper", Milestone: &ProjectMilestone{Name: "Foundations complete"}}}
	api := &LinearAPI{graph: graph}
	title, milestone := "Clear wrapper", "Foundations complete"
	updated, err := api.UpdateIssue(context.Background(), "TRAWL-1", "lane photos", IssueUpdateOptions{Title: &title, Milestone: &milestone})
	if err != nil {
		t.Fatalf("UpdateIssue returned error: %v", err)
	}
	if !reflect.DeepEqual(graph.updateInput, map[string]any{"title": title, "projectMilestoneId": "milestone-1"}) {
		t.Fatalf("issue update input = %#v", graph.updateInput)
	}
	if got := updated.Changes; !reflect.DeepEqual(got, []IssueChange{{Field: "title", Value: title}, {Field: "milestone", Value: milestone}}) {
		t.Fatalf("changes = %#v", got)
	}
	clear := "none"
	graph = &milestoneIssueGraph{issue: issue, readBack: Issue{ID: "issue-1", Identifier: "TRAWL-1"}}
	_, err = (&LinearAPI{graph: graph}).UpdateIssue(context.Background(), "TRAWL-1", "lane photos", IssueUpdateOptions{Milestone: &clear})
	if err != nil || !reflect.DeepEqual(graph.updateInput, map[string]any{"projectMilestoneId": nil}) {
		t.Fatalf("clear error = %v, input = %#v", err, graph.updateInput)
	}
}

func TestIssueUpdateRefusesInvalidMilestoneAndProjectCombination(t *testing.T) {
	issue := Issue{ID: "issue-1", Identifier: "TRAWL-1", Project: &Project{ID: "project-1", Name: "Photos", SlugID: "photos"}}
	project := sampleProject()
	project.Milestones.Nodes = []ProjectMilestone{{ID: "milestone-1", Name: "Elsewhere", Project: &Project{ID: "project-2", Name: "Other"}}}
	graph := &milestoneIssueGraph{issue: issue, project: project}
	api := &LinearAPI{graph: graph}
	milestone, projectName := "Elsewhere", "Other"
	for _, options := range []IssueUpdateOptions{{Milestone: &milestone}, {Milestone: &milestone, Project: &projectName}} {
		_, err := api.UpdateIssue(context.Background(), "TRAWL-1", "lane photos", options)
		if err == nil {
			t.Fatal("UpdateIssue accepted an invalid milestone assignment")
		}
	}
	if graph.updateCalls != 0 {
		t.Fatalf("issue mutation calls = %d, want 0", graph.updateCalls)
	}
	empty := " "
	_, err := api.UpdateIssue(context.Background(), "TRAWL-1", "lane photos", IssueUpdateOptions{Title: &empty})
	if err == nil || err.Error() != "--title needs a value" {
		t.Fatalf("empty title error = %v", err)
	}
	withoutProject := &milestoneIssueGraph{issue: Issue{ID: "issue-2", Identifier: "TRAWL-2"}}
	_, err = (&LinearAPI{graph: withoutProject}).UpdateIssue(context.Background(), "TRAWL-2", "lane photos", IssueUpdateOptions{Milestone: &milestone})
	if err == nil || !strings.Contains(err.Error(), "has no project") {
		t.Fatalf("missing project error = %v", err)
	}
}

func TestMCPProjectToolsMatchCLIFields(t *testing.T) {
	tools := map[string]map[string]any{}
	for _, tool := range mcpTools() {
		tools[tool["name"].(string)] = tool
	}
	for name, fields := range map[string][]string{
		"update_issue":             {"issue", "actor", "description", "priority", "project", "milestone", "title"},
		"update_project":           {"project", "actor", "summary", "description", "status", "priority"},
		"ensure_project_milestone": {"project", "name", "actor", "description"},
	} {
		tool := tools[name]
		if tool == nil {
			t.Fatalf("tools/list is missing %s", name)
		}
		schema := tool["inputSchema"].(map[string]any)
		properties := schema["properties"].(map[string]any)
		for _, field := range fields {
			if _, ok := properties[field]; !ok {
				t.Errorf("%s is missing %s", name, field)
			}
		}
	}
}

func TestMCPUpdateProjectUsesTheAppMutationPath(t *testing.T) {
	project := sampleProject()
	graph := &projectGraph{project: project, readBack: project, statuses: []ProjectStatus{{ID: "status-progress", Name: "In Progress"}}}
	server := &MCPServer{api: &LinearAPI{graph: graph}}
	text, err := server.runTool("update_project", map[string]json.RawMessage{
		"project":     json.RawMessage(`"Photos"`),
		"actor":       json.RawMessage(`"lane photos"`),
		"summary":     json.RawMessage(`"One clear outcome"`),
		"description": json.RawMessage(`"Brief\n\n"`),
		"status":      json.RawMessage(`"In Progress"`),
		"priority":    json.RawMessage(`"high"`),
	})
	if err != nil {
		t.Fatalf("update_project returned error: %v", err)
	}
	want := map[string]any{"description": "One clear outcome", "content": "Brief\n\n", "statusId": "status-progress", "priority": 2}
	if !reflect.DeepEqual(graph.projectUpdateInput, want) {
		t.Fatalf("project update input = %#v, want %#v", graph.projectUpdateInput, want)
	}
	if !strings.Contains(text, "Photos\nStatus: In Progress") {
		t.Fatalf("MCP output = %q", text)
	}
}

func sampleProject() Project {
	return Project{ID: "project-1", Name: "Photos", SlugID: "photos", Description: "Current Apple Photos to one reconstructable stored card", Content: "Build the simplest proven Photos pipeline.", Status: ProjectStatus{ID: "status-progress", Name: "In Progress"}, Priority: 2, PriorityLabel: "High"}
}

type projectPaginationGraph struct{ reads int }

func (graph *projectPaginationGraph) Do(_ context.Context, query string, variables map[string]any, out any) error {
	switch query {
	case resolveProjectQuery:
		return setGraphOut(out, map[string]any{"projects": map[string]any{"nodes": []Project{{ID: "project-1", Name: "Photos", SlugID: "photos"}}, "pageInfo": PageInfo{}}})
	case projectByIDQuery:
		graph.reads++
		page := sampleProject()
		page.Milestones.Nodes = []ProjectMilestone{{ID: "m-1", Name: "Foundations complete"}}
		page.Milestones.PageInfo = PageInfo{HasNextPage: true, EndCursor: "milestone-cursor"}
		page.Issues.Nodes = []Issue{{State: IssueState{Type: "started"}}, {State: IssueState{Type: "completed"}}}
		page.Issues.PageInfo = PageInfo{HasNextPage: true, EndCursor: "issue-cursor"}
		if variables["milestonesAfter"] == "milestone-cursor" {
			page.Milestones.Nodes = []ProjectMilestone{{ID: "m-2", Name: "Deterministic input integrity"}}
			page.Milestones.PageInfo = PageInfo{}
		}
		if variables["issuesAfter"] == "issue-cursor" {
			page.Issues.Nodes = []Issue{{State: IssueState{Type: "backlog"}}}
			page.Issues.PageInfo = PageInfo{}
		}
		return setGraphOut(out, map[string]any{"project": page})
	default:
		return errors.New("unexpected query")
	}
}

type projectGraph struct {
	project              Project
	readBack             Project
	statuses             []ProjectStatus
	projectReads         int
	projectUpdateInput   map[string]any
	projectUpdateCalls   int
	milestoneCreateInput map[string]any
	milestoneCreateCalls int
	milestoneUpdateInput map[string]any
	milestoneUpdateCalls int
	written              bool
}

func (graph *projectGraph) Do(_ context.Context, query string, variables map[string]any, out any) error {
	switch query {
	case resolveProjectQuery:
		return setGraphOut(out, map[string]any{"projects": map[string]any{"nodes": []Project{{ID: graph.project.ID, Name: graph.project.Name, SlugID: graph.project.SlugID}}, "pageInfo": PageInfo{}}})
	case projectByIDQuery:
		graph.projectReads++
		project := graph.project
		if graph.written && graph.readBack.ID != "" {
			project = graph.readBack
		}
		return setGraphOut(out, map[string]any{"project": project})
	case projectStatusesQuery:
		return setGraphOut(out, map[string]any{"projectStatuses": map[string]any{"nodes": graph.statuses, "pageInfo": PageInfo{}}})
	case updateProjectMutation:
		graph.projectUpdateCalls++
		graph.written = true
		graph.projectUpdateInput = variables["input"].(map[string]any)
		return setGraphOut(out, map[string]any{"projectUpdate": map[string]any{"success": true}})
	case createProjectMilestoneMutation:
		graph.milestoneCreateCalls++
		graph.written = true
		graph.milestoneCreateInput = variables["input"].(map[string]any)
		return setGraphOut(out, map[string]any{"projectMilestoneCreate": map[string]any{"success": true}})
	case updateProjectMilestoneMutation:
		graph.milestoneUpdateCalls++
		graph.written = true
		graph.milestoneUpdateInput = variables["input"].(map[string]any)
		return setGraphOut(out, map[string]any{"projectMilestoneUpdate": map[string]any{"success": true}})
	default:
		return errors.New("unexpected query")
	}
}

type milestoneIssueGraph struct {
	issue       Issue
	project     Project
	readBack    Issue
	updateInput map[string]any
	updateCalls int
}

func (graph *milestoneIssueGraph) Do(_ context.Context, query string, variables map[string]any, out any) error {
	switch query {
	case resolveIssueIDQuery:
		return setGraphOut(out, map[string]any{"issues": map[string]any{"nodes": []Issue{graph.issue}}})
	case resolveProjectQuery:
		return setGraphOut(out, map[string]any{"projects": map[string]any{"nodes": []Project{{ID: graph.project.ID, Name: graph.project.Name, SlugID: graph.project.SlugID}}, "pageInfo": PageInfo{}}})
	case projectByIDQuery:
		return setGraphOut(out, map[string]any{"project": graph.project})
	case updateIssueMutation:
		graph.updateCalls++
		graph.updateInput = variables["input"].(map[string]any)
		return setGraphOut(out, map[string]any{"issueUpdate": map[string]any{"success": true, "issue": graph.readBack}})
	case issueByIdentifierQuery:
		return setGraphOut(out, map[string]any{"issues": map[string]any{"nodes": []Issue{graph.readBack}}})
	default:
		return errors.New("unexpected query")
	}
}
