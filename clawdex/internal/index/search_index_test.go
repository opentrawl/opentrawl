package index

import (
	"database/sql"
	"strings"
	"testing"
	"time"
)

func TestSearchIndexRebuildsTrigramCJK(t *testing.T) {
	r := testRepo(t)
	s := New(r)
	if _, err := s.AddPerson("鹤鸣茶社 Launch Team", []string{"team@example.com"}, nil, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", s.indexPath())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
drop table person_fts;
create virtual table person_fts using fts5(person_id unindexed, names, aliases, handles, tokenize='unicode61');
insert into person_fts(person_id, names, aliases, handles)
select id, name, '', '' from people;
update meta set value = '3' where key = 'schema_version';
`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if hits, err := s.Search("茶社"); err != nil {
		t.Fatal(err)
	} else if len(hits) == 0 {
		t.Fatal("initial search did not trigger a rebuilt hit")
	}
	assertClawdexTrigramDDL(t, s)
	assertClawdexFTSSearch(t, s, "茶社")
	assertClawdexFTSSearch(t, s, "鸣茶社")
	assertClawdexFTSSearch(t, s, "鹤鸣茶社")
	assertClawdexFTSSearch(t, s, "launch")

	db, err = sql.Open("sqlite3", s.indexPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var version string
	if err := db.QueryRow(`select value from meta where key = 'schema_version'`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != indexSchemaVersion {
		t.Fatalf("index schema version = %s, want %s", version, indexSchemaVersion)
	}
}

func assertClawdexTrigramDDL(t *testing.T, s Store) {
	t.Helper()
	db, err := sql.Open("sqlite3", s.indexPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var ddl string
	if err := db.QueryRow(`select sql from sqlite_master where name = 'person_fts'`).Scan(&ddl); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ddl, "trigram") {
		t.Fatalf("person_fts was not rebuilt with trigram: %s", ddl)
	}
}

func assertClawdexFTSSearch(t *testing.T, s Store, query string) {
	t.Helper()
	hits, err := s.searchPersonIndex(query)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("query %q returned %d FTS hits, want 1", query, len(hits))
	}
	if query == "茶社" || query == "鸣茶社" {
		t.Logf("query=%q total=%d person_id=%s name=%q", query, len(hits), hits[0].ID, hits[0].Name)
	}
}
