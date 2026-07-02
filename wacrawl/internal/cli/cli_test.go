package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/openclaw/wacrawl/internal/store"
	_ "modernc.org/sqlite"
)

func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "wacrawl-home-*")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("HOME", home)
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}

func TestRunEndToEnd(t *testing.T) {
	ctx := context.Background()
	source := t.TempDir()
	createDesktopFixture(t, source)
	dbPath := filepath.Join(t.TempDir(), "archive.db")

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"help", []string{"--db", dbPath, "help"}, "wacrawl reads local WhatsApp"},
		{"version", []string{"--version"}, version},
		{"metadata", []string{"--json", "metadata"}, `"id": "wacrawl"`},
		{"doctor", []string{"--db", dbPath, "--source", source, "doctor"}, "source_store=ok"},
		{"import", []string{"--db", dbPath, "--source", source, "import"}, "messages=3"},
		{"import copy media", []string{"--db", dbPath, "--source", source, "import", "--copy-media"}, "media_copied=1"},
		{"status", []string{"--db", dbPath, "status"}, "unread_messages=2"},
		{"status trailing json", []string{"--db", dbPath, "status", "--json"}, `"app_id": "wacrawl"`},
		{"chats", []string{"--db", dbPath, "chats", "--limit", "5"}, "UNREAD"},
		{"contacts export", []string{"--db", dbPath, "--json", "contacts", "export"}, `"display_name": "Alice Contact"`},
		{"chats unread", []string{"--db", dbPath, "chats", "--unread", "--limit", "5"}, "Launch Group"},
		{"unread", []string{"--db", dbPath, "unread", "--limit", "5"}, "Launch Group"},
		{"messages", []string{"--db", dbPath, "messages", "--chat", "123@g.us", "--asc"}, "launch now"},
		{"search", []string{"--db", dbPath, "search", "--limit", "5", "launch"}, "launch now"},
		{"search flags after query", []string{"--db", dbPath, "search", "launch", "--limit", "5"}, "launch now"},
		{"open", []string{"--db", dbPath, "open", "wacrawl:msg/group-image"}, "launch now"},
		{"sql", []string{"--db", dbPath, "sql", "SELECT count(*) AS messages FROM messages"}, "messages"},
		{"json", []string{"--db", dbPath, "--json", "search", "launch"}, `"ref": "wacrawl:msg/group-image"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if err := Run(ctx, tc.args, &stdout, &stderr); err != nil {
				t.Fatalf("Run() error = %v stderr=%s", err, stderr.String())
			}
			if !strings.Contains(stdout.String(), tc.want) {
				t.Fatalf("stdout missing %q:\n%s", tc.want, stdout.String())
			}
		})
	}
}

func TestRunSQLJSONAndReadOnlyValidation(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "archive.db")
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceAll(ctx, store.ImportStats{}, nil, []store.Chat{
		{JID: "123@g.us", Kind: "group", Name: "Launch Group", MessageCount: 2},
	}, nil, nil, []store.Message{
		{SourcePK: 1, ChatJID: "123@g.us", ChatName: "Launch Group", MessageID: "m1", Text: "launch now"},
		{SourcePK: 2, ChatJID: "123@g.us", ChatName: "Launch Group", MessageID: "m2", Text: "ship later"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := Run(ctx, []string{"--db", dbPath, "--json", "sql", "SELECT chat_jid, count(*) AS messages FROM messages GROUP BY chat_jid"}, &stdout, &stderr); err != nil {
		t.Fatalf("sql json: %v stderr=%s", err, stderr.String())
	}
	var rows []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &rows); err != nil {
		t.Fatalf("json = %s err=%v", stdout.String(), err)
	}
	if len(rows) != 1 || rows[0]["chat_jid"] != "123@g.us" || rows[0]["messages"] != float64(2) {
		t.Fatalf("rows = %#v", rows)
	}

	stdout.Reset()
	stderr.Reset()
	err = Run(ctx, []string{"--db", dbPath, "sql", "DELETE FROM messages"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), readOnlySelectError) {
		t.Fatalf("expected read-only select error, got %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	err = Run(ctx, []string{"--db", dbPath, "sql", "SELECT count(*) FROM messages; SELECT count(*) FROM chats"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "only a single read-only select statement is allowed") {
		t.Fatalf("expected single statement error, got %v", err)
	}

	invalidDBPath := filepath.Join(t.TempDir(), "archive.db")
	err = Run(ctx, []string{"--db", invalidDBPath, "sql", "DELETE FROM messages"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), readOnlySelectError) {
		t.Fatalf("expected read-only select error, got %v", err)
	}
	if _, statErr := os.Stat(invalidDBPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("invalid SQL created archive: %v", statErr)
	}
}

func TestContactsExportUsesContractShapeAndSkipsUnsafeNames(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "archive.db")
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	contacts := []store.Contact{
		{JID: "safe@s.whatsapp.net", Phone: "+15550100", FullName: "Safe Person"},
		{JID: "safe-duplicate@s.whatsapp.net", Phone: "+15550100", FullName: "Safe Person"},
		{JID: "business@s.whatsapp.net", Phone: "+15550101", BusinessName: "Business Name"},
		{JID: "first-last@s.whatsapp.net", Phone: "+15550102", FirstName: "First", LastName: "Last"},
		{JID: "username@s.whatsapp.net", Phone: "+15550103", Username: "handle", FullName: "@handle"},
		{JID: "phone@s.whatsapp.net", Phone: "+15550104", FullName: "+15550104"},
		{JID: "jid@s.whatsapp.net", Phone: "+15550105", FullName: "jid@s.whatsapp.net"},
		{JID: "case-jid@s.whatsapp.net", Phone: "+15550107", FullName: "CASE-JID@S.WHATSAPP.NET"},
		{JID: "blank@s.whatsapp.net", Phone: "+15550106"},
		{JID: "missing-phone@s.whatsapp.net", FullName: "Missing Phone"},
	}
	if err := st.ReplaceAll(ctx, store.ImportStats{}, contacts, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := Run(ctx, []string{"--db", dbPath, "--json", "contacts", "export"}, &stdout, &stderr); err != nil {
		t.Fatalf("contacts export: %v stderr=%s", err, stderr.String())
	}
	var payload struct {
		Contacts []struct {
			DisplayName  string   `json:"display_name"`
			PhoneNumbers []string `json:"phone_numbers"`
			JID          string   `json:"jid"`
			Username     string   `json:"username"`
		} `json:"contacts"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("json = %s err=%v", stdout.String(), err)
	}
	assertContactExportKeys(t, stdout.Bytes())
	gotNames := make([]string, 0, len(payload.Contacts))
	for _, contact := range payload.Contacts {
		gotNames = append(gotNames, contact.DisplayName)
		if contact.JID != "" || contact.Username != "" {
			t.Fatalf("leaked source fields = %#v", contact)
		}
		if len(contact.PhoneNumbers) != 1 {
			t.Fatalf("bad phone numbers = %#v", contact)
		}
	}
	wantNames := []string{"Business Name", "First Last", "Safe Person"}
	if !reflect.DeepEqual(gotNames, wantNames) {
		t.Fatalf("names = %#v, want %#v", gotNames, wantNames)
	}
}

func assertContactExportKeys(t *testing.T, data []byte) {
	t.Helper()
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	contactsJSON, ok := root["contacts"]
	if !ok || len(root) != 1 {
		t.Fatalf("root keys = %#v, want only contacts", root)
	}
	var contacts []map[string]json.RawMessage
	if err := json.Unmarshal(contactsJSON, &contacts); err != nil {
		t.Fatal(err)
	}
	for _, contact := range contacts {
		if _, ok := contact["display_name"]; !ok {
			t.Fatalf("contact keys = %#v, missing display_name", contact)
		}
		if _, ok := contact["phone_numbers"]; !ok {
			t.Fatalf("contact keys = %#v, missing phone_numbers", contact)
		}
		if len(contact) != 2 {
			t.Fatalf("contact keys = %#v, want only display_name and phone_numbers", contact)
		}
	}
}

