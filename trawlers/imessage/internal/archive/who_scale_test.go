package archive

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/imessage/internal/messages"
)

const (
	whoScaleParticipants = 5000
	whoScaleReturned     = 20
	whoScaleBudget       = time.Second
)

func TestResolveWhoScalesToManyDistinctParticipants(t *testing.T) {
	if testing.Short() {
		t.Skip("large resolver scale test is skipped in short mode")
	}
	ctx := context.Background()
	st := createLargeWhoStore(t, whoScaleParticipants)
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
}

func createLargeWhoStore(t *testing.T, participantCount int) *Store {
	t.Helper()
	ctx := context.Background()
	archivePath := filepath.Join(t.TempDir(), "archive.db")
	st, err := Open(ctx, archivePath)
	if err != nil {
		t.Fatal(err)
	}

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
		_ = st.Close()
		t.Fatal(err)
	}
	return st
}
