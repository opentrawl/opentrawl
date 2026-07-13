package main

import (
	"context"
	"errors"
)

type portfolioGraph struct {
	project               Project
	updatedProject        Project
	initiative            Initiative
	createInput           map[string]any
	updateInput           map[string]any
	attachInput           map[string]any
	initiativeUpdateInput map[string]any
	createCalls           int
	resolveProjectPages   int
	initiativeReadPages   int
	initiativeReadErrorAt int
	initiativeUpdateCalls int
	initiativeIDLookups   int
	duplicateOnSecondPage bool
	initiativeResolution  string
	attachError           bool
	mismatchAfterUpdate   bool
	mismatchAfterCreate   bool
	projectReadCalls      int
	projectReadErrorAt    int
}

func newPortfolioGraph() *portfolioGraph {
	project := Project{ID: "project-1", Name: "Process & tooling", SlugID: "process-tooling", Description: "Current summary", Content: "Current brief", Status: ProjectStatus{ID: "status-triage", Name: "Triage"}, Priority: 2, PriorityLabel: "High"}
	project.Teams.Nodes = []Team{{ID: "team-1", Key: "TRAWL", Name: "TRAWL"}}
	initiative := Initiative{ID: "initiative-1", Name: "OpenTrawl", SlugID: "opentrawl", Description: "Current summary", Content: "Current brief"}
	return &portfolioGraph{project: project, updatedProject: project, initiative: initiative}
}

func newPortfolioGraphWithInitiativeResolution(resolution string) *portfolioGraph {
	graph := newPortfolioGraph()
	graph.initiativeResolution = resolution
	return graph
}

func newPortfolioGraphWithAttachError() *portfolioGraph {
	graph := newPortfolioGraph()
	graph.attachError = true
	return graph
}

func newPortfolioGraphWithUpdateMismatch() *portfolioGraph {
	graph := newPortfolioGraph()
	graph.mismatchAfterUpdate = true
	return graph
}

