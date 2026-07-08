package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestStoreReplaceStatusListSearch(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	stats := ImportStats{SourcePath: "/tmp/source", DBPath: st.Path(), StartedAt: now.Add(-time.Second), FinishedAt: now}
	contacts := []Contact{{JID: "alice@s.whatsapp.net", FullName: "Alice", UpdatedAt: now}}
	chats := []Chat{{JID: "chat@g.us", Kind: "group", Name: "Chat", LastMessageAt: now, UnreadCount: 2, MessageCount: 2}}
	groups := []Group{{JID: "chat@g.us", Name: "Chat", OwnerJID: "owner@s.whatsapp.net", CreatedAt: now.Add(-time.Hour)}}
	participants := []GroupParticipant{{GroupJID: "chat@g.us", UserJID: "alice@s.whatsapp.net", ContactName: "Alice", IsAdmin: true, IsActive: true}}
	messages := []Message{
		{SourcePK: 1, ChatJID: "chat@g.us", ChatName: "Chat", MessageID: "a", SenderJID: "alice@s.whatsapp.net", SenderName: "Alice", Timestamp: now.Add(-time.Minute), Text: "hello launch", RawType: 0, MessageType: "text"},
		{SourcePK: 2, ChatJID: "chat@g.us", ChatName: "Chat", MessageID: "b", SenderJID: "me", SenderName: "me", Timestamp: now, FromMe: true, Text: "photo", RawType: 1, MessageType: "image", MediaType: "image", MediaTitle: "launch image", MediaPath: "/tmp/image.jpg", MediaSize: 123},
	}
	if err := st.ReplaceAll(ctx, stats, contacts, chats, groups, participants, messages); err != nil {
		t.Fatal(err)
	}

	status, err := st.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Messages != 2 || status.MediaMessages != 1 || status.UnreadChats != 1 || status.UnreadMessages != 2 || status.LastSource != "/tmp/source" {
		t.Fatalf("unexpected status: %+v", status)
	}
	if st.DB() == nil {
		t.Fatal("DB should be available")
	}

	listed, err := st.Messages(ctx, MessageFilter{ChatJID: "chat@g.us", Limit: 10, Asc: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 || listed[0].MessageID != "a" || listed[1].MessageID != "b" {
		t.Fatalf("unexpected messages: %+v", listed)
	}

	onlyMine := true
	filtered, err := st.Messages(ctx, MessageFilter{FromMe: &onlyMine, HasMedia: true, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].MessageID != "b" {
		t.Fatalf("unexpected filtered messages: %+v", filtered)
	}

	results, err := st.Search(ctx, MessageFilter{Query: "launch", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 search results, got %d", len(results))
	}
	if strings.ContainsAny(results[0].Snippet, "[]") || strings.Contains(results[0].Snippet, "...") || strings.ContainsAny(results[0].Snippet, "\n\t") {
		t.Fatalf("search snippet kept marker or multiline text: %q", results[0].Snippet)
	}
	total, err := st.SearchCount(ctx, MessageFilter{Query: "launch", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Fatalf("expected 2 total search results, got %d", total)
	}
	filterOnlyAfter := now.Add(-2 * time.Minute)
	filterOnly, err := st.Search(ctx, MessageFilter{After: &filterOnlyAfter, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(filterOnly) != 1 || filterOnly[0].MessageID != "b" {
		t.Fatalf("expected newest filter-only result, got %+v", filterOnly)
	}
	filterOnlyTotal, err := st.SearchCount(ctx, MessageFilter{After: &filterOnlyAfter, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if filterOnlyTotal != 2 {
		t.Fatalf("expected 2 filter-only matches, got %d", filterOnlyTotal)
	}
	if _, err := st.Search(ctx, MessageFilter{}); err == nil {
		t.Fatal("expected empty search query error")
	}
	if _, err := st.SearchCount(ctx, MessageFilter{}); err == nil {
		t.Fatal("expected empty search count query error")
	}

	target, err := st.MessageByID(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	if target.MessageID != "a" || target.ChatName != "Chat" {
		t.Fatalf("unexpected message by id: %+v", target)
	}
	window, err := st.MessageWindow(ctx, target, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(window) != 2 || window[0].MessageID != "a" || window[1].MessageID != "b" {
		t.Fatalf("unexpected message window: %+v", window)
	}

	after := now.Add(-2 * time.Minute)
	before := now.Add(time.Minute)
	results, err = st.Messages(ctx, MessageFilter{After: &after, Before: &before, Sender: "alice@s.whatsapp.net", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].MessageID != "a" {
		t.Fatalf("unexpected ranged sender results: %+v", results)
	}

	chatsOut, err := st.ListChats(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(chatsOut) != 1 || chatsOut[0].JID != "chat@g.us" {
		t.Fatalf("unexpected chats: %+v", chatsOut)
	}
	unreadChats, err := st.ListUnreadChats(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(unreadChats) != 1 || unreadChats[0].UnreadCount != 2 {
		t.Fatalf("unexpected unread chats: %+v", unreadChats)
	}

	contactsOut, err := st.Contacts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(contactsOut) != 1 || contactsOut[0].JID != "alice@s.whatsapp.net" {
		t.Fatalf("unexpected contacts: %+v", contactsOut)
	}
	var groupCount, participantCount int
	if err := st.DB().QueryRowContext(ctx, `select count(*) from groups`).Scan(&groupCount); err != nil {
		t.Fatal(err)
	}
	if err := st.DB().QueryRowContext(ctx, `select count(*) from group_participants`).Scan(&participantCount); err != nil {
		t.Fatal(err)
	}
	if groupCount != 1 || participantCount != 1 {
		t.Fatalf("unexpected group counts: groups=%d participants=%d", groupCount, participantCount)
	}
}

func TestOpenRequiresPath(t *testing.T) {
	if _, err := Open(context.Background(), ""); err == nil {
		t.Fatal("expected error")
	}
	if _, err := Open(context.Background(), t.TempDir()); err == nil {
		t.Fatal("expected opening directory as db to fail")
	}
	if err := (*Store)(nil).Close(); err != nil {
		t.Fatal(err)
	}
	if unix(time.Time{}) != 0 {
		t.Fatal("zero time unix should be zero")
	}
	if !fromUnix(0).IsZero() {
		t.Fatal("zero unix should be zero time")
	}
}

func TestFromUnixJSONBounds(t *testing.T) {
	got := fromUnix(maxJSONUnixSecond)
	want := time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
	if !got.Equal(want) || got.Location().String() != "UTC" {
		t.Fatalf("fromUnix(max) = %s (%s), want %s UTC", got, got.Location(), want)
	}
	if _, err := json.Marshal(got); err != nil {
		t.Fatalf("max JSON-safe timestamp should marshal: %v", err)
	}
	if got := fromUnix(maxJSONUnixSecond + 1); !got.IsZero() {
		t.Fatalf("out-of-range unix should clamp to zero, got %v", got)
	}
	if got := fromUnix(-1); !got.IsZero() {
		t.Fatalf("negative unix should clamp to zero, got %v", got)
	}
}

func TestReplaceAllDuplicateSourcePKFails(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	err = st.ReplaceAll(
		ctx, ImportStats{FinishedAt: now}, nil,
		[]Chat{{JID: "chat", Kind: "dm", Name: "Chat", LastMessageAt: now}},
		nil,
		nil,
		[]Message{
			{SourcePK: 1, ChatJID: "chat", MessageID: "a", Timestamp: now, RawType: 0},
			{SourcePK: 1, ChatJID: "chat", MessageID: "b", Timestamp: now, RawType: 0},
		},
	)
	if err == nil {
		t.Fatal("expected duplicate source_pk error")
	}
	status, statusErr := st.Status(ctx)
	if statusErr != nil {
		t.Fatal(statusErr)
	}
	if status.Messages != 0 {
		t.Fatalf("failed replace should roll back, got %+v", status)
	}
}

func TestReplaceAllRefreshesFTS(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	chats := []Chat{{JID: "chat", Kind: "dm", Name: "Chat", LastMessageAt: now}}
	messages := []Message{{
		SourcePK:  1,
		ChatJID:   "chat",
		ChatName:  "Chat",
		MessageID: "a",
		Timestamp: now,
		Text:      "old import text",
		RawType:   0,
	}}
	stats := ImportStats{SourcePath: "first", DBPath: st.Path(), Chats: len(chats), Messages: len(messages), StartedAt: now, FinishedAt: now}
	if err := st.ReplaceAll(ctx, stats, nil, chats, nil, nil, messages); err != nil {
		t.Fatal(err)
	}
	results, err := st.Search(ctx, MessageFilter{Query: "old", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected old FTS result, got %d", len(results))
	}

	messages[0].Text = "new import text"
	messages[0].MediaTitle = "fresh media title"
	stats.SourcePath = "second"
	stats.FinishedAt = now.Add(time.Minute)
	if err := st.ReplaceAll(ctx, stats, nil, chats, nil, nil, messages); err != nil {
		t.Fatal(err)
	}
	results, err = st.Search(ctx, MessageFilter{Query: "old", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("expected old FTS text to be removed, got %+v", results)
	}
	results, err = st.Search(ctx, MessageFilter{Query: "fresh", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].MessageID != "a" {
		t.Fatalf("expected updated media title FTS result, got %+v", results)
	}
}

func TestSearchMatchesNonSequentialSourcePK(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	if err := st.ReplaceAll(
		ctx,
		ImportStats{FinishedAt: now},
		nil,
		[]Chat{{JID: "chat", Kind: "dm", Name: "Chat", LastMessageAt: now}},
		nil,
		nil,
		[]Message{{
			SourcePK:  9001,
			ChatJID:   "chat",
			ChatName:  "Chat",
			MessageID: "non-sequential",
			Timestamp: now,
			Text:      "needle survives rowid mapping",
			RawType:   0,
		}},
	); err != nil {
		t.Fatal(err)
	}

	results, err := st.Search(ctx, MessageFilter{Query: "needle", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].SourcePK != 9001 || results[0].MessageID != "non-sequential" {
		t.Fatalf("FTS rowid mapping returned wrong message: %+v", results)
	}
}

func TestSearchWhoFilterMatchesParticipantsAndCounts(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	contacts := []Contact{
		{JID: "bob@s.whatsapp.net", FullName: "Bob Example"},
		{JID: "alice@s.whatsapp.net", FullName: "Alice Example"},
		{JID: "other@s.whatsapp.net", FullName: "Other Person"},
	}
	chats := []Chat{
		{JID: "bob@s.whatsapp.net", Kind: "dm", Name: "Bob Example", LastMessageAt: now, MessageCount: 2},
		{JID: "team@g.us", Kind: "group", Name: "Team", LastMessageAt: now, MessageCount: 1},
		{JID: "other@s.whatsapp.net", Kind: "dm", Name: "Other Person", LastMessageAt: now, MessageCount: 1},
	}
	participants := []GroupParticipant{
		{GroupJID: "team@g.us", UserJID: "alice@s.whatsapp.net", ContactName: "Alice Example", IsActive: true},
	}
	messages := []Message{
		{SourcePK: 1, ChatJID: "bob@s.whatsapp.net", ChatName: "Bob Example", MessageID: "bob-in", SenderJID: "bob@s.whatsapp.net", SenderName: "Bob Example", Timestamp: now, Text: "needle incoming", RawType: 0},
		{SourcePK: 2, ChatJID: "bob@s.whatsapp.net", ChatName: "Bob Example", MessageID: "bob-out", SenderJID: "bob@s.whatsapp.net", SenderName: "me", Timestamp: now.Add(time.Minute), FromMe: true, Text: "needle outgoing", RawType: 0},
		{SourcePK: 3, ChatJID: "team@g.us", ChatName: "Team", MessageID: "group", SenderJID: "other@s.whatsapp.net", SenderName: "Other Person", Timestamp: now.Add(2 * time.Minute), Text: "needle group", RawType: 0},
		{SourcePK: 4, ChatJID: "other@s.whatsapp.net", ChatName: "Other Person", MessageID: "other", SenderJID: "other@s.whatsapp.net", SenderName: "Other Person", Timestamp: now.Add(3 * time.Minute), Text: "needle other", RawType: 0},
	}
	if err := st.ReplaceAll(ctx, ImportStats{FinishedAt: now}, contacts, chats, nil, participants, messages); err != nil {
		t.Fatal(err)
	}

	total, err := st.SearchCount(ctx, MessageFilter{Query: "needle", Who: "bob example", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Fatalf("filtered total = %d, want 2", total)
	}
	results, err := st.Search(ctx, MessageFilter{Query: "needle", Who: "bob example", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("limit should apply after archive filter, got %d results", len(results))
	}

	results, err = st.Search(ctx, MessageFilter{Query: "needle", Who: "ALICE EXAMPLE", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].MessageID != "group" {
		t.Fatalf("group participant filter returned %+v", results)
	}

	total, err = st.SearchCount(ctx, MessageFilter{Query: "needle", Who: "No One", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 {
		t.Fatalf("unmatched who total = %d, want 0", total)
	}
}

func TestSearchWhoFilterMatchesUnicodeCaseFold(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	contacts := []Contact{
		{JID: "ozge@s.whatsapp.net", FullName: "Özge"},
		{JID: "other@s.whatsapp.net", FullName: "Other Person"},
	}
	chats := []Chat{
		{JID: "ozge@s.whatsapp.net", Kind: "dm", Name: "Özge", LastMessageAt: now, MessageCount: 1},
		{JID: "other@s.whatsapp.net", Kind: "dm", Name: "Other Person", LastMessageAt: now, MessageCount: 1},
	}
	messages := []Message{
		{SourcePK: 1, ChatJID: "ozge@s.whatsapp.net", ChatName: "Özge", MessageID: "unicode", SenderJID: "ozge@s.whatsapp.net", SenderName: "Özge", Timestamp: now, Text: "needle unicode", RawType: 0},
		{SourcePK: 2, ChatJID: "other@s.whatsapp.net", ChatName: "Other Person", MessageID: "other", SenderJID: "other@s.whatsapp.net", SenderName: "Other Person", Timestamp: now.Add(time.Minute), Text: "needle other", RawType: 0},
	}
	if err := st.ReplaceAll(ctx, ImportStats{FinishedAt: now}, contacts, chats, nil, nil, messages); err != nil {
		t.Fatal(err)
	}

	total, err := st.SearchCount(ctx, MessageFilter{Query: "needle", Who: "özge", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("filtered total = %d, want 1", total)
	}
	results, err := st.Search(ctx, MessageFilter{Query: "needle", Who: "özge", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].MessageID != "unicode" {
		t.Fatalf("unicode participant filter returned %+v", results)
	}
	matches, err := st.WhoMatches(ctx, "özge")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(matches, []string{"Özge"}) {
		t.Fatalf("matches = %#v, want Özge", matches)
	}
}

func TestResolveWhoPrefersContactFullNameOverPushName(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	contacts := []Contact{
		{JID: "katja@example.com", FullName: "Katja Example"},
	}
	chats := []Chat{
		{JID: "katja@example.com", Kind: "dm", LastMessageAt: now, MessageCount: 1},
	}
	messages := []Message{
		{SourcePK: 1, ChatJID: "katja@example.com", MessageID: "katja-contact", SenderJID: "katja@example.com", SenderName: "Katja", Timestamp: now, Text: "needle one", RawType: 0},
	}
	if err := st.ReplaceAll(ctx, ImportStats{FinishedAt: now}, contacts, chats, nil, nil, messages); err != nil {
		t.Fatal(err)
	}

	resolution, err := st.ResolveWho(ctx, "katja")
	if err != nil {
		t.Fatal(err)
	}
	if len(resolution.Candidates) != 1 || resolution.Candidates[0].Who != "Katja Example" {
		t.Fatalf("candidate = %#v, want contact full name", resolution.Candidates)
	}
}

func TestResolveWhoChoosesCleanPushNameAndNormalizesIdentifiers(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	contacts := []Contact{
		{JID: "katja@example.com", LID: "118390991671363@lid"},
	}
	chats := []Chat{
		{JID: "katja@example.com", Kind: "dm", LastMessageAt: now, MessageCount: 3},
	}
	messages := []Message{
		{SourcePK: 1, ChatJID: "katja@example.com", MessageID: "katja-corrupt", SenderJID: "118390991671363@lid", SenderName: "+EAA=", Timestamp: now, Text: "needle one", RawType: 0},
		{SourcePK: 2, ChatJID: "katja@example.com", MessageID: "katja-clean-one", SenderJID: "118390991671363@lid", SenderName: "Katja", Timestamp: now.Add(time.Minute), Text: "needle two", RawType: 0},
		{SourcePK: 3, ChatJID: "katja@example.com", MessageID: "katja-clean-two", SenderJID: "118390991671363@lid", SenderName: "Katja", Timestamp: now.Add(2 * time.Minute), Text: "needle three", RawType: 0},
	}
	if err := st.ReplaceAll(ctx, ImportStats{FinishedAt: now}, contacts, chats, nil, nil, messages); err != nil {
		t.Fatal(err)
	}

	resolution, err := st.ResolveWho(ctx, "katja")
	if err != nil {
		t.Fatal(err)
	}
	if len(resolution.Candidates) != 1 {
		t.Fatalf("candidates = %#v, want 1", resolution.Candidates)
	}
	candidate := resolution.Candidates[0]
	if candidate.Who != "Katja" {
		t.Fatalf("who = %q, want Katja", candidate.Who)
	}
	if stringSliceContains(candidate.Identifiers, "118390991671363@lid@lid") {
		t.Fatalf("identifiers contain double LID suffix: %#v", candidate.Identifiers)
	}
	if got := stringSliceCount(candidate.Identifiers, "118390991671363@lid"); got != 1 {
		t.Fatalf("identifiers = %#v, want one normalized LID identifier, got %d", candidate.Identifiers, got)
	}
}

func TestResolveWhoTiedPushNamesPickStructurally(t *testing.T) {
	// TRAWL-109: within the push-name tier, tied counts route through the
	// trawlkit picker — mixed case beats all-lowercase, not alpha order.
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	chats := []Chat{
		{JID: "katja@example.com", Kind: "dm", LastMessageAt: now, MessageCount: 4},
	}
	messages := []Message{
		{SourcePK: 1, ChatJID: "katja@example.com", MessageID: "m1", SenderJID: "katja@example.com", SenderName: "katja", Timestamp: now, Text: "one", RawType: 0},
		{SourcePK: 2, ChatJID: "katja@example.com", MessageID: "m2", SenderJID: "katja@example.com", SenderName: "katja", Timestamp: now.Add(time.Minute), Text: "two", RawType: 0},
		{SourcePK: 3, ChatJID: "katja@example.com", MessageID: "m3", SenderJID: "katja@example.com", SenderName: "Katja B", Timestamp: now.Add(2 * time.Minute), Text: "three", RawType: 0},
		{SourcePK: 4, ChatJID: "katja@example.com", MessageID: "m4", SenderJID: "katja@example.com", SenderName: "Katja B", Timestamp: now.Add(3 * time.Minute), Text: "four", RawType: 0},
	}
	if err := st.ReplaceAll(ctx, ImportStats{FinishedAt: now}, nil, chats, nil, nil, messages); err != nil {
		t.Fatal(err)
	}

	resolution, err := st.ResolveWho(ctx, "katja")
	if err != nil {
		t.Fatal(err)
	}
	if len(resolution.Candidates) != 1 {
		t.Fatalf("candidates = %#v, want 1", resolution.Candidates)
	}
	if got := resolution.Candidates[0].Who; got != "Katja B" {
		t.Fatalf("who = %q, want mixed-case spelling on tied push counts", got)
	}
}

func TestResolveWhoMergesSameNameCandidates(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	contacts := []Contact{
		{JID: "casey-one@s.whatsapp.net", Phone: "+15550100", FullName: "Casey Example"},
		{JID: "casey-two@s.whatsapp.net", Phone: "+15550101", FullName: "casey example"},
	}
	chats := []Chat{
		{JID: "casey-one@s.whatsapp.net", Kind: "dm", Name: "Casey Example", LastMessageAt: now, MessageCount: 1},
		{JID: "casey-two@s.whatsapp.net", Kind: "dm", Name: "casey example", LastMessageAt: now, MessageCount: 1},
	}
	messages := []Message{
		{SourcePK: 1, ChatJID: "casey-one@s.whatsapp.net", ChatName: "Casey Example", MessageID: "casey-one", SenderJID: "casey-one@s.whatsapp.net", SenderName: "Casey Example", Timestamp: now, Text: "needle one", RawType: 0},
		{SourcePK: 2, ChatJID: "casey-two@s.whatsapp.net", ChatName: "casey example", MessageID: "casey-two", SenderJID: "casey-two@s.whatsapp.net", SenderName: "casey example", Timestamp: now.Add(time.Minute), Text: "needle two", RawType: 0},
	}
	if err := st.ReplaceAll(ctx, ImportStats{FinishedAt: now}, contacts, chats, nil, nil, messages); err != nil {
		t.Fatal(err)
	}

	resolution, err := st.ResolveWho(ctx, "casey")
	if err != nil {
		t.Fatal(err)
	}
	if len(resolution.Candidates) != 1 {
		t.Fatalf("candidates = %#v, want 1", resolution.Candidates)
	}
	candidate := resolution.Candidates[0]
	if candidate.Who != "Casey Example" || candidate.Messages != 2 || !stringSliceContains(candidate.Identifiers, "+15550100") || !stringSliceContains(candidate.Identifiers, "+15550101") {
		t.Fatalf("candidate = %#v", candidate)
	}
	closeResolution, err := st.ResolveWho(ctx, "Casy")
	if err != nil {
		t.Fatal(err)
	}
	if len(closeResolution.Candidates) != 1 || !closeResolution.OnlyCloseSpellingMatch() {
		t.Fatalf("close-spelling candidates = %#v, want one close-spelling match", closeResolution.Candidates)
	}
}

func TestResolveWhoMeUsesFromMeRows(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	contacts := []Contact{
		{JID: "emery@s.whatsapp.net", Phone: "+15550100", FullName: "Emery Example"},
	}
	chats := []Chat{
		{JID: "emery@s.whatsapp.net", Kind: "dm", Name: "Emery Example", LastMessageAt: now, MessageCount: 3},
	}
	messages := []Message{
		{SourcePK: 1, ChatJID: "emery@s.whatsapp.net", ChatName: "Emery Example", MessageID: "incoming", SenderJID: "emery@s.whatsapp.net", SenderName: "Emery Example", Timestamp: now, Text: "incoming needle", RawType: 0},
		{SourcePK: 2, ChatJID: "emery@s.whatsapp.net", ChatName: "Emery Example", MessageID: "mine", SenderJID: "emery@s.whatsapp.net", SenderName: "me", Timestamp: now.Add(time.Minute), FromMe: true, Text: "outgoing needle", RawType: 0},
	}
	if err := st.ReplaceAll(ctx, ImportStats{FinishedAt: now}, contacts, chats, nil, nil, messages); err != nil {
		t.Fatal(err)
	}

	resolution, err := st.ResolveWho(ctx, "me")
	if err != nil {
		t.Fatal(err)
	}
	if len(resolution.Candidates) != 1 {
		t.Fatalf("candidates = %#v, want only owner", resolution.Candidates)
	}
	candidate := resolution.Candidates[0]
	if candidate.Who != "me" || candidate.Messages != 1 || !stringSliceContains(candidate.Identifiers, "me") {
		t.Fatalf("candidate = %#v, want owner as me", candidate)
	}

	filtered, err := st.Search(ctx, MessageFilter{Who: "me", Query: "needle", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].MessageID != "mine" {
		t.Fatalf("filtered owner search = %#v, want only from-me row", filtered)
	}
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func stringSliceCount(values []string, want string) int {
	count := 0
	for _, value := range values {
		if value == want {
			count++
		}
	}
	return count
}

func TestListChatsClampsOutOfRangePersistedTimestamp(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	valid := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	if _, err := st.DB().ExecContext(ctx, `
insert into chats(jid, kind, name, last_message_at, unread_count, archived, removed, hidden, raw_session_type)
values
	('0@status', 'status', 'Status', ?, 1, 0, 0, 0, 0),
	('valid@s.whatsapp.net', 'dm', 'Valid', ?, 1, 0, 0, 0, 0)
`, maxJSONUnixSecond+1, valid.Unix()); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		list func() ([]Chat, error)
	}{
		{"ListChats", func() ([]Chat, error) { return st.ListChats(ctx, 10) }},
		{"ListUnreadChats", func() ([]Chat, error) { return st.ListUnreadChats(ctx, 10) }},
	} {
		got, err := tc.list()
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if len(got) != 2 {
			t.Fatalf("%s: want 2 chats, got %d", tc.name, len(got))
		}
		if got[0].JID != "valid@s.whatsapp.net" || !got[0].LastMessageAt.Equal(valid) {
			t.Fatalf("%s: valid chat should sort before clamped poison, got %+v", tc.name, got)
		}
		if got[1].JID != "0@status" || !got[1].LastMessageAt.IsZero() {
			t.Fatalf("%s: poisoned chat should clamp to zero and sort oldest, got %+v", tc.name, got)
		}
		if _, err := json.Marshal(got); err != nil {
			t.Fatalf("%s: JSON marshal of already-populated archive failed: %v", tc.name, err)
		}
	}
}

func TestMessagesClampsOutOfRangePersistedTimestamp(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	valid := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	if _, err := st.DB().ExecContext(ctx, `
insert into chats(jid, kind, name, last_message_at, unread_count, archived, removed, hidden, raw_session_type)
values('c@s.whatsapp.net', 'dm', 'C', ?, 0, 0, 0, 0, 0)
`, valid.Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx, `
insert into messages(source_pk, chat_jid, chat_name, msg_id, sender_jid, sender_name, ts, from_me, text, raw_type, message_type, media_type, media_title, media_path, media_url, media_size, starred)
values
	(1, 'c@s.whatsapp.net', 'C', 'poison', '', '', ?, 0, 'poison', 0, 'text', '', '', '', '', 0, 0),
	(2, 'c@s.whatsapp.net', 'C', 'valid', '', '', ?, 0, 'valid', 0, 'text', '', '', '', '', 0, 0)
`, maxJSONUnixSecond+1, valid.Unix()); err != nil {
		t.Fatal(err)
	}

	desc, err := st.Messages(ctx, MessageFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(desc) != 2 || desc[0].MessageID != "valid" || desc[1].MessageID != "poison" || !desc[1].Timestamp.IsZero() {
		t.Fatalf("poisoned message should clamp to zero and sort oldest in desc order: %+v", desc)
	}
	if _, err := json.Marshal(desc); err != nil {
		t.Fatalf("messages JSON marshal failed on poisoned messages.ts: %v", err)
	}

	asc, err := st.Messages(ctx, MessageFilter{Limit: 10, Asc: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(asc) != 2 || asc[0].MessageID != "poison" || asc[1].MessageID != "valid" {
		t.Fatalf("poisoned message should sort as oldest in asc order: %+v", asc)
	}

	after := valid.Add(-time.Hour)
	filtered, err := st.Messages(ctx, MessageFilter{After: &after, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].MessageID != "valid" {
		t.Fatalf("date filters should exclude unknown poisoned timestamps, got %+v", filtered)
	}
}

func TestStatusClampsOutOfRangeMessageTimestamp(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	valid := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	if _, err := st.DB().ExecContext(ctx, `
insert into chats(jid, kind, name, last_message_at, unread_count, archived, removed, hidden, raw_session_type)
values('c@s.whatsapp.net', 'dm', 'C', ?, 0, 0, 0, 0, 0)
`, valid.Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx, `
insert into messages(source_pk, chat_jid, chat_name, msg_id, sender_jid, sender_name, ts, from_me, text, raw_type, message_type, media_type, media_title, media_path, media_url, media_size, starred)
values
	(1, 'c@s.whatsapp.net', 'C', 'poison', '', '', ?, 0, 'poison', 0, 'text', '', '', '', '', 0, 0),
	(2, 'c@s.whatsapp.net', 'C', 'valid', '', '', ?, 0, 'valid', 0, 'text', '', '', '', '', 0, 0)
`, maxJSONUnixSecond+1, valid.Unix()); err != nil {
		t.Fatal(err)
	}

	status, err := st.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !status.OldestMessage.Equal(valid) || !status.NewestMessage.Equal(valid) {
		t.Fatalf("status bounds should ignore poisoned messages.ts and keep valid bounds: %+v", status)
	}
	if _, err := json.Marshal(status); err != nil {
		t.Fatalf("status JSON marshal failed on poisoned messages.ts: %v", err)
	}

	if _, err := st.DB().ExecContext(ctx, `delete from messages where source_pk = 2`); err != nil {
		t.Fatal(err)
	}
	status, err = st.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !status.OldestMessage.IsZero() || !status.NewestMessage.IsZero() {
		t.Fatalf("all-invalid status bounds should clamp to zero: %+v", status)
	}
}
