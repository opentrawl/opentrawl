package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCreateProjectAttachesInitiativeAndReadsBack(t *testing.T) {
	graph := newPortfolioGraph()
	initiative := "OpenTrawl"
	project, err := (&LinearAPI{graph: graph}).CreateProject(context.Background(), "portfolio bot", ProjectCreateOptions{
		Team: "TRAWL", Name: "Engineering delivery system", Summary: "One clear outcome", Description: "# Brief\n", Status: "Triage", Priority: "high", Initiative: &initiative,
	})
	if err != nil {
		t.Fatalf("CreateProject returned error: %v", err)
	}
	if !reflect.DeepEqual(graph.createInput, map[string]any{"teamIds": []string{"team-1"}, "name": "Engineering delivery system", "description": "One clear outcome", "content": "# Brief\n", "statusId": "status-triage", "priority": 2}) {
		t.Fatalf("project create input = %#v", graph.createInput)
	}
	if !reflect.DeepEqual(graph.attachInput, map[string]any{"projectId": "project-1", "initiativeId": "initiative-1"}) {
		t.Fatalf("initiative attachment = %#v", graph.attachInput)
	}
	if !projectHasInitiative(project, "initiative-1") || !initiativeHasProject(graph.initiativeReadBack(), "project-1") {
		t.Fatalf("read-back did not contain the attachment: %#v", project)
	}
}

func TestCreateProjectRefusesDuplicateBeyondFirstPage(t *testing.T) {
	graph := newPortfolioGraph()
	graph.duplicateOnSecondPage = true
	_, err := (&LinearAPI{graph: graph}).CreateProject(context.Background(), "portfolio bot", ProjectCreateOptions{Team: "TRAWL", Name: "Engineering delivery system", Summary: "Summary", Description: "Brief", Status: "Triage", Priority: "high"})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate error = %v", err)
	}
	if graph.createCalls != 0 || graph.resolveProjectPages != 2 {
		t.Fatalf("create calls = %d, project pages = %d", graph.createCalls, graph.resolveProjectPages)
	}
}

func TestUpdateProjectRenamesAndAddsInitiativeWithoutRemovingMembership(t *testing.T) {
	graph := newPortfolioGraph()
	graph.project.Initiatives.Nodes = []Initiative{{ID: "initiative-existing", Name: "Existing"}}
	graph.updatedProject = graph.project
	name, initiative := "Engineering delivery system", "OpenTrawl"
	project, err := (&LinearAPI{graph: graph}).UpdateProject(context.Background(), "Process & tooling", "portfolio bot", ProjectUpdateOptions{Name: &name, Initiative: &initiative})
	if err != nil {
		t.Fatalf("UpdateProject returned error: %v", err)
	}
	if !reflect.DeepEqual(graph.updateInput, map[string]any{"name": name}) {
		t.Fatalf("project update input = %#v", graph.updateInput)
	}
	if !projectHasInitiative(project, "initiative-existing") || !projectHasInitiative(project, "initiative-1") {
		t.Fatalf("project membership = %#v", project.Initiatives.Nodes)
	}
	if graph.initiativeReadPages != 2 {
		t.Fatalf("initiative membership pages = %d, want 2", graph.initiativeReadPages)
	}
}

func TestUpdateProjectKeepsMembershipWhenInitiativeIsOmitted(t *testing.T) {
	graph := newPortfolioGraph()
	graph.project.Initiatives.Nodes = []Initiative{{ID: "initiative-existing", Name: "Existing"}}
	graph.updatedProject = graph.project
	summary := "Updated summary"
	graph.updatedProject.Description = summary
	project, err := (&LinearAPI{graph: graph}).UpdateProject(context.Background(), "Process & tooling", "portfolio bot", ProjectUpdateOptions{Summary: &summary})
	if err != nil {
		t.Fatalf("UpdateProject returned error: %v", err)
	}
	if graph.attachInput != nil || !projectHasInitiative(project, "initiative-existing") {
		t.Fatalf("attachment = %#v, memberships = %#v", graph.attachInput, project.Initiatives.Nodes)
	}
}

