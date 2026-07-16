package clawdex

import (
	"context"
	"errors"
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

func TestReconcileContactExportGroupsSourceAccountsIntoPeople(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "contacts.db")
	store, err := ckstore.Open(ctx, ckstore.Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	app := New()
	req := &trawlkit.Request{Store: store, Paths: trawlkit.Paths{Archive: path}}
	exported := &control.ContactExport{Contacts: []control.Contact{{
		DisplayName:    "Ada Chat",
		EmailAddresses: []string{"ada@example.com"},
		Accounts:       map[string][]string{"telegram": {"ada_example"}},
	}}}
	report, err := app.ReconcileContactExport(ctx, req, "telegram", exported)
	if err != nil || report.Added != 1 {
		t.Fatalf("report=%#v err=%v", report, err)
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
