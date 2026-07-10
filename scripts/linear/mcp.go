package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const mcpProtocolVersion = "2025-06-18"

type MCPServer struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
	api    *LinearAPI
}

type rpcRequest struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id"`
	Method  string           `json:"method"`
	Params  json.RawMessage  `json:"params"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type toolResult struct {
	Content []toolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

type toolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func runMCP(stdin io.Reader, stdout, stderr io.Writer) error {
	return (&MCPServer{stdin: stdin, stdout: stdout, stderr: stderr}).Serve()
}

func (s *MCPServer) Serve() error {
	defer func() {
		if s.api != nil {
			_ = s.api.Close()
		}
	}()
	reader := bufio.NewReader(s.stdin)
	for {
		line, err := reader.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			if frameErr := s.serveFrame(line); frameErr != nil {
				return frameErr
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read MCP stdin: %w", err)
		}
	}
}

func (s *MCPServer) serveFrame(line []byte) error {
	var request rpcRequest
	decoder := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(line)))
	if err := decoder.Decode(&request); err != nil {
		return s.writeError(nil, -32700, "parse error")
	}
	if request.ID == nil {
		return nil
	}
	result, rpcErr := s.handle(request)
	if rpcErr != nil {
		return s.writeError(request.ID, rpcErr.Code, rpcErr.Message)
	}
	return s.writeResult(request.ID, result)
}

func (s *MCPServer) handle(request rpcRequest) (any, *rpcError) {
	switch request.Method {
	case "initialize":
		return s.initialize(request.Params), nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": mcpTools()}, nil
	case "tools/call":
		result, err := s.callTool(request.Params)
		if err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		return result, nil
	default:
		return nil, &rpcError{Code: -32601, Message: "method not found"}
	}
}

func (s *MCPServer) initialize(params json.RawMessage) map[string]any {
	var input struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(params, &input)
	return map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities": map[string]any{
			"tools": map[string]any{"listChanged": false},
		},
		"serverInfo": map[string]any{
			"name":    "opentrawl-linear",
			"version": "dev",
		},
	}
}

func (s *MCPServer) callTool(params json.RawMessage) (toolResult, error) {
	var call struct {
		Name      string                     `json:"name"`
		Arguments map[string]json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &call); err != nil {
		return toolResult{}, fmt.Errorf("invalid tools/call params")
	}
	if call.Arguments == nil {
		call.Arguments = map[string]json.RawMessage{}
	}
	text, err := s.runTool(call.Name, call.Arguments)
	if err != nil {
		return textToolResult(err.Error(), true), nil
	}
	return textToolResult(text, false), nil
}

func (s *MCPServer) runTool(name string, args map[string]json.RawMessage) (string, error) {
	ctx := context.Background()
	switch name {
	case "inbox":
		team, err := optionalString(args, "team")
		if err != nil {
			return "", err
		}
		since, err := optionalString(args, "since")
		if err != nil {
			return "", err
		}
		all, err := optionalBool(args, "all")
		if err != nil {
			return "", err
		}
		api, err := s.linear()
		if err != nil {
			return "", err
		}
		result, err := api.ListInbox(ctx, InboxOptions{Team: team, Since: since, All: all})
		if err != nil {
			return "", err
		}
		return renderString(func(w io.Writer) error { return RenderInbox(w, result) })
	case "ack_comment":
		commentID, err := requiredString(args, "comment_id")
		if err != nil {
			return "", err
		}
		api, err := s.linear()
		if err != nil {
			return "", err
		}
		result, err := api.AckComment(ctx, commentID)
		if err != nil {
			return "", err
		}
		return renderString(func(w io.Writer) error { return RenderAck(w, result) })
	case "create_comment":
		issue, err := requiredString(args, "issue")
		if err != nil {
			return "", err
		}
		actor, err := requiredString(args, "actor")
		if err != nil {
			return "", err
		}
		body, err := requiredString(args, "body")
		if err != nil {
			return "", err
		}
		api, err := s.linear()
		if err != nil {
			return "", err
		}
		created, err := api.CreateComment(ctx, issue, actor, body)
		if err != nil {
			return "", err
		}
		return renderString(func(w io.Writer) error { return RenderCreatedComment(w, created) })
	case "create_issue":
		team, err := requiredString(args, "team")
		if err != nil {
			return "", err
		}
		title, err := requiredString(args, "title")
		if err != nil {
			return "", err
		}
		actor, err := requiredString(args, "actor")
		if err != nil {
			return "", err
		}
		description, err := optionalString(args, "description")
		if err != nil {
			return "", err
		}
		labels, err := optionalStringList(args, "labels")
		if err != nil {
			return "", err
		}
		api, err := s.linear()
		if err != nil {
			return "", err
		}
		created, err := api.CreateIssue(ctx, team, title, actor, description, labels)
		if err != nil {
			return "", err
		}
		return renderString(func(w io.Writer) error { return RenderCreatedIssue(w, created) })
	case "get_issue":
		issueID, err := requiredString(args, "issue")
		if err != nil {
			return "", err
		}
		api, err := s.linear()
		if err != nil {
			return "", err
		}
		issue, err := api.GetIssue(ctx, issueID)
		if err != nil {
			return "", err
		}
		return renderString(func(w io.Writer) error { return RenderIssue(w, issue) })
	case "update_issue":
		issueID, err := requiredString(args, "issue")
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
		priority, err := optionalStringPointer(args, "priority")
		if err != nil {
			return "", err
		}
		project, err := optionalStringPointer(args, "project")
		if err != nil {
			return "", err
		}
		milestone, err := optionalStringPointer(args, "milestone")
		if err != nil {
			return "", err
		}
		title, err := optionalStringPointer(args, "title")
		if err != nil {
			return "", err
		}
		api, err := s.linear()
		if err != nil {
			return "", err
		}
		updated, err := api.UpdateIssue(ctx, issueID, actor, IssueUpdateOptions{
			Description: description,
			Priority:    priority,
			Project:     project,
			Milestone:   milestone,
			Title:       title,
		})
		if err != nil {
			return "", err
		}
		return renderString(func(w io.Writer) error { return RenderUpdatedIssue(w, updated) })
	case "get_project":
		project, err := requiredString(args, "project")
		if err != nil {
			return "", err
		}
		api, err := s.linear()
		if err != nil {
			return "", err
		}
		result, err := api.GetProject(ctx, project)
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
		summary, err := optionalStringPointer(args, "summary")
		if err != nil {
			return "", err
		}
		description, err := optionalStringPointer(args, "description")
		if err != nil {
			return "", err
		}
		status, err := optionalStringPointer(args, "status")
		if err != nil {
			return "", err
		}
		priority, err := optionalStringPointer(args, "priority")
		if err != nil {
			return "", err
		}
		api, err := s.linear()
		if err != nil {
			return "", err
		}
		result, err := api.UpdateProject(ctx, project, actor, ProjectUpdateOptions{Summary: summary, Description: description, Status: status, Priority: priority})
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
		api, err := s.linear()
		if err != nil {
			return "", err
		}
		result, err := api.EnsureProjectMilestone(ctx, project, actor, ProjectMilestoneOptions{Name: name, Description: description})
		if err != nil {
			return "", err
		}
		return renderString(func(w io.Writer) error { return RenderEnsuredProjectMilestone(w, result) })
	case "list_issues":
		team, err := requiredString(args, "team")
		if err != nil {
			return "", err
		}
		state, err := optionalString(args, "state")
		if err != nil {
			return "", err
		}
		api, err := s.linear()
		if err != nil {
			return "", err
		}
		result, err := api.ListIssues(ctx, team, state)
		if err != nil {
			return "", err
		}
		return renderString(func(w io.Writer) error { return RenderIssues(w, result) })
	default:
		return "", fmt.Errorf("unknown tool %q", name)
	}
}

func (s *MCPServer) linear() (*LinearAPI, error) {
	if s.api != nil {
		return s.api, nil
	}
	api, err := NewLinearAPI(s.stderr, 0)
	if err != nil {
		return nil, err
	}
	s.api = api
	return api, nil
}

func (s *MCPServer) writeResult(id *json.RawMessage, result any) error {
	if result == nil {
		result = map[string]any{}
	}
	response := rpcResponse{JSONRPC: "2.0", ID: responseID(id), Result: result}
	return json.NewEncoder(s.stdout).Encode(response)
}

func (s *MCPServer) writeError(id *json.RawMessage, code int, message string) error {
	response := rpcResponse{
		JSONRPC: "2.0",
		ID:      responseID(id),
		Error:   &rpcError{Code: code, Message: message},
	}
	return json.NewEncoder(s.stdout).Encode(response)
}

func responseID(id *json.RawMessage) json.RawMessage {
	if id == nil {
		return json.RawMessage("null")
	}
	return *id
}

func textToolResult(text string, isError bool) toolResult {
	return toolResult{
		Content: []toolContent{{Type: "text", Text: text}},
		IsError: isError,
	}
}
