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
	return UpdatedIssue{
		Issue:   out.IssueUpdate.Issue,
		Actor:   actor,
		Changes: issueChanges(out.IssueUpdate.Issue, options),
	}, nil
}

func (options IssueUpdateOptions) empty() bool {
	return options.Description == nil && options.Priority == nil && options.Project == nil
}

func issueChanges(issue Issue, options IssueUpdateOptions) []IssueChange {
	changes := make([]IssueChange, 0, 3)
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