func assertRootKeys(t *testing.T, data []byte, keys ...string) {
	t.Helper()
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("json = %s err=%v", string(data), err)
	}
	want := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		want[key] = struct{}{}
		if _, ok := root[key]; !ok {
			t.Fatalf("root keys = %#v, missing %s", root, key)
		}
	}
	if len(root) != len(want) {
		t.Fatalf("root keys = %#v, want %#v", root, keys)
	}
}

func assertSearchResultKeys(t *testing.T, data []byte) {
	t.Helper()
	var root struct {
		Results []map[string]json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("json = %s err=%v", string(data), err)
	}
	if len(root.Results) == 0 {
		t.Fatal("search results are empty")
	}
	want := []string{"ref", "time", "who", "where", "snippet"}
	for _, result := range root.Results {
		assertRawMapKeys(t, result, want...)
	}
}

func assertRawMapKeys(t *testing.T, got map[string]json.RawMessage, keys ...string) {
	t.Helper()
	want := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		want[key] = struct{}{}
		if _, ok := got[key]; !ok {
			t.Fatalf("keys = %#v, missing %s", got, key)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("keys = %#v, want %#v", got, keys)
	}
}

func assertNoRawFields(t *testing.T, data []byte) {
	t.Helper()
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("json = %s err=%v", string(data), err)
	}
	raw := map[string]struct{}{
		"source_pk":  {},
		"chat_jid":   {},
		"sender_jid": {},
		"message_id": {},
		"from_me":    {},
		"raw_type":   {},
		"timestamp":  {},
		"media_path": {},
		"media_url":  {},
	}
	assertNoRawFieldValue(t, value, raw)
}

func assertNoRawFieldValue(t *testing.T, value any, raw map[string]struct{}) {
	t.Helper()
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			if _, ok := raw[key]; ok {
				t.Fatalf("raw field %q leaked in %#v", key, typed)
			}
			assertNoRawFieldValue(t, nested, raw)
		}
	case []any:
		for _, nested := range typed {
			assertNoRawFieldValue(t, nested, raw)
		}
	}
}

func TestMetadataAdvertisesContactExport(t *testing.T) {
	manifest := controlManifest()
	if manifest.DisplayName != "WhatsApp" {
		t.Fatalf("display_name = %q, want WhatsApp", manifest.DisplayName)
	}
	if !hasCapability(manifest.Capabilities, "open") {
		t.Fatalf("capabilities = %#v, missing open", manifest.Capabilities)
	}
	if !hasCapability(manifest.Capabilities, "contacts_export") {
		t.Fatalf("capabilities = %#v, missing contacts_export", manifest.Capabilities)
	}
	if !hasCapability(manifest.Capabilities, "who") {
		t.Fatalf("capabilities = %#v, missing who", manifest.Capabilities)
	}
	if !hasCapability(manifest.Capabilities, "short_refs") {
		t.Fatalf("capabilities = %#v, missing short_refs", manifest.Capabilities)
	}
	openCommand, ok := manifest.Commands["open"]
	if !ok {
		t.Fatalf("commands = %#v", manifest.Commands)
	}
	if openCommand.Mutates || !openCommand.JSON {
		t.Fatalf("open command = %#v", openCommand)
	}
	openWant := []string{"wacrawl", "--json", "open", "REF"}
	if !reflect.DeepEqual(openCommand.Argv, openWant) {
		t.Fatalf("open argv = %#v, want %#v", openCommand.Argv, openWant)
	}
	webCommand, ok := manifest.Commands["web"]
	if !ok {
		t.Fatalf("commands = %#v", manifest.Commands)
	}
	if webCommand.Mutates || webCommand.JSON {
		t.Fatalf("web command = %#v", webCommand)
	}
	webWant := []string{"wacrawl", "web"}
	if !reflect.DeepEqual(webCommand.Argv, webWant) {
		t.Fatalf("web argv = %#v, want %#v", webCommand.Argv, webWant)
	}

	sqlCommand, ok := manifest.Commands["sql"]
	if !ok {
		t.Fatalf("commands = %#v", manifest.Commands)
	}
	if sqlCommand.Mutates || !sqlCommand.JSON {
		t.Fatalf("sql command = %#v", sqlCommand)
	}
	sqlWant := []string{"wacrawl", "--json", "sql"}
	if !reflect.DeepEqual(sqlCommand.Argv, sqlWant) {
		t.Fatalf("sql argv = %#v, want %#v", sqlCommand.Argv, sqlWant)
	}

	command, ok := manifest.Commands["contact-export"]
	if !ok {
		t.Fatalf("commands = %#v", manifest.Commands)
	}
	if command.Mutates || !command.JSON {
		t.Fatalf("contact-export command = %#v", command)
	}
	want := []string{"wacrawl", "--json", "contacts", "export"}
	if !reflect.DeepEqual(command.Argv, want) {
		t.Fatalf("argv = %#v, want %#v", command.Argv, want)
	}
}

func hasCapability(capabilities []string, want string) bool {
	for _, capability := range capabilities {
		if capability == want {
			return true
		}
	}
	return false
}

type statusCountPayload struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Value any    `json:"value"`
}

func TestStatusJSONUsesContractEnvelope(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "archive.db")
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	imported := time.Date(2026, 7, 2, 14, 3, 11, 0, time.UTC)
	if err := st.ReplaceAll(
		ctx,
		store.ImportStats{SourcePath: "/synthetic", DBPath: dbPath, FinishedAt: imported},
		nil,
		[]store.Chat{{JID: "chat@g.us", Kind: "group", Name: "Launch Group", LastMessageAt: imported, MessageCount: 1}},
		nil,
		nil,
		[]store.Message{{SourcePK: 1, ChatJID: "chat@g.us", ChatName: "Launch Group", MessageID: "m1", Timestamp: time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC), RawType: 0, Text: "hello"}},
	); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := Run(ctx, []string{"--db", dbPath, "--json", "status"}, &stdout, &stderr); err != nil {
		t.Fatalf("status error = %v stderr=%s", err, stderr.String())
	}
	var payload struct {
		AppID     string `json:"app_id"`
		State     string `json:"state"`
		Summary   string `json:"summary"`
		Freshness struct {
			LastSync string `json:"last_sync"`
		} `json:"freshness"`
		Counts []statusCountPayload `json:"counts"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("status json = %s err=%v", stdout.String(), err)
	}
	if payload.AppID != "wacrawl" || payload.State != "ok" || payload.Summary == "" {
		t.Fatalf("payload = %#v", payload)
	}
	if _, err := time.Parse(time.RFC3339, payload.Freshness.LastSync); err != nil {
		t.Fatalf("last_sync = %q err=%v", payload.Freshness.LastSync, err)
	}
	if got := statusCountIDs(payload.Counts); !reflect.DeepEqual(got, []string{"messages", "chats", "since"}) {
		t.Fatalf("status count ids = %#v", got)
	}
	if payload.Counts[2].Value != float64(2020) {
		t.Fatalf("since count = %#v", payload.Counts[2])
	}
}

func TestDoctorJSONUsesContractChecks(t *testing.T) {
	ctx := context.Background()
	source := t.TempDir()
	createDesktopFixture(t, source)
	dbPath := filepath.Join(t.TempDir(), "archive.db")

	var stdout, stderr bytes.Buffer
	if err := Run(ctx, []string{"--db", dbPath, "--source", source, "--json", "doctor"}, &stdout, &stderr); err != nil {
		t.Fatalf("doctor error = %v stderr=%s", err, stderr.String())
	}
	var payload struct {
		Checks []struct {
			ID      string `json:"id"`
			State   string `json:"state"`
			Message string `json:"message"`
			Remedy  string `json:"remedy"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("doctor json = %s err=%v", stdout.String(), err)
	}
	checks := map[string]string{}
	for _, check := range payload.Checks {
		checks[check.ID] = check.State
		if check.State != "ok" && check.Remedy == "" {
			t.Fatalf("failing check lacks remedy: %#v", check)
		}
	}
	if checks["source_store"] != "ok" || checks["archive"] != "missing" || checks["full_disk_access"] != "ok" {
		t.Fatalf("checks = %#v payload=%#v", checks, payload.Checks)
	}
}

