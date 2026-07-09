package telecrawl

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit"
	ckoutput "github.com/opentrawl/opentrawl/trawlkit/output"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
)

func TestCrawlerVerbs(t *testing.T) {
	crawler := New()
	verbs := map[string]trawlkit.Verb{}
	for _, verb := range crawler.Verbs() {
		verbs[verb.Name] = verb
	}
	// chats is a shared trawlkit capability now (ChatLister), not a bespoke
	// verb, so it no longer appears in Verbs(); folders and topics stay.
	for _, name := range []string{"doctor", "sync", "search", "folders", "topics", "messages", "contacts"} {
		if _, ok := verbs[name]; !ok {
			t.Fatalf("missing verb %q", name)
		}
	}
	for _, name := range []string{"doctor", "sync", "search"} {
		verb := verbs[name]
		if verb.Name != name || verb.Flags == nil || verb.Help != "" || verb.Run != nil || verb.Mutates || verb.Timeout != 0 || len(verb.Args) != 0 {
			t.Fatalf("spine verb %q has invalid declaration: %+v", name, verb)
		}
	}
}

func TestCrawlerSpineMethodsUseSyntheticArchive(t *testing.T) {
	ctx := context.Background()
	archivePath := t.TempDir() + "/telecrawl.db"
	writeSyntheticArchive(t, ctx, archivePath)

	rawStore, err := ckstore.OpenReadOnly(ctx, archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rawStore.Close() }()

	var out bytes.Buffer
	req := &trawlkit.Request{
		Store:  rawStore,
		Paths:  trawlkit.Paths{Archive: archivePath, Config: t.TempDir() + "/config.toml", Logs: t.TempDir()},
		Format: ckoutput.JSON,
		Out:    &out,
	}
	crawler := New()
	search, err := crawler.Search(ctx, req, trawlkit.Query{Text: "launch", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	fillTestShortRefs(t, ctx, req, search.Results)
	if search.TotalMatches != 1 || len(search.Results) != 1 || search.Results[0].Ref != "telegram:msg/1" {
		t.Fatalf("search result = %+v", search)
	}
	if search.Results[0].ShortRef == "" {
		t.Fatalf("search result has no short ref: %+v", search.Results[0])
	}

	who, err := crawler.Who(ctx, req, "Alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(who) != 1 || who[0].Who != "Alice Example" || who[0].Messages != 1 {
		t.Fatalf("who = %+v", who)
	}

	out.Reset()
	if err := crawler.Open(ctx, req, search.Results[0].ShortRef); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "synthetic launch note") {
		t.Fatalf("open output = %s", out.String())
	}

	contacts, err := crawler.ContactExport(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if contacts == nil || len(contacts.Contacts) != 1 {
		t.Fatalf("contact export = %+v", contacts)
	}
	if got := contacts.Contacts[0]; got.DisplayName != "Alice Example" || len(got.PhoneNumbers) != 1 || got.PhoneNumbers[0] != "+15550100001" {
		t.Fatalf("contact export contact = %+v", got)
	}
}

func TestOpenGroupTranscriptShowsParticipantsAndContext(t *testing.T) {
	ctx := context.Background()
	archivePath := t.TempDir() + "/telecrawl.db"
	writeSyntheticGroupArchive(t, ctx, archivePath)

	rawStore, err := ckstore.OpenReadOnly(ctx, archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rawStore.Close() }()

	var out bytes.Buffer
	req := &trawlkit.Request{
		Store:  rawStore,
		Paths:  trawlkit.Paths{Archive: archivePath, Config: t.TempDir() + "/config.toml", Logs: t.TempDir()},
		Format: ckoutput.Text,
		Out:    &out,
	}
	if err := New().Open(ctx, req, "telegram:msg/1"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Participants: Alice Example",
		"Context: 1 messages around this one.",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("open output missing %q:\n%s", want, out.String())
		}
	}
}

func writeSyntheticArchive(t *testing.T, ctx context.Context, archivePath string) {
	t.Helper()
	st, err := store.Open(ctx, archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	now := time.Date(2026, 7, 2, 14, 0, 0, 0, time.UTC)
	contacts := []store.Contact{
		{JID: "100", PeerType: "user", Phone: "+15550100001", FullName: "Alice Example", Username: "alice_example", UpdatedAt: now},
		{JID: "200", PeerType: "user", FullName: "Bob Example", Username: "bob_example", UpdatedAt: now},
	}
	chats := []store.Chat{{JID: "100", Kind: "user", Name: "Alice Example", LastMessageAt: now, MessageCount: 1}}
	messages := []store.Message{{
		SourcePK:    1,
		ChatJID:     "100",
		ChatName:    "Alice Example",
		MessageID:   "1",
		SenderJID:   "100",
		SenderName:  "Alice Example",
		Timestamp:   now,
		Text:        "synthetic launch note",
		RawType:     0,
		MessageType: "text",
	}}
	stats := store.ImportStats{SourcePath: "/synthetic/source", DBPath: st.Path(), Chats: len(chats), Messages: len(messages), StartedAt: now, FinishedAt: now}
	if _, err := st.ReplaceAll(ctx, stats, contacts, chats, nil, nil, nil, nil, messages); err != nil {
		t.Fatal(err)
	}
	assignSyntheticShortRefs(t, ctx, archivePath)
}

func fillTestShortRefs(t *testing.T, ctx context.Context, req *trawlkit.Request, hits []trawlkit.Hit) {
	t.Helper()
	refs := make([]string, 0, len(hits))
	for _, hit := range hits {
		refs = append(refs, hit.Ref)
	}
	aliases, err := req.ShortRefAliases(ctx, refs)
	if err != nil {
		t.Fatal(err)
	}
	for i := range hits {
		hits[i].ShortRef = aliases[hits[i].Ref]
	}
}

func assignSyntheticShortRefs(t *testing.T, ctx context.Context, archivePath string) {
	t.Helper()
	rawStore, err := ckstore.Open(ctx, ckstore.Options{Path: archivePath})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rawStore.Close() }()
	req := &trawlkit.Request{Store: rawStore, Paths: trawlkit.Paths{Archive: archivePath}}
	records, err := New().ShortRefRecords(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := req.AssignShortRefs(ctx, records); err != nil {
		t.Fatal(err)
	}
}

func writeSyntheticGroupArchive(t *testing.T, ctx context.Context, archivePath string) {
	t.Helper()
	st, err := store.Open(ctx, archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	now := time.Date(2026, 7, 2, 14, 0, 0, 0, time.UTC)
	contacts := []store.Contact{
		{JID: "100", PeerType: "user", Phone: "+15550100001", FullName: "Alice Example", Username: "alice_example", UpdatedAt: now},
	}
	chats := []store.Chat{{JID: "-10042", Kind: "group", Name: "Launch Group", LastMessageAt: now, MessageCount: 1}}
	participants := []store.GroupParticipant{{
		GroupJID:    "-10042",
		UserJID:     "100",
		ContactName: "Alice Example",
		FirstName:   "Alice",
		IsActive:    true,
	}}
	messages := []store.Message{{
		SourcePK:    1,
		ChatJID:     "-10042",
		ChatName:    "Launch Group",
		MessageID:   "1",
		SenderJID:   "100",
		SenderName:  "Alice",
		Timestamp:   now,
		Text:        "synthetic group launch note",
		RawType:     0,
		MessageType: "text",
	}}
	stats := store.ImportStats{SourcePath: "/synthetic/source", DBPath: st.Path(), Chats: len(chats), Messages: len(messages), StartedAt: now, FinishedAt: now}
	if _, err := st.ReplaceAll(ctx, stats, contacts, chats, nil, nil, nil, participants, messages); err != nil {
		t.Fatal(err)
	}
}
