package telecrawl

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/openclaw/crawlkit"
	ckoutput "github.com/openclaw/crawlkit/output"
	ckstore "github.com/openclaw/crawlkit/store"
	"github.com/openclaw/telecrawl/internal/store"
)

func TestCrawlerVerbs(t *testing.T) {
	crawler := New()
	verbs := map[string]crawlkit.Verb{}
	for _, verb := range crawler.Verbs() {
		verbs[verb.Name] = verb
	}
	for _, name := range []string{"doctor", "sync", "search", "chats", "folders", "topics", "messages", "contacts", "backup init", "backup push", "backup pull", "backup status", "backup snapshots"} {
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
	for _, name := range []string{"backup init", "backup status", "backup snapshots"} {
		verb := verbs[name]
		if verb.Store != crawlkit.StoreNone {
			t.Fatalf("%s Store = %v, want StoreNone", name, verb.Store)
		}
	}
	if !crawler.Info().ShortRefs {
		t.Fatal("telecrawl must declare short refs")
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
	req := &crawlkit.Request{
		Store:  rawStore,
		Paths:  crawlkit.Paths{Archive: archivePath, Config: t.TempDir() + "/config.toml", Logs: t.TempDir()},
		Format: ckoutput.JSON,
		Out:    &out,
	}
	crawler := New()
	search, err := crawler.Search(ctx, req, crawlkit.Query{Text: "launch", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if search.TotalMatches != 1 || len(search.Results) != 1 || search.Results[0].Ref != "telecrawl:msg/1" {
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

func writeSyntheticArchive(t *testing.T, ctx context.Context, archivePath string) {
	t.Helper()
	st, err := store.Open(ctx, archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	now := time.Date(2026, 7, 2, 14, 0, 0, 0, time.UTC)
	data := store.SnapshotData{
		Contacts: []store.Contact{
			{JID: "100", PeerType: "user", Phone: "+15550100001", FullName: "Alice Example", Username: "alice_example", UpdatedAt: now},
			{JID: "200", PeerType: "user", FullName: "Bob Example", Username: "bob_example", UpdatedAt: now},
		},
		Chats: []store.Chat{{JID: "100", Kind: "user", Name: "Alice Example", LastMessageAt: now, MessageCount: 1}},
		Messages: []store.Message{{
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
		}},
	}
	if err := st.ImportSnapshot(ctx, data, "/synthetic/source", now); err != nil {
		t.Fatal(err)
	}
	if err := st.RebuildShortRefs(ctx); err != nil {
		t.Fatal(err)
	}
}
