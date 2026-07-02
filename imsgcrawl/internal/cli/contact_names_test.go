package cli

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openclaw/crawlkit/control"
	"github.com/openclaw/imsgcrawl/internal/archive"
)

func TestSyncedAddressBookNamesPopulateSearchAndOpen(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "chat.db")
	archivePath := filepath.Join(dir, "archive.db")
	addressBookPath := filepath.Join(dir, "AddressBook-v22.abcddb")
	createContactlessMessagesFixture(t, dbPath)
	createAddressBookFixture(t, addressBookPath)

	result, err := archive.SyncWithOptions(context.Background(), archive.SyncOptions{
		ArchivePath:      archivePath,
		SourcePath:       dbPath,
		AddressBookPaths: []string{addressBookPath},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.NamedContacts != 2 {
		t.Fatalf("named contacts = %d, want 2", result.NamedContacts)
	}

	searchOut := runOK(t, "--archive", archivePath, "--json", "search", "--limit", "3", "dinner")
	var search searchListJSON
	if err := json.Unmarshal([]byte(searchOut), &search); err != nil {
		t.Fatalf("search json = %s err=%v", searchOut, err)
	}
	bySnippet := map[string]searchResultJSON{}
	for _, item := range search.Results {
		bySnippet[item.Snippet] = item
	}
	assertSearchName(t, bySnippet, "phone dinner plan", "Katja Example", "Katja Example")
	assertSearchName(t, bySnippet, "email dinner plan", "Alice Mail", "Alice Mail")
	assertSearchName(t, bySnippet, "unmatched dinner plan", "+15550999", "+15550999")

	openOut := runOK(t, "--archive", archivePath, "--json", "open", bySnippet["phone dinner plan"].Ref)
	var opened openJSON
	if err := json.Unmarshal([]byte(openOut), &opened); err != nil {
		t.Fatalf("open json = %s err=%v", openOut, err)
	}
	if opened.Chat.Name != "Katja Example" || opened.Message.Who != "Katja Example" || opened.Message.Where != "Katja Example" {
		t.Fatalf("open names = %#v", opened)
	}
	if len(opened.Chat.Participants) != 1 || opened.Chat.Participants[0] != "Katja Example" {
		t.Fatalf("open participants = %#v", opened.Chat.Participants)
	}

	statusOut := runOK(t, "--db", dbPath, "--archive", archivePath, "--json", "status")
	var status statusOutput
	if err := json.Unmarshal([]byte(statusOut), &status); err != nil {
		t.Fatalf("status json = %s err=%v", statusOut, err)
	}
	if status.Archive == nil || status.Archive.NamedContacts != 2 {
		t.Fatalf("status named contacts = %#v", status.Archive)
	}
	if got := countValue(status.Counts, "named_contacts"); got != 2 {
		t.Fatalf("named_contacts count = %d, want 2; counts=%#v", got, status.Counts)
	}
}

func assertSearchName(t *testing.T, results map[string]searchResultJSON, snippet, who, where string) {
	t.Helper()
	item, ok := results[snippet]
	if !ok {
		t.Fatalf("missing snippet %q in %#v", snippet, results)
	}
	if item.Who != who || item.Where != where {
		t.Fatalf("result %q = %#v, want who=%q where=%q", snippet, item, who, where)
	}
	if strings.Contains(item.Who+item.Where, "alice@example.com") {
		t.Fatalf("result leaked matched handle = %#v", item)
	}
}

func countValue(counts []control.Count, id string) int64 {
	for _, count := range counts {
		if count.ID == id {
			return count.Value
		}
	}
	return -1
}
