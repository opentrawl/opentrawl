package main

import (
	"encoding/json"
	"fmt"
)

func mcpTools() []map[string]any {
	return []map[string]any{
		{
			"name":        "inbox",
			"description": "List Josh's unacknowledged directive comments.",
			"inputSchema": objectSchema(map[string]any{
				"team":  stringSchema("Optional Linear team key, for example TRAWL."),
				"since": stringSchema("Optional duration window. Uses Go duration syntax plus d for days."),
				"all":   boolSchema("List across all time. Cannot be used with since."),
			}, nil),
		},
		{
			"name":        "ack_comment",
			"description": "Mark a directive comment as picked up with a visible eyes reaction.",
			"inputSchema": objectSchema(map[string]any{
				"comment_id": stringSchema("Linear comment id to acknowledge."),
			}, []string{"comment_id"}),
		},
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
			"description": "Show one Linear issue, its full description, priority, project, assignee and comments.",
			"inputSchema": objectSchema(map[string]any{
				"issue": stringSchema("Linear issue identifier, for example TRAWL-99."),
			}, []string{"issue"}),
		},
		{
			"name":        "update_issue",
			"description": "Update selected issue fields as the OpenTrawl app. The actor is recorded in the local request log.",
			"inputSchema": objectSchema(map[string]any{
				"issue":       stringSchema("Linear issue identifier, for example TRAWL-99."),
				"actor":       stringSchema("Required actor name for the local request log."),
				"description": stringSchema("Optional replacement description. An empty string clears it."),
				"priority": map[string]any{
					"type":        "string",
					"description": "Optional replacement priority.",
					"enum":        []string{"none", "urgent", "high", "medium", "low"},
				},
				"project": stringSchema("Optional project name or slug. Use none to clear it."),
			}, []string{"issue", "actor"}),
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

func boolSchema(description string) map[string]any {
	return map[string]any{"type": "boolean", "description": description}
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

func optionalStringPointer(args map[string]json.RawMessage, name string) (*string, error) {
	raw, ok := args[name]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("%s must be a string", name)
	}
	return &value, nil
}

func optionalBool(args map[string]json.RawMessage, name string) (bool, error) {
	raw, ok := args[name]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return false, nil
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, fmt.Errorf("%s must be a boolean", name)
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
