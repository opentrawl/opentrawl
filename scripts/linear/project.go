package main

import (
	"context"
	"fmt"
	"strings"
)

type ProjectUpdateOptions struct {
	Name        *string
	Summary     *string
	Description *string
	Status      *string
	Priority    *string
	Initiative  *string
}

type ProjectCreateOptions struct {
	Team        string
	Name        string
	Summary     string
	Description string
	Status      string
	Priority    string
	Initiative  *string
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
	return api.GetProjectByID(ctx, project.ID)
}
func (api *LinearAPI) GetProjectByID(ctx context.Context, id string) (Project, error) {
	readMilestones, readIssues, readInitiatives := true, true, true
	milestonesAfter, issuesAfter, initiativesAfter := "", "", ""
	var project Project
	var err error
	for page := 0; readMilestones || readIssues || readInitiatives; page++ {
		var out struct {
			Project *Project `json:"project"`
		}
		variables := map[string]any{
			"id":              id,
			"readMilestones":  readMilestones,
			"readIssues":      readIssues,
			"readInitiatives": readInitiatives,
		}
		if milestonesAfter != "" {
			variables["milestonesAfter"] = milestonesAfter
		}
		if issuesAfter != "" {
			variables["issuesAfter"] = issuesAfter
		}
		if initiativesAfter != "" {
			variables["initiativesAfter"] = initiativesAfter
		}
		if err := api.graph.Do(ctx, projectByIDQuery, variables, &out); err != nil {
			return Project{}, err
		}
		if out.Project == nil {
			return Project{}, fmt.Errorf("project %q was not found", id)
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
			if readInitiatives {
				project.Initiatives.Nodes = append(project.Initiatives.Nodes, pageProject.Initiatives.Nodes...)
				project.Initiatives.PageInfo = pageProject.Initiatives.PageInfo
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
		if readInitiatives {
			readInitiatives, initiativesAfter, err = nextProjectPage(pageProject.Initiatives.PageInfo, "initiative")
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
	before, err := api.GetProject(ctx, reference)
	if err != nil {
		return Project{}, err
	}
	var initiative *Initiative
	if options.Initiative != nil {
		resolved, err := api.ResolveInitiative(ctx, *options.Initiative)
		if err != nil {
			return Project{}, err
		}
		initiative = &resolved
	}
	input := map[string]any{}
	if options.Name != nil {
		name := strings.TrimSpace(*options.Name)
		if name == "" {
			return Project{}, fmt.Errorf("--name needs a value")
		}
		input["name"] = name
	}
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
	if len(input) > 0 {
		if api.logger != nil {
			api.logger.LogDiagnostic("info", fmt.Sprintf("projectUpdate requested: %s by %s", before.Name, actor))
		}
		var out struct {
			ProjectUpdate struct {
				Success bool `json:"success"`
			} `json:"projectUpdate"`
		}
		if err := api.graph.Do(ctx, updateProjectMutation, map[string]any{"id": before.ID, "input": input}, &out); err != nil {
			return Project{}, err
		}
		if !out.ProjectUpdate.Success {
			return Project{}, fmt.Errorf("linear did not update the project")
		}
		project, err := api.GetProjectByID(ctx, before.ID)
		if err != nil {
			return Project{}, fieldsChangedError(before, err)
		}
		if err := verifyProjectReadBack(project, before, options, ""); err != nil {
			return Project{}, fieldsChangedError(project, err)
		}
		before = project
	}
	initiativeID := ""
	attached := false
	if initiative != nil {
		initiativeID = initiative.ID
		if !projectHasInitiative(before, initiative.ID) {
			if err := api.attachProjectToInitiative(ctx, before.ID, initiative.ID, actor); err != nil {
				if len(input) > 0 {
					return Project{}, fmt.Errorf("%s fields were changed but could not be attached to initiative %q: %w", projectIdentity(before), initiative.Name, err)
				}
				return Project{}, fmt.Errorf("%s could not be attached to initiative %q: %w", projectIdentity(before), initiative.Name, err)
			}
			attached = true
			initiativeReadBack, err := api.GetInitiativeByID(ctx, initiative.ID)
			if err != nil {
				return Project{}, attachedReadBackError(before, *initiative, len(input) > 0, err)
			}
			if !initiativeHasProject(initiativeReadBack, before.ID) {
				return Project{}, attachedReadBackError(before, *initiative, len(input) > 0, fmt.Errorf("initiative read-back did not include the attached project"))
			}
		}
	}
	readBack, err := api.GetProjectByID(ctx, before.ID)
	if err != nil {
		if attached {
			return Project{}, attachedReadBackError(before, *initiative, len(input) > 0, err)
		}
		if len(input) > 0 {
			return Project{}, fieldsChangedError(before, err)
		}
		return Project{}, err
	}
	if err := verifyProjectReadBack(readBack, before, options, initiativeID); err != nil {
		if attached {
			return Project{}, attachedReadBackError(readBack, *initiative, len(input) > 0, err)
		}
		if len(input) > 0 {
			return Project{}, fieldsChangedError(readBack, err)
		}
		return Project{}, err
	}
	return readBack, nil
}

func (options ProjectUpdateOptions) empty() bool {
	return options.Name == nil && options.Summary == nil && options.Description == nil && options.Status == nil && options.Priority == nil && options.Initiative == nil
}

func (api *LinearAPI) CreateProject(ctx context.Context, actor string, options ProjectCreateOptions) (Project, error) {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return Project{}, fmt.Errorf("--as is required for write commands")
	}
	if err := options.validate(); err != nil {
		return Project{}, err
	}
	name := strings.TrimSpace(options.Name)
	if matches, err := api.findProjects(ctx, name); err != nil {
		return Project{}, err
	} else if len(matches) > 0 {
		return Project{}, fmt.Errorf("project %q already exists", name)
	}
	team, err := api.ResolveTeam(ctx, options.Team)
	if err != nil {
		return Project{}, err
	}
	status, err := api.ResolveProjectStatus(ctx, options.Status)
	if err != nil {
		return Project{}, err
	}
	priority, err := parsePriority(options.Priority)
	if err != nil {
		return Project{}, err
	}
	var initiative Initiative
	if options.Initiative != nil {
		initiative, err = api.ResolveInitiative(ctx, *options.Initiative)
		if err != nil {
			return Project{}, err
		}
	}
	input := map[string]any{
		"teamIds":     []string{team.ID},
		"name":        name,
		"description": options.Summary,
		"content":     options.Description,
		"statusId":    status.ID,
		"priority":    priority,
	}
	if api.logger != nil {
		api.logger.LogDiagnostic("info", fmt.Sprintf("projectCreate requested: %s by %s", name, actor))
	}
	var out struct {
		ProjectCreate struct {
			Success bool `json:"success"`
			Project struct {
				ID string `json:"id"`
			} `json:"project"`
		} `json:"projectCreate"`
	}
	if err := api.graph.Do(ctx, createProjectMutation, map[string]any{"input": input}, &out); err != nil {
		return Project{}, err
	}
	if !out.ProjectCreate.Success || out.ProjectCreate.Project.ID == "" {
		return Project{}, fmt.Errorf("linear did not create the project")
	}
	project, err := api.GetProjectByID(ctx, out.ProjectCreate.Project.ID)
	if err != nil {
		return Project{}, fmt.Errorf("project %q (%s) was created but could not be read back: %w", name, out.ProjectCreate.Project.ID, err)
	}
	if err := verifyCreatedProject(project, options, status, priority, Initiative{}, false); err != nil {
		return Project{}, fmt.Errorf("%s was created but its requested fields did not verify; it was not attached to an initiative: %w", projectIdentity(project), err)
	}
	if options.Initiative != nil {
		if err := api.attachProjectToInitiative(ctx, project.ID, initiative.ID, actor); err != nil {
			return Project{}, fmt.Errorf("%s was created but could not be attached to initiative %q: %w", projectIdentity(project), initiative.Name, err)
		}
		project, err = api.GetProjectByID(ctx, project.ID)
		if err != nil {
			return Project{}, fmt.Errorf("%s was created and attached to initiative %q but could not be read back: %w", projectIdentity(Project{ID: out.ProjectCreate.Project.ID, Name: name}), initiative.Name, err)
		}
		initiativeReadBack, err := api.GetInitiativeByID(ctx, initiative.ID)
		if err != nil {
			return Project{}, fmt.Errorf("%s was created and attached to initiative %q but the attachment could not be read back: %w", projectIdentity(project), initiative.Name, err)
		}
		if !initiativeHasProject(initiativeReadBack, project.ID) {
			return Project{}, fmt.Errorf("%s was created and attached to initiative %q but the attachment was not returned by Linear", projectIdentity(project), initiative.Name)
		}
	}
	if err := verifyCreatedProject(project, options, status, priority, initiative, options.Initiative != nil); err != nil {
		if options.Initiative != nil {
			return Project{}, fmt.Errorf("%s was created and attached to initiative %q but the final read-back did not match: %w", projectIdentity(project), initiative.Name, err)
		}
		return Project{}, fmt.Errorf("%s was created but the final read-back did not match: %w", projectIdentity(project), err)
	}
	return project, nil
}

func (options ProjectCreateOptions) validate() error {
	for _, field := range []struct{ name, value string }{
		{"team", options.Team}, {"name", options.Name}, {"summary", options.Summary}, {"status", options.Status}, {"priority", options.Priority},
	} {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("--%s is required", field.name)
		}
	}
	return nil
}

func projectIdentity(project Project) string {
	return fmt.Sprintf("project %q (%s)", project.Name, project.ID)
}

func fieldsChangedError(project Project, cause error) error {
	return fmt.Errorf("%s fields were changed but verification failed: %w", projectIdentity(project), cause)
}

func attachedReadBackError(project Project, initiative Initiative, fieldsChanged bool, cause error) error {
	if fieldsChanged {
		return fmt.Errorf("%s fields were changed and it was attached to initiative %q, but verification failed: %w", projectIdentity(project), initiative.Name, cause)
	}
	return fmt.Errorf("%s was attached to initiative %q, but verification failed: %w", projectIdentity(project), initiative.Name, cause)
}

func (api *LinearAPI) attachProjectToInitiative(ctx context.Context, projectID, initiativeID, actor string) error {
	if api.logger != nil {
		api.logger.LogDiagnostic("info", fmt.Sprintf("initiativeToProjectCreate requested by %s", actor))
	}
	var out struct {
		InitiativeToProjectCreate struct {
			Success bool `json:"success"`
		} `json:"initiativeToProjectCreate"`
	}
	if err := api.graph.Do(ctx, createInitiativeToProjectMutation, map[string]any{"input": map[string]any{"projectId": projectID, "initiativeId": initiativeID}}, &out); err != nil {
		return err
	}
	if !out.InitiativeToProjectCreate.Success {
		return fmt.Errorf("linear did not attach the project to the initiative")
	}
	return nil
}

func projectHasInitiative(project Project, initiativeID string) bool {
	for _, initiative := range project.Initiatives.Nodes {
		if initiative.ID == initiativeID {
			return true
		}
	}
	return false
}

func initiativeHasProject(initiative Initiative, projectID string) bool {
	for _, project := range initiative.Projects.Nodes {
		if project.ID == projectID {
			return true
		}
	}
	return false
}

func verifyCreatedProject(project Project, options ProjectCreateOptions, status ProjectStatus, priority int, initiative Initiative, checkInitiative bool) error {
	if project.Name != strings.TrimSpace(options.Name) || project.Description != options.Summary || project.Content != options.Description || project.Status.ID != status.ID || project.Priority != priority {
		return fmt.Errorf("project read-back did not match the requested fields")
	}
	if !projectHasTeam(project, options.Team) {
		return fmt.Errorf("project read-back did not include the requested team")
	}
	if checkInitiative && !projectHasInitiative(project, initiative.ID) {
		return fmt.Errorf("project read-back did not include the requested initiative")
	}
	return nil
}

func projectHasTeam(project Project, key string) bool {
	for _, team := range project.Teams.Nodes {
		if strings.EqualFold(team.Key, strings.TrimSpace(key)) {
			return true
		}
	}
	return false
}

func verifyProjectReadBack(readBack, before Project, options ProjectUpdateOptions, initiativeID string) error {
	if options.Name != nil && readBack.Name != strings.TrimSpace(*options.Name) {
		return fmt.Errorf("project read-back name did not match the requested value")
	}
	if options.Summary != nil {
		summary := *options.Summary
		if strings.EqualFold(strings.TrimSpace(summary), "none") {
			summary = ""
		}
		if readBack.Description != summary {
			return fmt.Errorf("project read-back summary did not match the requested value")
		}
	}
	if options.Description != nil && readBack.Content != *options.Description {
		return fmt.Errorf("project read-back description did not match the requested value")
	}
	if options.Status != nil && !strings.EqualFold(readBack.Status.Name, *options.Status) {
		return fmt.Errorf("project read-back status did not match the requested value")
	}
	if options.Priority != nil {
		priority, err := parsePriority(*options.Priority)
		if err != nil {
			return err
		}
		if readBack.Priority != priority {
			return fmt.Errorf("project read-back priority did not match the requested value")
		}
	}
	for _, initiative := range before.Initiatives.Nodes {
		if !projectHasInitiative(readBack, initiative.ID) {
			return fmt.Errorf("project read-back did not preserve initiative %q", initiative.Name)
		}
	}
	if initiativeID != "" {
		if !projectHasInitiative(readBack, initiativeID) {
			return fmt.Errorf("project read-back did not include the requested initiative")
		}
	}
	return nil
}
