package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/openclaw/telecrawl/internal/store"
)

func seedArchive(t *testing.T, messages int, finishedAt time.Time) string {
	t.Helper()
	return seedArchiveWithMessageTime(t, messages, finishedAt, time.Date(2020, 1, 2, 12, 0, 0, 0, time.UTC))
}

func seedArchiveWithMessageTime(t *testing.T, messages int, finishedAt, messageTime time.Time) string {
	t.Helper()
	db := filepath.Join(t.TempDir(), "telecrawl.db")
	st, err := store.Open(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	var chats []store.Chat
	var rows []store.Message
	if messages > 0 {
		chats = []store.Chat{{JID: "100", Kind: "user", Name: "example chat", LastMessageAt: messageTime, MessageCount: messages}}
		for i := 0; i < messages; i++ {
			rows = append(rows, store.Message{
				SourcePK:   int64(i + 1),
				ChatJID:    "100",
				ChatName:   "example chat",
				MessageID:  fmt.Sprintf("0:%d", i+1),
				SenderJID:  "200",
				SenderName: "Example Sender",
				Timestamp:  messageTime.Add(time.Duration(i) * time.Minute),
				Text:       "synthetic launch note",
			})
		}
	}
	if err := st.ReplaceAll(context.Background(), store.ImportStats{SourcePath: "postbox", StartedAt: finishedAt, FinishedAt: finishedAt}, nil, chats, nil, nil, nil, nil, rows); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedDirectSenderArchive(t *testing.T) string {
	t.Helper()
	db := filepath.Join(t.TempDir(), "telecrawl.db")
	st, err := store.Open(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	contacts := []store.Contact{{JID: "300", FullName: "Direct Person"}}
	chats := []store.Chat{{JID: "300", Kind: "user", Name: "Direct Person", LastMessageAt: now, MessageCount: 1}}
	messages := []store.Message{{
		SourcePK:  1,
		ChatJID:   "300",
		ChatName:  "Direct Person",
		MessageID: "0:1",
		SenderJID: "300",
		Timestamp: now,
		Text:      "direct sender needle",
	}}
	if err := st.ReplaceAll(context.Background(), store.ImportStats{SourcePath: "postbox", StartedAt: now, FinishedAt: now}, contacts, chats, nil, nil, nil, nil, messages); err != nil {
		t.Fatal(err)
	}
	if err := st.RebuildShortRefs(context.Background()); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedSearchArchive(t *testing.T, count int) string {
	t.Helper()
	db := filepath.Join(t.TempDir(), "telecrawl.db")
	st, err := store.Open(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	chats := []store.Chat{{JID: "100", Kind: "user", Name: "example chat", LastMessageAt: now, MessageCount: count}}
	messages := make([]store.Message, 0, count)
	for i := 0; i < count; i++ {
		messages = append(messages, store.Message{
			SourcePK:   int64(i + 1),
			ChatJID:    "100",
			ChatName:   "example chat",
			MessageID:  fmt.Sprintf("0:%d", i+1),
			SenderJID:  "200",
			SenderName: "Example Sender",
			Timestamp:  now.Add(time.Duration(i) * time.Minute),
			Text:       fmt.Sprintf("synthetic launch note %03d", i+1),
		})
	}
	if err := st.ReplaceAll(context.Background(), store.ImportStats{SourcePath: "postbox", StartedAt: now, FinishedAt: now}, nil, chats, nil, nil, nil, nil, messages); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedWhoSearchArchive(t *testing.T) string {
	t.Helper()
	db := filepath.Join(t.TempDir(), "telecrawl.db")
	st, err := store.Open(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	contacts := []store.Contact{
		{JID: "200", Phone: "+1555200200", FullName: "Alice Example", Username: "alice_example"},
		{JID: "300", FullName: "Recipient Person"},
		{JID: "400", FullName: "Jordan Example", Username: "jordan_lower"},
		{JID: "401", FullName: "JORDAN EXAMPLE", Username: "jordan_upper"},
	}
	chats := []store.Chat{
		{JID: "100", Kind: "user", Name: "example chat", LastMessageAt: now.Add(4 * time.Minute), MessageCount: 4},
		{JID: "300", Kind: "user", Name: "Recipient Person", LastMessageAt: now.Add(6 * time.Minute), MessageCount: 2},
	}
	messages := []store.Message{
		{SourcePK: 1, ChatJID: "100", ChatName: "example chat", MessageID: "0:1", SenderJID: "200", SenderName: "Alice Example", Timestamp: now, Text: "needle from alice"},
		{SourcePK: 2, ChatJID: "100", ChatName: "example chat", MessageID: "0:2", SenderJID: "201", SenderName: "Other Person", Timestamp: now.Add(time.Minute), Text: "needle from other"},
		{SourcePK: 3, ChatJID: "100", ChatName: "example chat", MessageID: "0:3", SenderJID: "400", SenderName: "Jordan Example", Timestamp: now.Add(2 * time.Minute), Text: "needle from jordan lower"},
		{SourcePK: 4, ChatJID: "100", ChatName: "example chat", MessageID: "0:4", SenderJID: "401", SenderName: "JORDAN EXAMPLE", Timestamp: now.Add(3 * time.Minute), Text: "needle from jordan upper"},
		{SourcePK: 5, ChatJID: "300", ChatName: "Recipient Person", MessageID: "0:5", SenderJID: "999", SenderName: "Archive Owner", Timestamp: now.Add(5 * time.Minute), FromMe: true, Text: "needle to recipient"},
		{SourcePK: 6, ChatJID: "300", ChatName: "Recipient Person", MessageID: "0:6", SenderJID: "300", SenderName: "Recipient Person", Timestamp: now.Add(6 * time.Minute), Text: "needle from recipient"},
	}
	if err := st.ReplaceAll(context.Background(), store.ImportStats{SourcePath: "postbox", StartedAt: now, FinishedAt: now}, contacts, chats, nil, nil, nil, nil, messages); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedWhoResolverDefectArchive(t *testing.T) string {
	t.Helper()
	db := filepath.Join(t.TempDir(), "telecrawl.db")
	st, err := store.Open(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	contacts := []store.Contact{
		{JID: "200", PeerType: "user", FullName: "Jef Example"},
		{JID: "-1001", PeerType: "group", FullName: "Jefs bachelor drive"},
		{JID: "-1002", PeerType: "group", FullName: "Presents for Tini and Sjefke"},
	}
	chats := []store.Chat{
		{JID: "-1001", Kind: "group", Name: "Jefs bachelor drive", LastMessageAt: now.Add(time.Minute), MessageCount: 2},
		{JID: "-1002", Kind: "group", Name: "Presents for Tini and Sjefke", LastMessageAt: now.Add(2 * time.Minute), MessageCount: 1},
	}
	participants := []store.GroupParticipant{
		{GroupJID: "-1001", UserJID: "200", ContactName: "Jef Example", FirstName: "Jef", IsActive: true},
		{GroupJID: "-1001", UserJID: "165355235", IsActive: true},
		{GroupJID: "-1002", UserJID: "700", ContactName: "Other Person", FirstName: "Other", IsActive: true},
	}
	messages := []store.Message{
		{SourcePK: 1, ChatJID: "-1001", ChatName: "Jefs bachelor drive", MessageID: "0:1", SenderJID: "200", SenderName: "Jef Example", Timestamp: now, Text: "jef group message"},
		{SourcePK: 2, ChatJID: "-1001", ChatName: "Jefs bachelor drive", MessageID: "0:2", SenderJID: "165355235", SenderName: "165355235", Timestamp: now.Add(time.Minute), Text: "numeric group message"},
		{SourcePK: 3, ChatJID: "-1002", ChatName: "Presents for Tini and Sjefke", MessageID: "0:3", SenderJID: "700", SenderName: "Other Person", Timestamp: now.Add(2 * time.Minute), Text: "other group message"},
	}
	if err := st.ReplaceAll(context.Background(), store.ImportStats{SourcePath: "postbox", StartedAt: now, FinishedAt: now}, contacts, chats, nil, nil, nil, participants, messages); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedSearchWhoArchive(t *testing.T) string {
	t.Helper()
	db := filepath.Join(t.TempDir(), "telecrawl.db")
	st, err := store.Open(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	contacts := []store.Contact{
		{JID: "87092563", Username: "fixture_user"},
		{JID: "87092564", FirstName: "Ada"},
		{JID: "87092565", FirstName: "+15551234567"},
	}
	chats := []store.Chat{{JID: "100", Kind: "user", Name: "example chat", LastMessageAt: now, MessageCount: 3}}
	messages := []store.Message{
		{SourcePK: 1, ChatJID: "100", ChatName: "example chat", MessageID: "0:1", SenderJID: "87092563", SenderName: "87092563", Timestamp: now, Text: "username-fallback needle"},
		{SourcePK: 2, ChatJID: "100", ChatName: "example chat", MessageID: "0:2", SenderJID: "87092564", SenderName: "", Timestamp: now.Add(time.Minute), Text: "firstname-fallback needle"},
		{SourcePK: 3, ChatJID: "100", ChatName: "example chat", MessageID: "0:3", SenderJID: "87092565", SenderName: "87092565", Timestamp: now.Add(2 * time.Minute), Text: "no-human-fallback needle"},
	}
	if err := st.ReplaceAll(context.Background(), store.ImportStats{SourcePath: "postbox", StartedAt: now, FinishedAt: now}, contacts, chats, nil, nil, nil, nil, messages); err != nil {
		t.Fatal(err)
	}
	return db
}

func readableTelegramSource(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".tempkeyEncrypted"), []byte("synthetic-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(root, "account-1", "postbox", "db", "db_sqlite")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dbPath, []byte("SQLite format 3\x00synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}
