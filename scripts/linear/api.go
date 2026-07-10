package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type graphDoer interface {
	Do(ctx context.Context, query string, variables map[string]any, out any) error
}

type LinearAPI struct {
	graph  graphDoer
	logger *requestLogger
}

type Team struct {
	ID   string `json:"id"`
	Key  string `json:"key"`
	Name string `json:"name"`
}

type Project struct {
	ID            string        `json:"id"`
	Name          string        `json:"name"`
	SlugID        string        `json:"slugId"`
	Description   string        `json:"description"`
	Content       string        `json:"content"`
	Status        ProjectStatus `json:"status"`
	Priority      int           `json:"priority"`
	PriorityLabel string        `json:"priorityLabel"`
	Health        string        `json:"health"`
	Lead          *Person       `json:"lead"`
	Milestones    struct {
		Nodes    []ProjectMilestone `json:"nodes"`
		PageInfo PageInfo           `json:"pageInfo"`
	} `json:"projectMilestones"`
	Issues struct {
		Nodes    []Issue  `json:"nodes"`
		PageInfo PageInfo `json:"pageInfo"`
	} `json:"issues"`
}

type ProjectStatus struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type ProjectMilestone struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Project     *Project `json:"project"`
}

type Person struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Name        string `json:"name"`
}

type BotActor struct {
	Name            string `json:"name"`
	UserDisplayName string `json:"userDisplayName"`
	Type            string `json:"type"`
	SubType         string `json:"subType"`
}

type IssueState struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

type IssueLabel struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	IsGroup bool   `json:"isGroup"`
	Team    *Team  `json:"team"`
}

type Issue struct {
	ID            string            `json:"id"`
	Identifier    string            `json:"identifier"`
	Title         string            `json:"title"`
	Description   string            `json:"description"`
	URL           string            `json:"url"`
	PriorityLabel string            `json:"priorityLabel"`
	State         IssueState        `json:"state"`
	Project       *Project          `json:"project"`
	Milestone     *ProjectMilestone `json:"projectMilestone"`
	Assignee      *Person           `json:"assignee"`
	Labels        struct {
		Nodes    []IssueLabel `json:"nodes"`
		PageInfo PageInfo     `json:"pageInfo"`
	} `json:"labels"`
	Comments struct {
		Nodes    []Comment `json:"nodes"`
		PageInfo PageInfo  `json:"pageInfo"`
	} `json:"comments"`
}

type Comment struct {
	ID           string     `json:"id"`
	URL          string     `json:"url"`
	CreatedAt    string     `json:"createdAt"`
	Body         string     `json:"body"`
	User         *Person    `json:"user"`
	BotActor     *BotActor  `json:"botActor"`
	ExternalUser *Person    `json:"externalUser"`
	Reactions    []Reaction `json:"reactions"`
	Issue        *Issue     `json:"issue"`
}

type Reaction struct {
	ID      string   `json:"id"`
	Emoji   string   `json:"emoji"`
	User    *Person  `json:"user"`
	Comment *Comment `json:"comment"`
}

type PageInfo struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}

type ListIssuesResult struct {
	Issues      []Issue
	HasNextPage bool
}

type CreatedIssue struct {
	Issue Issue
	Actor string
}

type CreatedComment struct {
	Issue   string
	Comment Comment
	Actor   string
}

type UpdatedIssue struct {
	Issue   Issue
	Actor   string
	Changes []IssueChange
}

func NewLinearAPI(stderr io.Writer, verbosity int) (*LinearAPI, error) {
	logger, err := newRequestLogger(stderr, verbosity)
	if err != nil {
		return nil, err
	}
	httpClient := &http.Client{Timeout: 30 * time.Second}
	tokens, err := NewTokenStore(httpClient, logger)
	if err != nil {
		_ = logger.Close()
		return nil, err
	}
	return &LinearAPI{
		graph:  NewGraphQLClient(httpClient, tokens, logger),
		logger: logger,
	}, nil
}

func (api *LinearAPI) Close() error {
	if api == nil || api.logger == nil {
		return nil
	}
	return api.logger.Close()
}

