package main

import (
	"context"
	"fmt"
	"reflect"
	"testing"
)

func TestGetIssuePaginatesLabelsRelationsAndComments(t *testing.T) {
	graph := &pagedIssueGraph{}
	api := &LinearAPI{graph: graph}
	issue, err := api.GetIssue(context.Background(), "TRAWL-1")
	if err != nil {
		t.Fatalf("GetIssue returned error: %v", err)
	}
	if got, want := labelNames(issue.Labels.Nodes), []string{"first", "second"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("labels = %#v, want %#v", got, want)
	}
	if got := len(issue.Relations.Nodes); got != 2 {
		t.Fatalf("relations = %d, want 2", got)
	}
	if got := len(issue.InverseRelations.Nodes); got != 2 {
		t.Fatalf("inverse relations = %d, want 2", got)
	}
	if got := len(issue.Comments.Nodes); got != 2 {
		t.Fatalf("comments = %d, want 2", got)
	}
	if len(graph.variables) != 2 || graph.variables[1]["labelsAfter"] != "labels-1" || graph.variables[1]["relationsAfter"] != "relations-1" || graph.variables[1]["inverseRelationsAfter"] != "inverse-relations-1" || graph.variables[1]["commentsAfter"] != "comments-1" {
		t.Fatalf("pagination variables = %#v", graph.variables)
	}
}

func TestListIssuesFiltersProjectInGraphQLAndPaginates(t *testing.T) {
	graph := &projectListGraph{}
	api := &LinearAPI{graph: graph}
	result, err := api.ListIssues(context.Background(), "TRAWL", "Todo", "Mac app")
	if err != nil {
		t.Fatalf("ListIssues returned error: %v", err)
	}
	if got, want := []string{result.Issues[0].Identifier, result.Issues[1].Identifier}, []string{"TRAWL-1", "TRAWL-2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("issues = %#v, want %#v", got, want)
	}
	if graph.filter["project"].(map[string]any)["id"].(map[string]any)["eq"] != "project-mac" {
		t.Fatalf("project filter = %#v", graph.filter)
	}
	if graph.after != "issues-1" {
		t.Fatalf("after = %q, want issues-1", graph.after)
	}
}

func TestChangeIssueLabelsPreservesExistingLabelsAndReadsBack(t *testing.T) {
	graph := &labelChangeGraph{}
	api := &LinearAPI{graph: graph}
	updated, err := api.ChangeIssueLabels(context.Background(), "TRAWL-1", "test actor", "add", []string{"new", "new"})
	if err != nil {
		t.Fatalf("ChangeIssueLabels returned error: %v", err)
	}
	if got, want := labelNames(updated.Issue.Labels.Nodes), []string{"existing", "new"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("labels = %#v, want %#v", got, want)
	}
	if got, want := graph.labelIDs, []string{"label-existing", "label-new"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("labelIds = %#v, want %#v", got, want)
	}
	if graph.reads != 2 {
		t.Fatalf("issue reads = %d, want 2", graph.reads)
	}
}

func TestChangeIssueRelationUsesBlocksDirectionAndVerifiesBothSides(t *testing.T) {
	graph := &relationChangeGraph{presentAfterWrite: true}
	api := &LinearAPI{graph: graph}
	updated, err := api.ChangeIssueRelation(context.Background(), "TRAWL-1", "TRAWL-2", "test actor", "add", RelationBlockedBy)
	if err != nil {
		t.Fatalf("ChangeIssueRelation returned error: %v", err)
	}
	if updated.Issue.Identifier != "TRAWL-1" {
		t.Fatalf("updated issue = %q, want TRAWL-1", updated.Issue.Identifier)
	}
	if got, want := graph.relationInput, map[string]any{"issueId": "issue-2", "relatedIssueId": "issue-1", "type": "blocks"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("relation input = %#v, want %#v", got, want)
	}
	if got, want := graph.readNumbers, []float64{1, 2, 1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("issue reads = %#v, want %#v", got, want)
	}
}

func TestChangeIssueRelationRefusesReadBackMismatch(t *testing.T) {
	graph := &relationChangeGraph{presentAfterWrite: false}
	api := &LinearAPI{graph: graph}
	_, err := api.ChangeIssueRelation(context.Background(), "TRAWL-1", "TRAWL-2", "test actor", "add", RelationBlocks)
	if err == nil || err.Error() != "linear relation read-back did not match the requested result on both issues" {
		t.Fatalf("error = %v, want read-back mismatch", err)
	}
}

