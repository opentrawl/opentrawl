package archive

import (
	"context"
	"path/filepath"
	"testing"
)

func TestInitAndStatus(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	paths := Paths{DataDir: root, Database: filepath.Join(root, "photos.sqlite")}

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
	if after.State != "empty" {
		t.Fatalf("state after init = %q, want empty", after.State)
	}
	if len(after.Counts) != 1 || after.Counts[0].ID != "photos" || after.Counts[0].Value != 0 {
		t.Fatalf("status counts after init = %#v", after.Counts)
	}
	if after.Freshness != nil {
		t.Fatalf("freshness after init = %#v", after.Freshness)
	}
}
