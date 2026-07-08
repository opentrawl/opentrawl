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

func countIDPresent(counts []control.Count, id string) bool {
	for _, count := range counts {
		if count.ID == id {
			return true
		}
	}
	return false
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
