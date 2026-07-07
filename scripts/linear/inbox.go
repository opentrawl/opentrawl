package main

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

type InboxOptions struct {
	Team  string
	Since string
	All   bool
	Now   time.Time
}

type InboxWindow struct {
	All    bool
	Label  string
	Cutoff time.Time
}

type InboxResult struct {
	Comments []Comment
	Window   InboxWindow
	Team     string
}

type AckResult struct {
	CommentID       string
	IssueIdentifier string
	AlreadyAcked    bool
}

const defaultInboxSince = 14 * 24 * time.Hour

func (api *LinearAPI) ViewerID(ctx context.Context) (string, error) {
	var out struct {
		Viewer Person `json:"viewer"`
	}
	if err := api.graph.Do(ctx, viewerIDQuery, nil, &out); err != nil {
		return "", err
	}
	if strings.TrimSpace(out.Viewer.ID) == "" {
		return "", fmt.Errorf("linear did not return the app user id")
	}
	return out.Viewer.ID, nil
}

func (api *LinearAPI) ListInbox(ctx context.Context, opts InboxOptions) (InboxResult, error) {
	window, err := parseInboxWindow(opts)
	if err != nil {
		return InboxResult{}, err
	}
	viewerID, err := api.ViewerID(ctx)
	if err != nil {
		return InboxResult{}, err
	}
	team := strings.ToUpper(strings.TrimSpace(opts.Team))
	filter := inboxCommentFilter(team, window)
	var comments []Comment
	after := ""
	for {
		var out struct {
			Comments struct {
				Nodes    []Comment `json:"nodes"`
				PageInfo PageInfo  `json:"pageInfo"`
			} `json:"comments"`
		}
		variables := map[string]any{"filter": filter, "after": nil}
		if after != "" {
			variables["after"] = after
		}
		if err := api.graph.Do(ctx, inboxCommentsQuery, variables, &out); err != nil {
			return InboxResult{}, err
		}
		filtered, err := filterInboxComments(out.Comments.Nodes, viewerID, window)
		if err != nil {
			return InboxResult{}, err
		}
		comments = append(comments, filtered...)
		if !out.Comments.PageInfo.HasNextPage {
			break
		}
		after = strings.TrimSpace(out.Comments.PageInfo.EndCursor)
		if after == "" {
			return InboxResult{}, fmt.Errorf("linear did not return a cursor for the next comments page")
		}
	}
	sortInboxComments(comments)
	return InboxResult{Comments: comments, Window: window, Team: team}, nil
}

func (api *LinearAPI) AckComment(ctx context.Context, id string) (AckResult, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return AckResult{}, fmt.Errorf("comment id is required")
	}
	viewerID, err := api.ViewerID(ctx)
	if err != nil {
		return AckResult{}, err
	}
	var lookup struct {
		Comment *Comment `json:"comment"`
	}
	if err := api.graph.Do(ctx, ackLookupQuery, map[string]any{"id": id}, &lookup); err != nil {
		return AckResult{}, err
	}
	if lookup.Comment == nil {
		return AckResult{}, fmt.Errorf("comment %s was not found", id)
	}
	issue := ""
	if lookup.Comment.Issue != nil {
		issue = lookup.Comment.Issue.Identifier
	}
	for _, reaction := range lookup.Comment.Reactions {
		if reaction.User != nil && reaction.User.ID == viewerID {
			return AckResult{CommentID: id, IssueIdentifier: issue, AlreadyAcked: true}, nil
		}
	}
	var out struct {
		ReactionCreate struct {
			Success  bool     `json:"success"`
			Reaction Reaction `json:"reaction"`
		} `json:"reactionCreate"`
	}
	input := map[string]any{"commentId": id, "emoji": "eyes"}
	if err := api.graph.Do(ctx, ackCommentMutation, map[string]any{"input": input}, &out); err != nil {
		return AckResult{}, err
	}
	if !out.ReactionCreate.Success {
		return AckResult{}, fmt.Errorf("linear did not ack the comment")
	}
	return AckResult{CommentID: id, IssueIdentifier: issue}, nil
}

func parseInboxWindow(opts InboxOptions) (InboxWindow, error) {
	since := strings.TrimSpace(opts.Since)
	if opts.All && since != "" {
		return InboxWindow{}, fmt.Errorf("--since and --all cannot be used together")
	}
	if opts.All {
		return InboxWindow{All: true, Label: "across all time"}, nil
	}
	duration := defaultInboxSince
	label := "14d"
	if since != "" {
		parsed, err := parseDurationWithDays(since)
		if err != nil {
			return InboxWindow{}, fmt.Errorf("--since must be a duration like 14d or 36h")
		}
		duration = parsed
		label = since
	}
	if duration <= 0 {
		return InboxWindow{}, fmt.Errorf("--since must be positive")
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	return InboxWindow{Label: "in the last " + label, Cutoff: now.Add(-duration)}, nil
}

func parseDurationWithDays(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if days, ok := strings.CutSuffix(raw, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil {
			return 0, err
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return time.ParseDuration(raw)
}

func inboxCommentFilter(team string, window InboxWindow) map[string]any {
	filter := map[string]any{}
	if !window.All {
		filter["createdAt"] = map[string]any{"gte": window.Cutoff.Format(time.RFC3339)}
	}
	if strings.TrimSpace(team) != "" {
		filter["issue"] = map[string]any{
			"team": map[string]any{
				"key": map[string]any{"eq": team},
			},
		}
	}
	if len(filter) == 0 {
		return nil
	}
	return filter
}

func filterInboxComments(comments []Comment, viewerID string, window InboxWindow) ([]Comment, error) {
	var out []Comment
	for _, comment := range comments {
		if !window.All {
			created, err := parseCommentCreatedAt(comment)
			if err != nil {
				return nil, err
			}
			if created.Before(window.Cutoff) {
				continue
			}
		}
		if !inboxCommentEligible(comment, viewerID) {
			continue
		}
		out = append(out, comment)
	}
	return out, nil
}

func inboxCommentEligible(comment Comment, viewerID string) bool {
	if comment.User == nil || comment.BotActor != nil || comment.ExternalUser != nil {
		return false
	}
	if comment.Issue == nil || strings.TrimSpace(comment.Issue.Identifier) == "" {
		return false
	}
	for _, reaction := range comment.Reactions {
		if reaction.User != nil && reaction.User.ID == viewerID {
			return false
		}
	}
	return true
}

func sortInboxComments(comments []Comment) {
	sort.SliceStable(comments, func(i, j int) bool {
		left, leftErr := time.Parse(time.RFC3339Nano, comments[i].CreatedAt)
		right, rightErr := time.Parse(time.RFC3339Nano, comments[j].CreatedAt)
		if leftErr == nil && rightErr == nil {
			return left.Before(right)
		}
		return comments[i].CreatedAt < comments[j].CreatedAt
	})
}

func parseCommentCreatedAt(comment Comment) (time.Time, error) {
	created, err := time.Parse(time.RFC3339Nano, comment.CreatedAt)
	if err != nil {
		return time.Time{}, fmt.Errorf("linear returned an unreadable comment createdAt %q", comment.CreatedAt)
	}
	return created, nil
}
