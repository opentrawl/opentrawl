package gog

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionReturnsRawVersion(t *testing.T) {
	client := New(fakeGog(t, `printf 'v0.31.1 (test)\n'
`))
	version, err := client.Version(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if version != "v0.31.1 (test)" {
		t.Fatalf("version = %q", version)
	}
}

func TestAuthStatusParsesPlainRows(t *testing.T) {
	client := New(fakeGog(t, `printf 'alice@example.com\tmain\tgmail\t2030-01-02T03:04:05Z\ttrue\t\toauth\n'
`))
	status, err := client.AuthStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.FoundAccount || !status.Authorized || status.AccountEmail != "alice@example.com" {
		t.Fatalf("status = %#v", status)
	}
}

func TestAuthStatusTrustsCheckedValidityOverExpiry(t *testing.T) {
	client := New(fakeGog(t, `printf 'alice@example.com\tmain\tgmail\t2000-01-02T03:04:05Z\ttrue\t\toauth\n'
`))
	status, err := client.AuthStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.FoundAccount || !status.Authorized {
		t.Fatalf("status = %#v", status)
	}
}

func TestAuthStatusRejectsInvalidCheckedRows(t *testing.T) {
	client := New(fakeGog(t, `printf 'alice@example.com\tmain\tgmail\t2030-01-02T03:04:05Z\tfalse\t\toauth\n'
`))
	status, err := client.AuthStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.FoundAccount || status.Authorized {
		t.Fatalf("status = %#v", status)
	}
}

func TestContactsParsesPage(t *testing.T) {
	client := New(fakeGog(t, `cat <<'JSON'
{"contacts":[{"resource":"people/c1","name":"Alice Example","phone":"+15550101000"}],"nextPageToken":"next"}
JSON
`))
	page, err := client.Contacts(context.Background(), 10, "")
	if err != nil {
		t.Fatal(err)
	}
	if page.NextPageToken != "next" {
		t.Fatalf("next page = %q", page.NextPageToken)
	}
	if len(page.Contacts) != 1 || page.Contacts[0].Phone != "+15550101000" {
		t.Fatalf("contacts = %#v", page.Contacts)
	}
}

func TestBackupWrappersUseRepoAndNoPush(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "calls.log")
	client := New(fakeGog(t, `printf '%s\n' "$*" >> "`+logPath+`"
`))
	ctx := context.Background()
	if err := client.BackupInit(ctx, "/tmp/repo"); err != nil {
		t.Fatal(err)
	}
	if err := client.BackupGmailPush(ctx, BackupPushRequest{Repo: "/tmp/repo", Query: "from:me", Max: 25}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.BackupCat(ctx, "/tmp/repo", "data/gmail/account/messages/part-000001.jsonl.gz.age"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	log := string(data)
	for _, want := range []string{
		"backup init --no-push --repo /tmp/repo",
		"backup gmail push --no-push --gmail-cache --repo /tmp/repo --query from:me --max 25",
		"backup cat --no-pull --repo /tmp/repo data/gmail/account/messages/part-000001.jsonl.gz.age",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("calls missing %q in:\n%s", want, log)
		}
	}
}

func fakeGog(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gog")
	script := "#!/bin/sh\n" + body + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}
