package main

import (
	"context"
	"fmt"
	"strings"
)

type IssueChange struct {
	Field string
	Value string
}

type IssueUpdateOptions struct {
	Description *string
	Priority    *string
	Project     *string
	Milestone   *string
	Title       *string
}

func (api *LinearAPI) UpdateIssue(ctx context.Context, rawIssue, actor string, options IssueUpdateOptions) (UpdatedIssue, error) {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return UpdatedIssue{}, fmt.Errorf("--as is required for write commands")
	}
	if options.empty() {
		return UpdatedIssue{}, fmt.Errorf("at least one issue field is required")
	}
	input := map[string]any{}
	changed := []string{}
	if options.Description != nil {
		input["description"] = *options.Description
		changed = append(changed, "description")
	}
	if options.Priority != nil {
		priority, err := parsePriority(*options.Priority)
		if err != nil {
			return UpdatedIssue{}, err
		}
		input["priority"] = priority
		changed = append(changed, "priority")
	}
	if options.Project != nil && options.Milestone != nil {
		return UpdatedIssue{}, fmt.Errorf("--project and --milestone cannot be used together")
	}
	if options.Title != nil {
		title := strings.TrimSpace(*options.Title)
		if title == "" {
			return UpdatedIssue{}, fmt.Errorf("--title needs a value")
		}
		input["title"] = title
		changed = append(changed, "title")
	}
	if options.Project != nil {
		projectName := strings.TrimSpace(*options.Project)
		if projectName == "" {
			return UpdatedIssue{}, fmt.Errorf("--project needs a value")
		}
		if strings.EqualFold(projectName, "none") {
			input["projectId"] = nil
		}
		changed = append(changed, "project")
	}
	issue, err := api.ResolveIssue(ctx, rawIssue)
	if err != nil {
		return UpdatedIssue{}, err
	}
	if options.Project != nil && !strings.EqualFold(strings.TrimSpace(*options.Project), "none") {
		project, err := api.ResolveProject(ctx, *options.Project)
		if err != nil {
			return UpdatedIssue{}, err
		}
		input["projectId"] = project.ID
	}
	if options.Milestone != nil {
		milestoneName := strings.TrimSpace(*options.Milestone)
		if milestoneName == "" {
			return UpdatedIssue{}, fmt.Errorf("--milestone needs a value")
		}
		if strings.EqualFold(milestoneName, "none") {
			input["projectMilestoneId"] = nil
		} else {
			if issue.Project == nil {
				return UpdatedIssue{}, fmt.Errorf("issue %s has no project; assign a project before assigning a milestone", issue.Identifier)
			}
			projectReference := issue.Project.SlugID
			if strings.TrimSpace(projectReference) == "" {
				projectReference = issue.Project.Name
			}
			project, err := api.GetProject(ctx, projectReference)
			if err != nil {
				return UpdatedIssue{}, err
			}
			var matches []ProjectMilestone
			for _, candidate := range project.Milestones.Nodes {
				if candidate.Name == milestoneName {
					matches = append(matches, candidate)
				}
			}
			if len(matches) == 0 {
				return UpdatedIssue{}, fmt.Errorf("milestone %q was not found in project %q", milestoneName, issue.Project.Name)
			}
			if len(matches) > 1 {
				return UpdatedIssue{}, fmt.Errorf("milestone %q is ambiguous in project %q", milestoneName, issue.Project.Name)
			}
			if matches[0].Project != nil && matches[0].Project.ID != "" && matches[0].Project.ID != issue.Project.ID {
				return UpdatedIssue{}, fmt.Errorf("milestone %q belongs to project %q, not %q", milestoneName, matches[0].Project.Name, issue.Project.Name)
			}
			input["projectMilestoneId"] = matches[0].ID
		}
		changed = append(changed, "milestone")
	}
	if api.logger != nil {
		api.logger.LogDiagnostic("info", fmt.Sprintf("issueUpdate requested: %s fields %s by %s", issue.Identifier, strings.Join(changed, ", "), actor))
	}
	var out struct {
		IssueUpdate struct {
			Success bool  `json:"success"`
			Issue   Issue `json:"issue"`
		} `json:"issueUpdate"`
	}
	if err := api.graph.Do(ctx, updateIssueMutation, map[string]any{"id": issue.ID, "input": input}, &out); err != nil {
		return UpdatedIssue{}, err
	}
	if !out.IssueUpdate.Success {
		return UpdatedIssue{}, fmt.Errorf("linear did not update the issue")
	}
	readBack, err := api.GetIssue(ctx, rawIssue)
	if err != nil {
		return UpdatedIssue{}, err
	}
	return UpdatedIssue{
		Issue:   readBack,
		Actor:   actor,
		Changes: issueChanges(readBack, options),
	}, nil
}

func (options IssueUpdateOptions) empty() bool {
	return options.Description == nil && options.Priority == nil && options.Project == nil && options.Milestone == nil && options.Title == nil
}

func issueChanges(issue Issue, options IssueUpdateOptions) []IssueChange {
	changes := make([]IssueChange, 0, 5)
	if options.Title != nil {
		changes = append(changes, IssueChange{Field: "title", Value: issue.Title})
	}
	if options.Description != nil {
		value := "replaced"
		if strings.TrimSpace(issue.Description) == "" {
			value = "cleared"
		}
		changes = append(changes, IssueChange{Field: "description", Value: value})
	}
	if options.Priority != nil {
		changes = append(changes, IssueChange{Field: "priority", Value: issue.PriorityLabel})
	}
	if options.Project != nil {
		value := "none"
		if issue.Project != nil {
			value = issue.Project.Name
		}
		changes = append(changes, IssueChange{Field: "project", Value: value})
	}
	if options.Milestone != nil {
		value := "none"
		if issue.Milestone != nil {
			value = issue.Milestone.Name
		}
		changes = append(changes, IssueChange{Field: "milestone", Value: value})
	}
	return changes
}

func parsePriority(raw string) (int, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "none", "no priority":
		return 0, nil
	case "urgent":
		return 1, nil
	case "high":
		return 2, nil
	case "medium":
		return 3, nil
	case "low":
		return 4, nil
	default:
		return 0, fmt.Errorf("priority %q is invalid. Valid priorities: none, urgent, high, medium, low", raw)
	}
}