func (api *LinearAPI) ResolveIssue(ctx context.Context, raw string) (Issue, error) {
	id, err := ParseIssueIdentifier(raw)
	if err != nil {
		return Issue{}, err
	}
	var out struct {
		Issues struct {
			Nodes []Issue `json:"nodes"`
		} `json:"issues"`
	}
	err = api.graph.Do(ctx, resolveIssueIDQuery, map[string]any{
		"team":   id.TeamKey,
		"number": float64(id.Number),
	}, &out)
	if err != nil {
		return Issue{}, err
	}
	if len(out.Issues.Nodes) == 0 {
		return Issue{}, fmt.Errorf("issue %s was not found", id.String())
	}
	if len(out.Issues.Nodes) > 1 {
		return Issue{}, fmt.Errorf("issue %s matched more than one Linear issue", id.String())
	}
	return out.Issues.Nodes[0], nil
}

func (api *LinearAPI) GetIssue(ctx context.Context, raw string) (Issue, error) {
	id, err := ParseIssueIdentifier(raw)
	if err != nil {
		return Issue{}, err
	}
	var issue Issue
	commentsAfter := ""
	for page := 0; ; page++ {
		var out struct {
			Issues struct {
				Nodes []Issue `json:"nodes"`
			} `json:"issues"`
		}
		variables := map[string]any{
			"team":   id.TeamKey,
			"number": float64(id.Number),
		}
		if commentsAfter != "" {
			variables["commentsAfter"] = commentsAfter
		}
		if err := api.graph.Do(ctx, issueByIdentifierQuery, variables, &out); err != nil {
			return Issue{}, err
		}
		if len(out.Issues.Nodes) == 0 {
			return Issue{}, fmt.Errorf("issue %s was not found", id.String())
		}
		if len(out.Issues.Nodes) > 1 {
			return Issue{}, fmt.Errorf("issue %s matched more than one Linear issue", id.String())
		}
		pageIssue := out.Issues.Nodes[0]
		if page == 0 {
			issue = pageIssue
		} else {
			issue.Comments.Nodes = append(issue.Comments.Nodes, pageIssue.Comments.Nodes...)
			issue.Comments.PageInfo = pageIssue.Comments.PageInfo
		}
		if !pageIssue.Comments.PageInfo.HasNextPage {
			return issue, nil
		}
		commentsAfter = pageIssue.Comments.PageInfo.EndCursor
		if commentsAfter == "" {
			return Issue{}, fmt.Errorf("linear did not return a cursor for the next issue comment page")
		}
	}
}

func (api *LinearAPI) ListIssues(ctx context.Context, team, state string) (ListIssuesResult, error) {
	team = strings.ToUpper(strings.TrimSpace(team))
	if team == "" {
		return ListIssuesResult{}, fmt.Errorf("--team is required")
	}
	filter := openIssueFilter(team)
	if strings.TrimSpace(state) != "" {
		resolved, err := api.ResolveState(ctx, team, state)
		if err != nil {
			return ListIssuesResult{}, err
		}
		filter = stateIssueFilter(team, resolved.Name)
	}
	var out struct {
		Issues struct {
			Nodes    []Issue  `json:"nodes"`
			PageInfo PageInfo `json:"pageInfo"`
		} `json:"issues"`
	}
	if err := api.graph.Do(ctx, listIssuesQuery, map[string]any{"filter": filter}, &out); err != nil {
		return ListIssuesResult{}, err
	}
	return ListIssuesResult{Issues: out.Issues.Nodes, HasNextPage: out.Issues.PageInfo.HasNextPage}, nil
}

func (api *LinearAPI) CreateComment(ctx context.Context, rawIssue, actor, body string) (CreatedComment, error) {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return CreatedComment{}, fmt.Errorf("--as is required for write commands")
	}
	body = strings.TrimRight(body, "\r\n")
	if strings.TrimSpace(body) == "" {
		return CreatedComment{}, fmt.Errorf("comment body is required")
	}
	issue, err := api.ResolveIssue(ctx, rawIssue)
	if err != nil {
		return CreatedComment{}, err
	}
	var out struct {
		CommentCreate struct {
			Success bool    `json:"success"`
			Comment Comment `json:"comment"`
		} `json:"commentCreate"`
	}
	input := map[string]any{"issueId": issue.ID, "body": body, "createAsUser": actor}
	if err := api.graph.Do(ctx, createCommentMutation, map[string]any{"input": input}, &out); err != nil {
		return CreatedComment{}, err
	}
	if !out.CommentCreate.Success {
		return CreatedComment{}, fmt.Errorf("linear did not create the comment")
	}
	return CreatedComment{Issue: issue.Identifier, Comment: out.CommentCreate.Comment, Actor: actor}, nil
}

