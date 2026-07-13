package main

import (
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"

	crender "github.com/opentrawl/opentrawl/trawlkit/render"
)

func RenderIssue(w io.Writer, issue Issue) error {
	if err := crender.WriteCard(w, crender.Card{
		Title: issue.Identifier,
		Fields: []crender.CardField{
			{Label: "title", Value: issue.Title},
			{Label: "state", Value: issue.State.Name},
			{Label: "priority", Value: issue.PriorityLabel},
			{Label: "project", Value: projectName(issue.Project)},
			{Label: "milestone", Value: milestoneName(issue.Milestone)},
			{Label: "assignee", Value: personName(issue.Assignee, "Unassigned")},
			{Label: "labels", Value: labelList(issue.Labels.Nodes)},
			{Label: "blocks", Value: relationTargets(issue, RelationBlocks)},
			{Label: "blocked by", Value: relationTargets(issue, RelationBlockedBy)},
			{Label: "url", Value: issue.URL},
		},
	}); err != nil {
		return err
	}
	if _, err := fmt.Fprint(w, "\nDescription\n\n"); err != nil {
		return err
	}
	if err := writeIssueDescription(w, issueDescription(issue.Description)); err != nil {
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
	return nil
}

func RenderProject(w io.Writer, project Project) error {
	if _, err := fmt.Fprintf(w, "%s\nStatus: %s\nPriority: %s\nHealth: %s\nLead: %s\nIssues: %d open, %d total\n\n", project.Name, project.Status.Name, projectPriority(project), projectHealth(project.Health), personName(project.Lead, "Unassigned"), projectOpenIssues(project), len(project.Issues.Nodes)); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "Summary"); err != nil {
		return err
	}
	if err := writeProjectText(w, project.Description); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "\nMilestones"); err != nil {
		return err
	}
	if len(project.Milestones.Nodes) == 0 {
		if _, err := fmt.Fprintln(w, "None"); err != nil {
			return err
		}
	} else {
		for _, milestone := range project.Milestones.Nodes {
			if _, err := fmt.Fprintf(w, "- %s\n", milestone.Name); err != nil {
				return err
			}
		}
	}
	if _, err := fmt.Fprintln(w, "\nDescription"); err != nil {
		return err
	}
	return writeProjectText(w, project.Content)
}

func RenderEnsuredProjectMilestone(w io.Writer, result EnsuredProjectMilestone) error {
	verb := "Ensured"
	if result.Created {
		verb = "Created"
	} else if result.Changed {
		verb = "Updated"
	}
	return crender.WriteCard(w, crender.Card{
		Title: verb + " milestone " + result.Milestone.Name,
		Fields: []crender.CardField{
			{Label: "project", Value: result.Project.Name},
			{Label: "actor", Value: result.Actor},
		},
	})
}

func RenderIssues(w io.Writer, result ListIssuesResult) error {
	if len(result.Issues) == 0 {
		_, err := fmt.Fprintln(w, "No issues found.")
		return err
	}
	rows := make([][]string, 0, len(result.Issues))
	for _, issue := range result.Issues {
		rows = append(rows, []string{
			issue.Identifier,
			issue.State.Name,
			issue.PriorityLabel,
			projectName(issue.Project),
			milestoneName(issue.Milestone),
			labelList(issue.Labels.Nodes),
			relationTargets(issue, RelationBlocks),
			relationTargets(issue, RelationBlockedBy),
			issue.Title,
		})
	}
	if err := crender.WriteTable(w, []crender.TableColumn{
		{Header: "issue"},
		{Header: "state"},
		{Header: "priority"},
		{Header: "project", Wrap: true},
		{Header: "milestone", Wrap: true},
		{Header: "labels", Wrap: true},
		{Header: "blocks", Wrap: true},
		{Header: "blocked by", Wrap: true},
		{Header: "title", Wrap: true},
	}, rows); err != nil {
		return err
	}
	return nil
}

func relationTargets(issue Issue, direction RelationDirection) string {
	var targets []string
	for _, relation := range issue.Relations.Nodes {
		if strings.EqualFold(relation.Type, "blocks") {
			if direction == RelationBlocks && relation.Issue.ID == issue.ID {
				targets = append(targets, relation.RelatedIssue.Identifier)
			}
			if direction == RelationBlockedBy && relation.RelatedIssue.ID == issue.ID {
				targets = append(targets, relation.Issue.Identifier)
			}
		}
		if strings.EqualFold(strings.ReplaceAll(relation.Type, "_", "-"), "blocked-by") {
			if direction == RelationBlockedBy && relation.Issue.ID == issue.ID {
				targets = append(targets, relation.RelatedIssue.Identifier)
			}
			if direction == RelationBlocks && relation.RelatedIssue.ID == issue.ID {
				targets = append(targets, relation.Issue.Identifier)
			}
		}
	}
	for _, relation := range issue.InverseRelations.Nodes {
		if strings.EqualFold(relation.Type, "blocks") && direction == RelationBlockedBy && relation.RelatedIssue.ID == issue.ID {
			targets = append(targets, relation.Issue.Identifier)
		}
	}
	if len(targets) == 0 {
		return "None"
	}
	return strings.Join(targets, ", ")
}

