package state

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func TestSetGetAndStale(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite3", "file:"+filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := EnsureSchema(ctx, db); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	store := NewWithClock(db, func() time.Time { return now })
	if err := store.Set(ctx, "share", "repo", "last_import", "2026-05-01T12:00:00Z"); err != nil {
		t.Fatal(err)
	}
	rec, ok, err := store.Get(ctx, "share", "repo", "last_import")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || rec.Value == "" {
		t.Fatalf("record not found: %+v", rec)
	}
	stale, err := store.IsStale(ctx, "share", "repo", "last_import", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if stale {
		t.Fatal("fresh record reported stale")
	}
	store.now = func() time.Time { return now.Add(2 * time.Hour) }
	stale, err = store.IsStale(ctx, "share", "repo", "last_import", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !stale {
		t.Fatal("old record reported fresh")
	}
}

func TestDefaultConstructorsAndManifestKey(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite3", "file:"+filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := EnsureSchema(ctx, db); err != nil {
		t.Fatal(err)
	}
	store := New(db)
	source, entityType, entityID := ManifestKey("share")
	if err := store.Set(ctx, source, entityType, entityID, "2026-05-01T12:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.Get(ctx, source, entityType, entityID); err != nil || !ok {
		t.Fatalf("manifest record ok=%v err=%v", ok, err)
	}

	cursorDB, err := sql.Open("sqlite3", "file:"+filepath.Join(t.TempDir(), "cursor.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer cursorDB.Close()
	if err := EnsureCursorSchema(ctx, cursorDB); err != nil {
		t.Fatal(err)
	}
	cursor := NewCursor(cursorDB)
	if err := cursor.Set(ctx, "share", "manifest", "generated_at", "cursor"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := cursor.Get(ctx, "share", "manifest", "generated_at"); err != nil || !ok {
		t.Fatalf("cursor record ok=%v err=%v", ok, err)
	}
}

func TestCursorStoreSetGetAndStale(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite3", "file:"+filepath.Join(t.TempDir(), "cursor.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := EnsureCursorSchema(ctx, db); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	store := NewCursorWithClock(db, func() time.Time { return now })
	if err := store.Set(ctx, "share", "manifest", "generated_at", "2026-05-01T12:00:00Z"); err != nil {
		t.Fatal(err)
	}
	rec, ok, err := store.Get(ctx, "share", "manifest", "generated_at")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || rec.Cursor == "" {
		t.Fatalf("record not found: %+v", rec)
	}
	stale, err := store.IsStale(ctx, "share", "manifest", "generated_at", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if stale {
		t.Fatal("fresh cursor record reported stale")
	}
	store.now = func() time.Time { return now.Add(2 * time.Hour) }
	stale, err = store.IsStale(ctx, "share", "manifest", "generated_at", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !stale {
		t.Fatal("old cursor record reported fresh")
	}
}

func TestStateSchemasCoexistInOneDatabase(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite3", "file:"+filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := EnsureSchema(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := EnsureCursorSchema(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := New(db).Set(ctx, "share", "repo", "last_import", "value"); err != nil {
		t.Fatal(err)
	}
	if err := NewCursor(db).Set(ctx, "share", "manifest", "generated_at", "cursor"); err != nil {
		t.Fatal(err)
	}
}

func TestMappedStoresUseExistingSyncStateTables(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite3", "file:"+filepath.Join(t.TempDir(), "mapped.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `
create table notcrawl_state(source text not null, kind text not null, entity text not null, cursor text not null, synced_at text not null, primary key(source, kind, entity));
`); err != nil {
		t.Fatal(err)
	}
	cursor, err := NewCursorMapped(db, CursorMapping{Table: "notcrawl_state", Source: "source", EntityType: "kind", EntityID: "entity", Cursor: "cursor", SyncedAt: "synced_at"})
	if err != nil {
		t.Fatal(err)
	}
	if err := cursor.Set(ctx, "api", "page", "one", "done"); err != nil {
		t.Fatal(err)
	}
	if rec, ok, err := cursor.Get(ctx, "api", "page", "one"); err != nil || !ok || rec.Cursor != "done" {
		t.Fatalf("mapped cursor record = %+v ok=%v err=%v", rec, ok, err)
	}
	if _, err := NewCursorMapped(db, CursorMapping{Table: `bad"table`, Source: "source", EntityType: "kind", EntityID: "entity", Cursor: "cursor", SyncedAt: "synced_at"}); err == nil {
		t.Fatal("unsafe mapped identifier should fail")
	}
}
