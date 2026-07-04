package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestOpenJSONRoundTripsSearchRef(t *testing.T) {
	db := seedSearchArchive(t, 25)
	search := runSearchJSON(t, db, "search", "launch", "--json")
	if len(search.Results) == 0 {
		t.Fatal("search returned no refs")
	}
	payload := runOpenJSON(t, db, search.Results[0].Ref)
	if payload.Ref != search.Results[0].Ref || payload.Message.Ref != search.Results[0].Ref || !payload.Message.IsTarget {
		t.Fatalf("open target = %#v, want %s", payload, search.Results[0].Ref)
	}
	if payload.Chat.Name != "example chat" || payload.Message.Chat.Name != "example chat" {
		t.Fatalf("chat names = root %q message %q", payload.Chat.Name, payload.Message.Chat.Name)
	}
	if payload.Message.Sender.DisplayName != "Example Sender" || payload.Message.Text == "" {
		t.Fatalf("message = %#v", payload.Message)
	}
	if _, err := time.Parse(time.RFC3339, payload.Message.Time); err != nil {
		t.Fatalf("message time = %q err=%v", payload.Message.Time, err)
	}
	if payload.TargetPosition < 0 || payload.TargetPosition >= len(payload.Context) || !payload.Context[payload.TargetPosition].IsTarget {
		t.Fatalf("target position = %d context = %#v", payload.TargetPosition, payload.Context)
	}
	for _, message := range payload.Context {
		if message.Chat.Name != "example chat" || message.Sender.DisplayName == "" {
			t.Fatalf("context message = %#v", message)
		}
		if _, err := time.Parse(time.RFC3339, message.Time); err != nil {
			t.Fatalf("context time = %q err=%v", message.Time, err)
		}
	}
}

func TestOpenAcceptsShortRefAlias(t *testing.T) {
	db := seedSearchArchive(t, 3)
	search := runSearchJSON(t, db, "search", "launch", "--json")
	if len(search.Results) == 0 {
		t.Fatal("search returned no refs")
	}
	alias := search.Results[0].ShortRef
	payload := runOpenJSON(t, db, alias)
	if payload.Ref == "" || !strings.HasPrefix(payload.Ref, "telecrawl:msg/") {
		t.Fatalf("open by alias payload = %#v", payload)
	}
}

func TestOpenShortRefErrorsAreContractCodes(t *testing.T) {
	db := seedSearchArchive(t, 1)
	stdout, _, err := runCLI(t, "--db", db, "open", "22222", "--json")
	if err == nil {
		t.Fatalf("unknown alias succeeded: stdout=%s", stdout)
	}
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("error json = %s err=%v", stdout, err)
	}
	if payload.Error.Code != "unknown_short_ref" {
		t.Fatalf("error code = %q, want unknown_short_ref", payload.Error.Code)
	}
}

