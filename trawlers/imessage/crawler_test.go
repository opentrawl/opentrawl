package imsgcrawl

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opentrawl/opentrawl/trawlers/imessage/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	ckoutput "github.com/opentrawl/opentrawl/trawlkit/output"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"

	_ "github.com/mattn/go-sqlite3"
)

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
		} else if _, rebuildErr := syncReq.RebuildShortRefs(ctx, records); rebuildErr != nil {
			err = rebuildErr
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
	if hit.Who == "" || hit.Where != "Most Recent Name" || !strings.Contains(hit.Snippet, "launch") {
		t.Fatalf("search hit = %#v", hit)
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
	contacts, err := source.ContactExport(ctx, readRequest(readStore, paths))
	_ = readStore.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(contacts.Contacts) != 2 || contacts.Contacts[0].DisplayName != "Fixture Person" || contacts.Contacts[0].PhoneNumbers[0] != "+15550103" {
		t.Fatalf("contacts = %#v", contacts)
	}
}

func TestChatsListsConversationsWithoutReadState(t *testing.T) {
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
	var group *trawlkit.Chat
	for i := range chats {
		if chats[i].Unread != nil {
			t.Fatalf("iMessage stores no read state; unread must stay nil: %#v", chats[i])
		}
		if chats[i].Participants == nil {
			t.Fatalf("iMessage counts participants; the count must be set: %#v", chats[i])
		}
		if chats[i].Group {
			group = &chats[i]
		}
	}
	// The fixture's room-named chat has three handles, so it is a group; the
	// rest are one-to-one dms.
	if group == nil || *group.Participants < 3 {
		t.Fatalf("expected one group chat with 3+ participants: %#v", group)
	}

	if _, err := source.Chats(ctx, req, trawlkit.ChatQuery{Unread: true}); !errors.Is(err, trawlkit.ErrChatsNoReadState) {
		t.Fatalf("--unread on a read-state-less surface must be ErrChatsNoReadState, got %v", err)
	}
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
		`create table message (ROWID integer primary key, guid text not null, handle_id integer, date integer, service text, is_from_me integer, text text, attributedBody blob)`,
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
		`insert into message(rowid, guid, handle_id, date, service, is_from_me, text, attributedBody) values (1, 'message-one', 1, 100, 'iMessage', 0, 'older hello', null)`,
		`insert into message(rowid, guid, handle_id, date, service, is_from_me, text, attributedBody) values (2, 'message-two', 2, 200, 'SMS', 0, 'earlier launch note', null)`,
		`insert into message(rowid, guid, handle_id, date, service, is_from_me, text, attributedBody) values (3, 'message-three', 2, 250, 'SMS', 1, '` + longLaunchNote + `', null)`,
		`insert into message(rowid, guid, handle_id, date, service, is_from_me, text, attributedBody) values (4, 'message-four', 4, 300, 'SMS', 0, 'group fallback row', null)`,
		`insert into message(rowid, guid, handle_id, date, service, is_from_me, text, attributedBody) values (5, 'message-five', 5, 350, 'SMS', 0, 'opaque sender row', null)`,
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
