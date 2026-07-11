package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

type RelationDirection string

const (
	RelationBlocks    RelationDirection = "blocks"
	RelationBlockedBy RelationDirection = "blocked-by"
)

func parseRelationDirection(value string) (RelationDirection, error) {
	switch strings.ToLower(strings.TrimSpace(strings.ReplaceAll(value, "_", "-"))) {
	case string(RelationBlocks):
		return RelationBlocks, nil
	case string(RelationBlockedBy):
		return RelationBlockedBy, nil
	default:
		return "", fmt.Errorf("relation direction must be blocks or blocked-by")
	}
}

func (api *LinearAPI) ChangeIssueLabels(ctx context.Context, rawIssue, actor, operation string, names []string) (UpdatedIssue, error) {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return UpdatedIssue{}, fmt.Errorf("--as is required for write commands")
	}
	if operation != "add" && operation != "remove" {
		return UpdatedIssue{}, fmt.Errorf("label operation must be add or remove")
	}
	identifier, err := ParseIssueIdentifier(rawIssue)
	if err != nil {
		return UpdatedIssue{}, err
	}
	issue, err := api.GetIssue(ctx, rawIssue)
	if err != nil {
		return UpdatedIssue{}, err
	}
	requested, err := api.ResolveLabelRecords(ctx, identifier.TeamKey, names)
	if err != nil {
		return UpdatedIssue{}, err
	}
	if len(requested) == 0 {
		return UpdatedIssue{}, fmt.Errorf("--label is required")
	}
	requestedIDs := labelIDSet(requested)
	currentIDs := labelIDSet(issue.Labels.Nodes)
	nextIDs := copySet(currentIDs)
	changed := false
	for id := range requestedIDs {
		if operation == "add" {
			if !nextIDs[id] {
				nextIDs[id] = true
				changed = true
			}
		} else if nextIDs[id] {
			delete(nextIDs, id)
			changed = true
		}
	}
	if changed {
		if api.logger != nil {
			api.logger.LogDiagnostic("info", fmt.Sprintf("issue labels %s: %s by %s", operation, issue.Identifier, actor))
		}
		var out struct {
			IssueUpdate struct {
				Success bool `json:"success"`
			} `json:"issueUpdate"`
		}
		if err := api.graph.Do(ctx, updateIssueMutation, map[string]any{
			"id":    issue.ID,
			"input": map[string]any{"labelIds": sortedIDs(nextIDs)},
		}, &out); err != nil {
			return UpdatedIssue{}, err
		}
		if !out.IssueUpdate.Success {
			return UpdatedIssue{}, fmt.Errorf("linear did not update issue labels")
		}
	}
	readBack, err := api.GetIssue(ctx, rawIssue)
	if err != nil {
		return UpdatedIssue{}, err
	}
	if !labelSetEqual(nextIDs, labelIDSet(readBack.Labels.Nodes)) {
		return UpdatedIssue{}, fmt.Errorf("linear label read-back did not match the requested result")
	}
	return UpdatedIssue{
		Issue:   readBack,
		Actor:   actor,
		Changes: []IssueChange{{Field: "labels", Value: operation + " " + strings.Join(labelNames(requested), ", ")}},
	}, nil
}

