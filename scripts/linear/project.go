package main

import (
	"context"
	"fmt"
	"strings"
)

type ProjectUpdateOptions struct {
	Summary     *string
	Description *string
	Status      *string
	Priority    *string
}

type ProjectMilestoneOptions struct {
	Name        string
	Description *string
}

type EnsuredProjectMilestone struct {
	Project   Project
	Milestone ProjectMilestone
	Actor     string
	Created   bool
	Changed   bool
}

func (api *LinearAPI) GetProject(ctx context.Context, reference string) (Project, error) {
	project, err := api.ResolveProject(ctx, reference)
	if err != nil {
		return Project{}, err
	}
	readMilestones, readIssues := true, true
	milestonesAfter, issuesAfter := "", ""
	for page := 0; readMilestones || readIssues; page++ {
		var out struct {
			Project *Project `json:"project"`
		}
		variables := map[string]any{
			"id":             project.ID,
			"readMilestones": readMilestones,
			"readIssues":     readIssues,
		}
		if milestonesAfter != "" {
			variables["milestonesAfter"] = milestonesAfter
		}
		if issuesAfter != "" {
			variables["issuesAfter"] = issuesAfter
		}
		if err := api.graph.Do(ctx, projectByIDQuery, variables, &out); err != nil {
			return Project{}, err
		}
		if out.Project == nil {
			return Project{}, fmt.Errorf("project %q was not found", strings.TrimSpace(reference))
		}
		pageProject := *out.Project
		if page == 0 {
			project = pageProject
		} else {
			if readMilestones {
				project.Milestones.Nodes = append(project.Milestones.Nodes, pageProject.Milestones.Nodes...)
				project.Milestones.PageInfo = pageProject.Milestones.PageInfo
			}
			if readIssues {
				project.Issues.Nodes = append(project.Issues.Nodes, pageProject.Issues.Nodes...)
				project.Issues.PageInfo = pageProject.Issues.PageInfo
			}
		}
		if readMilestones {
			readMilestones, milestonesAfter, err = nextProjectPage(pageProject.Milestones.PageInfo, "milestone")
			if err != nil {
				return Project{}, err
			}
		}
		if readIssues {
			readIssues, issuesAfter, err = nextProjectPage(pageProject.Issues.PageInfo, "issue")
			if err != nil {
				return Project{}, err
			}
		}
	}
	return project, nil
}

func nextProjectPage(page PageInfo, item string) (bool, string, error) {
	if !page.HasNextPage {
		return false, "", nil
	}
	if page.EndCursor == "" {
		return false, "", fmt.Errorf("linear did not return a cursor for the next project %s page", item)
	}
	return true, page.EndCursor, nil
}

func (api *LinearAPI) UpdateProject(ctx context.Context, reference, actor string, options ProjectUpdateOptions) (Project, error) {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return Project{}, fmt.Errorf("--as is required for write commands")
	}
	if options.empty() {
		return Project{}, fmt.Errorf("at least one project field is required")
	}
	project, err := api.ResolveProject(ctx, reference)
	if err != nil {
		return Project{}, err
	}
	input := map[string]any{}
	if options.Summary != nil {
		summary := *options.Summary
		if strings.EqualFold(strings.TrimSpace(summary), "none") {
			summary = ""
		}
		input["description"] = summary
	}
	if options.Description != nil {
		input["content"] = *options.Description
	}
	if options.Status != nil {
		status, err := api.ResolveProjectStatus(ctx, *options.Status)
		if err != nil {
			return Project{}, err
		}
		input["statusId"] = status.ID
	}
	if options.Priority != nil {
		priority, err := parsePriority(*options.Priority)
		if err != nil {
			return Project{}, err
		}
		input["priority"] = priority
	}
	if api.logger != nil {
		api.logger.LogDiagnostic("info", fmt.Sprintf("projectUpdate requested: %s by %s", project.Name, actor))
	}
	var out struct {
		ProjectUpdate struct {
			Success bool `json:"success"`
		} `json:"projectUpdate"`
	}
	if err := api.graph.Do(ctx, updateProjectMutation, map[string]any{"id": project.ID, "input": input}, &out); err != nil {
		return Project{}, err
	}
	if !out.ProjectUpdate.Success {
		return Project{}, fmt.Errorf("linear did not update the project")
	}
	return api.GetProject(ctx, reference)
}

func (options ProjectUpdateOptions) empty() bool {
	return options.Summary == nil && options.Description == nil && options.Status == nil && options.Priority == nil
}

func (api *LinearAPI) EnsureProjectMilestone(ctx context.Context, reference, actor string, options ProjectMilestoneOptions) (EnsuredProjectMilestone, error) {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return EnsuredProjectMilestone{}, fmt.Errorf("--as is required for write commands")
	}
	name := strings.TrimSpace(options.Name)
	if name == "" {
		return EnsuredProjectMilestone{}, fmt.Errorf("--name is required")
	}
	project, err := api.GetProject(ctx, reference)
	if err != nil {
		return EnsuredProjectMilestone{}, err
	}
	matches := make([]ProjectMilestone, 0, 1)
	for _, milestone := range project.Milestones.Nodes {
		if milestone.Name == name {
			matches = append(matches, milestone)
		}
	}
	if len(matches) > 1 {
		return EnsuredProjectMilestone{}, fmt.Errorf("milestone %q is ambiguous in project %q", name, project.Name)
	}
	created := len(matches) == 0
	changed := created
	if created {
		input := map[string]any{"projectId": project.ID, "name": name}
		if options.Description != nil {
			input["description"] = *options.Description
		}
		if api.logger != nil {
			api.logger.LogDiagnostic("info", fmt.Sprintf("projectMilestoneCreate requested: %s in %s by %s", name, project.Name, actor))
		}
		var out struct {
			ProjectMilestoneCreate struct {
				Success bool `json:"success"`
			} `json:"projectMilestoneCreate"`
		}
		if err := api.graph.Do(ctx, createProjectMilestoneMutation, map[string]any{"input": input}, &out); err != nil {
			return EnsuredProjectMilestone{}, err
		}
		if !out.ProjectMilestoneCreate.Success {
			return EnsuredProjectMilestone{}, fmt.Errorf("linear did not create the project milestone")
		}
	} else if options.Description != nil {
		changed = true
		if api.logger != nil {
			api.logger.LogDiagnostic("info", fmt.Sprintf("projectMilestoneUpdate requested: %s in %s by %s", name, project.Name, actor))
		}
		var out struct {
			ProjectMilestoneUpdate struct {
				Success bool `json:"success"`
			} `json:"projectMilestoneUpdate"`
		}
		if err := api.graph.Do(ctx, updateProjectMilestoneMutation, map[string]any{"id": matches[0].ID, "input": map[string]any{"description": *options.Description}}, &out); err != nil {
			return EnsuredProjectMilestone{}, err
		}
		if !out.ProjectMilestoneUpdate.Success {
			return EnsuredProjectMilestone{}, fmt.Errorf("linear did not update the project milestone")
		}
	}
	readBack, err := api.GetProject(ctx, reference)
	if err != nil {
		return EnsuredProjectMilestone{}, err
	}
	var found []ProjectMilestone
	for _, milestone := range readBack.Milestones.Nodes {
		if milestone.Name == name {
			found = append(found, milestone)
		}
	}
	if len(found) != 1 {
		return EnsuredProjectMilestone{}, fmt.Errorf("linear did not return exactly one milestone %q after the write", name)
	}
	return EnsuredProjectMilestone{Project: readBack, Milestone: found[0], Actor: actor, Created: created, Changed: changed}, nil
}