func TestUpdateProjectRefusesInvalidInitiativeBeforeProjectMutation(t *testing.T) {
	name, initiative := "Renamed project", "OpenTrawl"
	for _, mode := range []string{"missing", "ambiguous"} {
		graph := newPortfolioGraph()
		graph.initiativeResolution = mode
		_, err := (&LinearAPI{graph: graph}).UpdateProject(context.Background(), "Process & tooling", "portfolio bot", ProjectUpdateOptions{Name: &name, Initiative: &initiative})
		if err == nil {
			t.Fatalf("%s initiative was accepted", mode)
		}
		if graph.updateInput != nil || graph.attachInput != nil {
			t.Fatalf("%s initiative caused a project mutation: update=%#v attach=%#v", mode, graph.updateInput, graph.attachInput)
		}
	}
}

func TestCreateProjectReportsAttachFailureAfterCreation(t *testing.T) {
	graph := newPortfolioGraph()
	graph.attachError = true
	initiative := "OpenTrawl"
	_, err := (&LinearAPI{graph: graph}).CreateProject(context.Background(), "portfolio bot", ProjectCreateOptions{Team: "TRAWL", Name: "Engineering delivery system", Summary: "Summary", Description: "Brief", Status: "Triage", Priority: "high", Initiative: &initiative})
	if err == nil || !strings.Contains(err.Error(), "was created but could not be attached") || !strings.Contains(err.Error(), "(project-1)") {
		t.Fatalf("attach failure = %v", err)
	}
	if graph.createCalls != 1 || graph.attachInput == nil {
		t.Fatalf("create calls = %d, attach input = %#v", graph.createCalls, graph.attachInput)
	}
}

func TestProjectWriteVerifiesBeforeAttachment(t *testing.T) {
	initiative := "OpenTrawl"
	summary := "Requested summary"
	update := newPortfolioGraphWithUpdateMismatch()
	_, err := (&LinearAPI{graph: update}).UpdateProject(context.Background(), "Process & tooling", "portfolio bot", ProjectUpdateOptions{Summary: &summary, Initiative: &initiative})
	if err == nil || !strings.Contains(err.Error(), "fields were changed") || update.attachInput != nil {
		t.Fatalf("update mismatch = %v; attach = %#v", err, update.attachInput)
	}
	create := newPortfolioGraph()
	create.mismatchAfterCreate = true
	_, err = (&LinearAPI{graph: create}).CreateProject(context.Background(), "portfolio bot", ProjectCreateOptions{Team: "TRAWL", Name: "Engineering delivery system", Summary: "Requested summary", Description: "Brief", Status: "Triage", Priority: "high", Initiative: &initiative})
	if err == nil || !strings.Contains(err.Error(), "was created but its requested fields did not verify") || create.attachInput != nil {
		t.Fatalf("create mismatch = %v; attach = %#v", err, create.attachInput)
	}
}

func TestUpdateProjectReportsChangedFieldsWhenAttachmentReadBackFails(t *testing.T) {
	graph := newPortfolioGraph()
	graph.projectReadErrorAt = 3
	name, initiative := "Engineering delivery system", "OpenTrawl"
	_, err := (&LinearAPI{graph: graph}).UpdateProject(context.Background(), "Process & tooling", "portfolio bot", ProjectUpdateOptions{Name: &name, Initiative: &initiative})
	if err == nil || !strings.Contains(err.Error(), "fields were changed and it was attached") || !strings.Contains(err.Error(), "(project-1)") {
		t.Fatalf("partial update error = %v", err)
	}
}

func TestCreateProjectReportsAttachmentSuccessWhenFinalReadBackFails(t *testing.T) {
	graph := newPortfolioGraph()
	graph.projectReadErrorAt = 2
	initiative := "OpenTrawl"
	_, err := (&LinearAPI{graph: graph}).CreateProject(context.Background(), "portfolio bot", ProjectCreateOptions{Team: "TRAWL", Name: "Engineering delivery system", Summary: "Summary", Description: "Brief", Status: "Triage", Priority: "high", Initiative: &initiative})
	if err == nil || !strings.Contains(err.Error(), "was created and attached") || !strings.Contains(err.Error(), "(project-1)") {
		t.Fatalf("partial create error = %v", err)
	}
}

