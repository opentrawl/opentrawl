package messages

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotPathCopiesSQLiteDatabase(t *testing.T) {
	source := filepath.Join(t.TempDir(), "chat.db")
	db, err := sql.Open("sqlite3", source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`create table message(rowid integer primary key, text text); insert into message(text) values('synthetic');`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	snapshot, err := SnapshotPath(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	snapshotDB, err := sql.Open("sqlite3", snapshot.Path)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := snapshotDB.QueryRow(`select count(*) from message`).Scan(&count); err != nil {
		_ = snapshotDB.Close()
		t.Fatal(err)
	}
	if err := snapshotDB.Close(); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
	root := snapshot.root
	if err := snapshot.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("snapshot root should be removed: %v", err)
	}
}
