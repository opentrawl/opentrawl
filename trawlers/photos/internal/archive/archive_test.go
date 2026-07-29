package archive

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/opentrawl/opentrawl/trawlkit/store"
)

func TestStatusRejectsIncompatibleArchiveWithoutMigration(t *testing.T) {
	ctx := context.Background()
	paths := Paths{Database: filepath.Join(t.TempDir(), "photos.db")}
	db, err := store.Open(ctx, store.Options{
		Path:          paths.Database,
		Schema:        Schema,
		SchemaVersion: SchemaVersion - 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = Status(ctx, paths)
	if !errors.Is(err, ArchiveIncompatibleError{}) {
		t.Fatalf("status error = %v, want incompatible archive", err)
	}

	readStore, err := store.OpenReadOnly(ctx, paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = readStore.Close() }()
	version, err := readStore.SchemaVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if version != SchemaVersion-1 {
		t.Fatalf("schema version after status = %d, want %d", version, SchemaVersion-1)
	}
}
