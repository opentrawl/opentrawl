package calendarstore

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestTableHasColumnDetectsCalendarItemAvailability(t *testing.T) {
	ctx := context.Background()
	db := openSchemaTestDB(t)
	mustSchemaExec(t, db, `create table CalendarItem(ROWID integer primary key, summary text, availability integer)`)
	ok, err := tableHasColumn(ctx, db, "CalendarItem", "availability")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("CalendarItem.availability was not detected")
	}
	ok, err = tableHasColumn(ctx, db, "CalendarItem", "missing_flag")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("missing CalendarItem column was reported as present")
	}
}

func openSchemaTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "Calendar.sqlitedb"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func mustSchemaExec(t *testing.T, db *sql.DB, query string) {
	t.Helper()
	if _, err := db.Exec(query); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}