func TestOpenRejectsForeignRefWithContractError(t *testing.T) {
	db := seedSearchArchive(t, 1)
	stdout, stderr, err := runCLI(t, "--db", db, "open", "othercrawl:msg/1", "--json")
	if err == nil {
		t.Fatalf("open foreign ref succeeded: stdout=%s stderr=%s", stdout, stderr)
	}
	if code := ExitCode(err); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Remedy  string `json:"remedy"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("error json = %s err=%v", stdout, err)
	}
	if payload.Error.Code != "invalid_ref" || payload.Error.Message == "" || payload.Error.Remedy == "" {
		t.Fatalf("error payload = %#v", payload)
	}
}

func TestOpenContextWindowIsBounded(t *testing.T) {
	db := seedSearchArchive(t, 35)
	payload := runOpenJSON(t, db, "telecrawl:msg/18")
	if len(payload.Context) != 21 {
		t.Fatalf("context messages = %d, want 21", len(payload.Context))
	}
	if payload.ContextWindow.Before != 10 || payload.ContextWindow.After != 10 {
		t.Fatalf("context window = %#v", payload.ContextWindow)
	}
	if !payload.ContextWindow.BeforeTruncated || !payload.ContextWindow.AfterTruncated {
		t.Fatalf("context truncation = %#v", payload.ContextWindow)
	}
	if payload.Context[0].Ref != "telecrawl:msg/8" || payload.Context[20].Ref != "telecrawl:msg/28" {
		t.Fatalf("context refs = first %s last %s", payload.Context[0].Ref, payload.Context[20].Ref)
	}
}

func TestOpenHumanContextLineIsPlain(t *testing.T) {
	db := seedSearchArchive(t, 35)
	stdout, stderr, err := runCLI(t, "--db", db, "open", "telecrawl:msg/35")
	if err != nil {
		t.Fatalf("open text: %v stderr=%s", err, stderr)
	}
	if !strings.Contains(stdout, "Showing 10 earlier messages and none after.\nMore: telecrawl messages --chat 100") {
		t.Fatalf("open context line is not plain:\n%s", stdout)
	}
	if strings.Contains(stdout, "context:") || strings.Contains(stdout, "bounded") || strings.Contains(stdout, "omitted") {
		t.Fatalf("open context kept old wording:\n%s", stdout)
	}
}

func TestContractTimestampsUseLocalOffset(t *testing.T) {
	loc := useFixedLocalZone(t)

	statusTime := time.Now().Add(-time.Hour).UTC()
	statusDB := seedArchive(t, 1, statusTime)
	wantStatusTime := statusTime.In(loc).Format(time.RFC3339)
	status := runStatusJSON(t, statusDB)
	if status.Freshness.LastSync != wantStatusTime {
		t.Fatalf("status last_sync = %q, want %q", status.Freshness.LastSync, wantStatusTime)
	}
	statusText, stderr, err := runCLI(t, "--db", statusDB, "status")
	if err != nil {
		t.Fatalf("status text: %v stderr=%s", err, stderr)
	}
	assertContainsHumanTime(t, statusText, statusTime)

	db := seedSearchArchive(t, 1)
	messageTime := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	wantMessageTime := messageTime.In(loc).Format(time.RFC3339)

	search := runSearchJSON(t, db, "search", "launch", "--json")
	if len(search.Results) != 1 || search.Results[0].Time != wantMessageTime {
		t.Fatalf("search result time = %#v, want %q", search.Results, wantMessageTime)
	}
	searchText, stderr, err := runCLI(t, "--db", db, "search", "launch")
	if err != nil {
		t.Fatalf("search text: %v stderr=%s", err, stderr)
	}
	assertContainsHumanTime(t, searchText, messageTime)

	open := runOpenJSON(t, db, "telecrawl:msg/1")
	if open.Message.Time != wantMessageTime || len(open.Context) != 1 || open.Context[0].Time != wantMessageTime {
		t.Fatalf("open times = message %q context %#v, want %q", open.Message.Time, open.Context, wantMessageTime)
	}
	openText, stderr, err := runCLI(t, "--db", db, "open", "telecrawl:msg/1")
	if err != nil {
		t.Fatalf("open text: %v stderr=%s", err, stderr)
	}
	assertContainsHumanTime(t, openText, messageTime)

	whoText, stderr, err := runCLI(t, "--db", db, "who", "Example Sender")
	if err != nil {
		t.Fatalf("who text: %v stderr=%s", err, stderr)
	}
	assertContainsHumanTime(t, whoText, messageTime)
}

func TestStatusSinceYearUsesLocalOffset(t *testing.T) {
	loc := useFixedLocalZone(t)
	messageTime := time.Date(2020, 12, 31, 23, 30, 0, 0, time.UTC)
	db := seedArchiveWithMessageTime(t, 1, time.Now(), messageTime)
	status := runStatusJSON(t, db)
	if got := statusCountValue(t, status, "since"); got != int64(messageTime.In(loc).Year()) {
		t.Fatalf("since count = %d, want local year %d", got, messageTime.In(loc).Year())
	}
}

func TestPerVerbHelpExitsZero(t *testing.T) {
	tests := [][]string{
		{"metadata", "--help"},
		{"doctor", "--help"},
		{"import", "--help"},
		{"sync", "--help"},
		{"status", "--help"},
		{"folders", "--help"},
		{"contacts", "--help"},
		{"contacts", "export", "--help"},
		{"chats", "--help"},
		{"topics", "--help"},
		{"messages", "--help"},
		{"search", "--help"},
		{"who", "--help"},
		{"open", "--help"},
		{"backup", "--help"},
		{"backup", "init", "--help"},
		{"backup", "push", "--help"},
		{"backup", "pull", "--help"},
		{"backup", "status", "--help"},
		{"backup", "snapshots", "--help"},
		{"version", "--help"},
	}
	for _, args := range tests {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			stdout, stderr, err := runCLI(t, args...)
			if err != nil {
				t.Fatalf("%v: err=%v stderr=%s stdout=%s", args, err, stderr, stdout)
			}
			if stderr != "" {
				t.Fatalf("%v: stderr=%q", args, stderr)
			}
			if !strings.Contains(stdout, "usage: telecrawl") {
				t.Fatalf("%v: help missing usage:\n%s", args, stdout)
			}
			if !strings.Contains(stdout, diagnosticsLine) {
				t.Fatalf("%v: help missing diagnostics line:\n%s", args, stdout)
			}
			if !strings.HasSuffix(strings.TrimSpace(stdout), diagnosticsLine) {
				t.Fatalf("%v: help does not end with diagnostics line:\n%s", args, stdout)
			}
		})
	}
}
