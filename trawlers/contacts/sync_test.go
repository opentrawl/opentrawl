package clawdex

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/apple"
	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
)

func TestSyncExplainsAppleContactsReadFailuresDirectly(t *testing.T) {
	app := New()
	app.readApple = func(context.Context) ([]apple.Contact, error) {
		return nil, errors.New("synthetic: operation not permitted")
	}

	_, err := app.Sync(context.Background(), &trawlkit.Request{})
	if err == nil || !strings.Contains(err.Error(), "grant OpenTrawl Full Disk Access") {
		t.Fatalf("sync error = %v, want direct Full Disk Access remedy", err)
	}
}

func TestSyncAutomaticallyCreatesAndReusesApplePeopleArchive(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "contacts.db")
	store, err := ckstore.Open(ctx, ckstore.Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	app := New()
	app.readApple = func(context.Context) ([]apple.Contact, error) {
		return []apple.Contact{{Identifier: "apple-1", FullName: "Ada Example", Emails: []string{"ada@example.com"}, Phones: []string{"+15550100"}}}, nil
	}
	req := &trawlkit.Request{Store: store, Paths: trawlkit.Paths{Archive: path}}
	first, err := app.Sync(ctx, req)
	if err != nil || first.Added != 1 {
		t.Fatalf("first sync report=%#v err=%v", first, err)
	}
	second, err := app.Sync(ctx, req)
	if err != nil || second.Added != 0 || second.Updated != 0 || second.Removed != 0 {
		t.Fatalf("second sync report=%#v err=%v", second, err)
	}
	st, err := archive.UseExisting(ctx, store, path)
	if err != nil {
		t.Fatal(err)
	}
	people, err := st.People(ctx)
	if err != nil || len(people) != 1 || people[0].Name != "Ada Example" {
		t.Fatalf("people=%#v err=%v", people, err)
	}
}

func TestReconcilePeopleSnapshotGroupsSourceAccountsIntoPeople(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "contacts.db")
	store, err := ckstore.Open(ctx, ckstore.Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	app := New()
	req := &trawlkit.Request{Store: store, Paths: trawlkit.Paths{Archive: path}}
	exported := &control.PeopleSnapshot{Contacts: []control.Contact{{
		DisplayName:    "Ada Chat",
		EmailAddresses: []string{"ada@example.com"},
		Accounts:       map[string][]string{"telegram": {"ada_example"}},
	}}}
	input := filepath.Join(t.TempDir(), "export.json")
	data, err := json.Marshal(exported)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(input, data, 0o600); err != nil {
		t.Fatal(err)
	}
	verb := peopleReconcileVerb(app)
	flags := flag.NewFlagSet(verb.Name, flag.ContinueOnError)
	verb.Flags(flags)
	if err := flags.Parse([]string{"--source", "telegram", "--input", input}); err != nil {
		t.Fatal(err)
	}
	if err := verb.Run(ctx, req); err != nil {
		t.Fatal(err)
	}
	st, err := archive.UseExisting(ctx, store, path)
	if err != nil {
		t.Fatal(err)
	}
	person, err := st.FindPerson(ctx, "ada@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if person.Accounts["telegram"][0] != "ada_example" || person.Sources["telegram"].Emails[0] != "ada@example.com" {
		t.Fatalf("person = %#v", person)
	}
}

func TestReconcilePeopleSnapshotKeepsSourceIdentityAcrossChangedFacts(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "contacts.db")
	store, err := ckstore.Open(ctx, ckstore.Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	app := New()
	req := &trawlkit.Request{Store: store, Paths: trawlkit.Paths{Archive: path}}
	first := &control.PeopleSnapshot{Contacts: []control.Contact{{
		SourceID: "peer-1", DisplayName: "Ada Old", Accounts: map[string][]string{"telegram": {"ada_old"}},
	}}}
	if _, err := app.ReconcilePeopleSnapshot(ctx, req, "telegram", first); err != nil {
		t.Fatal(err)
	}
	st, err := archive.UseExisting(ctx, store, path)
	if err != nil {
		t.Fatal(err)
	}
	before, err := st.FindPerson(ctx, "ada_old")
	if err != nil {
		t.Fatal(err)
	}
	second := &control.PeopleSnapshot{Contacts: []control.Contact{{
		SourceID: "peer-1", DisplayName: "Ada New", Accounts: map[string][]string{"telegram": {"ada_new"}},
	}}}
	report, err := app.ReconcilePeopleSnapshot(ctx, req, "telegram", second)
	if err != nil || report.Updated != 1 || report.Added != 0 || report.Removed != 0 {
		t.Fatalf("changed snapshot report=%#v err=%v", report, err)
	}
	after, err := st.FindPerson(ctx, "ada_new")
	if err != nil {
		t.Fatal(err)
	}
	if after.ID != before.ID || after.Name != "Ada New" {
		t.Fatalf("source identity churned across changed facts: before=%#v after=%#v", before, after)
	}
}

func TestInternalPeopleReconciliationIsAbsentFromManifest(t *testing.T) {
	manifest, err := trawlkit.Manifest(New())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := manifest.Commands["__people_reconcile"]; ok {
		t.Fatalf("internal People reconciliation leaked into commands: %#v", manifest.Commands)
	}
	for _, capability := range manifest.Capabilities {
		if capability == "__people_reconcile" {
			t.Fatalf("internal People reconciliation leaked into capabilities: %#v", manifest.Capabilities)
		}
	}
}
