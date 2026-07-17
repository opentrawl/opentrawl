package telecrawl

import (
	"bytes"
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit"
	ckoutput "github.com/opentrawl/opentrawl/trawlkit/output"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestCrawlerVerbs(t *testing.T) {
	crawler := New()
	verbs := map[string]trawlkit.Verb{}
	for _, verb := range crawler.Verbs() {
		verbs[verb.Name] = verb
	}
	// chats is a shared trawlkit capability now (ChatLister), not a bespoke
	// verb, so it no longer appears in Verbs(); folders and topics stay.
	for _, name := range []string{"sync", "search", "folders", "topics", "messages", "contacts"} {
		if _, ok := verbs[name]; !ok {
			t.Fatalf("missing verb %q", name)
		}
	}
	for _, name := range []string{"sync", "search"} {
		verb := verbs[name]
		if verb.Name != name || verb.Flags == nil || verb.Help != "" || verb.Run != nil || verb.Mutates || verb.Timeout != 0 || len(verb.Args) != 0 {
			t.Fatalf("spine verb %q has invalid declaration: %+v", name, verb)
		}
	}
}

func TestOpenRecordCallsItsLoaderOnce(t *testing.T) {
	assertOpenRecordLoaderCall(t, "open_record.go", "loadOpenMessage")
}

func TestOpenRecordTruncatedContextOffersChatContinuation(t *testing.T) {
	ctx := context.Background()
	archivePath := t.TempDir() + "/telecrawl.db"
	archive, err := store.Open(ctx, archivePath)
	if err != nil {
		t.Fatal(err)
	}
	const truncatedChatID = "6874052333386892567"
	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	messages := make([]store.Message, 0, 13)
	for index := int64(1); index <= 12; index++ {
		messages = append(messages, store.Message{
			SourcePK:    index,
			ChatJID:     truncatedChatID,
			ChatName:    "Synthetic chat",
			MessageID:   strconv.FormatInt(index, 10),
			SenderJID:   "peer",
			SenderName:  "Avery Example",
			Timestamp:   now.Add(time.Duration(index) * time.Minute),
			Text:        "Synthetic message",
			MessageType: "text",
		})
	}
	messages = append(messages, store.Message{
		SourcePK:    100,
		ChatJID:     "short-chat",
		ChatName:    "Short chat",
		MessageID:   "100",
		SenderJID:   "peer",
		SenderName:  "Avery Example",
		Timestamp:   now,
		Text:        "Untruncated message",
		MessageType: "text",
	})
	stats := store.ImportStats{StartedAt: now, FinishedAt: now}
	chats := []store.Chat{{JID: truncatedChatID, Kind: "group", Name: "Synthetic chat"}, {JID: "short-chat", Kind: "user", Name: "Short chat"}}
	if _, err := archive.ReplaceAll(ctx, stats, nil, chats, nil, nil, nil, nil, messages); err != nil {
		_ = archive.Close()
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}

	rawStore, err := ckstore.Open(ctx, ckstore.Options{Path: archivePath})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rawStore.Close() }()
	req := &trawlkit.Request{Store: rawStore, Paths: trawlkit.Paths{Archive: archivePath}}
	crawler := New()
	records, err := crawler.ShortRefRecords(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := req.AssignShortRefs(ctx, records); err != nil {
		t.Fatal(err)
	}
	chatRef := store.ChatRef(truncatedChatID)
	aliases, err := req.ShortRefAliases(ctx, []string{chatRef})
	if err != nil {
		t.Fatal(err)
	}
	chatAlias := aliases[chatRef]
	if chatAlias == "" {
		t.Fatal("missing assigned chat short ref")
	}

	truncated, err := crawler.OpenRecord(ctx, req, store.MessageRef(1))
	if err != nil {
		t.Fatal(err)
	}
	remedy := ""
	truncationFacts := 0
	for _, fact := range truncated.Presentation.Facts {
		if fact.Kind == presentationv1.Fact_KIND_TRUNCATION {
			truncationFacts++
			if truncationFacts > 1 {
				t.Fatalf("duplicate truncation remedy: %#v", truncated.Presentation.Facts)
			}
			remedy = fact.Remedy
		}
	}
	if truncationFacts != 1 {
		t.Fatalf("truncation facts = %d, want 1: %#v", truncationFacts, truncated.Presentation.Facts)
	}
	want := "trawl telegram messages --chat " + chatAlias
	if remedy != want || strings.Contains(remedy, truncatedChatID) {
		t.Fatalf("truncated continuation = %q, want %q without %q", remedy, want, truncatedChatID)
	}

	notTruncated, err := crawler.OpenRecord(ctx, req, store.MessageRef(100))
	if err != nil {
		t.Fatal(err)
	}
	for _, fact := range notTruncated.Presentation.Facts {
		if fact.Remedy != "" {
			t.Fatalf("untruncated record offered continuation: %#v", notTruncated.Presentation.Facts)
		}
	}
}

func TestOpenRecordTruncatedContextWithoutChatAliasHasNoContinuation(t *testing.T) {
	ctx := context.Background()
	archivePath := t.TempDir() + "/telecrawl.db"
	archive, err := store.Open(ctx, archivePath)
	if err != nil {
		t.Fatal(err)
	}
	const truncatedChatID = "6874052333386892567"
	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	messages := make([]store.Message, 0, 12)
	for index := int64(1); index <= 12; index++ {
		messages = append(messages, store.Message{
			SourcePK: index, ChatJID: truncatedChatID, ChatName: "Synthetic chat",
			MessageID: strconv.FormatInt(index, 10), SenderJID: "peer", SenderName: "Avery Example",
			Timestamp: now.Add(time.Duration(index) * time.Minute), Text: "Synthetic message", MessageType: "text",
		})
	}
	stats := store.ImportStats{StartedAt: now, FinishedAt: now}
	if _, err := archive.ReplaceAll(ctx, stats, nil, []store.Chat{{JID: truncatedChatID, Kind: "group", Name: "Synthetic chat"}}, nil, nil, nil, nil, messages); err != nil {
		_ = archive.Close()
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}

	rawStore, err := ckstore.Open(ctx, ckstore.Options{Path: archivePath})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rawStore.Close() }()
	req := &trawlkit.Request{Store: rawStore, Paths: trawlkit.Paths{Archive: archivePath}}
	record, err := New().OpenRecord(ctx, req, store.MessageRef(1))
	if err != nil {
		t.Fatal(err)
	}
	truncationFacts := 0
	for _, fact := range record.Presentation.Facts {
		if fact.Kind == presentationv1.Fact_KIND_TRUNCATION {
			truncationFacts++
			if fact.Remedy != "" {
				t.Fatalf("unassigned chat ref offered continuation: %#v", record.Presentation.Facts)
			}
		}
	}
	if truncationFacts != 1 {
		t.Fatalf("truncation facts = %d, want 1: %#v", truncationFacts, record.Presentation.Facts)
	}
	presentationJSON, err := protojson.Marshal(record.Presentation)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(presentationJSON), truncatedChatID) || strings.Contains(string(presentationJSON), "messages --chat") {
		t.Fatalf("presentation leaked unassigned chat continuation: %s", presentationJSON)
	}
}