func (api *LinearAPI) CreateIssue(ctx context.Context, teamKey, title, actor, description string, labels []string) (CreatedIssue, error) {
	team, err := api.ResolveTeam(ctx, teamKey)
	if err != nil {
		return CreatedIssue{}, err
	}
	title = strings.TrimSpace(title)
	actor = strings.TrimSpace(actor)
	if title == "" {
		return CreatedIssue{}, fmt.Errorf("--title is required")
	}
	if actor == "" {
		return CreatedIssue{}, fmt.Errorf("--as is required for write commands")
	}
	labelIDs, err := api.ResolveLabels(ctx, team.Key, labels)
	if err != nil {
		return CreatedIssue{}, err
	}
	input := map[string]any{"teamId": team.ID, "title": title, "createAsUser": actor}
	if strings.TrimSpace(description) != "" {
		input["description"] = strings.TrimRight(description, "\r\n")
	}
	if len(labelIDs) > 0 {
		input["labelIds"] = labelIDs
	}
	var out struct {
		IssueCreate struct {
			Success bool  `json:"success"`
			Issue   Issue `json:"issue"`
		} `json:"issueCreate"`
	}
	if err := api.graph.Do(ctx, createIssueMutation, map[string]any{"input": input}, &out); err != nil {
		return CreatedIssue{}, err
	}
	if !out.IssueCreate.Success {
		return CreatedIssue{}, fmt.Errorf("linear did not create the issue")
	}
	return CreatedIssue{Issue: out.IssueCreate.Issue, Actor: actor}, nil
}

func (api *LinearAPI) UpdateIssueState(ctx context.Context, rawIssue, state, actor string) (UpdatedIssue, error) {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return UpdatedIssue{}, fmt.Errorf("--as is required for write commands")
	}
	id, err := ParseIssueIdentifier(rawIssue)
	if err != nil {
		return UpdatedIssue{}, err
	}
	resolved, err := api.ResolveState(ctx, id.TeamKey, state)
	if err != nil {
		return UpdatedIssue{}, err
	}
	issue, err := api.ResolveIssue(ctx, rawIssue)
	if err != nil {
		return UpdatedIssue{}, err
	}
	// issueUpdate has no createAsUser attribution, so the actor is recorded here.
	api.logger.LogDiagnostic("info", fmt.Sprintf("issueUpdate requested: %s -> %q by %s", issue.Identifier, resolved.Name, actor))
	var out struct {
		IssueUpdate struct {
			Success bool  `json:"success"`
			Issue   Issue `json:"issue"`
		} `json:"issueUpdate"`
	}
	input := map[string]any{"stateId": resolved.ID}
	if err := api.graph.Do(ctx, updateIssueMutation, map[string]any{"id": issue.ID, "input": input}, &out); err != nil {
		return UpdatedIssue{}, err
	}
	if !out.IssueUpdate.Success {
		return UpdatedIssue{}, fmt.Errorf("linear did not update the issue")
	}
	return UpdatedIssue{
		Issue: out.IssueUpdate.Issue,
		Actor: actor,
		Changes: []IssueChange{
			{Field: "state", Value: out.IssueUpdate.Issue.State.Name},
		},
	}, nil
}

func openIssueFilter(team string) map[string]any {
	filter := issueTeamFilter(team)
	filter["state"] = map[string]any{
		"type": map[string]any{
			"nin": []string{"completed", "canceled", "duplicate"},
		},
	}
	return filter
}

func stateIssueFilter(team, state string) map[string]any {
	filter := issueTeamFilter(team)
	filter["state"] = map[string]any{
		"name": map[string]any{"eq": state},
	}
	return filter
}

func issueTeamFilter(team string) map[string]any {
	return map[string]any{
		"team": map[string]any{
			"key": map[string]any{"eq": team},
		},
	}
}
