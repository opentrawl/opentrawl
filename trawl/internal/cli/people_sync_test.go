package cli

import (
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
)

func TestReconcileSourcePeopleFeedsContactExportToContacts(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	exportPath := filepath.Join(root, "messages.db")
	contactsPath := filepath.Join(root, "contacts.db")
	for _, path := range []string{exportPath, contactsPath} {
		st, err := ckstore.Open(context.Background(), ckstore.Options{Path: path})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
	}

	exported := &control.ContactExport{Contacts: []control.Contact{{
		DisplayName:    "Avery Example",
		EmailAddresses: []string{"avery@example.com"},
		Accounts:       map[string][]string{"imessage": {"avery@example.com"}},
	}}}
	sourceCrawler := &peopleExportFixture{info: fixtureInfo("imessage", "Messages", exportPath), exported: exported}
	contactsCrawler := &peopleReconcileFixture{info: fixtureInfo("contacts", "Contacts", contactsPath)}
	source := Source{ID: "imessage", DisplayName: "Messages", Crawler: sourceCrawler}
	contacts := Source{ID: "contacts", DisplayName: "Contacts", Crawler: contactsCrawler}
	runtime := &Runtime{ctx: context.Background(), stderr: io.Discard, root: &CLI{}, timeout: time.Second}

	if err := runtime.reconcileSourcePeople(source, []Source{source, contacts}); err != nil {
		t.Fatal(err)
	}
	if contactsCrawler.source != "imessage" {
		t.Fatalf("source = %q, want imessage", contactsCrawler.source)
	}
	if contactsCrawler.exported == nil || len(contactsCrawler.exported.Contacts) != 1 {
		t.Fatalf("export = %#v", contactsCrawler.exported)
	}
	got := contactsCrawler.exported.Contacts[0]
	if got.EmailAddresses[0] != "avery@example.com" || got.Accounts["imessage"][0] != "avery@example.com" {
		t.Fatalf("contact = %#v", got)
	}
}

func fixtureInfo(id, displayName, archivePath string) trawlkit.Info {
	return trawlkit.Info{
		ID: id, Surface: id, DisplayName: displayName,
		DefaultPaths: trawlkit.Paths{Archive: archivePath, Logs: filepath.Join(filepath.Dir(archivePath), id+"-logs")},
	}
}

type peopleExportFixture struct {
	info     trawlkit.Info
	exported *control.ContactExport
}

func (f *peopleExportFixture) Info() trawlkit.Info    { return f.info }
func (f *peopleExportFixture) Verbs() []trawlkit.Verb { return nil }
func (f *peopleExportFixture) Status(context.Context, *trawlkit.Request) (*control.Status, error) {
	status := control.NewStatus(f.info.ID, "ready")
	return &status, nil
}
func (f *peopleExportFixture) ContactExport(context.Context, *trawlkit.Request) (*control.ContactExport, error) {
	return f.exported, nil
}

type peopleReconcileFixture struct {
	info     trawlkit.Info
	source   string
	exported *control.ContactExport
}

func (f *peopleReconcileFixture) Info() trawlkit.Info    { return f.info }
func (f *peopleReconcileFixture) Verbs() []trawlkit.Verb { return nil }
func (f *peopleReconcileFixture) Status(context.Context, *trawlkit.Request) (*control.Status, error) {
	status := control.NewStatus(f.info.ID, "ready")
	return &status, nil
}
func (f *peopleReconcileFixture) ReconcileContactExport(_ context.Context, _ *trawlkit.Request, source string, exported *control.ContactExport) (*trawlkit.SyncReport, error) {
	f.source = source
	f.exported = exported
	return &trawlkit.SyncReport{Added: int64(len(exported.Contacts))}, nil
}
