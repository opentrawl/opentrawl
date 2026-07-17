package control

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestManifestDefaultsSchemaAndBinary(t *testing.T) {
	manifest := NewManifest("slacrawl", "Slack Crawl", "slacrawl")
	if manifest.SchemaVersion != RunnerManifestVersion || manifest.ContractVersion != ContractVersion {
		t.Fatalf("manifest versions = %#v", manifest)
	}
	if manifest.Binary.Name != "slacrawl" {
		t.Fatalf("binary = %#v", manifest.Binary)
	}
	if manifest.Commands == nil {
		t.Fatal("commands map should be initialised")
	}
}

func TestStatusAndRemoteDatabaseDefaults(t *testing.T) {
	status := NewStatus(" gitcrawl ", " ready ")
	if status.SchemaVersion != StatusSchemaVersion || status.AppID != "gitcrawl" || status.State != "unknown" || status.Summary != "ready" {
		t.Fatalf("status = %#v", status)
	}
	if status.GeneratedAt == "" {
		t.Fatal("generated_at should be set")
	}

	counts := []Count{NewCount("threads", "Threads", 3)}
	db := RemoteDatabase(" cloud ", " Cloud archive ", "", "", " https://remote.example/ ", " gitcrawl/cloud ", true, counts)
	if db.ID != "cloud" || db.Label != "Cloud archive" || db.Kind != "remote" || db.Role != "archive" {
		t.Fatalf("remote database defaults = %#v", db)
	}
	if db.Endpoint != "https://remote.example" || db.Archive != "gitcrawl/cloud" || !db.IsPrimary {
		t.Fatalf("remote database routing = %#v", db)
	}
	counts[0].Value = 99
	if db.Counts[0].Value != 3 {
		t.Fatalf("counts should be copied: %#v", db.Counts)
	}
}

func TestSQLiteDatabaseStatsPathReadOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "archive.db")
	if err := os.WriteFile(path, []byte("sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	db := SQLiteDatabase("primary", "Primary archive", "archive", path, true, []Count{NewCount("messages", "Messages", 7)})
	if db.Kind != "sqlite" || !db.IsPrimary || db.Bytes != 6 {
		t.Fatalf("unexpected database: %#v", db)
	}
	if db.ModifiedAt == "" {
		t.Fatal("modified_at should be set for existing paths")
	}
	if len(db.Counts) != 1 || db.Counts[0].Value != 7 {
		t.Fatalf("counts = %#v", db.Counts)
	}
}

func TestValidatePeopleSnapshot(t *testing.T) {
	valid := []PeopleSnapshot{
		{Contacts: []Contact{{DisplayName: "Alice", PhoneNumbers: []string{"+15550100"}}}},
		{Contacts: []Contact{{DisplayName: "Alice", EmailAddresses: []string{"alice@example.com"}}}},
		{Contacts: []Contact{{DisplayName: "Alice", Accounts: map[string][]string{"telegram": {"alice"}}}}},
	}
	for _, value := range valid {
		if err := ValidatePeopleSnapshot(value); err != nil {
			t.Fatal(err)
		}
	}
	invalid := []PeopleSnapshot{
		{Contacts: []Contact{{SourceID: "same", DisplayName: "Alice", PhoneNumbers: []string{"+15550100"}}, {SourceID: "same", DisplayName: "Bob", PhoneNumbers: []string{"+15550200"}}}},
		{Contacts: []Contact{{PhoneNumbers: []string{"+15550100"}}}},
		{Contacts: []Contact{{DisplayName: "Alice"}}},
		{Contacts: []Contact{{DisplayName: "Alice", PhoneNumbers: []string{""}}}},
		{Contacts: []Contact{{DisplayName: "Alice", PhoneNumbers: []string{"+15550100", "+15550100"}}}},
		{Contacts: []Contact{{DisplayName: "Alice", EmailAddresses: []string{""}}}},
		{Contacts: []Contact{{DisplayName: "Alice", EmailAddresses: []string{"Alice@example.com", "alice@example.com"}}}},
		{Contacts: []Contact{{DisplayName: "Alice", Accounts: map[string][]string{"": {"alice"}}}}},
		{Contacts: []Contact{{DisplayName: "Alice", Accounts: map[string][]string{"telegram": {}}}}},
		{Contacts: []Contact{{DisplayName: "Alice", Accounts: map[string][]string{"telegram": {""}}}}},
		{Contacts: []Contact{{DisplayName: "Alice", Accounts: map[string][]string{"telegram": {"Alice", "alice"}}}}},
		{Contacts: []Contact{{DisplayName: "Alice", Accounts: map[string][]string{"Telegram": {"alice"}, "telegram": {"bob"}}}}},
	}
	for _, value := range invalid {
		if err := ValidatePeopleSnapshot(value); err == nil {
			t.Fatalf("expected invalid People snapshot: %+v", value)
		}
	}
}

func TestSetupRequirementCopiesCommandAndClassifiesErrors(t *testing.T) {
	command := []string{"gog", "login", "<email>"}
	requirement := NewSetupRequirement("account", SetupKindAccount, SetupStateNeedsAction, "log in", SetupActionRunCommand, command)
	command[0] = "changed"
	if requirement.ID != "account" || requirement.Kind != SetupKindAccount || requirement.State != SetupStateNeedsAction || requirement.Explanation != "log in" || requirement.Action != SetupActionRunCommand {
		t.Fatalf("requirement = %#v", requirement)
	}
	if requirement.Command[0] != "gog" {
		t.Fatalf("command was not copied: %#v", requirement.Command)
	}
	if SetupStateForError(nil) != SetupStateReady {
		t.Fatal("nil error should be ready")
	}
	if SetupStateForError(errors.New("permission denied")) != SetupStateNeedsAction {
		t.Fatal("permission error should need action")
	}
	if SetupStateForError(errors.New("source is unavailable")) != SetupStateUnavailable {
		t.Fatal("other errors should be unavailable")
	}
	unavailable := NewSetupRequirement("source", SetupKindFullDiskAccess, SetupStateUnavailable, "unavailable", SetupActionOpenFullDiskAccess, []string{"open"})
	if unavailable.Action != SetupActionNone || len(unavailable.Command) != 0 {
		t.Fatalf("unavailable requirement should not offer an action: %#v", unavailable)
	}
}
