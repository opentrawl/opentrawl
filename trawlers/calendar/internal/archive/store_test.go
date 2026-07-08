package archive

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestOpenMigratesV2ArchiveToCurrentSchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "calendar.db")
	createV2Archive(t, path, false)

	st, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	if got := schemaVersion(t, st.DB()); got != SchemaVersion {
		t.Fatalf("schema version = %d, want %d", got, SchemaVersion)
	}
	for _, column := range []struct {
		table string
		name  string
	}{
		{"calendars", "meaning"},
		{"calendars", "meaning_stated_at"},
		{"events", "availability"},
	} {
		if !columnExists(t, st.DB(), column.table, column.name) {
			t.Fatalf("%s.%s missing after migration", column.table, column.name)
		}
	}
	var meaning string
	if err := st.DB().QueryRowContext(ctx, `select meaning from calendars where calendar_id = 'work'`).Scan(&meaning); err != nil {
		t.Fatal(err)
	}
	if meaning != "" {
		t.Fatalf("migrated calendar meaning = %q, want empty default", meaning)
	}
}

func TestOpenMigrationFailureDoesNotStampCurrentSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "calendar.db")
	createV2Archive(t, path, true)

	st, err := Open(context.Background(), path)
	if err == nil {
		_ = st.Close()
		t.Fatal("Open succeeded, want migration failure")
	}

	db := openSQLite(t, path)
	defer func() { _ = db.Close() }()
	if got := schemaVersion(t, db); got != 2 {
		t.Fatalf("schema version after failed migration = %d, want 2", got)
	}
}

func createV2Archive(t *testing.T, path string, duplicateMeaning bool) {
	t.Helper()
	db := openSQLite(t, path)
	defer func() { _ = db.Close() }()
	mustArchiveExec(t, db, `
create table calendars (
  calendar_id text primary key,
  source_row_id integer not null,
  title text not null,
  type integer not null default 0,
  external_id text not null default '',
  store_id integer not null default 0,
  account_name text not null default '',
  account_type integer not null default 0,
  account_disabled integer not null default 0,
  sync_run_id text not null default ''
)`)
	if duplicateMeaning {
		mustArchiveExec(t, db, `alter table calendars add column meaning text default ''`)
	}
	mustArchiveExec(t, db, `
create table events (
  event_uid text primary key,
  source_row_id integer not null,
  uuid text not null default '',
  unique_identifier text not null default '',
  calendar_id text not null,
  calendar_title text not null,
  calendar_type integer not null default 0,
  calendar_external_id text not null default '',
  account_name text not null default '',
  account_type integer not null default 0,
  start_time text not null,
  end_time text not null,
  start_unix integer not null,
  end_unix integer not null,
  all_day integer not null default 0,
  summary text not null default '',
  description text not null default '',
  status text not null default '',
  url text not null default '',
  has_recurrences integer not null default 0,
  organizer_name text not null default '',
  organizer_email text not null default '',
  organizer_phone text not null default '',
  location_title text not null default '',
  location_address text not null default '',
  attendees_json text not null default '[]',
  participants_text text not null default '',
  fingerprint text not null default '',
  sync_run_id text not null default '',
  foreign key(calendar_id) references calendars(calendar_id)
)`)
	mustArchiveExec(t, db, `insert into calendars(calendar_id, source_row_id, title) values ('work', 10, 'Work')`)
	mustArchiveExec(t, db, `create table schema_migrations(version integer not null)`)
	mustArchiveExec(t, db, `insert into schema_migrations(version) values (2)`)
}

func openSQLite(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func columnExists(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	rows, err := db.Query(`pragma table_info(` + table + `)`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, pk int
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			t.Fatal(err)
		}
		if name == column {
			return true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return false
}

func schemaVersion(t *testing.T, db *sql.DB) int {
	t.Helper()
	var version int
	if err := db.QueryRow(`select coalesce(max(version), 0) from schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	return version
}

func mustArchiveExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}