func TestChangeIssueRelationRefusesSelfBeforeGraphQL(t *testing.T) {
	graph := &unexpectedGraph{}
	api := &LinearAPI{graph: graph}
	_, err := api.ChangeIssueRelation(context.Background(), "TRAWL-1", "trawl-1", "test actor", "add", RelationBlocks)
	if err == nil || err.Error() != "an issue cannot have a relation to itself" {
		t.Fatalf("error = %v, want self-relation refusal", err)
	}
}

func TestChangeIssueRelationRejectsUnknownIssueBeforeMutation(t *testing.T) {
	graph := &unknownIssueGraph{}
	api := &LinearAPI{graph: graph}
	_, err := api.ChangeIssueRelation(context.Background(), "TRAWL-404", "TRAWL-2", "test actor", "add", RelationBlocks)
	if err == nil || err.Error() != "issue TRAWL-404 was not found" {
		t.Fatalf("error = %v, want unknown issue refusal", err)
	}
	if graph.calls != 1 {
		t.Fatalf("GraphQL calls = %d, want one issue read and no mutation", graph.calls)
	}
}

func TestResolveLabelRecordsRefusesUnknownAndAmbiguous(t *testing.T) {
	api := &LinearAPI{graph: labelResolutionGraph{labels: nil}}
	_, err := api.ResolveLabelRecords(context.Background(), "TRAWL", []string{"missing"})
	if err == nil || err.Error() != `label "missing" was not found for team TRAWL` {
		t.Fatalf("unknown label error = %v", err)
	}
	api.graph = labelResolutionGraph{labels: []IssueLabel{
		{ID: "one", Name: "duplicate", Team: &Team{Key: "TRAWL"}},
		{ID: "two", Name: "duplicate", Team: &Team{Key: "TRAWL"}},
	}}
	_, err = api.ResolveLabelRecords(context.Background(), "TRAWL", []string{"duplicate"})
	if err == nil || err.Error() != `label "duplicate" is ambiguous: team TRAWL, team TRAWL` {
		t.Fatalf("ambiguous label error = %v", err)
	}
}

type unexpectedGraph struct{}

func (unexpectedGraph) Do(_ context.Context, _ string, _ map[string]any, _ any) error {
	return fmt.Errorf("GraphQL must not be called")
}

type unknownIssueGraph struct{ calls int }

func (g *unknownIssueGraph) Do(_ context.Context, query string, _ map[string]any, out any) error {
	if query != issueByIdentifierQuery {
		return fmt.Errorf("unexpected query")
	}
	g.calls++
	return setGraphOut(out, map[string]any{"issues": map[string]any{"nodes": []Issue{}}})
}

type labelResolutionGraph struct{ labels []IssueLabel }

func (g labelResolutionGraph) Do(_ context.Context, query string, _ map[string]any, out any) error {
	if query != resolveLabelsQuery {
		return fmt.Errorf("unexpected query")
	}
	return setGraphOut(out, map[string]any{"issueLabels": map[string]any{"nodes": g.labels}})
}

type pagedIssueGraph struct{ variables []map[string]any }

func (g *pagedIssueGraph) Do(_ context.Context, query string, variables map[string]any, out any) error {
	if query != issueByIdentifierQuery {
		return fmt.Errorf("unexpected query")
	}
	g.variables = append(g.variables, variables)
	page := Issue{ID: "issue-1", Identifier: "TRAWL-1"}
	if len(g.variables) == 1 {
		page.Labels.Nodes = []IssueLabel{{ID: "label-1", Name: "first"}}
		page.Labels.PageInfo = PageInfo{HasNextPage: true, EndCursor: "labels-1"}
		page.Relations.Nodes = []IssueRelation{{ID: "relation-1"}}
		page.Relations.PageInfo = PageInfo{HasNextPage: true, EndCursor: "relations-1"}
		page.InverseRelations.Nodes = []IssueRelation{{ID: "inverse-relation-1"}}
		page.InverseRelations.PageInfo = PageInfo{HasNextPage: true, EndCursor: "inverse-relations-1"}
		page.Comments.Nodes = []Comment{{ID: "comment-1"}}
		page.Comments.PageInfo = PageInfo{HasNextPage: true, EndCursor: "comments-1"}
	} else {
		page.Labels.Nodes = []IssueLabel{{ID: "label-2", Name: "second"}}
		page.Relations.Nodes = []IssueRelation{{ID: "relation-2"}}
		page.InverseRelations.Nodes = []IssueRelation{{ID: "inverse-relation-2"}}
		page.Comments.Nodes = []Comment{{ID: "comment-2"}}
	}
	return setGraphOut(out, map[string]any{"issues": map[string]any{"nodes": []Issue{page}}})
}

