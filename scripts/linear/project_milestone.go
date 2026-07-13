package main

import (
	"context"
	"fmt"
	"strings"
)

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
