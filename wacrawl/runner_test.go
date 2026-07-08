package wacrawl

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openclaw/crawlkit"
	"github.com/openclaw/crawlkit/control"
	"github.com/openclaw/crawlkit/output"
	wastore "github.com/openclaw/wacrawl/internal/store"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == crawlkit.HiddenWireSubcommand {
		os.Exit(crawlkit.Run(os.Args[1:], []crawlkit.Crawler{New()}))
	}
	os.Exit(m.Run())
}

func TestRunStatusOmitsSinceForEmptyArchive(t *testing.T) {
	ctx := context.Background()
	stateRoot := stateRootForRun(t)
	archivePath := filepath.Join(stateRoot, "whatsapp", "whatsapp.db")
	st, err := wastore.Open(ctx, archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := captureRun(t, []string{"status", "--json"}, New())
	if code != 0 {
		t.Fatalf("status code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var status control.Status
	if err := json.Unmarshal([]byte(stdout), &status); err != nil {
		t.Fatalf("status JSON: %v\n%s", err, stdout)
	}
	if status.State != "empty" {
		t.Fatalf("status state = %q, want empty\n%s", status.State, stdout)
	}
	if countIDPresent(status.Counts, "since") {
		t.Fatalf("empty archive status should omit since count: %#v", status.Counts)
	}
}

func TestRunSearchWhoAmbiguousRefusesWithCandidates(t *testing.T) {
	stateRoot := stateRootForRun(t)
	createAmbiguousWhoArchive(t, stateRoot)

	code, stdout, stderr := captureRun(t, []string{"search", "needle", "--who", "CASEY", "--json"}, New())
	if code != 4 || stderr != "" {
		t.Fatalf("ambiguous JSON code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var envelope output.ErrorEnvelope
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("ambiguous JSON: %v\n%s", err, stdout)
	}
	candidates, ok := envelope.Error.Fields["candidates"].([]any)
	if envelope.Error.Code != "ambiguous_who" || !ok || len(candidates) != 2 {
		t.Fatalf("ambiguous error = %#v", envelope.Error)
	}
	if !strings.Contains(stdout, "Casey One") || !strings.Contains(stdout, "Casey Two") {
		t.Fatalf("ambiguous candidates missing from JSON:\n%s", stdout)
	}

	code, stdout, stderr = captureRun(t, []string{"search", "needle", "--who", "CASEY"}, New())
	if code != 4 || stdout != "" {
		t.Fatalf("ambiguous text code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	for _, want := range []string{
		"--who matched more than one person",
		"Casey One",
		"Casey Two",
		"Retry with one listed identifier: search needle --who casey-two@s.whatsapp.net",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("ambiguous text missing %q:\n%s", want, stderr)
		}
	}
}

func TestRunLIDOnlyHumanOutputUsesPrivacyPlaceholder(t *testing.T) {
	stateRoot := stateRootForRun(t)
	createLIDOnlyArchive(t, stateRoot)

	humanCommands := map[string][]string{
		"chats":      {"chats"},
		"messages":   {"messages", "--limit", "10", "--asc"},
		"search":     {"search", "synthetic", "--limit", "10"},
		"open group": {"open", "whatsapp:msg/group-lid"},
		"open dm":    {"open", "whatsapp:msg/dm-lid"},
		"who group":  {"who", "155500000000001@lid"},
	}
	for name, args := range humanCommands {
		code, stdout, stderr := captureRun(t, args, New())
		if code != 0 {
			t.Fatalf("%s code=%d stdout=%s stderr=%s", name, code, stdout, stderr)
		}
		if strings.Contains(stdout, "@lid") {
			t.Fatalf("%s human output leaked LID:\n%s", name, stdout)
		}
		if !containsHumanPrivacyPlaceholder(stdout) {
			t.Fatalf("%s human output missing privacy placeholder:\n%s", name, stdout)
		}
	}

	jsonCommands := map[string][]string{
		"chats":      {"--json", "chats"},
		"messages":   {"--json", "messages", "--limit", "10", "--asc"},
		"search":     {"--json", "search", "synthetic", "--limit", "10"},
		"open group": {"--json", "open", "whatsapp:msg/group-lid"},
		"open dm":    {"--json", "open", "whatsapp:msg/dm-lid"},
		"who group":  {"--json", "who", "155500000000001@lid"},
	}
	for name, args := range jsonCommands {
		code, stdout, stderr := captureRun(t, args, New())
		if code != 0 {
			t.Fatalf("%s JSON code=%d stdout=%s stderr=%s", name, code, stdout, stderr)
		}
		if !strings.Contains(stdout, "@lid") {
			t.Fatalf("%s JSON output should keep LID:\n%s", name, stdout)
		}
		if strings.Contains(stdout, "unknown participant") {
			t.Fatalf("%s JSON output should not use privacy placeholder:\n%s", name, stdout)
		}
	}
}

func containsHumanPrivacyPlaceholder(output string) bool {
	// TRAWL-1 list tables can truncate the final "t"; the visible stem still
	// proves the LID was masked instead of printed raw.
	return strings.Contains(output, "unknown participan")
}

func TestMessageWhoPrefersPhoneJIDOverLIDSuffixedName(t *testing.T) {
	message := wastore.Message{
		SenderJID:  "casey@s.whatsapp.net",
		SenderName: "Casey@lid",
	}

	got := messageWho(message)
	if got != "casey@s.whatsapp.net" {
		t.Fatalf("messageWho = %q, want sender JID", got)
	}
	if strings.Contains(got, "unknown participant") {
		t.Fatalf("messageWho masked non-LID sender JID: %q", got)
	}
}

func countIDPresent(counts []control.Count, id string) bool {
	for _, count := range counts {
		if count.ID == id {
			return true
		}
	}
	return false
}

func createLIDOnlyArchive(t *testing.T, stateRoot string) {
	t.Helper()
	ctx := context.Background()
	st, err := wastore.Open(ctx, filepath.Join(stateRoot, "whatsapp", "whatsapp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	groupLID := "155500000000001@lid"
	directLID := "155500000000002@lid"
	now := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	chats := []wastore.Chat{
		{JID: "trail@g.us", Kind: "group", Name: "Trail crew", LastMessageAt: now, MessageCount: 1},
		{JID: directLID, Kind: "dm", LastMessageAt: now.Add(time.Minute), MessageCount: 1},
	}
	groups := []wastore.Group{{JID: "trail@g.us", Name: "Trail crew", OwnerJID: "owner@s.whatsapp.net", CreatedAt: now.Add(-time.Hour)}}
	participants := []wastore.GroupParticipant{{GroupJID: "trail@g.us", UserJID: groupLID, IsActive: true}}
	messages := []wastore.Message{
		{SourcePK: 1, ChatJID: "trail@g.us", ChatName: "Trail crew", MessageID: "group-lid", SenderJID: groupLID, SenderName: groupLID, Timestamp: now, Text: "synthetic group message", RawType: 0, MessageType: "text"},
		{SourcePK: 2, ChatJID: directLID, MessageID: "dm-lid", SenderJID: directLID, SenderName: directLID, Timestamp: now.Add(time.Minute), Text: "synthetic direct message", RawType: 0, MessageType: "text"},
	}
	if err := st.ReplaceAll(ctx, wastore.ImportStats{FinishedAt: now}, nil, chats, groups, participants, messages); err != nil {
		t.Fatal(err)
	}
}

func createAmbiguousWhoArchive(t *testing.T, stateRoot string) {
	t.Helper()
	ctx := context.Background()
	st, err := wastore.Open(ctx, filepath.Join(stateRoot, "whatsapp", "whatsapp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	now := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	contacts := []wastore.Contact{
		{JID: "casey-one@s.whatsapp.net", FullName: "Casey One"},
		{JID: "casey-two@s.whatsapp.net", FullName: "Casey Two"},
	}
	chats := []wastore.Chat{
		{JID: "casey-one@s.whatsapp.net", Kind: "dm", Name: "Casey One", LastMessageAt: now, MessageCount: 1},
		{JID: "casey-two@s.whatsapp.net", Kind: "dm", Name: "Casey Two", LastMessageAt: now, MessageCount: 1},
	}
	messages := []wastore.Message{
		{SourcePK: 1, ChatJID: "casey-one@s.whatsapp.net", ChatName: "Casey One", MessageID: "casey-one", SenderJID: "casey-one@s.whatsapp.net", SenderName: "Casey One", Timestamp: now, RawType: 0, MessageType: "text", Text: "needle one"},
		{SourcePK: 2, ChatJID: "casey-two@s.whatsapp.net", ChatName: "Casey Two", MessageID: "casey-two", SenderJID: "casey-two@s.whatsapp.net", SenderName: "Casey Two", Timestamp: now.Add(time.Minute), RawType: 0, MessageType: "text", Text: "needle two"},
	}
	if err := st.ReplaceAll(ctx, wastore.ImportStats{FinishedAt: now}, contacts, chats, nil, nil, messages); err != nil {
		t.Fatal(err)
	}
}
