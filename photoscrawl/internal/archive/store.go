package archive

import (
	"context"

	"github.com/openclaw/crawlkit/store"
)

func openWritableStore(ctx context.Context, paths Paths) (*store.Store, error) {
	return store.Open(ctx, store.Options{
		Path:          paths.Database,
		Schema:        Schema,
		SchemaVersion: SchemaVersion,
	})
}
