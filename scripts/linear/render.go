package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	crender "github.com/openclaw/crawlkit/render"
)

func RenderIssue(w io.Writer, issue Issue) error {
	hints := []string{}
	if issue.Labels.PageInfo.HasNextPage {
		hints = append(hints, "Showing first 50 labels.")
	}
	if err := crender.WriteCard(w, crender.Card{
		Title: issue.Identifier,
		Fields: []crender.CardField{
			{Label: "title", Value: issue.Title},
			{Label: "state", Value: issue.State.Name},
			{Label: "assignee", Value: personName(issue.Assignee, "Unassigned")},
			{Label: "labels", Value: labelList(issue.Labels.Nodes)},
			{Label: "url", Value: issue.URL},
		},
		Hints: hints,
	}); err != nil {
		return err
	}
	if len(issue.Comments.Nodes) == 0 {
		_, err := fmt.Fprintln(w, "\nNo comments.")
		return err
	}
	if _, err := fmt.Fprint(w, "\nComments\n\n"); err != nil {
		return err
	}
	for i, comment := range issue.Comments.Nodes {
		if i > 0 {
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "%s · %s\n", commentAuthor(comment), commentTime(comment.CreatedAt)); err != nil {
			return err
		}
		if err := writeIndentedBody(w, comment.Body); err != nil {
			return err
		}
	}
	if issue.Comments.PageInfo.HasNextPage {
		_, err := fmt.Fprintln(w, "\nShowing first 100 comments.")
		return err
	}
	return nil
}

func RenderIssues(w io.Writer, result ListIssuesResult) error {
	if len(result.Issues) == 0 {
		_, err := fmt.Fprintln(w, "No issues found.")
		return err
	}
	rows := make([][]string, 0, len(result.Issues))
	for _, issue := range result.Issues {
		rows = append(rows, []string{issue.Identifier, issue.State.Name, issue.Title})
	}
	if err := crender.WriteTable(w, []crender.TableColumn{
		{Header: "issue"},
		{Header: "state"},
		{Header: "title", Wrap: true},
	}, rows); err != nil {
		return err
	}
	if result.HasNextPage {
		_, err := fmt.Fprintln(w, "\nShowing first 50 issues.")
		return err
	}
	return nil
}

func RenderCreatedIssue(w io.Writer, created CreatedIssue) error {
	return crender.WriteCard(w, crender.Card{
		Title: "Created " + created.Issue.Identifier,
		Fields: []crender.CardField{
			{Label: "actor", Value: created.Actor},
			{Label: "title", Value: created.Issue.Title},
			{Label: "url", Value: created.Issue.URL},
		},
	})
}

func RenderUpdatedIssue(w io.Writer, updated UpdatedIssue) error {
	return crender.WriteCard(w, crender.Card{
		Title: "Updated " + updated.Issue.Identifier,
		Fields: []crender.CardField{
			{Label: "actor", Value: updated.Actor},
			{Label: "state", Value: updated.Issue.State.Name},
			{Label: "url", Value: updated.Issue.URL},
		},
	})
}

func RenderCreatedComment(w io.Writer, created CreatedComment) error {
	return crender.WriteCard(w, crender.Card{
		Title: "Created comment on " + created.Issue,
		Fields: []crender.CardField{
			{Label: "actor", Value: created.Actor},
			{Label: "url", Value: created.Comment.URL},
		},
	})
}

func writeIndentedBody(w io.Writer, body string) error {
	body = strings.TrimRight(body, "\r\n")
	if body == "" {
		_, err := fmt.Fprintln(w, "  (empty comment)")
		return err
	}
	for _, line := range strings.Split(body, "\n") {
		if _, err := fmt.Fprintf(w, "  %s\n", strings.TrimRight(line, "\r")); err != nil {
			return err
		}
	}
	return nil
}

func personName(person *Person, empty string) string {
	if person == nil {
		return empty
	}
	if strings.TrimSpace(person.DisplayName) != "" {
		return person.DisplayName
	}
	if strings.TrimSpace(person.Name) != "" {
		return person.Name
	}
	return empty
}

func commentAuthor(comment Comment) string {
	if comment.BotActor != nil {
		user := strings.TrimSpace(comment.BotActor.UserDisplayName)
		app := strings.TrimSpace(comment.BotActor.Name)
		if user != "" && app != "" {
			return user + " via " + app
		}
		if user != "" {
			return user
		}
		if app != "" {
			return app
		}
	}
	if name := personName(comment.User, ""); name != "" {
		return name
	}
	if name := personName(comment.ExternalUser, ""); name != "" {
		return name + " (external)"
	}
	return "Unknown"
}

func labelList(labels []IssueLabel) string {
	if len(labels) == 0 {
		return "none"
	}
	names := make([]string, 0, len(labels))
	for _, label := range labels {
		if strings.TrimSpace(label.Name) != "" {
			names = append(names, label.Name)
		}
	}
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

func commentTime(raw string) string {
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return raw
	}
	return crender.ShortLocalTime(t)
}
