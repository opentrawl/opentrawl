package archive

import (
	"context"
	"path/filepath"
	"testing"
)

func TestInitAndStatus(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	paths := Paths{ArchiveRoot: t.TempDir()}
	paths.Database = filepath.Join(paths.ArchiveRoot, "photos.sqlite")

	before, err := Status(ctx, paths)
	if err != nil {
		t.Fatal(err)
	}
	if before.State != "missing" {
		t.Fatalf("state before init = %q, want missing", before.State)
	}

	result, err := Init(ctx, paths)
	if err != nil {
		t.Fatal(err)
	}
	if result.Database != paths.Database {
		t.Fatalf("database = %q, want %q", result.Database, paths.Database)
	}

	after, err := Status(ctx, paths)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != "ready" {
		t.Fatalf("state after init = %q, want ready", after.State)
	}
	if len(after.Counts) == 0 {
		t.Fatal("status returned no counts")
	}
}