type projectListGraph struct {
	filter map[string]any
	after  string
}

func (g *projectListGraph) Do(_ context.Context, query string, variables map[string]any, out any) error {
	switch query {
	case resolveProjectQuery:
		return setGraphOut(out, map[string]any{"projects": map[string]any{"nodes": []Project{{ID: "project-mac", Name: "Mac app", SlugID: "mac-app"}}}})
	case teamStatesQuery:
		return setGraphOut(out, map[string]any{"workflowStates": map[string]any{"nodes": []IssueState{{ID: "todo", Name: "Todo"}}}})
	case listIssuesQuery:
		g.filter = variables["filter"].(map[string]any)
		if after, ok := variables["after"].(string); ok {
			g.after = after
			return setGraphOut(out, map[string]any{"issues": map[string]any{"nodes": []Issue{{ID: "issue-2", Identifier: "TRAWL-2"}}, "pageInfo": PageInfo{}}})
		}
		return setGraphOut(out, map[string]any{"issues": map[string]any{"nodes": []Issue{{ID: "issue-1", Identifier: "TRAWL-1"}}, "pageInfo": PageInfo{HasNextPage: true, EndCursor: "issues-1"}}})
	default:
		return fmt.Errorf("unexpected query")
	}
}

type labelChangeGraph struct {
	reads    int
	labelIDs []string
}

func (g *labelChangeGraph) Do(_ context.Context, query string, variables map[string]any, out any) error {
	switch query {
	case issueByIdentifierQuery:
		g.reads++
		labels := []IssueLabel{{ID: "label-existing", Name: "existing"}}
		if g.reads == 2 {
			labels = append(labels, IssueLabel{ID: "label-new", Name: "new"})
		}
		issue := Issue{ID: "issue-1", Identifier: "TRAWL-1"}
		issue.Labels.Nodes = labels
		return setGraphOut(out, map[string]any{"issues": map[string]any{"nodes": []Issue{issue}}})
	case resolveLabelsQuery:
		return setGraphOut(out, map[string]any{"issueLabels": map[string]any{"nodes": []IssueLabel{{ID: "label-new", Name: "new", Team: &Team{Key: "TRAWL"}}}}})
	case updateIssueMutation:
		g.labelIDs = variables["input"].(map[string]any)["labelIds"].([]string)
		return setGraphOut(out, map[string]any{"issueUpdate": map[string]any{"success": true}})
	default:
		return fmt.Errorf("unexpected query")
	}
}

type relationChangeGraph struct {
	presentAfterWrite bool
	relationInput     map[string]any
	readNumbers       []float64
	written           bool
}

func (g *relationChangeGraph) Do(_ context.Context, query string, variables map[string]any, out any) error {
	switch query {
	case issueByIdentifierQuery:
		number := variables["number"].(float64)
		g.readNumbers = append(g.readNumbers, number)
		issue := relationTestIssue(number, g.written && g.presentAfterWrite)
		if g.written && g.presentAfterWrite {
			relation := IssueRelation{
				ID:           "relation-1",
				Type:         "blocks",
				Issue:        IssueReference{ID: g.relationInput["issueId"].(string), Identifier: "TRAWL-2"},
				RelatedIssue: IssueReference{ID: g.relationInput["relatedIssueId"].(string), Identifier: "TRAWL-1"},
			}
			issue.Relations.Nodes = nil
			if issue.ID == relation.Issue.ID {
				issue.Relations.Nodes = []IssueRelation{relation}
			} else {
				issue.InverseRelations.Nodes = []IssueRelation{relation}
			}
		}
		return setGraphOut(out, map[string]any{"issues": map[string]any{"nodes": []Issue{issue}}})
	case createIssueRelationMutation:
		g.relationInput = variables["input"].(map[string]any)
		g.written = true
		return setGraphOut(out, map[string]any{"issueRelationCreate": map[string]any{"success": true}})
	default:
		return fmt.Errorf("unexpected query")
	}
}

func relationTestIssue(number float64, relation bool) Issue {
	issue := Issue{ID: fmt.Sprintf("issue-%d", int(number)), Identifier: fmt.Sprintf("TRAWL-%d", int(number))}
	if relation {
		issue.Relations.Nodes = []IssueRelation{{
			ID: "relation-1", Type: "blocks",
			Issue:        IssueReference{ID: "issue-1", Identifier: "TRAWL-1"},
			RelatedIssue: IssueReference{ID: "issue-2", Identifier: "TRAWL-2"},
		}}
	}
	return issue
}
