package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestFilterInboxCommentsKeepsOnlyUnackedHumanIssueComments(t *testing.T) {
	window := InboxWindow{All: true}
	viewerID := "app-user"
	comments := []Comment{
		{
			ID:        "human",
			CreatedAt: "2026-07-05T10:00:00Z",
			User:      &Person{ID: "josh", DisplayName: "Josh"},
			Issue:     &Issue{Identifier: "TRAWL-1"},
		},
		{
			ID:        "bot",
			CreatedAt: "2026-07-05T10:01:00Z",
			BotActor:  &BotActor{Type: "oauthClient"},
			Issue:     &Issue{Identifier: "TRAWL-1"},
		},
		{
			ID:           "external",
			CreatedAt:    "2026-07-05T10:02:00Z",
			User:         &Person{ID: "external-user"},
			ExternalUser: &Person{DisplayName: "External"},
			Issue:        &Issue{Identifier: "TRAWL-1"},
		},
		{
			ID:        "acked",
			CreatedAt: "2026-07-05T10:03:00Z",
			User:      &Person{ID: "josh", DisplayName: "Josh"},
			Issue:     &Issue{Identifier: "TRAWL-1"},
			Reactions: []Reaction{{Emoji: "eyes", User: &Person{ID: viewerID}}},
		},
	}
	got, err := filterInboxComments(comments, viewerID, window)
	if err != nil {
		t.Fatalf("filterInboxComments returned error: %v", err)
	}
	if len(got) != 1 || got[0].ID != "human" {
		t.Fatalf("kept comments = %#v, want only human", got)
	}
}

func TestParseInboxWindow(t *testing.T) {
	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	window, err := parseInboxWindow(InboxOptions{Now: now})
	if err != nil {
		t.Fatalf("default window returned error: %v", err)
	}
	if window.All {
		t.Fatal("default window All = true, want false")
	}
	if window.Label != "in the last 14d" {
		t.Fatalf("default label = %q, want in the last 14d", window.Label)
	}
	if want := now.Add(-14 * 24 * time.Hour); !window.Cutoff.Equal(want) {
		t.Fatalf("default cutoff = %s, want %s", window.Cutoff, want)
	}

	window, err = parseInboxWindow(InboxOptions{Since: "36h", Now: now})
	if err != nil {
		t.Fatalf("36h window returned error: %v", err)
	}
	if window.Label != "in the last 36h" {
		t.Fatalf("36h label = %q, want in the last 36h", window.Label)
	}
	if want := now.Add(-36 * time.Hour); !window.Cutoff.Equal(want) {
		t.Fatalf("36h cutoff = %s, want %s", window.Cutoff, want)
	}

	window, err = parseInboxWindow(InboxOptions{Since: "14d", Now: now})
	if err != nil {
		t.Fatalf("14d window returned error: %v", err)
	}
	if want := now.Add(-14 * 24 * time.Hour); !window.Cutoff.Equal(want) {
		t.Fatalf("14d cutoff = %s, want %s", window.Cutoff, want)
	}

	if _, err := parseInboxWindow(InboxOptions{Since: "14d", All: true, Now: now}); err == nil {
		t.Fatal("since plus all returned nil error")
	}
}

func TestInboxSinceAllIsUsageErrorBeforeAPI(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := execute([]string{"inbox", "--since", "14d", "--all"}, strings.NewReader(""), &stdout, &stderr)
	var usage usageError
	if !errors.As(err, &usage) {
		t.Fatalf("error = %#v, want usageError", err)
	}
	if exitCode(err) != 2 {
		t.Fatalf("exit code = %d, want 2", exitCode(err))
	}
	if !strings.Contains(err.Error(), "--since and --all cannot be used together") {
		t.Fatalf("error = %q, want since/all message", err.Error())
	}
}

func TestSortInboxCommentsOldestFirst(t *testing.T) {
	comments := []Comment{
		{ID: "new", CreatedAt: "2026-07-05T12:00:00Z"},
		{ID: "old", CreatedAt: "2026-07-05T10:00:00Z"},
		{ID: "middle", CreatedAt: "2026-07-05T11:00:00Z"},
	}
	sortInboxComments(comments)
	got := []string{comments[0].ID, comments[1].ID, comments[2].ID}
	want := []string{"old", "middle", "new"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %#v, want %#v", got, want)
	}
}