func TestMessagesAndSearchReportTruncation(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "archive.db")
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	messages := []store.Message{
		{SourcePK: 1, ChatJID: "chat@g.us", ChatName: "Launch Group", MessageID: "m1", Timestamp: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC), RawType: 0, Text: "launch one"},
		{SourcePK: 2, ChatJID: "chat@g.us", ChatName: "Launch Group", MessageID: "m2", Timestamp: time.Date(2026, 1, 1, 10, 1, 0, 0, time.UTC), RawType: 0, Text: "launch two"},
	}
	if err := st.ReplaceAll(ctx, store.ImportStats{}, nil, []store.Chat{{JID: "chat@g.us", Kind: "group", Name: "Launch Group", MessageCount: len(messages)}}, nil, nil, messages); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := Run(ctx, []string{"--db", dbPath, "--json", "messages", "--limit", "1"}, &stdout, &stderr); err != nil {
		t.Fatalf("messages error = %v stderr=%s", err, stderr.String())
	}
	var messagesPayload struct {
		Returned  int             `json:"returned"`
		Limit     int             `json:"limit"`
		Truncated bool            `json:"truncated"`
		Messages  []store.Message `json:"results"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &messagesPayload); err != nil {
		t.Fatalf("messages json = %s err=%v", stdout.String(), err)
	}
	if messagesPayload.Returned != 1 || messagesPayload.Limit != 1 || !messagesPayload.Truncated || len(messagesPayload.Messages) != 1 {
		t.Fatalf("messages payload = %#v", messagesPayload)
	}

	stdout.Reset()
	stderr.Reset()
	if err := Run(ctx, []string{"--db", dbPath, "search", "--limit", "1", "launch"}, &stdout, &stderr); err != nil {
		t.Fatalf("search error = %v stderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "showing 1 of 2 matches") {
		t.Fatalf("search should report truncation:\n%s", stdout.String())
	}
}

func TestSearchJSONUsesContractEnvelopeAndStableRefs(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "archive.db")
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	messages := []store.Message{
		{SourcePK: 1, ChatJID: "chat@g.us", ChatName: "Launch Group", MessageID: "stable-1", SenderJID: "alice@s.whatsapp.net", SenderName: "Alice", Timestamp: now, RawType: 0, MessageType: "text", Text: "launch alpha"},
		{SourcePK: 2, ChatJID: "chat@g.us", ChatName: "Launch Group", MessageID: "stable-2", SenderJID: "bob@s.whatsapp.net", SenderName: "Bob", Timestamp: now.Add(time.Minute), RawType: 0, MessageType: "text", Text: "launch beta"},
		{SourcePK: 3, ChatJID: "chat@g.us", ChatName: "Launch Group", MessageID: "other", SenderJID: "bob@s.whatsapp.net", SenderName: "Bob", Timestamp: now.Add(2 * time.Minute), RawType: 0, MessageType: "text", Text: "ship later"},
	}
	if err := st.ReplaceAll(ctx, store.ImportStats{}, nil, []store.Chat{{JID: "chat@g.us", Kind: "group", Name: "Launch Group", MessageCount: len(messages)}}, nil, nil, messages); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := Run(ctx, []string{"--db", dbPath, "--json", "search", "--limit", "1", "launch"}, &stdout, &stderr); err != nil {
		t.Fatalf("search error = %v stderr=%s", err, stderr.String())
	}
	assertNoRawFields(t, stdout.Bytes())
	assertRootKeys(t, stdout.Bytes(), "query", "results", "total_matches", "truncated")
	assertSearchResultKeys(t, stdout.Bytes())
	var payload searchEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("search json = %s err=%v", stdout.String(), err)
	}
	// All three fixture messages live in "Launch Group", and the FTS
	// index covers chat names, so "launch" matches all of them.
	if payload.Query != "launch" || payload.TotalMatches != 3 || !payload.Truncated || len(payload.Results) != 1 {
		t.Fatalf("payload = %#v", payload)
	}
	result := payload.Results[0]
	if !strings.HasPrefix(result.Ref, messageRefPrefix) || result.Time == "" || result.Who == "" || result.Where != "Launch Group" || result.Snippet == "" {
		t.Fatalf("result = %#v", result)
	}
	if strings.ContainsAny(result.Who+result.Where+result.Snippet, "\n\t") || strings.ContainsAny(result.Snippet, "[]") || strings.Contains(result.Snippet, "...") {
		t.Fatalf("search result kept marker or multiline fields = %#v", result)
	}
	if _, err := time.Parse(time.RFC3339, result.Time); err != nil {
		t.Fatalf("time = %q err=%v", result.Time, err)
	}

	stdout.Reset()
	stderr.Reset()
	if err := Run(ctx, []string{"--db", dbPath, "--json", "search", "alpha"}, &stdout, &stderr); err != nil {
		t.Fatalf("stable search error = %v stderr=%s", err, stderr.String())
	}
	var stable searchEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &stable); err != nil {
		t.Fatalf("stable search json = %s err=%v", stdout.String(), err)
	}
	if len(stable.Results) != 1 || stable.Results[0].Ref != "wacrawl:msg/stable-1" {
		t.Fatalf("stable ref = %#v", stable.Results)
	}
}

func TestSearchWhoFiltersAndKeepsFilteredTotals(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "archive.db")
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	contacts := []store.Contact{
		{JID: "bob@s.whatsapp.net", FullName: "Bob Example"},
		{JID: "alice@s.whatsapp.net", FullName: "Alice Example"},
		{JID: "other@s.whatsapp.net", FullName: "Other Person"},
	}
	chats := []store.Chat{
		{JID: "bob@s.whatsapp.net", Kind: "dm", Name: "Bob Example", LastMessageAt: now, MessageCount: 2},
		{JID: "team@g.us", Kind: "group", Name: "Team", LastMessageAt: now, MessageCount: 1},
		{JID: "other@s.whatsapp.net", Kind: "dm", Name: "Other Person", LastMessageAt: now, MessageCount: 1},
	}
	participants := []store.GroupParticipant{
		{GroupJID: "team@g.us", UserJID: "alice@s.whatsapp.net", ContactName: "Alice Example", IsActive: true},
	}
	messages := []store.Message{
		{SourcePK: 1, ChatJID: "bob@s.whatsapp.net", ChatName: "Bob Example", MessageID: "bob-in", SenderJID: "bob@s.whatsapp.net", SenderName: "Bob Example", Timestamp: now, RawType: 0, MessageType: "text", Text: "needle incoming"},
		{SourcePK: 2, ChatJID: "bob@s.whatsapp.net", ChatName: "Bob Example", MessageID: "bob-out", SenderJID: "bob@s.whatsapp.net", SenderName: "me", Timestamp: now.Add(time.Minute), FromMe: true, RawType: 0, MessageType: "text", Text: "needle outgoing"},
		{SourcePK: 3, ChatJID: "team@g.us", ChatName: "Team", MessageID: "group", SenderJID: "other@s.whatsapp.net", SenderName: "Other Person", Timestamp: now.Add(2 * time.Minute), RawType: 0, MessageType: "text", Text: "needle group"},
		{SourcePK: 4, ChatJID: "other@s.whatsapp.net", ChatName: "Other Person", MessageID: "other", SenderJID: "other@s.whatsapp.net", SenderName: "Other Person", Timestamp: now.Add(3 * time.Minute), RawType: 0, MessageType: "text", Text: "needle other"},
	}
	if err := st.ReplaceAll(ctx, store.ImportStats{}, contacts, chats, nil, participants, messages); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := Run(ctx, []string{"--db", dbPath, "--json", "search", "needle", "--who", "  bob \t example  ", "--limit", "1"}, &stdout, &stderr); err != nil {
		t.Fatalf("search error = %v stderr=%s", err, stderr.String())
	}
	assertRootKeys(t, stdout.Bytes(), "query", "results", "total_matches", "truncated")
	var payload searchEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("search json = %s err=%v", stdout.String(), err)
	}
	if payload.TotalMatches != 2 || !payload.Truncated || len(payload.Results) != 1 || payload.WhoMatched != nil {
		t.Fatalf("payload = %#v", payload)
	}

	stdout.Reset()
	stderr.Reset()
	if err := Run(ctx, []string{"--db", dbPath, "--json", "search", "--who", "ALICE EXAMPLE", "needle"}, &stdout, &stderr); err != nil {
		t.Fatalf("search error = %v stderr=%s", err, stderr.String())
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("search json = %s err=%v", stdout.String(), err)
	}
	if payload.TotalMatches != 1 || len(payload.Results) != 1 || payload.Results[0].Ref != "wacrawl:msg/group" {
		t.Fatalf("group participant payload = %#v", payload)
	}
}

func TestSearchWhoMatchedReportsAmbiguousParticipants(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "archive.db")
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	contacts := []store.Contact{
		{JID: "casey-one@s.whatsapp.net", FullName: "Casey Example"},
		{JID: "casey-two@s.whatsapp.net", FullName: "casey example"},
	}
	chats := []store.Chat{
		{JID: "casey-one@s.whatsapp.net", Kind: "dm", Name: "Casey Example", LastMessageAt: now, MessageCount: 1},
		{JID: "casey-two@s.whatsapp.net", Kind: "dm", Name: "casey example", LastMessageAt: now, MessageCount: 1},
		{JID: "other@s.whatsapp.net", Kind: "dm", Name: "Other Person", LastMessageAt: now, MessageCount: 1},
	}
	messages := []store.Message{
		{SourcePK: 1, ChatJID: "casey-one@s.whatsapp.net", ChatName: "Casey Example", MessageID: "casey-one", SenderJID: "casey-one@s.whatsapp.net", SenderName: "Casey Example", Timestamp: now, RawType: 0, MessageType: "text", Text: "needle one"},
		{SourcePK: 2, ChatJID: "casey-two@s.whatsapp.net", ChatName: "casey example", MessageID: "casey-two", SenderJID: "casey-two@s.whatsapp.net", SenderName: "casey example", Timestamp: now.Add(time.Minute), RawType: 0, MessageType: "text", Text: "needle two"},
		{SourcePK: 3, ChatJID: "other@s.whatsapp.net", ChatName: "Other Person", MessageID: "other", SenderJID: "other@s.whatsapp.net", SenderName: "Other Person", Timestamp: now.Add(2 * time.Minute), RawType: 0, MessageType: "text", Text: "needle other"},
	}
	if err := st.ReplaceAll(ctx, store.ImportStats{}, contacts, chats, nil, nil, messages); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := Run(ctx, []string{"--db", dbPath, "--json", "search", "--who", "CASEY EXAMPLE", "needle"}, &stdout, &stderr); err != nil {
		t.Fatalf("search error = %v stderr=%s", err, stderr.String())
	}
	assertRootKeys(t, stdout.Bytes(), "query", "who_matched", "results", "total_matches", "truncated")
	var payload searchEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("search json = %s err=%v", stdout.String(), err)
	}
	wantMatched := []string{"Casey Example", "casey example"}
	if !reflect.DeepEqual(payload.WhoMatched, wantMatched) || payload.TotalMatches != 2 || payload.Truncated || len(payload.Results) != 2 {
		t.Fatalf("payload = %#v, want who_matched %#v and 2 results", payload, wantMatched)
	}
}

func TestOpenJSONRoundTripsSearchRef(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "archive.db")
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 2, 1, 9, 0, 0, 0, time.UTC)
	messages := make([]store.Message, 0, 26)
	for i := 0; i < 25; i++ {
		text := fmt.Sprintf("context line %02d", i)
		if i == 12 {
			text = "roundtrip target"
		}
		fromMe := i%2 == 1
		senderName := "Alice"
		if fromMe {
			senderName = "me"
		}
		messages = append(messages, store.Message{
			SourcePK:    int64(i + 1),
			ChatJID:     "chat@g.us",
			ChatName:    "Launch Group",
			MessageID:   fmt.Sprintf("msg-%02d", i),
			SenderJID:   "alice@s.whatsapp.net",
			SenderName:  senderName,
			Timestamp:   now.Add(time.Duration(i) * time.Minute),
			FromMe:      fromMe,
			Text:        text,
			RawType:     0,
			MessageType: "text",
		})
	}
	messages = append(messages, store.Message{
		SourcePK:    100,
		ChatJID:     "other@g.us",
		ChatName:    "Other Chat",
		MessageID:   "other-target",
		SenderName:  "Eve",
		Timestamp:   now,
		Text:        "roundtrip other chat",
		RawType:     0,
		MessageType: "text",
	})
	chats := []store.Chat{
		{JID: "chat@g.us", Kind: "group", Name: "Launch Group", MessageCount: 25},
		{JID: "other@g.us", Kind: "group", Name: "Other Chat", MessageCount: 1},
	}
	if err := st.ReplaceAll(ctx, store.ImportStats{}, nil, chats, nil, nil, messages); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := Run(ctx, []string{"--db", dbPath, "--json", "search", "target", "--chat", "chat@g.us"}, &stdout, &stderr); err != nil {
		t.Fatalf("search error = %v stderr=%s", err, stderr.String())
	}
	var search searchEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &search); err != nil {
		t.Fatalf("search json = %s err=%v", stdout.String(), err)
	}
	if len(search.Results) != 1 {
		t.Fatalf("search results = %#v", search.Results)
	}
	ref := search.Results[0].Ref

	stdout.Reset()
	stderr.Reset()
	if err := Run(ctx, []string{"--db", dbPath, "--json", "open", ref}, &stdout, &stderr); err != nil {
		t.Fatalf("open error = %v stderr=%s", err, stderr.String())
	}
	assertNoRawFields(t, stdout.Bytes())
	var payload openEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("open json = %s err=%v", stdout.String(), err)
	}
	if payload.Ref != ref || payload.Chat != "Launch Group" || payload.Message.Ref != ref || payload.Message.Text != "roundtrip target" {
		t.Fatalf("open payload = %#v", payload)
	}
	if payload.Window.Before != 10 || payload.Window.After != 10 || len(payload.Context) != 21 {
		t.Fatalf("window = %#v context=%d", payload.Window, len(payload.Context))
	}
	if payload.Context[0].Ref != "wacrawl:msg/msg-02" || payload.Context[len(payload.Context)-1].Ref != "wacrawl:msg/msg-22" {
		t.Fatalf("context bounds = %#v ... %#v", payload.Context[0], payload.Context[len(payload.Context)-1])
	}
	current := 0
	for _, item := range payload.Context {
		if item.Where != "Launch Group" {
			t.Fatalf("context leaked another chat: %#v", item)
		}
		if _, err := time.Parse(time.RFC3339, item.Time); err != nil {
			t.Fatalf("time = %q err=%v", item.Time, err)
		}
		if item.Current {
			current++
		}
	}
	if current != 1 {
		t.Fatalf("current markers = %d", current)
	}
}

func TestOpenAcceptsShortRefAndSearchJSONKeepsFullRef(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "archive.db")
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 2, 1, 9, 0, 0, 0, time.UTC)
	if err := st.ReplaceAll(ctx, store.ImportStats{}, nil, []store.Chat{
		{JID: "chat@g.us", Kind: "group", Name: "Launch Group", MessageCount: 1},
	}, nil, nil, []store.Message{
		{SourcePK: 1, ChatJID: "chat@g.us", ChatName: "Launch Group", MessageID: "short-target", SenderName: "Alice", Timestamp: now, RawType: 0, Text: "shortref target"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := Run(ctx, []string{"--db", dbPath, "search", "shortref"}, &stdout, &stderr); err != nil {
		t.Fatalf("search error = %v stderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "full_ref: wacrawl:msg/short-target") {
		t.Fatalf("human search should print canonical full ref:\n%s", stdout.String())
	}
	alias := searchOutputRef(stdout.String())
	if alias == "" || strings.HasPrefix(alias, "wacrawl:") {
		t.Fatalf("human search ref should be a short alias, got %q in:\n%s", alias, stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if err := Run(ctx, []string{"--db", dbPath, "--json", "search", "shortref"}, &stdout, &stderr); err != nil {
		t.Fatalf("json search error = %v stderr=%s", err, stderr.String())
	}
	assertSearchResultKeys(t, stdout.Bytes())
	var search searchEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &search); err != nil {
		t.Fatalf("search json = %s err=%v", stdout.String(), err)
	}
	if len(search.Results) != 1 || search.Results[0].Ref != "wacrawl:msg/short-target" || search.Results[0].Alias != "" {
		t.Fatalf("search refs = %#v", search.Results)
	}

	stdout.Reset()
	stderr.Reset()
	if err := Run(ctx, []string{"--db", dbPath, "--json", "open", alias}, &stdout, &stderr); err != nil {
		t.Fatalf("open alias error = %v stderr=%s", err, stderr.String())
	}
	var opened openEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &opened); err != nil {
		t.Fatalf("open json = %s err=%v", stdout.String(), err)
	}
	if opened.Ref != "wacrawl:msg/short-target" || opened.Message.Text != "shortref target" {
		t.Fatalf("opened payload = %#v", opened)
	}
}

func TestOpenShortRefErrorsUseContractCodes(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "archive.db")
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceAll(ctx, store.ImportStats{}, nil, []store.Chat{
		{JID: "chat@g.us", Kind: "group", Name: "Launch Group", MessageCount: 2},
	}, nil, nil, []store.Message{
		{SourcePK: 1, ChatJID: "chat@g.us", ChatName: "Launch Group", MessageID: "a", Text: "alpha"},
		{SourcePK: 2, ChatJID: "chat@g.us", ChatName: "Launch Group", MessageID: "b", Text: "beta"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`insert into short_refs(alias, full_ref) values('22222', 'wacrawl:msg/a'), ('22222', 'wacrawl:msg/b')`); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	err = Run(ctx, []string{"--db", dbPath, "--json", "open", "zzzzz"}, &stdout, &stderr)
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("expected unknown short ref error, got %v", err)
	}
	var payload errorEnvelope
	if jsonErr := json.Unmarshal(stdout.Bytes(), &payload); jsonErr != nil {
		t.Fatalf("unknown error json = %s err=%v", stdout.String(), jsonErr)
	}
	if payload.Error.Code != "unknown_short_ref" {
		t.Fatalf("unknown error payload = %#v", payload)
	}

	stdout.Reset()
	stderr.Reset()
	err = Run(ctx, []string{"--db", dbPath, "--json", "open", "22222"}, &stdout, &stderr)
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("expected ambiguous short ref error, got %v", err)
	}
	if jsonErr := json.Unmarshal(stdout.Bytes(), &payload); jsonErr != nil {
		t.Fatalf("ambiguous error json = %s err=%v", stdout.String(), jsonErr)
	}
	if payload.Error.Code != "ambiguous_short_ref" {
		t.Fatalf("ambiguous error payload = %#v", payload)
	}
}

func TestOwnerIdentityRendersAsMe(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "archive.db")
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceAll(ctx, store.ImportStats{}, nil, []store.Chat{
		{JID: "friend@s.whatsapp.net", Kind: "dm", Name: "Friend", MessageCount: 1},
	}, nil, nil, []store.Message{
		{SourcePK: 1, ChatJID: "friend@s.whatsapp.net", ChatName: "Friend", MessageID: "mine", SenderJID: "owner@s.whatsapp.net", SenderName: "owner@s.whatsapp.net", FromMe: true, Text: "owner needle"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := Run(ctx, []string{"--db", dbPath, "--json", "search", "owner"}, &stdout, &stderr); err != nil {
		t.Fatalf("search error = %v stderr=%s", err, stderr.String())
	}
	var search searchEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &search); err != nil {
		t.Fatalf("search json = %s err=%v", stdout.String(), err)
	}
	if len(search.Results) != 1 || search.Results[0].Who != "me" {
		t.Fatalf("search owner rendering = %#v", search.Results)
	}

	stdout.Reset()
	stderr.Reset()
	if err := Run(ctx, []string{"--db", dbPath, "--json", "open", "wacrawl:msg/mine"}, &stdout, &stderr); err != nil {
		t.Fatalf("open error = %v stderr=%s", err, stderr.String())
	}
	var opened openEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &opened); err != nil {
		t.Fatalf("open json = %s err=%v", stdout.String(), err)
	}
	if opened.Message.Who != "me" || opened.Context[0].Who != "me" {
		t.Fatalf("open owner rendering = %#v", opened)
	}
}

func TestStatusAndDoctorReadLogTail(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "archive.db")
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceAll(ctx, store.ImportStats{}, nil, []store.Chat{
		{JID: "chat@g.us", Kind: "group", Name: "Launch Group", MessageCount: 1},
	}, nil, nil, []store.Message{
		{SourcePK: 1, ChatJID: "chat@g.us", ChatName: "Launch Group", MessageID: "a", Text: "alpha"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	err = Run(ctx, []string{"--db", dbPath, "--json", "open", "zzzzz"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected open to fail")
	}

	stdout.Reset()
	stderr.Reset()
	if err := Run(ctx, []string{"--db", dbPath, "--json", "status"}, &stdout, &stderr); err != nil {
		t.Fatalf("status error = %v stderr=%s", err, stderr.String())
	}
	var status statusEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
		t.Fatalf("status json = %s err=%v", stdout.String(), err)
	}
	if status.LastRun == nil || status.LastRun.Command != "open" || status.LastRun.Outcome != "error" {
		t.Fatalf("status last run = %#v", status.LastRun)
	}
	if status.Error == nil || status.Error.Event != "unknown_short_ref" || status.Error.Remedy == "" {
		t.Fatalf("status recent error = %#v", status.Error)
	}

	stdout.Reset()
	stderr.Reset()
	if err := Run(ctx, []string{"--db", dbPath, "--json", "doctor"}, &stdout, &stderr); err != nil {
		t.Fatalf("doctor error = %v stderr=%s", err, stderr.String())
	}
	var doctor doctorEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &doctor); err != nil {
		t.Fatalf("doctor json = %s err=%v", stdout.String(), err)
	}
	if doctor.LastRun == nil || doctor.LastRun.Command != "status" || doctor.LastRun.Outcome != "success" {
		t.Fatalf("doctor last run = %#v", doctor.LastRun)
	}
	if doctor.Error == nil || doctor.Error.Event != "unknown_short_ref" || doctor.Error.Remedy == "" {
		t.Fatalf("doctor recent error = %#v", doctor.Error)
	}
}

func searchOutputRef(output string) string {
	for _, line := range strings.Split(output, "\n") {
		if value, ok := strings.CutPrefix(line, "ref: "); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func TestOpenRejectsForeignRefWithContractError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"--json", "open", "imsgcrawl:msg/abc"}, &stdout, &stderr)
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("expected foreign ref exit 1, got err=%v code=%d", err, ExitCode(err))
	}
	assertRootKeys(t, stdout.Bytes(), "error")
	var payload errorEnvelope
	if jsonErr := json.Unmarshal(stdout.Bytes(), &payload); jsonErr != nil {
		t.Fatalf("error json = %s err=%v", stdout.String(), jsonErr)
	}
	if payload.Error.Code != "foreign_ref" || payload.Error.Message == "" || payload.Error.Remedy == "" {
		t.Fatalf("error payload = %#v", payload)
	}
}

func TestRunUsageErrors(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), nil, &stdout, &stderr); err != nil {
		t.Fatalf("empty args should print help: %v", err)
	}
	err := Run(context.Background(), []string{"bogus"}, &stdout, &stderr)
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("expected usage exit, got err=%v code=%d", err, ExitCode(err))
	}
	if ExitCode(nil) != 0 {
		t.Fatal("nil exit code should be zero")
	}
	if ExitCode(errors.New("plain")) != 1 {
		t.Fatal("plain error exit code should be one")
	}

	err = Run(context.Background(), []string{"messages", "--from-me", "--from-them"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected mutually exclusive error, got %v", err)
	}
	err = Run(context.Background(), []string{"messages", "--after", "nope"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "invalid time") {
		t.Fatalf("expected invalid time error, got %v", err)
	}
	err = Run(context.Background(), []string{"messages", "--limit", "201"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "--limit must be between") {
		t.Fatalf("expected message limit error, got %v", err)
	}
	err = Run(context.Background(), []string{"search"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "exactly one query") {
		t.Fatalf("expected query error, got %v", err)
	}
	err = Run(context.Background(), []string{"open"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "exactly one ref") {
		t.Fatalf("expected open ref error, got %v", err)
	}
	err = Run(context.Background(), []string{"search", "--limit", "0", "query"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "--limit must be between") {
		t.Fatalf("expected search limit error, got %v", err)
	}
	err = Run(context.Background(), []string{"search", "query", "--who", " \t "}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "--who requires an identity") {
		t.Fatalf("expected blank who error, got %v", err)
	}
	err = Run(context.Background(), []string{"doctor", "extra"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "flags only") {
		t.Fatalf("expected doctor args error, got %v", err)
	}
	for _, args := range [][]string{
		{"import", "extra"},
		{"sync", "extra"},
		{"chats", "extra"},
		{"messages", "extra"},
		{"unread", "extra"},
		{"web", "extra"},
	} {
		err = Run(context.Background(), args, &stdout, &stderr)
		if err == nil || !strings.Contains(err.Error(), "flags only") {
			t.Fatalf("expected flags-only error for %v, got %v", args, err)
		}
	}
	err = Run(context.Background(), []string{"--db", "", "status"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "db path is required") {
		t.Fatalf("expected db path error, got %v", err)
	}
	err = Run(context.Background(), []string{"status", "extra"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "flags only") {
		t.Fatalf("expected status args error, got %v", err)
	}
	err = Run(context.Background(), []string{"contacts"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "contacts supports export only") {
		t.Fatalf("expected contacts command error, got %v", err)
	}
	err = Run(context.Background(), []string{"contacts", "import"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "contacts supports export only") {
		t.Fatalf("expected contacts subcommand error, got %v", err)
	}
	err = Run(context.Background(), []string{"contacts", "export", "extra"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "contacts export takes no arguments") {
		t.Fatalf("expected contacts export args error, got %v", err)
	}
	err = Run(context.Background(), []string{"web", "--port", "-1"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "--port must be between") {
		t.Fatalf("expected web port error, got %v", err)
	}
	err = Run(context.Background(), []string{"--json", "web"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "does not support --json") {
		t.Fatalf("expected web json error, got %v", err)
	}
}

func TestRunHelpMenus(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"global short", []string{"--help"}, "Examples:"},
		{"backup help", []string{"backup", "help"}, "wacrawl backup <init|push|pull|status|snapshots>"},
		{"backup topic", []string{"help", "backup"}, "wacrawl backup <init|push|pull|status|snapshots>"},
		{"doctor topic", []string{"help", "doctor"}, "wacrawl doctor [--source PATH]"},
		{"command help topic", []string{"help", "messages"}, "wacrawl messages [flags]"},
		{"doctor flag", []string{"doctor", "--help"}, "wacrawl doctor [--source PATH]"},
		{"status flag", []string{"status", "--help"}, "unread counts"},
		{"chats flag", []string{"chats", "--help"}, "wacrawl chats [--limit N] [--unread]"},
		{"contacts topic", []string{"help", "contacts"}, "wacrawl [--json] contacts export"},
		{"contacts export flag", []string{"contacts", "export", "--help"}, "wacrawl [--json] contacts export"},
		{"unread flag", []string{"unread", "--help"}, "wacrawl unread [--limit N]"},
		{"command flag", []string{"messages", "--help"}, "--has-media"},
		{"search flag", []string{"search", "--help"}, "--who NAME"},
		{"open topic", []string{"help", "open"}, "wacrawl open <ref>"},
		{"open flag", []string{"open", "--help"}, "wacrawl:msg/MESSAGE_ID"},
		{"sql topic", []string{"help", "sql"}, "wacrawl sql <select query>"},
		{"sql flag", []string{"sql", "--help"}, "read-only SQL query"},
		{"web topic", []string{"help", "web"}, "wacrawl web [--port N]"},
		{"web flag", []string{"web", "--help"}, "private web viewer"},
		{"import flag", []string{"import", "--help"}, "--copy-media"},
		{"sync topic", []string{"help", "sync"}, "wacrawl sync [--source PATH]"},
		{"backup flag", []string{"backup", "--help"}, "wacrawl backup <init|push|pull|status|snapshots>"},
		{"backup nested flag", []string{"backup", "init", "--help"}, "wacrawl backup init [flags]"},
		{"backup nested topic", []string{"backup", "help", "push"}, "wacrawl backup push [flags]"},
		{"backup pull topic", []string{"help", "backup", "pull"}, "wacrawl backup pull [flags]"},
		{"backup status topic", []string{"help", "backup", "status"}, "wacrawl backup status [flags]"},
		{"backup snapshots topic", []string{"help", "backup", "snapshots"}, "wacrawl backup snapshots [flags]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if err := Run(context.Background(), tc.args, &stdout, &stderr); err != nil {
				t.Fatalf("Run() error = %v stderr=%s", err, stderr.String())
			}
			if !strings.Contains(stdout.String(), tc.want) {
				t.Fatalf("stdout missing %q:\n%s", tc.want, stdout.String())
			}
		})
	}
	var discard bytes.Buffer
	if printCommandUsage(&discard, "missing") {
		t.Fatal("unknown help topic should return false")
	}
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"help", "missing"}, &stdout, &stderr)
	if err == nil || ExitCode(err) != 2 || !strings.Contains(err.Error(), "unknown help topic") {
		t.Fatalf("expected unknown help topic error, got %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	err = Run(context.Background(), []string{"backup", "help", "missing"}, &stdout, &stderr)
	if err == nil || ExitCode(err) != 2 || !strings.Contains(err.Error(), "unknown backup help topic") {
		t.Fatalf("expected unknown backup help topic error, got %v", err)
	}
}

func TestReadCommandsNeverMutateSourceContent(t *testing.T) {
	ctx := context.Background()
	source := t.TempDir()
	createDesktopFixture(t, source)
	dbPath := filepath.Join(t.TempDir(), "archive.db")

	var stdout, stderr bytes.Buffer
	if err := Run(ctx, []string{"--db", dbPath, "--source", source, "sync"}, &stdout, &stderr); err != nil {
		t.Fatalf("sync error = %v stderr=%s", err, stderr.String())
	}
	// wacrawl keeps derived caches in archive.db: messages_fts, short_refs and
	// the short-ref fingerprint in sync_state. The control contract allows read
	// commands to refresh those caches at point of use, so this test protects the
	// source-content tables instead of the whole SQLite file.
	before := archiveSourceContentChecksum(t, dbPath)

	missingSource := filepath.Join(t.TempDir(), "missing")
	readCommands := [][]string{
		{"status"},
		{"chats", "--limit", "5"},
		{"unread", "--limit", "5"},
		{"messages", "--limit", "2"},
		{"search", "--limit", "2", "launch"},
		{"open", "wacrawl:msg/group-image"},
		{"--json", "contacts", "export"},
	}
	for _, command := range readCommands {
		t.Run(strings.Join(command, " "), func(t *testing.T) {
			stdout.Reset()
			stderr.Reset()
			args := append([]string{"--db", dbPath, "--source", missingSource}, command...)
			if err := Run(ctx, args, &stdout, &stderr); err != nil {
				t.Fatalf("Run(%v) error = %v stderr=%s", args, err, stderr.String())
			}
		})
	}

	after := archiveSourceContentChecksum(t, dbPath)
	if before != after {
		t.Fatalf("read commands changed source-content tables: before=%x after=%x", before, after)
	}
}

func TestNeverSyncedReadPath(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "archive.db")
	missingSource := filepath.Join(t.TempDir(), "missing")

	var stdout, stderr bytes.Buffer
	if err := Run(ctx, []string{"--db", dbPath, "--source", missingSource, "--json", "status"}, &stdout, &stderr); err != nil {
		t.Fatalf("status error = %v stderr=%s", err, stderr.String())
	}
	var status struct {
		AppID   string               `json:"app_id"`
		State   string               `json:"state"`
		Summary string               `json:"summary"`
		Counts  []statusCountPayload `json:"counts"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
		t.Fatalf("status json = %s err=%v", stdout.String(), err)
	}
	if status.AppID != "wacrawl" || status.State != "missing" || !strings.Contains(status.Summary, "wacrawl sync") {
		t.Fatalf("status = %#v", status)
	}
	if len(status.Counts) != 0 {
		t.Fatalf("missing archive should declare no counts, got %#v", status.Counts)
	}

	for _, command := range [][]string{
		{"chats"},
		{"unread"},
		{"messages"},
		{"search", "launch"},
		{"open", "wacrawl:msg/group-image"},
		{"--json", "contacts", "export"},
	} {
		t.Run(strings.Join(command, " "), func(t *testing.T) {
			stdout.Reset()
			stderr.Reset()
			args := append([]string{"--db", dbPath, "--source", missingSource}, command...)
			err := Run(ctx, args, &stdout, &stderr)
			if err == nil || !strings.Contains(err.Error(), "wacrawl sync") {
				t.Fatalf("Run(%v) should fail with sync guidance, got err=%v", args, err)
			}
			if _, statErr := os.Stat(dbPath); !os.IsNotExist(statErr) {
				t.Fatalf("read command created the archive at %s", dbPath)
			}
		})
	}
}

