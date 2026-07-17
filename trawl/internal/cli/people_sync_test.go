package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/opentrawl/opentrawl/trawlkit/control"
)

func TestSuccessfulSourceSyncReconcilesPeopleThroughCanonicalContactsMutation(t *testing.T) {
	home := syntheticHome(t)
	t.Setenv("HOME", home)
	peopleMarker := filepath.Join(t.TempDir(), "people.json")
	contactsPrepareMarker := filepath.Join(t.TempDir(), "contacts-prepare.txt")
	exported := &control.PeopleSnapshot{Contacts: []control.Contact{{
		DisplayName:    "Avery Example",
		EmailAddresses: []string{"avery@example.com"},
		Accounts:       map[string][]string{"imessage": {"avery@example.com"}},
	}}}
	writeFakeCrawlers(t,
		fakeCrawler{
			name:           "messages",
			metadata:       `{"schema_version":1,"contract_version":1,"capabilities":["status","sync"],"id":"imessage","display_name":"Messages"}`,
			sync:           `{"state":"ok","added":1}`,
			peopleSnapshot: exported,
		},
		fakeCrawler{
			name:          "contacts",
			metadata:      `{"schema_version":1,"contract_version":1,"capabilities":["status"],"id":"contacts","display_name":"Contacts"}`,
			peopleMarker:  peopleMarker,
			prepareMarker: contactsPrepareMarker,
		},
	)

	stdout, stderr, code := runCLI(t, "sync", "imessage")
	if code != 0 {
		t.Fatalf("sync code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	data, err := os.ReadFile(peopleMarker)
	if err != nil {
		t.Fatalf("People reconciliation marker: %v", err)
	}
	var got struct {
		Source string                 `json:"source"`
		Export control.PeopleSnapshot `json:"export"`
		Store  bool                   `json:"store"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Source != "imessage" || !got.Store || len(got.Export.Contacts) != 1 || got.Export.Contacts[0].EmailAddresses[0] != "avery@example.com" {
		t.Fatalf("People reconciliation = %#v", got)
	}
	prepareData, err := os.ReadFile(contactsPrepareMarker)
	if err != nil {
		t.Fatalf("Contacts preparation marker: %v", err)
	}
	wantArchive := filepath.Join(home, ".opentrawl", "contacts", "contacts.db")
	if string(prepareData) != wantArchive+"\n" {
		t.Fatalf("Contacts preparation = %q, want %q", prepareData, wantArchive+"\n")
	}
}

func TestFailedSourceSyncDoesNotReconcilePeople(t *testing.T) {
	t.Setenv("HOME", syntheticHome(t))
	peopleMarker := filepath.Join(t.TempDir(), "people.json")
	writeFakeCrawlers(t,
		fakeCrawler{
			name: "messages", metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync"],"id":"imessage","display_name":"Messages"}`,
			sync: `{"error":{"code":"permission_denied","message":"Synthetic source failure.","remedy":"Grant synthetic access."}}`, syncExit: 1,
			peopleSnapshot: &control.PeopleSnapshot{Contacts: []control.Contact{{DisplayName: "Avery Example"}}},
		},
		fakeCrawler{
			name: "contacts", metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status"],"id":"contacts","display_name":"Contacts"}`,
			peopleMarker: peopleMarker,
		},
	)

	if _, _, code := runCLI(t, "sync", "imessage"); code != 1 {
		t.Fatalf("failed sync code = %d, want 1", code)
	}
	if _, err := os.Stat(peopleMarker); !os.IsNotExist(err) {
		t.Fatalf("People reconciliation ran after failed sync: %v", err)
	}
}