func RenderInbox(w io.Writer, result InboxResult) error {
	if _, err := fmt.Fprintf(w, "inbox: %d unacked human comments %s\n", len(result.Comments), result.Window.Label); err != nil {
		return err
	}
	if len(result.Comments) == 0 {
		_, err := fmt.Fprintln(w, "Nothing unacked.")
		return err
	}
	for _, comment := range result.Comments {
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
		title := comment.Issue.Identifier
		if strings.TrimSpace(comment.Issue.Title) != "" {
			title += " · " + comment.Issue.Title
		}
		if err := crender.WriteCard(w, crender.Card{
			Title: title,
			Fields: []crender.CardField{
				{Label: "created", Value: commentTime(comment.CreatedAt)},
				{Label: "author", Value: personName(comment.User, "Unknown")},
				{Label: "comment id", Value: comment.ID},
				{Label: "body", Value: strings.TrimRight(comment.Body, "\r\n")},
			},
		}); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(w, "\nAck a comment: linear ack <comment-id>"); err != nil {
		return err
	}
	if !result.Window.All {
		if _, err := fmt.Fprintln(w, "See older comments: linear inbox --all"); err != nil {
			return err
		}
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
	fields := []crender.CardField{{Label: "actor", Value: updated.Actor}}
	for _, change := range updated.Changes {
		fields = append(fields, crender.CardField{Label: change.Field, Value: change.Value})
	}
	fields = append(fields, crender.CardField{Label: "url", Value: updated.Issue.URL})
	return crender.WriteCard(w, crender.Card{
		Title:  "Updated " + updated.Issue.Identifier,
		Fields: fields,
	})
}

func RenderAck(w io.Writer, result AckResult) error {
	fields := []crender.CardField{
		{Label: "comment id", Value: result.CommentID},
	}
	if strings.TrimSpace(result.IssueIdentifier) != "" {
		fields = append(fields, crender.CardField{Label: "issue", Value: result.IssueIdentifier})
	}
	status := "acked"
	if result.AlreadyAcked {
		status = "already acked"
	}
	fields = append(fields, crender.CardField{Label: "status", Value: status})
	return crender.WriteCard(w, crender.Card{
		Title:  "Comment acked",
		Fields: fields,
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

func writeIssueDescription(w io.Writer, body string) error {
	_, err := fmt.Fprint(w, body)
	return err
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

func issueDescription(description string) string {
	if description == "" {
		return "None"
	}
	return description
}

func projectName(project *Project) string {
	if project == nil || strings.TrimSpace(project.Name) == "" {
		return "None"
	}
	return project.Name
}

func milestoneName(milestone *ProjectMilestone) string {
	if milestone == nil || strings.TrimSpace(milestone.Name) == "" {
		return "None"
	}
	return milestone.Name
}

func projectPriority(project Project) string {
	if strings.TrimSpace(project.PriorityLabel) != "" {
		return project.PriorityLabel
	}
	switch project.Priority {
	case 1:
		return "Urgent"
	case 2:
		return "High"
	case 3:
		return "Medium"
	case 4:
		return "Low"
	default:
		return "No priority"
	}
}

func projectHealth(health string) string {
	health = strings.TrimSpace(health)
	if health == "" {
		return "Not set"
	}
	var words []string
	for _, rune := range health {
		if rune == '_' || rune == '-' {
			words = append(words, " ")
			continue
		}
		if rune >= 'A' && rune <= 'Z' && len(words) > 0 && words[len(words)-1] != " " {
			words = append(words, " ")
		}
		words = append(words, string(rune))
	}
	return titleWords(strings.ToLower(strings.Join(words, "")))
}

func titleWords(value string) string {
	words := strings.Fields(value)
	for index, word := range words {
		runes := []rune(word)
		runes[0] = unicode.ToUpper(runes[0])
		words[index] = string(runes)
	}
	return strings.Join(words, " ")
}

func projectOpenIssues(project Project) int {
	open := 0
	for _, issue := range project.Issues.Nodes {
		switch strings.ToLower(issue.State.Type) {
		case "completed", "canceled", "duplicate":
		default:
			open++
		}
	}
	return open
}

func writeProjectText(w io.Writer, text string) error {
	if text == "" {
		_, err := fmt.Fprintln(w, "None")
		return err
	}
	if _, err := fmt.Fprint(w, text); err != nil {
		return err
	}
	if !strings.HasSuffix(text, "\n") {
		_, err := fmt.Fprintln(w)
		return err
	}
	return nil
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
