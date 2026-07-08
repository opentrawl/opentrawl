package discrawl

import (
	"path/filepath"
	"strings"
	"testing"

	ckstore "github.com/openclaw/crawlkit/store"
)

func TestListDMContactsViaStoreAdapter(t *testing.T) {
	path := discrawlFixture(t, []string{
		`insert into channels(id, name, guild_id, kind) values('c0', 'Self note', '@me', 'dm')`,
		`insert into messages(id, channel_id, author_id, created_at) values('m0', 'c0', 'me', '2025-12-31T00:00:00Z')`,
		`insert into channels(id, name, guild_id, kind) values('c1', 'Ada DM', '@me', 'dm')`,
		`insert into messages(id, channel_id, author_id, created_at) values('m1', 'c1', 'me', '2026-01-01T00:00:00Z')`,
		`insert into messages(id, channel_id, author_id, created_at) values('m2', 'c1', 'u1', '2026-01-02T00:00:00Z')`,
		`insert into messages(id, channel_id, author_id, created_at) values('m3', 'c1', 'u1', '2026-01-03T00:00:00Z')`,
		`insert into messages(id, channel_id, author_id, created_at) values('m4', 'c1', 'u1', '2026-01-04T00:00:00Z')`,
		`insert into messages(id, channel_id, author_id, created_at) values('m5', 'c1', 'u1', '2026-01-05T00:00:00Z')`,
	})
	contacts, err := (Adapter{DBPath: path}).ListDMContacts(t.Context(), 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(contacts) != 1 || contacts[0].Name != "Ada DM" || contacts[0].Accounts["discord"][1] != "user:u1" {
		t.Fatalf("contacts = %#v", contacts)
	}
}

func TestListDMContactsEmptyAndFilters(t *testing.T) {
	path := discrawlFixture(t, []string{
		`insert into channels(id, name, guild_id, kind) values('', 'Skip', '@me', 'dm')`,
		`insert into messages(id, channel_id, author_id, created_at) values('m1', '', 'u1', '2026-01-01T00:00:00Z')`,
		`insert into messages(id, channel_id, author_id, created_at) values('m2', '', 'u1', '2026-01-02T00:00:00Z')`,
		`insert into channels(id, name, guild_id, kind) values('c2', '  ', '@me', 'dm')`,
		`insert into messages(id, channel_id, author_id, created_at) values('m3', 'c2', 'u2', '2026-01-03T00:00:00Z')`,
		`insert into messages(id, channel_id, author_id, created_at) values('m4', 'c2', 'u2', '2026-01-04T00:00:00Z')`,
	})
	contacts, err := (Adapter{DBPath: path}).ListDMContacts(t.Context(), -1)
	if err != nil {
		t.Fatal(err)
	}
	if len(contacts) != 0 {
		t.Fatalf("contacts = %#v", contacts)
	}
}

func TestListDMContactsErrors(t *testing.T) {
	_, err := (Adapter{DBPath: filepath.Join(t.TempDir(), "missing.db")}).ListDMContacts(t.Context(), 4)
	if err == nil || !strings.Contains(err.Error(), "discrawl sqlite query") {
		t.Fatalf("err = %v", err)
	}
}

func discrawlFixture(t *testing.T, statements []string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "discrawl.db")
	st, err := ckstore.Open(t.Context(), ckstore.Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	schema := []string{
		`create table channels(id text, name text, guild_id text, kind text)`,
		`create table messages(id text primary key, channel_id text, author_id text, created_at text)`,
	}
	for _, statement := range append(schema, statements...) {
		if _, err := st.DB().ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	return path
}
