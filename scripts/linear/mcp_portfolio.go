package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
)

func isPortfolioTool(name string) bool {
	switch name {
	case "get_project", "create_project", "update_project", "ensure_project_milestone", "get_initiative", "update_initiative":
		return true
	default:
		return false
	}
}

func (s *MCPServer) runPortfolioTool(ctx context.Context, api *LinearAPI, name string, args map[string]json.RawMessage) (string, error) {
	switch name {
	case "get_project":
		project, err := requiredString(args, "project")
		if err != nil {
			return "", err
		}
		result, err := api.GetProject(ctx, project)
		if err != nil {
			return "", err
		}
		return renderString(func(w io.Writer) error { return RenderProject(w, result) })
	case "create_project":
		options, actor, err := projectCreateOptions(args)
		if err != nil {
			return "", err
		}
		result, err := api.CreateProject(ctx, actor, options)
		if err != nil {
			return "", err
		}
		return renderString(func(w io.Writer) error { return RenderProject(w, result) })
	case "update_project":
		project, err := requiredString(args, "project")
		if err != nil {
			return "", err
		}
		actor, err := requiredString(args, "actor")
		if err != nil {
			return "", err
		}
		options, err := projectUpdateOptions(args)
		if err != nil {
			return "", err
		}
		result, err := api.UpdateProject(ctx, project, actor, options)
		if err != nil {
			return "", err
		}
		return renderString(func(w io.Writer) error { return RenderProject(w, result) })
	case "ensure_project_milestone":
		project, err := requiredString(args, "project")
		if err != nil {
			return "", err
		}
		name, err := requiredString(args, "name")
		if err != nil {
			return "", err
		}
		actor, err := requiredString(args, "actor")
		if err != nil {
			return "", err
		}
		description, err := optionalStringPointer(args, "description")
		if err != nil {
			return "", err
		}
		result, err := api.EnsureProjectMilestone(ctx, project, actor, ProjectMilestoneOptions{Name: name, Description: description})
		if err != nil {
			return "", err
		}
		return renderString(func(w io.Writer) error { return RenderEnsuredProjectMilestone(w, result) })
	case "get_initiative":
		initiative, err := requiredString(args, "initiative")
		if err != nil {
			return "", err
		}
		result, err := api.GetInitiative(ctx, initiative)
		if err != nil {
			return "", err
		}
		return renderString(func(w io.Writer) error { return RenderInitiative(w, result) })
	case "update_initiative":
		initiative, err := requiredString(args, "initiative")
		if err != nil {
			return "", err
		}
		actor, err := requiredString(args, "actor")
		if err != nil {
			return "", err
		}
		summary, err := optionalStringPointer(args, "summary")
		if err != nil {
			return "", err
		}
		description, err := optionalStringPointer(args, "description")
		if err != nil {
			return "", err
		}
		result, err := api.UpdateInitiative(ctx, initiative, actor, InitiativeUpdateOptions{Summary: summary, Description: description})
		if err != nil {
			return "", err
		}
		return renderString(func(w io.Writer) error { return RenderInitiative(w, result) })
	}
	return "", fmt.Errorf("unknown portfolio tool %q", name)
}

func projectCreateOptions(args map[string]json.RawMessage) (ProjectCreateOptions, string, error) {
	team, err := requiredString(args, "team")
	if err != nil {
		return ProjectCreateOptions{}, "", err
	}
	name, err := requiredString(args, "name")
	if err != nil {
		return ProjectCreateOptions{}, "", err
	}
	actor, err := requiredString(args, "actor")
	if err != nil {
		return ProjectCreateOptions{}, "", err
	}
	summary, err := requiredStringAllowEmpty(args, "summary")
	if err != nil {
		return ProjectCreateOptions{}, "", err
	}
	description, err := requiredStringAllowEmpty(args, "description")
	if err != nil {
		return ProjectCreateOptions{}, "", err
	}
	status, err := requiredString(args, "status")
	if err != nil {
		return ProjectCreateOptions{}, "", err
	}
	priority, err := requiredString(args, "priority")
	if err != nil {
		return ProjectCreateOptions{}, "", err
	}
	initiative, err := optionalStringPointer(args, "initiative")
	if err != nil {
		return ProjectCreateOptions{}, "", err
	}
	return ProjectCreateOptions{Team: team, Name: name, Summary: summary, Description: description, Status: status, Priority: priority, Initiative: initiative}, actor, nil
}

func projectUpdateOptions(args map[string]json.RawMessage) (ProjectUpdateOptions, error) {
	name, err := optionalStringPointer(args, "name")
	if err != nil {
		return ProjectUpdateOptions{}, err
	}
	summary, err := optionalStringPointer(args, "summary")
	if err != nil {
		return ProjectUpdateOptions{}, err
	}
	description, err := optionalStringPointer(args, "description")
	if err != nil {
		return ProjectUpdateOptions{}, err
	}
	status, err := optionalStringPointer(args, "status")
	if err != nil {
		return ProjectUpdateOptions{}, err
	}
	priority, err := optionalStringPointer(args, "priority")
	if err != nil {
		return ProjectUpdateOptions{}, err
	}
	initiative, err := optionalStringPointer(args, "initiative")
	if err != nil {
		return ProjectUpdateOptions{}, err
	}
	return ProjectUpdateOptions{Name: name, Summary: summary, Description: description, Status: status, Priority: priority, Initiative: initiative}, nil
}

func requiredStringAllowEmpty(args map[string]json.RawMessage, name string) (string, error) {
	raw, ok := args[name]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return "", fmt.Errorf("%s is required", name)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("%s must be a string", name)
	}
	return value, nil
}
