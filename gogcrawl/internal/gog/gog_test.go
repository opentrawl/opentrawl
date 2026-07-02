package gog

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSearchMessagesParsesSearchPage(t *testing.T) {
	client := New(fakeGog(t, `cat <<'JSON'
{"messages":[{"id":"m1","threadId":"t1","date":"Thu, 02 Jul 2026 14:03:11 +0200","from":"Alice Example <alice@example.com>","subject":"Re: project sync","labels":["INBOX"],"body":"Project sync moved to Friday."}],"nextPageToken":"p2"}
JSON
`))
	page, err := client.SearchMessages(context.Background(), SearchRequest{Query: DefaultArchiveQuery, Max: 10})
	if err != nil {
		t.Fatal(err)
	}
	if page.NextPageToken != "p2" {
		t.Fatalf("next page = %q, want p2", page.NextPageToken)
	}
	if len(page.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(page.Messages))
	}
	msg := page.Messages[0]
	if msg.ID != "m1" || msg.ThreadID != "t1" {
		t.Fatalf("message ids = %#v", msg)
	}
	if msg.FromName != "Alice Example" || msg.FromAddress != "alice@example.com" {
		t.Fatalf("from = %q <%s>", msg.FromName, msg.FromAddress)
	}
	if got := msg.Time.Format(time.RFC3339); got != "2026-07-02T14:03:11+02:00" {
		t.Fatalf("time = %s", got)
	}
}

func TestAuthStatusParsesPlainRows(t *testing.T) {
	client := New(fakeGog(t, `printf 'alice@example.com\tmain\tgmail\t2030-01-02T03:04:05Z\ttrue\t\toauth\n'
`))
	status, err := client.AuthStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.FoundAccount || !status.Authorized {
		t.Fatalf("status = %#v", status)
	}
	if status.Expires == nil || status.Expires.Format(time.RFC3339) != "2030-01-02T03:04:05Z" {
		t.Fatalf("expires = %#v", status.Expires)
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

func fakeGog(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gog")
	script := "#!/bin/sh\n" + body + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParseDateAcceptsGogFormats(t *testing.T) {
	for _, value := range []string{
		"2026-07-02 15:19",
		"17 mar 2026 11:09:45",
		"Mon, 02 Jan 2006 15:04:05 -0700",
	} {
		if _, err := parseDate(value); err != nil {
			t.Errorf("parseDate(%q) = %v", value, err)
		}
	}
}

func TestParseSearchMessageToleratesBadDate(t *testing.T) {
	msg, err := parseSearchMessage(searchMessage{ID: "m1", Date: ""})
	if err != nil {
		t.Fatal(err)
	}
	if !msg.Time.IsZero() {
		t.Fatalf("time = %v, want zero", msg.Time)
	}
}