func assertOpenRecordLoaderCall(t *testing.T, path, loader string) {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Recv == nil || function.Name.Name != "OpenRecord" {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if ok && selector.Sel.Name == loader {
				calls++
			}
			return true
		})
	}
	if calls != 1 {
		t.Fatalf("OpenRecord %s calls = %d, want 1", loader, calls)
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

	fullRecord, err := crawler.OpenRecord(ctx, req, search.Results[0].Ref)
	if err != nil {
		t.Fatal(err)
	}
	shortRecord, err := crawler.OpenRecord(ctx, req, search.Results[0].ShortRef)
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(fullRecord, shortRecord) || shortRecord.OpenRef != search.Results[0].Ref || shortRecord.Data.GetTypeUrl() != "type.googleapis.com/trawl.source.telegram.open.v1.TelegramRecord" || shortRecord.Presentation == nil {
		t.Fatalf("open records full=%#v short=%#v", fullRecord, shortRecord)
	}
	fullValue, err := crawler.loadOpenMessage(ctx, req, search.Results[0].Ref)
	if err != nil {
		t.Fatal(err)
	}
	shortValue, err := crawler.loadOpenMessage(ctx, req, search.Results[0].ShortRef)
	if err != nil {
		t.Fatal(err)
	}
	writeRuntimeOpenEvidence(t, "telegram", "full", search.Results[0].Ref, fullValue, fullRecord)
	writeRuntimeOpenEvidence(t, "telegram", "short", search.Results[0].ShortRef, shortValue, shortRecord)
	assertOpenRecordError := func(ref, want string) {
		_, err = crawler.OpenRecord(ctx, req, ref)
		var typed commandError
		if !errors.As(err, &typed) || typed.name != want {
			t.Fatalf("open %q error = %#v, want %q", ref, err, want)
		}
	}
	assertOpenRecordError("zzzzz", "unknown_short_ref")
	writeStore, err := ckstore.Open(ctx, ckstore.Options{Path: archivePath})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writeStore.DB().ExecContext(ctx, `insert into short_refs(alias, full_ref, canonical_ref) values (?, ?, ?), (?, ?, ?)`, "zzzzz", search.Results[0].Ref, search.Results[0].Ref, "zzzzz", "telegram:msg/999999999", "telegram:msg/999999999"); err != nil {
		_ = writeStore.Close()
		t.Fatal(err)
	}
	if err := writeStore.Close(); err != nil {
		t.Fatal(err)
	}
	assertOpenRecordError("zzzzz", "ambiguous_short_ref")
	assertOpenRecordError("photos:asset/example", "invalid_ref")
	assertOpenRecordError("telegram:msg/not-a-number", "invalid_ref")
	assertOpenRecordError("telegram:msg/999999999", "not_found")
	_, err = crawler.OpenRecord(ctx, &trawlkit.Request{Paths: trawlkit.Paths{Archive: archivePath + ".missing"}}, search.Results[0].Ref)
	var archiveFailure commandError
	if !errors.As(err, &archiveFailure) || archiveFailure.name != "archive" {
		t.Fatalf("missing archive error = %#v", err)
	}

	contacts, err := crawler.PeopleSnapshot(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if contacts == nil || len(contacts.Contacts) != 2 {
		t.Fatalf("People snapshot = %+v", contacts)
	}
	if got := contacts.Contacts[0]; got.SourceID == "" || got.DisplayName != "Alice Example" || len(got.PhoneNumbers) != 1 || got.PhoneNumbers[0] != "+15550100001" || got.Accounts["telegram"][0] != "alice_example" {
		t.Fatalf("People snapshot contact = %+v", got)
	}
	if got := contacts.Contacts[1]; got.SourceID == "" || got.DisplayName != "Bob Example" || len(got.PhoneNumbers) != 0 || got.Accounts["telegram"][0] != "bob_example" {
		t.Fatalf("username-only People snapshot = %+v", got)
	}
}

func TestSearchMediaOnlyMessageHasEvidence(t *testing.T) {
	ctx := context.Background()
	archivePath := t.TempDir() + "/telecrawl.db"
	st, err := store.Open(ctx, archivePath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 2, 14, 0, 0, 0, time.UTC)
	if _, err := st.ReplaceAll(ctx,
		store.ImportStats{SourcePath: "/synthetic/source", DBPath: st.Path(), Chats: 1, Messages: 1, StartedAt: now, FinishedAt: now},
		nil,
		[]store.Chat{{JID: "chat-1", Kind: "group", Name: "Example chat", LastMessageAt: now, MessageCount: 1}},
		nil, nil, nil, nil,
		[]store.Message{{SourcePK: 1, ChatJID: "chat-1", ChatName: "Example chat", MessageID: "1", SenderJID: "sender-1", SenderName: "Alex Example", Timestamp: now, MediaType: "document"}},
	); err != nil {
		_ = st.Close()
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	rawStore, err := ckstore.OpenReadOnly(ctx, archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rawStore.Close() }()
	result, err := New().Search(ctx, &trawlkit.Request{Store: rawStore, Paths: trawlkit.Paths{Archive: archivePath}}, trawlkit.Query{Text: "document", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Results) != 1 || len(result.Results[0].Evidence) != 1 || result.Results[0].Evidence[0].Text == nil || len(result.Results[0].Evidence[0].Text.Runs) != 1 || result.Results[0].Evidence[0].Text.Runs[0].Text == "" {
		t.Fatalf("media-only search result has invalid evidence: %#v", result)
	}
	if got := result.Results[0].Evidence[0].Text.Runs[0].Text; !strings.Contains(got, "document") {
		t.Fatalf("media-only evidence = %q, want indexed media type", got)
	}
}

func TestOpenGroupRecordPreservesParticipantsAndContext(t *testing.T) {
	ctx := context.Background()
	archivePath := t.TempDir() + "/telecrawl.db"
	writeSyntheticGroupArchive(t, ctx, archivePath)

	rawStore, err := ckstore.OpenReadOnly(ctx, archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rawStore.Close() }()

	req := &trawlkit.Request{
		Store:  rawStore,
		Paths:  trawlkit.Paths{Archive: archivePath, Config: t.TempDir() + "/config.toml", Logs: t.TempDir()},
		Format: ckoutput.Text,
	}
	record, err := New().OpenRecord(ctx, req, "telegram:msg/1")
	if err != nil {
		t.Fatal(err)
	}
	if record.GetPresentation().GetTitle() == "" {
		t.Fatalf("open presentation = %#v", record.GetPresentation())
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
