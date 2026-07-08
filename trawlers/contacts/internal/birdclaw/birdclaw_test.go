package birdclaw

import (
	"path/filepath"
	"strings"
	"testing"

	ckstore "github.com/openclaw/crawlkit/store"
)

func TestListDMContactsViaStoreAdapter(t *testing.T) {
	path := birdclawFixture(t, []string{
		`insert into profiles(id, handle, display_name) values('2', 'ada', 'Ada Lovelace')`,
		`insert into dm_conversations(id, participant_profile_id, title) values('1-2', '2', 'ada')`,
		`insert into dm_messages(id, conversation_id, created_at) values('m1', '1-2', '2026-01-01T00:00:00Z')`,
		`insert into dm_messages(id, conversation_id, created_at) values('m2', '1-2', '2026-01-02T00:00:00Z')`,
		`insert into dm_messages(id, conversation_id, created_at) values('m3', '1-2', '2026-01-03T00:00:00Z')`,
		`insert into dm_messages(id, conversation_id, created_at) values('m4', '1-2', '2026-01-04T00:00:00Z')`,
		`insert into dm_messages(id, conversation_id, created_at) values('m5', '1-2', '2026-01-05T00:00:00Z')`,
	})
	contacts, err := (Adapter{DBPath: path}).ListDMContacts(t.Context(), 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(contacts) != 1 || contacts[0].Name != "Ada Lovelace" {
		t.Fatalf("contacts = %#v", contacts)
	}
	accounts := contacts[0].Accounts["x"]
	if len(accounts) != 3 || accounts[1] != "@ada" || accounts[2] != "user:2" {
		t.Fatalf("accounts = %#v", accounts)
	}
}

func TestListDMContactsFallbacksAndFilters(t *testing.T) {
	path := birdclawFixture(t, []string{
		`insert into dm_conversations(id, participant_profile_id, title) values('c1', '', '')`,
		`insert into dm_messages(id, conversation_id, created_at) values('m1', 'c1', '2026-01-01T00:00:00Z')`,
		`insert into dm_messages(id, conversation_id, created_at) values('m2', 'c1', '2026-01-02T00:00:00Z')`,
		`insert into dm_conversations(id, participant_profile_id, title) values('', '', 'skip')`,
		`insert into dm_messages(id, conversation_id, created_at) values('m3', '', '2026-01-03T00:00:00Z')`,
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
	_, err := (Adapter{DBPath: filepath.Join(t.TempDir(), "missing.sqlite")}).ListDMContacts(t.Context(), 4)
	if err == nil || !strings.Contains(err.Error(), "birdclaw sqlite query") {
		t.Fatalf("err = %v", err)
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "  ", "x"); got != "x" {
		t.Fatalf("got = %q", got)
	}
}

func birdclawFixture(t *testing.T, statements []string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "birdclaw.sqlite")
	st, err := ckstore.Open(t.Context(), ckstore.Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	schema := []string{
		`create table profiles(id text primary key, handle text, display_name text)`,
		`create table dm_conversations(id text primary key, participant_profile_id text, title text)`,
		`create table dm_messages(id text primary key, conversation_id text, created_at text)`,
	}
	for _, statement := range append(schema, statements...) {
		if _, err := st.DB().ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	return path
}