func (api *LinearAPI) ChangeIssueRelation(ctx context.Context, rawIssue, rawOther, actor, operation string, direction RelationDirection) (UpdatedIssue, error) {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return UpdatedIssue{}, fmt.Errorf("--as is required for write commands")
	}
	if operation != "add" && operation != "remove" {
		return UpdatedIssue{}, fmt.Errorf("relation operation must be add or remove")
	}
	if direction != RelationBlocks && direction != RelationBlockedBy {
		return UpdatedIssue{}, fmt.Errorf("relation direction must be blocks or blocked-by")
	}
	issueIdentifier, err := ParseIssueIdentifier(rawIssue)
	if err != nil {
		return UpdatedIssue{}, err
	}
	otherIdentifier, err := ParseIssueIdentifier(rawOther)
	if err != nil {
		return UpdatedIssue{}, err
	}
	if issueIdentifier.String() == otherIdentifier.String() {
		return UpdatedIssue{}, fmt.Errorf("an issue cannot have a relation to itself")
	}
	issue, err := api.GetIssue(ctx, rawIssue)
	if err != nil {
		return UpdatedIssue{}, err
	}
	other, err := api.GetIssue(ctx, rawOther)
	if err != nil {
		return UpdatedIssue{}, err
	}
	source, related := issue, other
	if direction == RelationBlockedBy {
		source, related = other, issue
	}
	relation, exists := directedBlocksRelation(source.Relations.Nodes, source.ID, related.ID)
	if operation == "add" && !exists {
		if api.logger != nil {
			api.logger.LogDiagnostic("info", fmt.Sprintf("issue relation add: %s blocks %s by %s", source.Identifier, related.Identifier, actor))
		}
		var out struct {
			IssueRelationCreate struct {
				Success bool `json:"success"`
			} `json:"issueRelationCreate"`
		}
		if err := api.graph.Do(ctx, createIssueRelationMutation, map[string]any{
			"input": map[string]any{"issueId": source.ID, "relatedIssueId": related.ID, "type": "blocks"},
		}, &out); err != nil {
			return UpdatedIssue{}, err
		}
		if !out.IssueRelationCreate.Success {
			return UpdatedIssue{}, fmt.Errorf("linear did not create the issue relation")
		}
	}
	if operation == "remove" && exists {
		if api.logger != nil {
			api.logger.LogDiagnostic("info", fmt.Sprintf("issue relation remove: %s blocks %s by %s", source.Identifier, related.Identifier, actor))
		}
		var out struct {
			IssueRelationDelete struct {
				Success bool `json:"success"`
			} `json:"issueRelationDelete"`
		}
		if err := api.graph.Do(ctx, deleteIssueRelationMutation, map[string]any{"id": relation.ID}, &out); err != nil {
			return UpdatedIssue{}, err
		}
		if !out.IssueRelationDelete.Success {
			return UpdatedIssue{}, fmt.Errorf("linear did not delete the issue relation")
		}
	}
	issueReadBack, err := api.GetIssue(ctx, issue.Identifier)
	if err != nil {
		return UpdatedIssue{}, err
	}
	otherReadBack, err := api.GetIssue(ctx, other.Identifier)
	if err != nil {
		return UpdatedIssue{}, err
	}
	expected := operation == "add"
	if hasDirectedBlocks(sourceReadBack(issueReadBack, otherReadBack, source.ID).Relations.Nodes, source.ID, related.ID) != expected || hasDirectedBlocks(targetReadBack(issueReadBack, otherReadBack, related.ID).InverseRelations.Nodes, source.ID, related.ID) != expected {
		return UpdatedIssue{}, fmt.Errorf("linear relation read-back did not match the requested result on both issues")
	}
	return UpdatedIssue{
		Issue:   issueReadBack,
		Actor:   actor,
		Changes: []IssueChange{{Field: "relation", Value: string(direction) + " " + other.Identifier}},
	}, nil
}

func sourceReadBack(first, second Issue, sourceID string) Issue {
	if first.ID == sourceID {
		return first
	}
	return second
}

func targetReadBack(first, second Issue, targetID string) Issue {
	if first.ID == targetID {
		return first
	}
	return second
}

func directedBlocksRelation(relations []IssueRelation, sourceID, relatedID string) (IssueRelation, bool) {
	for _, relation := range relations {
		if strings.EqualFold(relation.Type, "blocks") && relation.Issue.ID == sourceID && relation.RelatedIssue.ID == relatedID {
			return relation, true
		}
	}
	return IssueRelation{}, false
}

func hasDirectedBlocks(relations []IssueRelation, sourceID, relatedID string) bool {
	_, exists := directedBlocksRelation(relations, sourceID, relatedID)
	return exists
}

func labelIDSet(labels []IssueLabel) map[string]bool {
	ids := make(map[string]bool, len(labels))
	for _, label := range labels {
		if label.ID != "" {
			ids[label.ID] = true
		}
	}
	return ids
}

func copySet(source map[string]bool) map[string]bool {
	copy := make(map[string]bool, len(source))
	for value := range source {
		copy[value] = true
	}
	return copy
}

func sortedIDs(values map[string]bool) []string {
	ids := make([]string, 0, len(values))
	for value := range values {
		ids = append(ids, value)
	}
	sort.Strings(ids)
	return ids
}

func labelSetEqual(left, right map[string]bool) bool {
	if len(left) != len(right) {
		return false
	}
	for value := range left {
		if !right[value] {
			return false
		}
	}
	return true
}

func labelNames(labels []IssueLabel) []string {
	names := make([]string, 0, len(labels))
	for _, label := range labels {
		names = append(names, label.Name)
	}
	sort.Strings(names)
	return names
}
