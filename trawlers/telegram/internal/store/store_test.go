package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit/state"
)

// TestLegacySyncStateTombstonedOnce covers the TRAWL-82 migration: a writable
// open drops the pre-canonical key/value sync_state and creates the canonical
// trawlkit shape, and a later open never re-drops the canonical table (so an
// already-migrated archive keeps its markers).
func TestLegacySyncStateTombstonedOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "telecrawl.db")

	// Seed the legacy key/value sync_state directly with a marker.
	raw, err := sql.Open("sqlite3", "file:"+path+"?_foreign_keys=1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, `create table sync_state(key text primary key, value text not null, updated_at integer not null);
insert into sync_state(key,value,updated_at) values('last_import_at','legacy',1),('source_path','/legacy/export',1);`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("open legacy archive: %v", err)
	}
	cols, err := columns(ctx, st.db, "sync_state")
	if err != nil {
		t.Fatal(err)
	}
	if !cols["source_name"] {
		t.Fatalf("sync_state not migrated to canonical shape: %v", cols)
	}
	// The canonical table reuses the sync_state name, so row count alone
	// can't tell legacy junk from carried-forward markers; the two legacy
	// values must have been carried into the canonical table before the
	// drop — status.LastSource must survive the migration so the next
	// import still preserves existing media refs (TRAWL-82 review fix).
	var rows int
	if err := st.db.QueryRowContext(ctx, `select count(*) from sync_state`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Fatalf("carried-forward markers missing: %d rows, want 2", rows)
	}
	markers := state.New(st.db)
	if rec, ok, err := markers.Get(ctx, syncSource, syncEntityType, syncLastImportAt); err != nil {
		t.Fatal(err)
	} else if !ok || rec.Value != "legacy" {
		t.Fatalf("last_import_at not carried forward: ok=%v value=%q", ok, rec.Value)
	}
	if rec, ok, err := markers.Get(ctx, syncSource, syncEntityType, syncSourcePath); err != nil {
		t.Fatal(err)
	} else if !ok || rec.Value != "/legacy/export" {
		t.Fatalf("source_path not carried forward: ok=%v value=%q", ok, rec.Value)
	}

	// A canonical marker must survive a reopen — the tombstone fires only for
	// the legacy shape, never the canonical one.
	if err := state.New(st.db).Set(ctx, syncSource, syncEntityType, syncLastImportAt, "kept"); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st2, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen migrated archive: %v", err)
	}
	defer func() { _ = st2.Close() }()
	rec, ok, err := state.New(st2.db).Get(ctx, syncSource, syncEntityType, syncLastImportAt)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || rec.Value != "kept" {
		t.Fatalf("canonical marker lost on reopen: ok=%v value=%q", ok, rec.Value)
	}
}

func TestReplaceAllPreservesTelegramStructure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 5, 9, 3, 17, 53, 0, time.UTC)
	contacts := []Contact{{
		JID:       "9",
		PeerType:  "user",
		Phone:     "+15551234567",
		FullName:  "Peter Example",
		FirstName: "Peter",
		LastName:  "Example",
		Username:  "peter",
		UpdatedAt: now,
	}}
	chats := []Chat{{
		JID:           "-10042",
		Kind:          "channel",
		Name:          "coding",
		LastMessageAt: now,
		UnreadCount:   1,
		MessageCount:  2,
		FolderID:      "2",
		Forum:         true,
	}}
	folders := []Folder{{
		ID:        "2",
		Title:     "Clawd",
		Emoticon:  "laptop",
		Color:     3,
		FlagsJSON: `{"groups":true}`,
	}}
	folderChats := []FolderChat{{
		FolderID: "2",
		ChatJID:  "-10042",
		Position: 0,
	}}
	topics := []Topic{{
		ChatJID:              "-10042",
		TopicID:              "17",
		Title:                "General",
		TopMessageID:         "17",
		IconColor:            0x6fb9f0,
		UnreadCount:          1,
		UnreadMentionsCount:  1,
		UnreadReactionsCount: 1,
		Pinned:               true,
		LastMessageAt:        now,
	}}
	participants := []GroupParticipant{{
		GroupJID:    "-10042",
		UserJID:     "9",
		ContactName: "Peter Example",
		FirstName:   "Peter",
		IsActive:    true,
	}}
	messages := []Message{{
		SourcePK:      1,
		ChatJID:       "-10042",
		ChatName:      "coding",
		MessageID:     "18",
		TopicID:       "17",
		ReplyToID:     "17",
		ThreadID:      "17",
		SenderJID:     "9",
		SenderName:    "Peter",
		Timestamp:     now,
		EditTime:      now.Add(time.Minute),
		Text:          "yo",
		MessageType:   "Message",
		MediaType:     "webpage",
		MediaTitle:    "GitHub",
		MediaSize:     123,
		MetadataType:  "web_page",
		MetadataTitle: "GitHub",
		MetadataURL:   "https://github.com/openclaw/telecrawl",
		MetadataJSON:  `{"url":"https://github.com/openclaw/telecrawl"}`,
		ForwardJSON:   `{"from_name":"someone"}`,
		ReactionsJSON: `{"results":[]}`,
		Views:         10,
		Forwards:      2,
		RepliesCount:  3,
		Pinned:        true,
	}}

	source := openTestStore(t, filepath.Join(t.TempDir(), "source.db"))
	stats := ImportStats{SourcePath: "tdata", DBPath: source.Path(), Chats: len(chats), Messages: len(messages), StartedAt: now, FinishedAt: now}
	if _, err := source.ReplaceAll(ctx, stats, contacts, chats, folders, folderChats, topics, participants, messages); err != nil {
		t.Fatal(err)
	}
	storedFolders, err := source.ListFolders(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(storedFolders); got != 1 {
		t.Fatalf("folders = %d, want 1", got)
	}
	storedContacts, err := source.ListContacts(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(storedContacts); got != 1 {
		t.Fatalf("contacts = %d, want 1", got)
	}
	var storedParticipants int
	if err := source.db.QueryRowContext(ctx, `select count(*) from group_participants`).Scan(&storedParticipants); err != nil {
		t.Fatal(err)
	}
	if storedParticipants != 1 {
		t.Fatalf("participants = %d, want 1", storedParticipants)
	}

	restored := openTestStore(t, filepath.Join(t.TempDir(), "restored.db"))
	stats.DBPath = restored.Path()
	if _, err := restored.ReplaceAll(ctx, stats, contacts, chats, folders, folderChats, topics, participants, messages); err != nil {
		t.Fatal(err)
	}
	chats, err = restored.ChatsInFolder(ctx, "2", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(chats) != 1 || chats[0].Name != "coding" || !chats[0].Forum {
		t.Fatalf("folder chats = %#v", chats)
	}
	topics, err = restored.ListTopics(ctx, "-10042", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(topics) != 1 || topics[0].TopicID != "17" || !topics[0].Pinned {
		t.Fatalf("topics = %#v", topics)
	}
	messages, err = restored.Messages(ctx, MessageFilter{ChatJID: "-10042", TopicID: "17", Pinned: true, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(messages))
	}
	msg := messages[0]
	if msg.ReplyToID != "17" || msg.ReactionsJSON == "" || msg.ForwardJSON == "" || msg.Views != 10 || !msg.Pinned || msg.MetadataType != "web_page" || msg.MetadataURL == "" {
		t.Fatalf("message metadata lost: %#v", msg)
	}
	restoredContacts, err := restored.ListContacts(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(restoredContacts) != 1 || restoredContacts[0].Phone != "+15551234567" || restoredContacts[0].PeerType != "user" {
		t.Fatalf("contact lost: %#v", restoredContacts)
	}
	var userJID string
	var isActive int
	if err := restored.db.QueryRowContext(ctx, `select user_jid,is_active from group_participants`).Scan(&userJID, &isActive); err != nil {
		t.Fatal(err)
	}
	if userJID != "9" || isActive == 0 {
		t.Fatalf("participant lost: user=%q active=%d", userJID, isActive)
	}
}

func TestReplaceAllReturnsSyncStats(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 5, 9, 3, 17, 53, 0, time.UTC)
	later := now.Add(time.Hour)
	st := openTestStore(t, filepath.Join(t.TempDir(), "replace-stats.db"))
	chat := Chat{JID: "100", Kind: "user", Name: "Alice Example", LastMessageAt: now, MessageCount: 2}
	firstMessages := []Message{
		{SourcePK: 1, ChatJID: "100", ChatName: "Alice Example", MessageID: "1", SenderJID: "100", SenderName: "Alice Example", Timestamp: now, Text: "kept"},
		{SourcePK: 2, ChatJID: "100", ChatName: "Alice Example", MessageID: "2", SenderJID: "100", SenderName: "Alice Example", Timestamp: now.Add(time.Minute), Text: "removed"},
	}
	first, err := st.ReplaceAll(ctx, ImportStats{SourcePath: "postbox", StartedAt: now, FinishedAt: now}, nil, []Chat{chat}, nil, nil, nil, nil, firstMessages)
	if err != nil {
		t.Fatal(err)
	}
	assertSyncStats(t, first, 2, 0, 0)

	chat.LastMessageAt = later
	chat.MessageCount = 2
	secondMessages := []Message{
		{SourcePK: 1, ChatJID: "100", ChatName: "Alice Example", MessageID: "1", SenderJID: "100", SenderName: "Alice Example", Timestamp: now, Text: "changed"},
		{SourcePK: 3, ChatJID: "100", ChatName: "Alice Example", MessageID: "3", SenderJID: "100", SenderName: "Alice Example", Timestamp: later, Text: "added"},
	}
	second, err := st.ReplaceAll(ctx, ImportStats{SourcePath: "postbox", StartedAt: later, FinishedAt: later}, nil, []Chat{chat}, nil, nil, nil, nil, secondMessages)
	if err != nil {
		t.Fatal(err)
	}
	assertSyncStats(t, second, 1, 1, 1)
}

func openTestStore(t *testing.T, path string) *Store {
	t.Helper()
	st, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
	})
	return st
}

func TestSearchWhoFiltersGroupParticipants(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	st := openTestStore(t, filepath.Join(t.TempDir(), "telecrawl.db"))
	if _, err := st.ReplaceAll(ctx, ImportStats{SourcePath: "postbox", StartedAt: now, FinishedAt: now},
		[]Contact{{JID: "600", FullName: "Group Member"}},
		[]Chat{{JID: "500", Kind: "group", Name: "team room", LastMessageAt: now.Add(time.Minute), MessageCount: 2}},
		nil,
		nil,
		nil,
		nil,
		[]Message{
			{SourcePK: 1, ChatJID: "500", ChatName: "team room", MessageID: "1", SenderJID: "700", SenderName: "Other Sender", Timestamp: now, Text: "member needle from other"},
			{SourcePK: 2, ChatJID: "500", ChatName: "team room", MessageID: "2", SenderJID: "600", SenderName: "Group Member", Timestamp: now.Add(time.Minute), Text: "member needle from group member"},
		}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `insert into group_participants(group_jid,user_jid,contact_name,is_active) values(?,?,?,?)`, "500", "600", "Group Member", 1); err != nil {
		t.Fatal(err)
	}

	filter := MessageFilter{Query: "needle", Who: "group member", Limit: 10}
	messages, err := st.Search(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	total, err := st.CountSearch(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || total != 2 {
		t.Fatalf("group participant search = %d total %d, want 2/2", len(messages), total)
	}
}

// Tripwire: the Telegram Desktop importer leaves sender fields empty on
// direct-chat rows (both directions carry identity via chat_jid + from_me),
// so the who filter must reach direct chats through the chat leg. A stale
// chat-kind vocabulary once left that leg dead and silently dropped entire
// direct conversations — including the owner's own messages — from --who
// filters and who stats.
func TestWhoFilterIncludesDirectChatBothSides(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	st := openTestStore(t, filepath.Join(t.TempDir(), "telecrawl.db"))
	if _, err := st.ReplaceAll(ctx, ImportStats{SourcePath: "tdata", StartedAt: now, FinishedAt: now},
		[]Contact{{JID: "200", PeerType: "user", FullName: "Direct Person"}},
		[]Chat{
			{JID: "200", Kind: "user", Name: "Direct Person", LastMessageAt: now.Add(2 * time.Minute), MessageCount: 3},
			{JID: "-500", Kind: "group", Name: "team room", LastMessageAt: now.Add(3 * time.Minute), MessageCount: 1},
		},
		nil,
		nil,
		nil,
		nil,
		[]Message{
			{SourcePK: 1, ChatJID: "200", ChatName: "Direct Person", MessageID: "1", Timestamp: now, Text: "inbound direct needle"},
			{SourcePK: 2, ChatJID: "200", ChatName: "Direct Person", MessageID: "2", SenderJID: "999", SenderName: "999", Timestamp: now.Add(time.Minute), FromMe: true, Text: "own direct needle"},
			{SourcePK: 3, ChatJID: "200", ChatName: "Direct Person", MessageID: "3", Timestamp: now.Add(2 * time.Minute), Text: "second inbound needle"},
			{SourcePK: 4, ChatJID: "-500", ChatName: "team room", MessageID: "4", SenderJID: "700", SenderName: "Other Sender", Timestamp: now.Add(3 * time.Minute), Text: "unrelated group needle"},
		}); err != nil {
		t.Fatal(err)
	}

	candidates, err := st.ResolveWho(ctx, "Direct Person")
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].Messages != 3 || !candidates[0].LastSeen.Equal(now.Add(2*time.Minute)) {
		t.Fatalf("candidates = %#v, want Direct Person with all 3 direct-chat messages", candidates)
	}

	filter := MessageFilter{Query: "needle", Who: "Direct Person", Limit: 10}
	messages, err := st.Search(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	total, err := st.CountSearch(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 3 || total != 3 {
		t.Fatalf("search --who Direct Person = %d total %d, want 3/3", len(messages), total)
	}
	ownIncluded := false
	for _, message := range messages {
		if message.ChatJID != "200" {
			t.Fatalf("message outside the direct chat leaked in: %#v", message)
		}
		if message.FromMe {
			ownIncluded = true
		}
	}
	if !ownIncluded {
		t.Fatal("own message missing from --who filter on the direct chat")
	}
}

func TestResolveWhoExcludesGroupChatTitles(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	st := openTestStore(t, filepath.Join(t.TempDir(), "telecrawl.db"))
	if _, err := st.ReplaceAll(ctx, ImportStats{SourcePath: "postbox", StartedAt: now, FinishedAt: now},
		[]Contact{
			{JID: "600", PeerType: "user", FullName: "Jeff Person"},
			{JID: "-1001", PeerType: "group", FullName: "Jefs bachelor drive"},
			{JID: "-1002", PeerType: "group", FullName: "Presents for Tini and Sjefke"},
		},
		[]Chat{
			{JID: "-1001", Kind: "group", Name: "Jefs bachelor drive", LastMessageAt: now, MessageCount: 1},
			{JID: "-1002", Kind: "group", Name: "Presents for Tini and Sjefke", LastMessageAt: now.Add(time.Minute), MessageCount: 1},
		},
		nil,
		nil,
		nil,
		[]GroupParticipant{
			{GroupJID: "-1001", UserJID: "600", ContactName: "Jeff Person", FirstName: "Jeff", IsActive: true},
			{GroupJID: "-1002", UserJID: "700", ContactName: "Other Person", FirstName: "Other", IsActive: true},
		},
		[]Message{
			{SourcePK: 1, ChatJID: "-1001", ChatName: "Jefs bachelor drive", MessageID: "1", SenderJID: "600", SenderName: "Jeff Person", Timestamp: now, Text: "group needle"},
			{SourcePK: 2, ChatJID: "-1002", ChatName: "Presents for Tini and Sjefke", MessageID: "2", SenderJID: "700", SenderName: "Other Person", Timestamp: now.Add(time.Minute), Text: "other group needle"},
		}); err != nil {
		t.Fatal(err)
	}

	candidates, err := st.ResolveWho(ctx, "jeff")
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].Who != "Jeff Person" {
		t.Fatalf("candidates = %#v, want only Jeff Person", candidates)
	}
	for _, candidate := range candidates {
		for _, disallowed := range []string{"Jefs bachelor drive", "Presents for Tini and Sjefke"} {
			if candidate.Who == disallowed || containsStoreString(candidate.Identifiers, "-1001") || containsStoreString(candidate.Identifiers, "-1002") {
				t.Fatalf("group chat leaked as who candidate: %#v", candidate)
			}
		}
	}
}

func TestResolveWhoMatchesUnnamedParticipantIdentifiersWithSharedMatcher(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	st := openTestStore(t, filepath.Join(t.TempDir(), "telecrawl.db"))
	if _, err := st.ReplaceAll(ctx, ImportStats{SourcePath: "postbox", StartedAt: now, FinishedAt: now},
		[]Contact{{JID: "200", PeerType: "user", FullName: "Jef Example"}},
		[]Chat{{JID: "-1009", Kind: "group", Name: "resolver room", LastMessageAt: now.Add(time.Minute), MessageCount: 2}},
		nil,
		nil,
		nil,
		[]GroupParticipant{
			{GroupJID: "-1009", UserJID: "200", ContactName: "Jef Example", FirstName: "Jef", IsActive: true},
			{GroupJID: "-1009", UserJID: "165355235", IsActive: true},
		},
		[]Message{
			{SourcePK: 1, ChatJID: "-1009", ChatName: "resolver room", MessageID: "1", SenderJID: "200", SenderName: "Jef Example", Timestamp: now, Text: "jef needle"},
			{SourcePK: 2, ChatJID: "-1009", ChatName: "resolver room", MessageID: "2", SenderJID: "165355235", SenderName: "165355235", Timestamp: now.Add(time.Minute), Text: "numeric needle"},
		}); err != nil {
		t.Fatal(err)
	}

	nameMatches, err := st.ResolveWho(ctx, "jef")
	if err != nil {
		t.Fatal(err)
	}
	if len(nameMatches) != 1 || nameMatches[0].Who != "Jef Example" {
		t.Fatalf("name matches = %#v, want only Jef Example", nameMatches)
	}

	partialIDMatches, err := st.ResolveWho(ctx, "165")
	if err != nil {
		t.Fatal(err)
	}
	if len(partialIDMatches) != 1 || partialIDMatches[0].Who != "165355235" || !containsStoreString(partialIDMatches[0].Identifiers, "165355235") {
		t.Fatalf("partial id matches = %#v, want unnamed numeric participant", partialIDMatches)
	}

	idMatches, err := st.ResolveWho(ctx, "165355235")
	if err != nil {
		t.Fatal(err)
	}
	if len(idMatches) != 1 || idMatches[0].Who != "165355235" || !containsStoreString(idMatches[0].Identifiers, "165355235") {
		t.Fatalf("identifier matches = %#v, want unnamed numeric participant", idMatches)
	}
}

func TestResolveWhoFoldsOwnerIdentifiersToMe(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	st := openTestStore(t, filepath.Join(t.TempDir(), "telecrawl.db"))
	if _, err := st.ReplaceAll(ctx, ImportStats{SourcePath: "postbox", StartedAt: now, FinishedAt: now},
		nil,
		[]Chat{{JID: "300", Kind: "user", Name: "Recipient Person", LastMessageAt: now.Add(time.Minute), MessageCount: 2}},
		nil,
		nil,
		nil,
		nil,
		[]Message{
			{SourcePK: 1, ChatJID: "300", ChatName: "Recipient Person", MessageID: "1", SenderJID: "999", SenderName: "999", Timestamp: now, FromMe: true, Text: "owner needle"},
			{SourcePK: 2, ChatJID: "300", ChatName: "Recipient Person", MessageID: "2", Timestamp: now.Add(time.Minute), FromMe: true, Text: "blank owner needle"},
			{SourcePK: 3, ChatJID: "300", ChatName: "Recipient Person", MessageID: "3", SenderJID: "300", SenderName: "Recipient Person", Timestamp: now.Add(2 * time.Minute), Text: "reply needle"},
		}); err != nil {
		t.Fatal(err)
	}

	for _, query := range []string{"999", "me"} {
		candidates, err := st.ResolveWho(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		if len(candidates) == 0 || candidates[0].Who != "me" || candidates[0].Messages != 2 || !containsStoreString(candidates[0].Identifiers, "me") || containsStoreString(candidates[0].Identifiers, "999") {
			t.Fatalf("query %q candidates = %#v, want owner as me", query, candidates)
		}
	}

	messages, err := st.Search(ctx, MessageFilter{Query: "owner", Who: "me", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || !messages[0].FromMe || !messages[1].FromMe {
		t.Fatalf("search --who me = %#v, want both owner rows", messages)
	}
}

func TestResolveWhoDedupesAndMatchesGenerously(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	st := openTestStore(t, filepath.Join(t.TempDir(), "telecrawl.db"))
	if _, err := st.ReplaceAll(ctx, ImportStats{SourcePath: "postbox", StartedAt: now, FinishedAt: now},
		[]Contact{{JID: "200", Phone: "+1555200200", FullName: "Alice Example", Username: "alice_example"}},
		[]Chat{{JID: "100", Kind: "user", Name: "example chat", LastMessageAt: now.Add(time.Minute), MessageCount: 2}},
		nil,
		nil,
		nil,
		nil,
		[]Message{
			{SourcePK: 1, ChatJID: "100", ChatName: "example chat", MessageID: "1", SenderJID: "200", SenderName: "Alice Example", Timestamp: now, Text: "hello from alice"},
			{SourcePK: 2, ChatJID: "100", ChatName: "example chat", MessageID: "2", SenderJID: "200", SenderName: "Alice Example", Timestamp: now.Add(time.Minute), Text: "second from alice"},
		}); err != nil {
		t.Fatal(err)
	}

	for _, query := range []string{"ali", "lice", "Alic Exampel", "@alice_example", "+1555200200"} {
		t.Run(query, func(t *testing.T) {
			candidates, err := st.ResolveWho(ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			if len(candidates) != 1 {
				t.Fatalf("candidates = %#v, want one Alice candidate", candidates)
			}
			candidate := candidates[0]
			if candidate.Who != "Alice Example" || candidate.Messages != 2 || !candidate.LastSeen.Equal(now.Add(time.Minute)) {
				t.Fatalf("candidate = %#v, want deduped Alice stats", candidate)
			}
			if !containsStoreString(candidate.Identifiers, "+1555200200") || !containsStoreString(candidate.Identifiers, "@alice_example") || !containsStoreString(candidate.Identifiers, "200") {
				t.Fatalf("identifiers = %#v, want phone, handle, and jid", candidate.Identifiers)
			}
		})
	}
}

func containsStoreString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestOpenMigratesSchema2MessageMetadataColumns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "schema2.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
create table messages (
	rowid integer primary key autoincrement,
	source_pk integer not null unique,
	chat_jid text not null,
	chat_name text,
	msg_id text not null,
	sender_jid text,
	sender_name text,
	ts integer not null,
	from_me integer not null,
	text text,
	raw_type integer not null default 0,
	message_type text,
	media_type text,
	media_title text,
	media_path text,
	media_url text,
	media_size integer,
	starred integer not null default 0,
	topic_id text,
	reply_to_msg_id text,
	reply_to_chat_jid text,
	thread_id text,
	edit_ts integer,
	forward_json text,
	reactions_json text,
	views integer not null default 0,
	forwards integer not null default 0,
	replies_count integer not null default 0,
	pinned integer not null default 0
);
pragma user_version = 2;
`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	st := openTestStore(t, path)
	cols, err := columns(ctx, st.db, "messages")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"metadata_type", "metadata_title", "metadata_url", "metadata_json"} {
		if !cols[name] {
			t.Fatalf("missing migrated column %q", name)
		}
	}
	var version int
	if err := st.db.QueryRowContext(ctx, "pragma user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion {
		t.Fatalf("user_version = %d, want %d", version, schemaVersion)
	}
}