func (graph *portfolioGraph) Do(_ context.Context, query string, variables map[string]any, out any) error {
	switch query {
	case resolveProjectQuery:
		graph.resolveProjectPages++
		if graph.duplicateOnSecondPage {
			if variables["after"] == "next" {
				return setGraphOut(out, map[string]any{"projects": map[string]any{"nodes": []Project{{ID: "duplicate", Name: "Engineering delivery system"}}, "pageInfo": PageInfo{}}})
			}
			return setGraphOut(out, map[string]any{"projects": map[string]any{"nodes": []Project{{ID: "other", Name: "Engineering delivery system"}}, "pageInfo": PageInfo{HasNextPage: true, EndCursor: "next"}}})
		}
		if variables["reference"] != graph.project.Name && variables["reference"] != graph.project.SlugID {
			return setGraphOut(out, map[string]any{"projects": map[string]any{"nodes": []Project{}, "pageInfo": PageInfo{}}})
		}
		return setGraphOut(out, map[string]any{"projects": map[string]any{"nodes": []Project{{ID: graph.project.ID, Name: graph.project.Name, SlugID: graph.project.SlugID}}, "pageInfo": PageInfo{}}})
	case projectByIDQuery:
		graph.projectReadCalls++
		if graph.projectReadErrorAt == graph.projectReadCalls {
			return errors.New("synthetic project read-back failure")
		}
		project := graph.project
		if graph.updateInput != nil || graph.createCalls > 0 || graph.attachInput != nil {
			project = graph.updatedProject
		}
		return setGraphOut(out, map[string]any{"project": project})
	case resolveTeamQuery:
		return setGraphOut(out, map[string]any{"teams": map[string]any{"nodes": []Team{{ID: "team-1", Key: "TRAWL"}}}})
	case projectStatusesQuery:
		return setGraphOut(out, map[string]any{"projectStatuses": map[string]any{"nodes": []ProjectStatus{{ID: "status-triage", Name: "Triage"}}, "pageInfo": PageInfo{}}})
	case resolveInitiativeByIDQuery:
		graph.initiativeIDLookups++
		if variables["id"] == graph.initiative.ID && isLinearID(graph.initiative.ID) {
			return setGraphOut(out, map[string]any{"initiative": graph.initiative})
		}
		return setGraphOut(out, map[string]any{"initiative": nil})
	case resolveInitiativeByNameQuery:
		if graph.initiativeResolution == "missing" {
			return setGraphOut(out, map[string]any{"initiatives": map[string]any{"nodes": []Initiative{}, "pageInfo": PageInfo{}}})
		}
		if graph.initiativeResolution == "ambiguous" {
			return setGraphOut(out, map[string]any{"initiatives": map[string]any{"nodes": []Initiative{graph.initiative, {ID: "initiative-2", Name: "OpenTrawl"}}, "pageInfo": PageInfo{}}})
		}
		return setGraphOut(out, map[string]any{"initiatives": map[string]any{"nodes": []Initiative{graph.initiative}, "pageInfo": PageInfo{}}})
	case createProjectMutation:
		graph.createCalls++
		graph.createInput = variables["input"].(map[string]any)
		graph.updatedProject = graph.project
		graph.updatedProject.Name = graph.createInput["name"].(string)
		graph.updatedProject.Description = graph.createInput["description"].(string)
		graph.updatedProject.Content = graph.createInput["content"].(string)
		if graph.mismatchAfterCreate {
			graph.updatedProject.Description = "unexpected summary"
		}
		return setGraphOut(out, map[string]any{"projectCreate": map[string]any{"success": true, "project": map[string]any{"id": "project-1"}}})
	case updateProjectMutation:
		graph.updateInput = variables["input"].(map[string]any)
		if !graph.mismatchAfterUpdate {
			applyProjectInput(&graph.updatedProject, graph.updateInput)
		}
		return setGraphOut(out, map[string]any{"projectUpdate": map[string]any{"success": true}})
	case createInitiativeToProjectMutation:
		graph.attachInput = variables["input"].(map[string]any)
		if graph.attachError {
			return errors.New("synthetic attachment failure")
		}
		if !projectHasInitiative(graph.updatedProject, "initiative-1") {
			graph.updatedProject.Initiatives.Nodes = append(graph.updatedProject.Initiatives.Nodes, graph.initiative)
		}
		return setGraphOut(out, map[string]any{"initiativeToProjectCreate": map[string]any{"success": true}})
	case initiativeByIDQuery:
		graph.initiativeReadPages++
		if graph.initiativeReadErrorAt == graph.initiativeReadPages {
			return errors.New("synthetic initiative read-back failure")
		}
		initiative := graph.initiativeReadBack()
		if graph.initiativeReadPages%2 == 1 {
			initiative.Projects.Nodes = initiative.Projects.Nodes[:1]
			initiative.Projects.PageInfo = PageInfo{HasNextPage: true, EndCursor: "projects-next"}
		} else {
			initiative.Projects.Nodes = initiative.Projects.Nodes[1:]
		}
		return setGraphOut(out, map[string]any{"initiative": initiative})
	case updateInitiativeMutation:
		graph.initiativeUpdateCalls++
		graph.initiativeUpdateInput = variables["input"].(map[string]any)
		if summary, ok := graph.initiativeUpdateInput["description"].(string); ok {
			graph.initiative.Description = summary
		}
		if description, ok := graph.initiativeUpdateInput["content"].(string); ok {
			graph.initiative.Content = description
		}
		return setGraphOut(out, map[string]any{"initiativeUpdate": map[string]any{"success": true}})
	default:
		return errors.New("unexpected query")
	}
}

func applyProjectInput(project *Project, input map[string]any) {
	if name, ok := input["name"].(string); ok {
		project.Name = name
	}
	if summary, ok := input["description"].(string); ok {
		project.Description = summary
	}
	if description, ok := input["content"].(string); ok {
		project.Content = description
	}
	if priority, ok := input["priority"].(int); ok {
		project.Priority = priority
	}
	if statusID, ok := input["statusId"].(string); ok {
		project.Status.ID = statusID
	}
}

func (graph *portfolioGraph) initiativeReadBack() Initiative {
	initiative := graph.initiative
	initiative.Projects.Nodes = []Project{{ID: "project-1", Name: graph.updatedProject.Name}, {ID: "project-2", Name: "Another project"}}
	return initiative
}
