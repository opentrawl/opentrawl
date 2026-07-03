package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/openclaw/imsgcrawl/internal/archive"
	"github.com/openclaw/imsgcrawl/internal/messages"
)

const (
	whoScaleParticipants = 5000
	whoScaleReturned     = 20
	whoScaleBudget       = time.Second
)

func TestWhoResolverScalesToManyDistinctParticipants(t *testing.T) {
	if testing.Short() {
		t.Skip("large resolver scale test is skipped in short mode")
	}
	ctx := context.Background()
	archivePath := createLargeWhoArchive(t, whoScaleParticipants)

	st, err := archive.OpenExisting(ctx, archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	start := time.Now()
	resolution, err := st.ResolveWho(ctx, "josh")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed > whoScaleBudget {
		t.Fatalf("ResolveWho over %d participants took %s, budget %s", whoScaleParticipants, elapsed, whoScaleBudget)
	}
	if resolution.TotalMatches != whoScaleParticipants || resolution.Returned != whoScaleReturned || !resolution.Truncated || len(resolution.Candidates) != whoScaleReturned {
		t.Fatalf("resolution = %#v", resolution)
	}
	if resolution.Candidates[0].Who != "Josh Candidate 05000" || resolution.Candidates[0].Messages != 1 {
		t.Fatalf("top candidate = %#v", resolution.Candidates[0])
	}

	var stdout, stderr bytes.Buffer
	start = time.Now()
	err = Run(ctx, []string{"--archive", archivePath, "--json", "search", "--who", "josh", "needle"}, &stdout, &stderr)
	elapsed = time.Since(start)
	if err == nil || ExitCode(err) != 4 {
		t.Fatalf("search --who common name err=%v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
	}
	if elapsed > whoScaleBudget {
		t.Fatalf("search --who resolver over %d participants took %s, budget %s", whoScaleParticipants, elapsed, whoScaleBudget)
	}
	var payload errorJSON
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("ambiguous error json = %s err=%v", stdout.String(), err)
	}
	if payload.Error.CandidateTotal != whoScaleParticipants || len(payload.Error.Candidates) != whoScaleReturned {
		t.Fatalf("ambiguous candidate cap = %#v", payload.Error)
	}
}

func createLargeWhoArchive(t *testing.T, participantCount int) string {
	t.Helper()
	ctx := context.Background()
	archivePath := filepath.Join(t.TempDir(), "archive.db")
	st, err := archive.Open(ctx, archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	data := messages.ArchiveData{
		SourcePath:       "synthetic-chat.db",
		SourceModifiedAt: time.Unix(0, 0).UTC(),
		ExtractedAt:      time.Unix(0, 0).UTC(),
		Handles:          make([]messages.Handle, 0, participantCount),
		Chats:            make([]messages.Chat, 0, participantCount),
		Participants:     make([]messages.Participant, 0, participantCount),
		ChatMessages:     make([]messages.ChatMessage, 0, participantCount),
		Messages:         make([]messages.Message, 0, participantCount),
	}
	for i := 1; i <= participantCount; i++ {
		id := int64(i)
		handle := fmt.Sprintf("+1555%06d", i)
		name := fmt.Sprintf("Josh Candidate %05d", i)
		data.Handles = append(data.Handles, messages.Handle{
			SourceRowID: id,
			ID:          handle,
			Service:     "iMessage",
			DisplayName: name,
		})
		data.Chats = append(data.Chats, messages.Chat{
			SourceRowID:    id,
			GUID:           fmt.Sprintf("chat-%05d", i),
			ChatIdentifier: handle,
			ServiceName:    "iMessage",
			DisplayName:    name,
		})
		data.Participants = append(data.Participants, messages.Participant{
			ChatRowID:   id,
			HandleRowID: id,
		})
		data.ChatMessages = append(data.ChatMessages, messages.ChatMessage{
			ChatRowID:    id,
			MessageRowID: id,
		})
		data.Messages = append(data.Messages, messages.Message{
			SourceRowID: id,
			GUID:        fmt.Sprintf("message-%05d", i),
			HandleRowID: id,
			Date:        id,
			Service:     "iMessage",
			Text:        "needle",
		})
	}
	if err := st.ReplaceAll(ctx, data, nil, nil, time.Unix(0, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	return archivePath
}
