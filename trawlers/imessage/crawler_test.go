package imsgcrawl

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/opentrawl/opentrawl/trawlers/imessage/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	ckoutput "github.com/opentrawl/opentrawl/trawlkit/output"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
	"google.golang.org/protobuf/proto"

	_ "github.com/mattn/go-sqlite3"
)

func TestOpenRecordCallsItsLoaderOnce(t *testing.T) {
	assertOpenRecordLoaderCall(t, "open_record.go", "loadOpenMessage")
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

func TestStatusUsesOnlyArchiveState(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	request := &trawlkit.Request{Paths: trawlkit.Paths{Archive: filepath.Join(t.TempDir(), "messages.db")}}
	status, err := New().Status(context.Background(), request)
	if err != nil || status.State != "missing" || len(status.SetupRequirements) != 0 {
		t.Fatalf("archive-only status = %#v, %v", status, err)
	}
}

func TestCrawlerSyncSearchOpenAndContacts(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("HOME", home)
	sourcePath := filepath.Join(home, "Library", "Messages", "chat.db")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
		t.Fatal(err)
	}
	createMessagesFixture(t, sourcePath)

	stateRoot := filepath.Join(home, ".opentrawl")
	paths := trawlkit.Paths{
		Archive: filepath.Join(stateRoot, appID, appID+".db"),
		Config:  filepath.Join(stateRoot, appID, "config.toml"),
		Logs:    filepath.Join(stateRoot, appID, "logs"),
	}
	source := New()

	writeStore, err := ckstore.Open(ctx, ckstore.Options{Path: paths.Archive})
	if err != nil {
		t.Fatal(err)
	}
	syncReq := &trawlkit.Request{
		Store:    writeStore,
		Paths:    paths,
		Format:   ckoutput.Text,
		Out:      &bytes.Buffer{},
		Progress: func(trawlkit.Progress) {},
	}
	report, err := source.Sync(ctx, syncReq)
	if err == nil {
		records, recordsErr := source.ShortRefRecords(ctx, syncReq)
		if recordsErr != nil {
			err = recordsErr
		} else if _, assignErr := syncReq.AssignShortRefs(ctx, records); assignErr != nil {
			err = assignErr
		}
	}
	if closeErr := writeStore.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	if report.Added != 5 || report.Updated != 0 || report.Removed != 0 {
		t.Fatalf("sync report = %#v, want 5 added, 0 updated, 0 removed", report)
	}

	readStore := openReadStore(t, ctx, paths.Archive)
	searchReq := readRequest(readStore, paths)
	search, err := source.Search(ctx, searchReq, trawlkit.Query{Text: "launch", Limit: 20})
	fillTestShortRefs(t, ctx, searchReq, search.Results)
	_ = readStore.Close()
	if err != nil {
		t.Fatal(err)
	}
	if search.TotalMatches != 2 || len(search.Results) != 2 {
		t.Fatalf("search = %#v, want two results", search)
	}
	hit := search.Results[0]
	if !strings.HasPrefix(hit.Ref, archive.MessageRefPrefix) || hit.ShortRef == "" {
		t.Fatalf("search hit refs = %#v", hit)
	}
	if hit.AnchorID != trawlkit.MatchAnchorID || hit.Summary.Title != "Most Recent Name" || hit.Summary.Subtitle == "" {
		t.Fatalf("search hit = %#v", hit)
	}
	if len(hit.Evidence) != 1 || hit.Evidence[0].Text == nil || len(hit.Evidence[0].Text.Runs) != 1 || !hit.Evidence[0].Text.Runs[0].Matched || !strings.Contains(hit.Evidence[0].Text.Runs[0].Text, "launch") {
		t.Fatalf("search evidence = %#v", hit.Evidence)
	}

	readStore = openReadStore(t, ctx, paths.Archive)
	var openOut bytes.Buffer
	err = source.Open(ctx, &trawlkit.Request{Store: readStore, Paths: paths, Format: ckoutput.JSON, Out: &openOut}, hit.Ref)
	_ = readStore.Close()
	if err != nil {
		t.Fatal(err)
	}
	var opened openOutput
	if err := json.Unmarshal(openOut.Bytes(), &opened); err != nil {
		t.Fatalf("open JSON: %v\n%s", err, openOut.String())
	}
	if opened.Ref != hit.Ref || opened.Chat.Name != "Most Recent Name" || !strings.Contains(opened.Message.Text, "launch") {
		t.Fatalf("opened message = %#v", opened)
	}

	readStore = openReadStore(t, ctx, paths.Archive)
	fullRecord, err := source.OpenRecord(ctx, &trawlkit.Request{Store: readStore, Paths: paths}, hit.Ref)
	_ = readStore.Close()
	if err != nil {
		t.Fatal(err)
	}
	readStore = openReadStore(t, ctx, paths.Archive)
	shortRecord, err := source.OpenRecord(ctx, &trawlkit.Request{Store: readStore, Paths: paths}, hit.ShortRef)
	_ = readStore.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(fullRecord, shortRecord) || shortRecord.OpenRef != hit.Ref || shortRecord.Data.GetTypeUrl() != "type.googleapis.com/trawl.source.imessage.open.v1.IMessageRecord" || shortRecord.Presentation == nil {
		t.Fatalf("open records full=%#v short=%#v", fullRecord, shortRecord)
	}
	load := func(ref string) archive.MessageContext {
		readStore = openReadStore(t, ctx, paths.Archive)
		value, loadErr := source.loadOpenMessage(ctx, &trawlkit.Request{Store: readStore, Paths: paths}, ref)
		_ = readStore.Close()
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		return value
	}
	captureLegacy := func(caseName, ref string) {
		goldens := map[string]string{"json": "7a6c387f1953fc4415195f3481ca1251392ed28f73e71b078bc45b273d4c762f", "text": "faba23b43d2cdea1b7f116389ff20a157b6b1c9f984c683bc7880b860cf92d76"}
		for _, format := range []struct {
			name  string
			value ckoutput.Format
		}{{"json", ckoutput.JSON}, {"text", ckoutput.Text}} {
			readStore = openReadStore(t, ctx, paths.Archive)
			var stdout bytes.Buffer
			openErr := source.Open(ctx, &trawlkit.Request{Store: readStore, Paths: paths, Format: format.value, Out: &stdout}, ref)
			_ = readStore.Close()
			assertLegacyOpenGolden(t, stdout.Bytes(), openErr, goldens[format.name])
			writeLegacyOpenEvidence(t, caseName, format.name, stdout.Bytes(), openErr)
			if openErr != nil {
				t.Fatal(openErr)
			}
		}
	}
	writeRuntimeOpenEvidence(t, "full", hit.Ref, load(hit.Ref), fullRecord)
	writeRuntimeOpenEvidence(t, "short", hit.ShortRef, load(hit.ShortRef), shortRecord)
	captureLegacy("full", hit.Ref)
	captureLegacy("short", hit.ShortRef)
	assertOpenRecordError := func(ref, want string) {
		readStore = openReadStore(t, ctx, paths.Archive)
		_, err = source.OpenRecord(ctx, &trawlkit.Request{Store: readStore, Paths: paths}, ref)
		_ = readStore.Close()
		var typed commandError
		if !errors.As(err, &typed) || typed.name != want {
			t.Fatalf("open %q error = %#v, want %q", ref, err, want)
		}
	}
	assertOpenRecordError("zzzzz", "unknown_short_ref")
	assertOpenRecordError("photos:asset/example", "foreign_ref")
	assertOpenRecordError("imessage:msg/not-a-number", "invalid_ref")
	assertOpenRecordError("imessage:msg/999999999", "not_found")
	writeStore, err = ckstore.Open(ctx, ckstore.Options{Path: paths.Archive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writeStore.DB().ExecContext(ctx, `insert into short_refs(alias, full_ref, canonical_ref) values (?, ?, ?), (?, ?, ?)`, "zzzzz", hit.Ref, hit.Ref, "zzzzz", "imessage:msg/999999999", "imessage:msg/999999999"); err != nil {
		_ = writeStore.Close()
		t.Fatal(err)
	}
	_, err = source.OpenRecord(ctx, &trawlkit.Request{Store: writeStore, Paths: paths}, "zzzzz")
	_ = writeStore.Close()
	var ambiguous commandError
	if !errors.As(err, &ambiguous) || ambiguous.name != "ambiguous_short_ref" {
		t.Fatalf("ambiguous short ref error = %#v", err)
	}
	_, err = source.OpenRecord(ctx, &trawlkit.Request{Paths: trawlkit.Paths{Archive: paths.Archive + ".missing"}}, hit.Ref)
	var archiveFailure commandError
	if !errors.As(err, &archiveFailure) || archiveFailure.name != "archive" {
		t.Fatalf("missing archive error = %#v", err)
	}

	writeStore, err = ckstore.Open(ctx, ckstore.Options{Path: paths.Archive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writeStore.DB().ExecContext(ctx, `update messages set date = ? where source_rowid = ?`, "not a timestamp", 2); err != nil {
		_ = writeStore.Close()
		t.Fatal(err)
	}
	_, err = source.OpenRecord(ctx, &trawlkit.Request{Store: writeStore, Paths: paths}, hit.Ref)
	_ = writeStore.Close()
	if err == nil {
		t.Fatal("malformed stored timestamp opened")
	}
	writeStore, err = ckstore.Open(ctx, ckstore.Options{Path: paths.Archive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writeStore.DB().ExecContext(ctx, `update messages set date = ? where source_rowid = ?`, 200, 2); err != nil {
		_ = writeStore.Close()
		t.Fatal(err)
	}
	_ = writeStore.Close()

	readStore = openReadStore(t, ctx, paths.Archive)
	contacts, err := source.ContactExport(ctx, readRequest(readStore, paths))
	_ = readStore.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(contacts.Contacts) != 3 || contacts.Contacts[0].DisplayName != "Fixture Person" || contacts.Contacts[0].PhoneNumbers[0] != "+15550103" || contacts.Contacts[2].EmailAddresses[0] != "person@example.test" {
		t.Fatalf("contacts = %#v", contacts)
	}
}

func TestChatsListsConversationsWithReadState(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("HOME", home)
	sourcePath := filepath.Join(home, "Library", "Messages", "chat.db")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
		t.Fatal(err)
	}
	createMessagesFixture(t, sourcePath)

	stateRoot := filepath.Join(home, ".opentrawl")
	paths := trawlkit.Paths{
		Archive: filepath.Join(stateRoot, appID, appID+".db"),
		Config:  filepath.Join(stateRoot, appID, "config.toml"),
		Logs:    filepath.Join(stateRoot, appID, "logs"),
	}
	source := New()

	writeStore, err := ckstore.Open(ctx, ckstore.Options{Path: paths.Archive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Sync(ctx, &trawlkit.Request{
		Store:    writeStore,
		Paths:    paths,
		Format:   ckoutput.Text,
		Out:      &bytes.Buffer{},
		Progress: func(trawlkit.Progress) {},
	}); err != nil {
		t.Fatal(err)
	}
	if err := writeStore.Close(); err != nil {
		t.Fatal(err)
	}

	readStore := openReadStore(t, ctx, paths.Archive)
	defer func() { _ = readStore.Close() }()
	req := readRequest(readStore, paths)

	chats, err := source.Chats(ctx, req, trawlkit.ChatQuery{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(chats) != 4 {
		t.Fatalf("chats = %d, want 4: %#v", len(chats), chats)
	}
	// Every chat reports a real unread count once the archive has ingested
	// read state. The counts prove the semantics: a read received message and
	// an owner-sent message never count, and one unread message shared by two
	// chats counts in both. Expected: chat-one 1, chat-two 0, chat-three 1,
	// chat-four 2, so the sorted multiset is {0, 1, 1, 2}.
	var unreadValues []int64
	var group *trawlkit.Chat
	for i := range chats {
		if chats[i].Unread == nil {
			t.Fatalf("read state was synced; unread must be set, not nil: %#v", chats[i])
		}
		unreadValues = append(unreadValues, *chats[i].Unread)
		if chats[i].Participants == nil {
			t.Fatalf("iMessage counts participants; the count must be set: %#v", chats[i])
		}
		if chats[i].Group {
			group = &chats[i]
		}
	}
	sort.Slice(unreadValues, func(i, j int) bool { return unreadValues[i] < unreadValues[j] })
	if got, want := unreadValues, []int64{0, 1, 1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unread counts = %v, want %v", got, want)
	}
	// The fixture's room-named chat has three handles, so it is a group; the
	// rest are one-to-one dms.
	if group == nil || *group.Participants < 3 {
		t.Fatalf("expected one group chat with 3+ participants: %#v", group)
	}
	// The chat column is the ref (imessage:chat/<id>); it must survive a round
	// trip into messages --chat and land on the same chat as the raw id.
	if got := archive.ChatRef(group.ID); got != "imessage:chat/"+group.ID {
		t.Fatalf("chat ref = %q", got)
	}
	rawOut := runImessageMessages(t, ctx, source, readStore, paths, group.ID)
	refOut := runImessageMessages(t, ctx, source, readStore, paths, "imessage:chat/"+group.ID)
	if rawOut == "" || rawOut != refOut {
		t.Fatalf("messages --chat ref must resolve identically to the raw id:\nraw=%s\nref=%s", rawOut, refOut)
	}

	// --unread returns only the chats that have unread received messages.
	unreadChats, err := source.Chats(ctx, req, trawlkit.ChatQuery{Unread: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(unreadChats) != 3 {
		t.Fatalf("--unread chats = %d, want 3: %#v", len(unreadChats), unreadChats)
	}
	for i := range unreadChats {
		if unreadChats[i].Unread == nil || *unreadChats[i].Unread == 0 {
			t.Fatalf("--unread must return only chats with a positive unread count: %#v", unreadChats[i])
		}
	}
}

// A pre-migration archive lacks the messages.is_read column. It must still
// list chats, with Unread nil rather than a fake zero, and refuse --unread
// with ErrChatsNoReadState so a stale archive degrades honestly until re-sync.
func TestChatsDegradesHonestlyWithoutReadStateColumn(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("HOME", home)
	sourcePath := filepath.Join(home, "Library", "Messages", "chat.db")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
		t.Fatal(err)
	}
	createMessagesFixture(t, sourcePath)

	stateRoot := filepath.Join(home, ".opentrawl")
	paths := trawlkit.Paths{
		Archive: filepath.Join(stateRoot, appID, appID+".db"),
		Config:  filepath.Join(stateRoot, appID, "config.toml"),
		Logs:    filepath.Join(stateRoot, appID, "logs"),
	}
	source := New()

	writeStore, err := ckstore.Open(ctx, ckstore.Options{Path: paths.Archive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Sync(ctx, &trawlkit.Request{
		Store:    writeStore,
		Paths:    paths,
		Format:   ckoutput.Text,
		Out:      &bytes.Buffer{},
		Progress: func(trawlkit.Progress) {},
	}); err != nil {
		t.Fatal(err)
	}
	// Simulate an archive synced before read-state ingestion by removing the
	// column the read path probes for.
	if _, err := writeStore.DB().ExecContext(ctx, `alter table messages drop column is_read`); err != nil {
		t.Fatalf("drop is_read column: %v", err)
	}
	if err := writeStore.Close(); err != nil {
		t.Fatal(err)
	}

	readStore := openReadStore(t, ctx, paths.Archive)
	defer func() { _ = readStore.Close() }()
	req := readRequest(readStore, paths)

	chats, err := source.Chats(ctx, req, trawlkit.ChatQuery{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(chats) != 4 {
		t.Fatalf("chats = %d, want 4: %#v", len(chats), chats)
	}
	for i := range chats {
		if chats[i].Unread != nil {
			t.Fatalf("a pre-migration archive stores no read state; unread must stay nil: %#v", chats[i])
		}
	}

	if _, err := source.Chats(ctx, req, trawlkit.ChatQuery{Unread: true}); !errors.Is(err, trawlkit.ErrChatsNoReadState) {
		t.Fatalf("--unread on a read-state-less archive must be ErrChatsNoReadState, got %v", err)
	}
}

func runImessageMessages(t *testing.T, ctx context.Context, source *Crawler, readStore *ckstore.Store, paths trawlkit.Paths, chat string) string {
	t.Helper()
	fs := flag.NewFlagSet("messages", flag.ContinueOnError)
	source.bindMessagesFlags(fs)
	if err := fs.Parse([]string{"--chat", chat}); err != nil {
		t.Fatalf("parse messages flags: %v", err)
	}
	var out bytes.Buffer
	req := &trawlkit.Request{Store: readStore, Paths: paths, Format: ckoutput.JSON, Out: &out}
	if err := source.runMessages(ctx, req); err != nil {
		t.Fatalf("messages --chat %q failed: %v", chat, err)
	}
	return out.String()
}

func TestCrawlerSyncClassifiesArchiveUseFailureAsArchiveError(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("HOME", home)
	sourcePath := filepath.Join(home, "Library", "Messages", "chat.db")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
		t.Fatal(err)
	}
	createMessagesFixture(t, sourcePath)

	paths := trawlkit.Paths{
		Archive: filepath.Join(home, ".opentrawl", appID, appID+".db"),
		Config:  filepath.Join(home, ".opentrawl", appID, "config.toml"),
		Logs:    filepath.Join(home, ".opentrawl", appID, "logs"),
	}
	initialStore, err := ckstore.Open(ctx, ckstore.Options{Path: paths.Archive})
	if err != nil {
		t.Fatal(err)
	}
	if err := initialStore.Close(); err != nil {
		t.Fatal(err)
	}

	readOnlyStore := openReadStore(t, ctx, paths.Archive)
	_, err = New().Sync(ctx, &trawlkit.Request{
		Store:  readOnlyStore,
		Paths:  paths,
		Format: ckoutput.Text,
		Out:    &bytes.Buffer{},
	})
	if closeErr := readOnlyStore.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err == nil {
		t.Fatal("sync succeeded with read-only archive store")
	}
	body := ckoutput.ErrorBodyFor(err)
	if body.Code != "archive" {
		t.Fatalf("sync error code = %q, want archive; body = %#v", body.Code, body)
	}
	wantRemedy := "make the archive path writable, free disk space if needed, and fix the reported archive error"
	if body.Remedy != wantRemedy {
		t.Fatalf("sync error remedy = %q, want %q; body = %#v", body.Remedy, wantRemedy, body)
	}
}

func readRequest(st *ckstore.Store, paths trawlkit.Paths) *trawlkit.Request {
	return &trawlkit.Request{
		Store:  st,
		Paths:  paths,
		Format: ckoutput.Text,
		Out:    &bytes.Buffer{},
	}
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

func openReadStore(t *testing.T, ctx context.Context, path string) *ckstore.Store {
	t.Helper()
	st, err := ckstore.OpenReadOnly(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func createMessagesFixture(t *testing.T, path string) {
	t.Helper()
	longLaunchNote := "latest launch note with candles budget and tariffs. " + strings.Repeat("This sentence keeps going so transcript output must stay whole. ", 3) + "full tail marker"
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	schema := []string{
		`create table handle (ROWID integer primary key, id text not null, service text not null, uncanonicalized_id text)`,
		`create table chat (ROWID integer primary key, guid text not null, display_name text, chat_identifier text, service_name text, room_name text, is_archived integer)`,
		`create table chat_handle_join (chat_id integer, handle_id integer)`,
		`create table message (ROWID integer primary key, guid text not null, handle_id integer, date integer, service text, is_from_me integer, text text, attributedBody blob, is_read integer default 0, date_read integer default 0)`,
		`create table chat_message_join (chat_id integer, message_id integer)`,
		`create table message_attachment_join (message_id integer, attachment_id integer)`,
	}
	for _, stmt := range schema {
		mustExec(t, db, stmt)
	}
	inserts := []string{
		`insert into handle(rowid, id, service, uncanonicalized_id) values (1, '+15550100', 'iMessage', '')`,
		`insert into handle(rowid, id, service, uncanonicalized_id) values (2, '0015550100', 'SMS', '')`,
		`insert into handle(rowid, id, service, uncanonicalized_id) values (3, 'person@example.test', 'iMessage', '')`,
		`insert into handle(rowid, id, service, uncanonicalized_id) values (4, '+15550103', 'SMS', '')`,
		`insert into handle(rowid, id, service, uncanonicalized_id) values (5, 'opaque-handle', 'SMS', '')`,
		`insert into handle(rowid, id, service, uncanonicalized_id) values (6, 'opaque123', 'SMS', '')`,
		`insert into chat(rowid, guid, display_name, chat_identifier, service_name, room_name, is_archived) values (1, 'chat-one', 'Older Name', '+15550100', 'iMessage', '', 0)`,
		`insert into chat(rowid, guid, display_name, chat_identifier, service_name, room_name, is_archived) values (2, 'chat-two', 'Most Recent Name', '0015550100', 'SMS', '', 0)`,
		`insert into chat(rowid, guid, display_name, chat_identifier, service_name, room_name, is_archived) values (3, 'chat-three', 'Fixture Person', '+15550103', 'SMS', '', 0)`,
		`insert into chat(rowid, guid, display_name, chat_identifier, service_name, room_name, is_archived) values (4, 'chat-four', '', 'group-chat', 'SMS', 'Cabinet Group', 0)`,
		`insert into chat_handle_join(chat_id, handle_id) values (1, 1)`,
		`insert into chat_handle_join(chat_id, handle_id) values (2, 2)`,
		`insert into chat_handle_join(chat_id, handle_id) values (3, 4)`,
		`insert into chat_handle_join(chat_id, handle_id) values (4, 4)`,
		`insert into chat_handle_join(chat_id, handle_id) values (4, 5)`,
		`insert into chat_handle_join(chat_id, handle_id) values (4, 6)`,
		// is_read is set exactly as Apple sets it: 1 on a read received message,
		// 0 on an unread one, and 1 on an owner-sent message (a delivery flag,
		// which unread must ignore). chat-one has one unread received message,
		// chat-two's received message is read, message-four is unread and lands
		// in both chat-three and chat-four, and chat-four also holds an unread
		// message-five. So unread counts are chat-one 1, chat-two 0, chat-three
		// 1, chat-four 2.
		`insert into message(rowid, guid, handle_id, date, service, is_from_me, text, attributedBody, is_read) values (1, 'message-one', 1, 100, 'iMessage', 0, 'older hello', null, 0)`,
		`insert into message(rowid, guid, handle_id, date, service, is_from_me, text, attributedBody, is_read) values (2, 'message-two', 2, 200, 'SMS', 0, 'earlier launch note', null, 1)`,
		`insert into message(rowid, guid, handle_id, date, service, is_from_me, text, attributedBody, is_read) values (3, 'message-three', 2, 250, 'SMS', 1, '` + longLaunchNote + `', null, 1)`,
		`insert into message(rowid, guid, handle_id, date, service, is_from_me, text, attributedBody, is_read) values (4, 'message-four', 4, 300, 'SMS', 0, 'group fallback row', null, 0)`,
		`insert into message(rowid, guid, handle_id, date, service, is_from_me, text, attributedBody, is_read) values (5, 'message-five', 5, 350, 'SMS', 0, 'opaque sender row', null, 0)`,
		`insert into chat_message_join(chat_id, message_id) values (1, 1)`,
		`insert into chat_message_join(chat_id, message_id) values (2, 2)`,
		`insert into chat_message_join(chat_id, message_id) values (2, 3)`,
		`insert into chat_message_join(chat_id, message_id) values (3, 4)`,
		`insert into chat_message_join(chat_id, message_id) values (4, 4)`,
		`insert into chat_message_join(chat_id, message_id) values (4, 5)`,
		`insert into message_attachment_join(message_id, attachment_id) values (4, 42)`,
	}
	for _, stmt := range inserts {
		mustExec(t, db, stmt)
	}
}

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}
