package cli

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"flag"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

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
		{"version", []string{"--version"}, "0.1.0-dev"},
		{"doctor", []string{"--db", dbPath, "--source", source, "doctor"}, "message_rows"},
		{"import", []string{"--db", dbPath, "--source", source, "import"}, "messages=3"},
		{"status", []string{"--db", dbPath, "status"}, "messages=3"},
		{"chats", []string{"--db", dbPath, "chats", "--limit", "5"}, "Launch Group"},
		{"messages", []string{"--db", dbPath, "messages", "--chat", "123@g.us", "--asc"}, "launch now"},
		{"search", []string{"--db", dbPath, "search", "--limit", "5", "launch"}, "[launch] now"},
		{"json", []string{"--db", dbPath, "--json", "search", "launch"}, `"message_id"`},
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
	err = Run(context.Background(), []string{"search"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "exactly one query") {
		t.Fatalf("expected query error, got %v", err)
	}
	err = Run(context.Background(), []string{"doctor", "extra"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "flags only") {
		t.Fatalf("expected doctor args error, got %v", err)
	}
	for _, args := range [][]string{
		{"import", "extra"},
		{"chats", "extra"},
		{"messages", "extra"},
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
}

func mustExec(t *testing.T, db *sql.DB, query string) {
	t.Helper()
	if _, err := db.Exec(query); err != nil {
		t.Fatal(err)
	}
}
