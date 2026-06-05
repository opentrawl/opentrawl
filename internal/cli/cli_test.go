package cli

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestRunEndToEnd(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "chat.db")
	createMessagesFixture(t, dbPath)
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"help", nil, "imsgcrawl reads local iMessage"},
		{"version", []string{"--version"}, version},
		{"metadata global json", []string{"--json", "metadata"}, `"id": "imsgcrawl"`},
		{"metadata trailing json", []string{"metadata", "--json"}, `"contact-export"`},
		{"status", []string{"--db", dbPath, "--json", "status"}, `"messages": 4`},
		{"contacts export", []string{"--db", dbPath, "--json", "contacts", "export"}, `"display_name": "+15550103"`},
		{"contacts export trailing json", []string{"--db", dbPath, "contacts", "export", "--json"}, `"phone_numbers"`},
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

func TestContactsExportShapeAndDedupe(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "chat.db")
	createMessagesFixture(t, dbPath)
	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{"--db", dbPath, "--json", "contacts", "export"}, &stdout, &stderr); err != nil {
		t.Fatalf("contacts export: %v stderr=%s", err, stderr.String())
	}
	assertContactExportKeys(t, stdout.Bytes())
	var payload struct {
		Contacts []struct {
			DisplayName  string   `json:"display_name"`
			PhoneNumbers []string `json:"phone_numbers"`
			Service      string   `json:"service"`
			Messages     int64    `json:"messages"`
		} `json:"contacts"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("json = %s err=%v", stdout.String(), err)
	}
	got := map[string]string{}
	for _, contact := range payload.Contacts {
		if contact.Service != "" || contact.Messages != 0 {
			t.Fatalf("leaked source fields = %#v", contact)
		}
		if len(contact.PhoneNumbers) != 1 {
			t.Fatalf("phone_numbers = %#v", contact.PhoneNumbers)
		}
		got[contact.PhoneNumbers[0]] = contact.DisplayName
	}
	want := map[string]string{
		"0015550100": "Most Recent Name",
		"+15550103":  "+15550103",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("contacts = %#v, want %#v", got, want)
	}
}

func TestMetadataAdvertisesContactExport(t *testing.T) {
	manifest := controlManifest()
	command, ok := manifest.Commands["contact-export"]
	if !ok {
		t.Fatalf("commands = %#v", manifest.Commands)
	}
	if command.Mutates || !command.JSON {
		t.Fatalf("contact-export command = %#v", command)
	}
	want := []string{"imsgcrawl", "--json", "contacts", "export"}
	if !reflect.DeepEqual(command.Argv, want) {
		t.Fatalf("argv = %#v, want %#v", command.Argv, want)
	}
}

func TestRunUsageErrors(t *testing.T) {
	var stdout, stderr bytes.Buffer
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

func createMessagesFixture(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	schema := []string{
		`create table handle (ROWID integer primary key, id text not null, service text not null)`,
		`create table chat (ROWID integer primary key, guid text not null, display_name text, chat_identifier text, service_name text)`,
		`create table chat_handle_join (chat_id integer, handle_id integer)`,
		`create table message (ROWID integer primary key, handle_id integer, date integer)`,
	}
	for _, stmt := range schema {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	inserts := []string{
		`insert into handle(rowid, id, service) values (1, '+15550100', 'iMessage')`,
		`insert into handle(rowid, id, service) values (2, '0015550100', 'SMS')`,
		`insert into handle(rowid, id, service) values (3, 'person@example.test', 'iMessage')`,
		`insert into handle(rowid, id, service) values (4, '+15550103', 'SMS')`,
		`insert into handle(rowid, id, service) values (5, 'opaque-handle', 'SMS')`,
		`insert into handle(rowid, id, service) values (6, 'opaque123', 'SMS')`,
		`insert into chat(rowid, guid, display_name, chat_identifier, service_name) values (1, 'chat-one', 'Older Name', '+15550100', 'iMessage')`,
		`insert into chat(rowid, guid, display_name, chat_identifier, service_name) values (2, 'chat-two', 'Most Recent Name', '0015550100', 'SMS')`,
		`insert into chat(rowid, guid, display_name, chat_identifier, service_name) values (3, 'chat-three', '', '+15550103', 'SMS')`,
		`insert into chat_handle_join(chat_id, handle_id) values (1, 1)`,
		`insert into chat_handle_join(chat_id, handle_id) values (2, 2)`,
		`insert into chat_handle_join(chat_id, handle_id) values (3, 4)`,
		`insert into message(rowid, handle_id, date) values (1, 1, 100)`,
		`insert into message(rowid, handle_id, date) values (2, 2, 200)`,
		`insert into message(rowid, handle_id, date) values (3, 2, 250)`,
		`insert into message(rowid, handle_id, date) values (4, 4, 300)`,
	}
	for _, stmt := range inserts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
}
