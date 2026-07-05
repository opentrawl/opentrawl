package main

import (
	"encoding/json"
	"fmt"
)

func mcpTools() []map[string]any {
	return []map[string]any{
		{
			"name":        "create_comment",
			"description": "Create a Linear issue comment as an app actor display name.",
			"inputSchema": objectSchema(map[string]any{
				"issue": stringSchema("Linear issue identifier, for example TRAWL-99."),
				"actor": stringSchema("Required display name for Linear createAsUser."),
				"body":  stringSchema("Comment body."),
			}, []string{"issue", "actor", "body"}),
		},
		{
			"name":        "create_issue",
			"description": "Create a Linear issue as an app actor display name.",
			"inputSchema": objectSchema(map[string]any{
				"team":        stringSchema("Linear team key, for example TRAWL."),
				"title":       stringSchema("Issue title."),
				"actor":       stringSchema("Required display name for Linear createAsUser."),
				"description": stringSchema("Optional issue description."),
				"labels": map[string]any{
					"type":        "array",
					"description": "Optional label names.",
					"items":       map[string]any{"type": "string"},
				},
			}, []string{"team", "title", "actor"}),
		},
		{
			"name":        "get_issue",
			"description": "Show one Linear issue and its comments.",
			"inputSchema": objectSchema(map[string]any{
				"issue": stringSchema("Linear issue identifier, for example TRAWL-99."),
			}, []string{"issue"}),
		},
		{
			"name":        "list_issues",
			"description": "List Linear issues for a team. Without state, this lists open issues.",
			"inputSchema": objectSchema(map[string]any{
				"team":  stringSchema("Linear team key, for example TRAWL."),
				"state": stringSchema("Optional state name."),
			}, []string{"team"}),
		},
	}
}

func objectSchema(properties map[string]any, required []string) map[string]any {
	return map[string]any{
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
	}
}

func stringSchema(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func requiredString(args map[string]json.RawMessage, name string) (string, error) {
	value, err := optionalString(args, name)
	if err != nil {
		return "", err
	}
	if value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return value, nil
}

func optionalString(args map[string]json.RawMessage, name string) (string, error) {
	raw, ok := args[name]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("%s must be a string", name)
	}
	return value, nil
}

func optionalStringList(args map[string]json.RawMessage, name string) ([]string, error) {
	raw, ok := args[name]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, fmt.Errorf("%s must be an array of strings", name)
	}
	return values, nil
}