func TestResolveProjectQuerySelectsEndCursor(t *testing.T) {
	if !strings.Contains(resolveProjectQuery, "pageInfo { hasNextPage endCursor }") {
		t.Fatalf("ResolveProject selection does not request endCursor:\n%s", resolveProjectQuery)
	}
}

func TestTopLevelGraphQLErrorRefusesSuccessfulData(t *testing.T) {
	logger, _ := testRequestLogger(t)
	err := decodeGraphResponse([]byte(`{"data":{"projectCreate":{"success":true}},"errors":[{"message":"synthetic top-level refusal"}]}`), &struct{}{}, logger)
	if err == nil || !strings.Contains(err.Error(), "synthetic top-level refusal") {
		t.Fatalf("top-level GraphQL error = %v", err)
	}
}

func TestProjectCreateValidationMatchesCLIAndMCP(t *testing.T) {
	descriptionPath := filepath.Join(t.TempDir(), "empty.md")
	if err := os.WriteFile(descriptionPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, summary := range []string{" ", ""} {
		cliGraph, mcpGraph := newPortfolioGraph(), newPortfolioGraph()
		old := newLinearWriteAPI
		newLinearWriteAPI = func(io.Writer, int) (*LinearAPI, error) { return &LinearAPI{graph: cliGraph}, nil }
		var cliOut, cliErr bytes.Buffer
		cliError := execute([]string{"project", "create", "--team", "TRAWL", "--name", "Engineering delivery system", "--summary", summary, "--description-file", descriptionPath, "--status", "Triage", "--priority", "high", "--as", "portfolio bot"}, bytes.NewReader(nil), &cliOut, &cliErr)
		newLinearWriteAPI = old
		server := &MCPServer{api: &LinearAPI{graph: mcpGraph}}
		_, mcpError := server.runTool("create_project", rawArguments(map[string]string{"team": "TRAWL", "name": "Engineering delivery system", "summary": summary, "description": "", "status": "Triage", "priority": "high", "actor": "portfolio bot"}))
		if cliError == nil || mcpError == nil || cliError.Error() != mcpError.Error() || cliGraph.createCalls != 0 || mcpGraph.createCalls != 0 {
			t.Fatalf("summary %q: cli=%v mcp=%v cli writes=%d mcp writes=%d", summary, cliError, mcpError, cliGraph.createCalls, mcpGraph.createCalls)
		}
	}
	for _, surface := range []string{"cli", "mcp"} {
		graph := newPortfolioGraph()
		if surface == "cli" {
			old := newLinearWriteAPI
			newLinearWriteAPI = func(io.Writer, int) (*LinearAPI, error) { return &LinearAPI{graph: graph}, nil }
			var stdout, stderr bytes.Buffer
			err := execute([]string{"project", "create", "--team", "TRAWL", "--name", "Engineering delivery system", "--summary", "Summary", "--description-file", descriptionPath, "--status", "Triage", "--priority", "high", "--as", "portfolio bot"}, bytes.NewReader(nil), &stdout, &stderr)
			newLinearWriteAPI = old
			if err != nil {
				t.Fatalf("CLI empty description = %v", err)
			}
		} else {
			server := &MCPServer{api: &LinearAPI{graph: graph}}
			if _, err := server.runTool("create_project", rawArguments(map[string]string{"team": "TRAWL", "name": "Engineering delivery system", "summary": "Summary", "description": "", "status": "Triage", "priority": "high", "actor": "portfolio bot"})); err != nil {
				t.Fatalf("MCP empty description = %v", err)
			}
		}
		if graph.createCalls != 1 {
			t.Fatalf("%s create calls = %d", surface, graph.createCalls)
		}
	}
}

func TestInitiativeNameResolutionSkipsIDLookup(t *testing.T) {
	graph := newPortfolioGraph()
	if _, err := (&LinearAPI{graph: graph}).ResolveInitiative(context.Background(), "OpenTrawl"); err != nil {
		t.Fatal(err)
	}
	if graph.initiativeIDLookups != 0 {
		t.Fatalf("initiative id lookups = %d, want 0", graph.initiativeIDLookups)
	}
	if !isLinearID("00000000-0000-4000-8000-000000000000") || isLinearID("OpenTrawl") {
		t.Fatal("initiative id recognition does not distinguish ids from names")
	}
	graph.initiative.ID = "00000000-0000-4000-8000-000000000000"
	resolved, err := (&LinearAPI{graph: graph}).ResolveInitiative(context.Background(), graph.initiative.ID)
	if err != nil || resolved.ID != graph.initiative.ID || graph.initiativeIDLookups != 1 {
		t.Fatalf("id resolution=%#v error=%v lookups=%d", resolved, err, graph.initiativeIDLookups)
	}
}

func TestInitiativeReadUpdateAndRefusals(t *testing.T) {
	graph := newPortfolioGraph()
	initiative, err := (&LinearAPI{graph: graph}).GetInitiative(context.Background(), "OpenTrawl")
	if err != nil || len(initiative.Projects.Nodes) != 2 || graph.initiativeReadPages != 2 {
		t.Fatalf("GetInitiative = %#v, %v; pages = %d", initiative, err, graph.initiativeReadPages)
	}
	summary := "Updated summary"
	graph.initiative.Description = summary
	updated, err := (&LinearAPI{graph: graph}).UpdateInitiative(context.Background(), "OpenTrawl", "portfolio bot", InitiativeUpdateOptions{Summary: &summary})
	if err != nil || updated.Description != summary || !reflect.DeepEqual(graph.initiativeUpdateInput, map[string]any{"description": summary}) {
		t.Fatalf("UpdateInitiative = %#v, %v; input = %#v", updated, err, graph.initiativeUpdateInput)
	}
	for _, mode := range []string{"missing", "ambiguous"} {
		graph := newPortfolioGraph()
		graph.initiativeResolution = mode
		_, err := (&LinearAPI{graph: graph}).UpdateInitiative(context.Background(), "OpenTrawl", "portfolio bot", InitiativeUpdateOptions{Summary: &summary})
		if err == nil || graph.initiativeUpdateCalls != 0 {
			t.Fatalf("%s initiative refusal = %v; writes = %d", mode, err, graph.initiativeUpdateCalls)
		}
	}
}

func TestUpdateInitiativeReportsPartialWriteOnReadBackFailure(t *testing.T) {
	graph := newPortfolioGraph()
	graph.initiativeReadErrorAt = 1
	summary := "Updated summary"
	_, err := (&LinearAPI{graph: graph}).UpdateInitiative(context.Background(), "OpenTrawl", "portfolio bot", InitiativeUpdateOptions{Summary: &summary})
	if err == nil || !strings.Contains(err.Error(), "fields may already have changed") || !strings.Contains(err.Error(), "initiative \"OpenTrawl\" (initiative-1)") {
		t.Fatalf("initiative partial-write error = %v", err)
	}
}

func TestProjectReadBackMismatchFails(t *testing.T) {
	graph := newPortfolioGraph()
	graph.mismatchAfterUpdate = true
	summary := "Requested summary"
	_, err := (&LinearAPI{graph: graph}).UpdateProject(context.Background(), "Process & tooling", "portfolio bot", ProjectUpdateOptions{Summary: &summary})
	if err == nil || !strings.Contains(err.Error(), "read-back") {
		t.Fatalf("mismatch error = %v", err)
	}
}

func TestPortfolioMCPToolsMatchCLIContract(t *testing.T) {
	tools := map[string]map[string]any{}
	for _, tool := range mcpTools() {
		tools[tool["name"].(string)] = tool
	}
	for name, fields := range map[string][]string{
		"create_project":    {"team", "name", "actor", "summary", "description", "status", "priority", "initiative"},
		"update_project":    {"project", "actor", "name", "summary", "description", "status", "priority", "initiative"},
		"get_initiative":    {"initiative"},
		"update_initiative": {"initiative", "actor", "summary", "description"},
	} {
		properties := tools[name]["inputSchema"].(map[string]any)["properties"].(map[string]any)
		for _, field := range fields {
			if _, ok := properties[field]; !ok {
				t.Errorf("%s is missing %s", name, field)
			}
		}
	}
	for _, command := range []string{"linear project create", "linear initiative <INITIATIVE>", "linear initiative update"} {
		if !strings.Contains(rootHelp, command) {
			t.Errorf("CLI help is missing %q", command)
		}
	}
}