func archiveSourceContentChecksum(t *testing.T, path string) [32]byte {
	t.Helper()
	ctx := context.Background()
	st, err := store.OpenReadOnly(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	hasher := sha256.New()
	for _, table := range []struct {
		name  string
		query string
	}{
		{name: "chats", query: `select jid, kind, name, last_message_at, unread_count, archived, removed, hidden, raw_session_type from chats order by jid`},
		{name: "contacts", query: `select jid, phone, full_name, first_name, last_name, business_name, username, lid, about_text, updated_at from contacts order by jid`},
		{name: "groups", query: `select jid, name, owner_jid, created_at from groups order by jid`},
		{name: "group_participants", query: `select group_jid, user_jid, contact_name, first_name, is_admin, is_active from group_participants order by group_jid, user_jid`},
		{name: "messages", query: `select rowid, source_pk, chat_jid, chat_name, msg_id, sender_jid, sender_name, ts, from_me, text, raw_type, message_type, media_type, media_title, media_path, media_url, media_size, starred from messages order by rowid`},
	} {
		fmt.Fprintf(hasher, "table:%s\n", table.name)
		rows, err := st.DB().QueryContext(ctx, table.query)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		columnBytes, err := json.Marshal(columns)
		if err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		hasher.Write(columnBytes)
		hasher.Write([]byte{'\n'})

		values := make([]any, len(columns))
		scans := make([]any, len(columns))
		for i := range values {
			scans[i] = &values[i]
		}
		for rows.Next() {
			if err := rows.Scan(scans...); err != nil {
				_ = rows.Close()
				t.Fatal(err)
			}
			for _, value := range values {
				valueBytes, err := json.Marshal(value)
				if err != nil {
					_ = rows.Close()
					t.Fatal(err)
				}
				fmt.Fprintf(hasher, "%T:", value)
				hasher.Write(valueBytes)
				hasher.Write([]byte{'\n'})
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}

	var sum [sha256.Size]byte
	copy(sum[:], hasher.Sum(nil))
	return sum
}

func statusCountIDs(counts []statusCountPayload) []string {
	ids := make([]string, 0, len(counts))
	for _, count := range counts {
		ids = append(ids, count.ID)
	}
	return ids
}

func TestBackupCommands(t *testing.T) {
	ctx := context.Background()
	source := t.TempDir()
	createDesktopFixture(t, source)
	dbPath := filepath.Join(t.TempDir(), "archive.db")
	remote := filepath.Join(t.TempDir(), "remote.git")
	if err := exec.Command("git", "init", "--bare", remote).Run(); err != nil { // #nosec G204 -- test creates a temp bare Git remote.
		t.Fatal(err)
	}
	config := filepath.Join(t.TempDir(), "backup.json")
	repo := filepath.Join(t.TempDir(), "backup")
	identity := filepath.Join(t.TempDir(), "age.key")

	var stdout, stderr bytes.Buffer
	if err := Run(ctx, []string{"--db", dbPath, "--source", source, "sync", "--copy-media"}, &stdout, &stderr); err != nil {
		t.Fatalf("sync error = %v stderr=%s", err, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if err := Run(ctx, []string{"backup", "init", "--config", config, "--repo", repo, "--remote", remote, "--identity", identity, "--no-push"}, &stdout, &stderr); err != nil {
		t.Fatalf("backup init error = %v stderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "recipient=age1") {
		t.Fatalf("backup init did not print recipient:\n%s", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if err := Run(ctx, []string{"--db", dbPath, "backup", "push", "--config", config, "--no-push", "--tag", "snapshot/initial"}, &stdout, &stderr); err != nil {
		t.Fatalf("backup push error = %v stderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "encrypted=true") || !strings.Contains(stdout.String(), "messages=3") || !strings.Contains(stdout.String(), "media_files=1") || !strings.Contains(stdout.String(), "tag=snapshot/initial") {
		t.Fatalf("backup push output mismatch:\n%s", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if err := Run(ctx, []string{"backup", "snapshots", "--config", config}, &stdout, &stderr); err != nil {
		t.Fatalf("backup snapshots error = %v stderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "snapshot/initial") || !strings.Contains(stdout.String(), "MESSAGES") {
		t.Fatalf("backup snapshots output mismatch:\n%s", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if err := Run(ctx, []string{"--json", "backup", "snapshots", "--config", config, "--limit", "1"}, &stdout, &stderr); err != nil {
		t.Fatalf("JSON backup snapshots error = %v stderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"repo"`) || !strings.Contains(stdout.String(), `"snapshots"`) || !strings.Contains(stdout.String(), `"ref"`) {
		t.Fatalf("JSON backup snapshots output mismatch:\n%s", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if err := Run(ctx, []string{"backup", "snapshots", "--config", config, "--limit", "0"}, &stdout, &stderr); err == nil || ExitCode(err) != 2 {
		t.Fatalf("invalid backup snapshots limit error = %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	if err := Run(ctx, []string{"backup", "snapshots", "--config", config, "extra"}, &stdout, &stderr); err == nil || ExitCode(err) != 2 {
		t.Fatalf("backup snapshots positional argument error = %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	if err := Run(ctx, []string{"backup", "status", "--config", config}, &stdout, &stderr); err != nil {
		t.Fatalf("backup status error = %v stderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "encrypted=true") || !strings.Contains(stdout.String(), "repo="+repo) {
		t.Fatalf("backup status output mismatch:\n%s", stdout.String())
	}
	restoredDB := filepath.Join(t.TempDir(), "restored.db")
	stdout.Reset()
	stderr.Reset()
	if err := Run(ctx, []string{"--db", restoredDB, "backup", "pull", "--config", config, "--ref", "snapshot/initial"}, &stdout, &stderr); err != nil {
		t.Fatalf("backup pull error = %v stderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "ref=") {
		t.Fatalf("backup pull should report resolved ref:\n%s", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if err := Run(ctx, []string{"--db", restoredDB, "search", "launch"}, &stdout, &stderr); err != nil {
		t.Fatalf("restored search error = %v stderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "launch now") || strings.Contains(stdout.String(), "[launch]") {
		t.Fatalf("restored search mismatch:\n%s", stdout.String())
	}
	restoredMedia := filepath.Join(filepath.Dir(restoredDB), "media", "Media", "123@g.us", "a", "test.jpg")
	if body, err := os.ReadFile(restoredMedia); err != nil || string(body) != "image" { // #nosec G304 -- test reads its expected temp restore path.
		t.Fatalf("restored media = %q err=%v", body, err)
	}
}

func TestCLIHelpers(t *testing.T) {
	if _, err := parseTime("2026-04-25"); err != nil {
		t.Fatal(err)
	}
	if _, err := parseTime("2026-04-25T12:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if formatTime(timeZero()) != "-" {
		t.Fatal("zero time should format as dash")
	}
	if firstNonEmpty("", "x") != "x" || firstNonEmpty("", "") != "" {
		t.Fatal("firstNonEmpty mismatch")
	}
	args, query, err := splitSearchArgs([]string{"launch", "--limit", "5", "--from-them", "--who", "Alice Example"})
	if err != nil {
		t.Fatal(err)
	}
	if query != "launch" || strings.Join(args, " ") != "--limit 5 --from-them --who Alice Example" {
		t.Fatalf("unexpected split args=%v query=%q", args, query)
	}
	if _, _, err := splitSearchArgs([]string{"one", "two"}); err == nil {
		t.Fatal("expected multi-query split error")
	}
	if _, _, err := splitSearchArgs([]string{"--limit"}); err == nil {
		t.Fatal("expected missing flag value error")
	}
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	flags := bindMessageFlags(fs)
	if err := fs.Parse([]string{"--from-them", "--after", "2026-04-25", "--before", "2026-04-26"}); err != nil {
		t.Fatal(err)
	}
	filter, err := flags.resolve()
	if err != nil {
		t.Fatal(err)
	}
	if filter.FromMe == nil || *filter.FromMe || filter.After == nil || filter.Before == nil {
		t.Fatalf("unexpected resolved filter: %+v", filter)
	}
}

func timeZero() (out time.Time) { return out }

func createDesktopFixture(t *testing.T, dir string) {
	t.Helper()
	chat, err := sql.Open("sqlite", filepath.Join(dir, "ChatStorage.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = chat.Close() }()
	mustExec(t, chat, `
create table ZWACHATSESSION (Z_PK integer primary key, ZCONTACTJID varchar, ZPARTNERNAME varchar, ZLASTMESSAGEDATE timestamp, ZUNREADCOUNT integer, ZARCHIVED integer, ZREMOVED integer, ZHIDDEN integer, ZSESSIONTYPE integer);
create table ZWAGROUPINFO (Z_PK integer primary key, ZCHATSESSION integer, ZOWNERJID varchar, ZCREATIONDATE timestamp);
create table ZWAGROUPMEMBER (Z_PK integer primary key, ZCHATSESSION integer, ZMEMBERJID varchar, ZCONTACTNAME varchar, ZFIRSTNAME varchar, ZISADMIN integer, ZISACTIVE integer);
create table ZWAMEDIAITEM (Z_PK integer primary key, ZMESSAGE integer, ZMEDIALOCALPATH varchar, ZMEDIAURL varchar, ZTITLE varchar, ZVCARDNAME varchar, ZFILESIZE integer);
create table ZWAMESSAGE (Z_PK integer primary key, ZCHATSESSION integer, ZGROUPMEMBER integer, ZMEDIAITEM integer, ZSTANZAID varchar, ZISFROMME integer, ZMESSAGEDATE timestamp, ZTEXT varchar, ZMESSAGETYPE integer, ZSTARRED integer, ZFROMJID varchar, ZTOJID varchar, ZPUSHNAME varchar);
insert into ZWACHATSESSION values (1, '111@s.whatsapp.net', 'Bob', 700000020, 0, 0, 0, 0, 0);
insert into ZWACHATSESSION values (2, '123@g.us', 'Launch Group', 700000010, 2, 0, 0, 0, 1);
insert into ZWAGROUPINFO values (1, 2, 'owner@s.whatsapp.net', 699999000);
insert into ZWAGROUPMEMBER values (1, 2, '222@lid', 'Alice', 'Alice', 1, 1);
insert into ZWAMEDIAITEM values (1, 3, 'Media/123@g.us/a/test.jpg', 'https://example.invalid/media.enc', 'launch image', '', 42);
insert into ZWAMESSAGE values (1, 1, null, null, 'dm-in', 0, 700000000, 'hello', 0, 0, '111@s.whatsapp.net', '', 'Bob');
insert into ZWAMESSAGE values (2, 1, null, null, 'dm-out', 1, 700000001, 'roger', 0, 0, '', '111@s.whatsapp.net', '');
insert into ZWAMESSAGE values (3, 2, 1, 1, 'group-image', 0, 700000002, 'launch now', 1, 1, '123@g.us', '', 'Alice');
`)
	contacts, err := sql.Open("sqlite", filepath.Join(dir, "ContactsV2.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = contacts.Close() }()
	mustExec(t, contacts, `
create table ZWAADDRESSBOOKCONTACT (ZWHATSAPPID varchar, ZPHONENUMBER varchar, ZFULLNAME varchar, ZGIVENNAME varchar, ZLASTNAME varchar, ZBUSINESSNAME varchar, ZUSERNAME varchar, ZLID varchar, ZABOUTTEXT varchar, ZLASTUPDATED timestamp);
insert into ZWAADDRESSBOOKCONTACT values ('111@s.whatsapp.net', '+111', 'Bob', 'Bob', '', '', '', '', '', 700000000);
insert into ZWAADDRESSBOOKCONTACT values ('222@s.whatsapp.net', '+222', 'Alice Contact', 'Alice', '', '', '', '222', '', 700000000);
`)
	mediaPath := filepath.Join(dir, "Media", "123@g.us", "a", "test.jpg")
	if err := os.MkdirAll(filepath.Dir(mediaPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mediaPath, []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustExec(t *testing.T, db *sql.DB, query string) {
	t.Helper()
	if _, err := db.Exec(query); err != nil {
		t.Fatal(err)
	}
}