func TestListInboxPaginatesAcrossPages(t *testing.T) {
	graph := &fakeGraph{
		viewerID: "app-user",
		pages: []fakeCommentPage{
			{
				comments: []Comment{
					inboxTestComment("second", "2026-07-05T11:00:00Z"),
				},
				pageInfo: PageInfo{HasNextPage: true, EndCursor: "cursor-1"},
			},
			{
				comments: []Comment{
					inboxTestComment("first", "2026-07-05T10:00:00Z"),
				},
				pageInfo: PageInfo{HasNextPage: false},
			},
		},
	}
	api := &LinearAPI{graph: graph}
	result, err := api.ListInbox(context.Background(), InboxOptions{All: true})
	if err != nil {
		t.Fatalf("ListInbox returned error: %v", err)
	}
	if got := []string{result.Comments[0].ID, result.Comments[1].ID}; !reflect.DeepEqual(got, []string{"first", "second"}) {
		t.Fatalf("comments = %#v, want oldest-first across pages", got)
	}
	if len(graph.inboxCalls) != 2 {
		t.Fatalf("inbox call count = %d, want 2", len(graph.inboxCalls))
	}
	if graph.inboxCalls[0]["after"] != nil {
		t.Fatalf("first after = %#v, want nil", graph.inboxCalls[0]["after"])
	}
	if graph.inboxCalls[1]["after"] != "cursor-1" {
		t.Fatalf("second after = %#v, want cursor-1", graph.inboxCalls[1]["after"])
	}
}

func TestAckAlreadyReactedSkipsMutation(t *testing.T) {
	graph := &fakeGraph{
		viewerID: "app-user",
		ackLookup: &Comment{
			ID:        "comment-1",
			Issue:     &Issue{Identifier: "TRAWL-1"},
			Reactions: []Reaction{{User: &Person{ID: "app-user"}}},
		},
	}
	api := &LinearAPI{graph: graph}
	result, err := api.AckComment(context.Background(), "comment-1")
	if err != nil {
		t.Fatalf("AckComment returned error: %v", err)
	}
	if !result.AlreadyAcked {
		t.Fatal("AlreadyAcked = false, want true")
	}
	if result.IssueIdentifier != "TRAWL-1" {
		t.Fatalf("IssueIdentifier = %q, want TRAWL-1", result.IssueIdentifier)
	}
	if graph.ackCalls != 0 {
		t.Fatalf("ack mutation calls = %d, want 0", graph.ackCalls)
	}
}

func TestAckMissingCommentFails(t *testing.T) {
	graph := &fakeGraph{viewerID: "app-user"}
	api := &LinearAPI{graph: graph}
	if _, err := api.AckComment(context.Background(), "comment-9"); err == nil {
		t.Fatal("AckComment succeeded, want not-found error")
	}
}

type fakeGraph struct {
	viewerID   string
	pages      []fakeCommentPage
	inboxCalls []map[string]any
	ackLookup  *Comment
	ackCalls   int
}

type fakeCommentPage struct {
	comments []Comment
	pageInfo PageInfo
}

func (f *fakeGraph) Do(ctx context.Context, query string, variables map[string]any, out any) error {
	switch query {
	case viewerIDQuery:
		return setGraphOut(out, map[string]any{"viewer": map[string]any{"id": f.viewerID}})
	case inboxCommentsQuery:
		call := map[string]any{}
		for key, value := range variables {
			call[key] = value
		}
		f.inboxCalls = append(f.inboxCalls, call)
		index := len(f.inboxCalls) - 1
		if index >= len(f.pages) {
			return errors.New("unexpected inbox page")
		}
		page := f.pages[index]
		return setGraphOut(out, map[string]any{
			"comments": map[string]any{
				"nodes":    page.comments,
				"pageInfo": page.pageInfo,
			},
		})
	case ackLookupQuery:
		return setGraphOut(out, map[string]any{"comment": f.ackLookup})
	case ackCommentMutation:
		f.ackCalls++
		return setGraphOut(out, map[string]any{
			"reactionCreate": map[string]any{
				"success":  true,
				"reaction": Reaction{Comment: &Comment{ID: "comment-1", Issue: &Issue{Identifier: "TRAWL-1"}}},
			},
		})
	default:
		return errors.New("unexpected query")
	}
}

func setGraphOut(out any, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func inboxTestComment(id, createdAt string) Comment {
	return Comment{
		ID:        id,
		URL:       "https://linear.app/opentrawl/comment/" + id,
		CreatedAt: createdAt,
		Body:      "please handle this",
		User:      &Person{ID: "josh", DisplayName: "Josh"},
		Issue:     &Issue{Identifier: "TRAWL-1", Title: "Test issue"},
	}
}