func TestOpenMigratesSchema1BeforeCreatingTopicIndex(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "schema1.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
create table chats (
	id integer primary key,
	kind text not null,
	name text,
	username text,
	last_message_at integer,
	unread_count integer not null default 0,
	message_count integer not null default 0
);
create table contacts (
	jid text primary key,
	phone text,
	full_name text,
	first_name text,
	last_name text,
	business_name text,
	username text,
	lid text,
	about_text text,
	updated_at integer
);
create table messages (
	rowid integer primary key autoincrement,
	source_pk integer not null unique,
	chat_jid text not null,
	chat_name text,
	msg_id text not null,
	sender_jid text,
	sender_name text,
	ts integer not null,
	from_me integer not null,
	text text,
	raw_type integer not null default 0,
	message_type text,
	media_type text,
	media_title text,
	media_path text,
	media_url text,
	media_size integer,
	starred integer not null default 0
);
create index idx_messages_chat_ts on messages(chat_jid, ts);
pragma user_version = 1;
`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	st := openTestStore(t, path)
	cols, err := columns(ctx, st.db, "messages")
	if err != nil {
		t.Fatal(err)
	}
	if !cols["topic_id"] {
		t.Fatal("missing migrated topic_id column")
	}
	var indexName string
	if err := st.db.QueryRowContext(ctx, `select name from sqlite_master where type='index' and name='idx_messages_chat_topic_ts'`).Scan(&indexName); err != nil {
		t.Fatal(err)
	}
	if indexName != "idx_messages_chat_topic_ts" {
		t.Fatalf("topic index = %q", indexName)
	}
	var version int
	if err := st.db.QueryRowContext(ctx, "pragma user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion {
		t.Fatalf("user_version = %d, want %d", version, schemaVersion)
	}
}

func TestMessagesToleratesNullableOptionalFields(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := openTestStore(t, filepath.Join(t.TempDir(), "nullable-messages.db"))
	if _, err := st.db.ExecContext(ctx, `insert into messages(source_pk,chat_jid,msg_id,ts,from_me,raw_type,starred) values(?,?,?,?,?,?,?)`, 1, "42", "1", unix(time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)), 0, 0, 0); err != nil {
		t.Fatal(err)
	}

	messages, err := st.Messages(ctx, MessageFilter{ChatJID: "42", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(messages))
	}
	if messages[0].EditTime.IsZero() == false {
		t.Fatalf("edit time = %v, want zero", messages[0].EditTime)
	}
	if messages[0].ChatName != "" || messages[0].TopicID != "" || messages[0].ForwardJSON != "" {
		t.Fatalf("nullable fields not normalized: %#v", messages[0])
	}
}

func TestUpsertChatPreservesUnrelatedChats(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 5, 9, 3, 17, 53, 0, time.UTC)
	later := now.Add(time.Hour)

	st := openTestStore(t, filepath.Join(t.TempDir(), "upsert.db"))

	chatA := Chat{JID: "-1001", Kind: "channel", Name: "Chat A", LastMessageAt: now, UnreadCount: 1, MessageCount: 1, FolderID: "1", Forum: false}
	chatB := Chat{JID: "-1002", Kind: "group", Name: "Chat B", LastMessageAt: now, UnreadCount: 3, MessageCount: 2, FolderID: "2", Forum: true}
	topicA := Topic{ChatJID: "-1001", TopicID: "1", Title: "Topic A", LastMessageAt: now}
	topicB := Topic{ChatJID: "-1002", TopicID: "2", Title: "Topic B", LastMessageAt: now}
	fcA := FolderChat{FolderID: "1", ChatJID: "-1001", Position: 0}
	fcB := FolderChat{FolderID: "2", ChatJID: "-1002", Position: 1}
	msgA := Message{SourcePK: 1, ChatJID: "-1001", ChatName: "Chat A", MessageID: "1", SenderJID: "10", SenderName: "Alice", Timestamp: now, Text: "hello a", MessageType: "Message"}
	msgB1 := Message{SourcePK: 2, ChatJID: "-1002", ChatName: "Chat B", MessageID: "1", SenderJID: "20", SenderName: "Bob", Timestamp: now, Text: "hello b1", MessageType: "Message"}
	msgB2 := Message{SourcePK: 3, ChatJID: "-1002", ChatName: "Chat B", MessageID: "2", SenderJID: "20", SenderName: "Bob", Timestamp: later, Text: "hello b2", MessageType: "Message"}

	initial := ImportStats{SourcePath: "tdata", DBPath: st.Path(), Chats: 2, Messages: 3, StartedAt: now, FinishedAt: now}
	if _, err := st.ReplaceAll(
		ctx, initial,
		nil,
		[]Chat{chatA, chatB},
		[]Folder{{ID: "1", Title: "F1"}, {ID: "2", Title: "F2"}},
		[]FolderChat{fcA, fcB},
		[]Topic{topicA, topicB},
		nil,
		[]Message{msgA, msgB1, msgB2},
	); err != nil {
		t.Fatal(err)
	}

	updatedChatA := Chat{JID: "-1001", Kind: "channel", Name: "Chat A Updated", LastMessageAt: later, UnreadCount: 5, MessageCount: 1, Forum: false}
	updatedMsgA := Message{SourcePK: 4, ChatJID: "-1001", ChatName: "Chat A Updated", MessageID: "2", SenderJID: "10", SenderName: "Alice", Timestamp: later, Text: "updated a", MessageType: "Message", MediaType: "photo", MediaTitle: "pic.jpg"}

	upsertStats := ImportStats{SourcePath: "tdata", DBPath: st.Path(), Chats: 1, Messages: 1, MediaMessages: 1, StartedAt: later, FinishedAt: later}
	chatStats, err := st.UpsertChat(
		ctx, upsertStats, "-1001",
		nil,
		[]Chat{updatedChatA},
		nil, nil,
		nil,
		nil,
		[]Message{updatedMsgA},
	)
	if err != nil {
		t.Fatal(err)
	}
	assertSyncStats(t, chatStats, 1, 0, 1)

	status, err := st.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Chats != 2 {
		t.Fatalf("chats = %d, want 2 (chat B preserved)", status.Chats)
	}
	if status.Messages != 3 {
		t.Fatalf("messages = %d, want 3 (2 from B + 1 updated A)", status.Messages)
	}
	if status.MediaMessages != 1 {
		t.Fatalf("media_messages = %d, want 1", status.MediaMessages)
	}
	if status.LastImportAt != later {
		t.Fatalf("last_import_at = %v, want %v", status.LastImportAt, later)
	}

	chats, err := st.ListChats(ctx, 10, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(chats) != 2 {
		t.Fatalf("chats list = %d, want 2", len(chats))
	}
	foundA, foundB := false, false
	for _, c := range chats {
		switch c.JID {
		case "-1001":
			foundA = true
			if c.Name != "Chat A Updated" {
				t.Fatalf("chat A name = %q, want %q", c.Name, "Chat A Updated")
			}
			if c.FolderID != "1" {
				t.Fatalf("chat A folder_id = %q, want preserved folder 1", c.FolderID)
			}
		case "-1002":
			foundB = true
			if c.Name != "Chat B" {
				t.Fatalf("chat B name = %q, want %q", c.Name, "Chat B")
			}
		}
	}
	if !foundA || !foundB {
		t.Fatalf("missing chats: A=%v B=%v", foundA, foundB)
	}

	msgAAll, err := st.Messages(ctx, MessageFilter{ChatJID: "-1001", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(msgAAll) != 1 || msgAAll[0].Text != "updated a" {
		t.Fatalf("chat A messages = %d (text=%q), want 1 (updated a)", len(msgAAll), msgAAll[0].Text)
	}

	msgBAll, err := st.Messages(ctx, MessageFilter{ChatJID: "-1002", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(msgBAll) != 2 {
		t.Fatalf("chat B messages = %d, want 2 (preserved)", len(msgBAll))
	}

	folders, err := st.ListFolders(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(folders) != 2 {
		t.Fatalf("folders = %d, want 2", len(folders))
	}

	fcsA, err := st.ChatsInFolder(ctx, "1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(fcsA) != 1 || fcsA[0].JID != "-1001" {
		t.Fatalf("folder 1 chats = %v, want chat A preserved", fcsA)
	}

	fcs, err := st.ChatsInFolder(ctx, "2", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(fcs) != 1 || fcs[0].JID != "-1002" {
		t.Fatalf("folder 2 chats = %v, want chat B only", fcs)
	}

	searchA, err := st.Search(ctx, MessageFilter{Query: "updated", ChatJID: "-1001", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(searchA) != 1 {
		t.Fatalf("FTS 'updated' in chat A = %d, want 1", len(searchA))
	}
	if searchA[0].Snippet != "updated a pic.jpg" {
		t.Fatalf("search snippet = %q, want marker-free text", searchA[0].Snippet)
	}

	searchB, err := st.Search(ctx, MessageFilter{Query: "hello", ChatJID: "-1002", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(searchB) != 2 {
		t.Fatalf("FTS 'hello' in chat B = %d, want 2 (preserved)", len(searchB))
	}

	searchOld, err := st.Search(ctx, MessageFilter{Query: "hello a", ChatJID: "-1001", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(searchOld) != 0 {
		t.Fatalf("FTS 'hello a' in chat A = %d, want 0 (old FTS removed)", len(searchOld))
	}

	rec, ok, err := state.New(st.db).Get(ctx, syncSource, syncEntityType, syncSourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || rec.Value != "tdata" {
		t.Fatalf("source_path = %q ok=%v, want %q", rec.Value, ok, "tdata")
	}
}

func assertSyncStats(t *testing.T, got SyncStats, added, updated, removed int64) {
	t.Helper()
	if got.Added != added || got.Updated != updated || got.Removed != removed {
		t.Fatalf("sync stats = %+v, want added=%d updated=%d removed=%d", got, added, updated, removed)
	}
}
