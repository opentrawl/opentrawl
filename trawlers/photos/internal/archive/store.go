package archive

import (
	"context"
	"errors"
	"fmt"

	"github.com/opentrawl/opentrawl/trawlkit/store"
)

func openArchive(ctx context.Context, path string) (*store.Store, error) {
	return store.Open(ctx, store.Options{Path: path, Schema: Schema})
}

func openExistingArchive(ctx context.Context, path string) (*store.Store, error) {
	return store.OpenReadOnly(ctx, path)
}

func validateReadStore(_ context.Context, openedStore *store.Store) error {
	if openedStore == nil {
		return errors.New("Photos archive store is not open")
	}
	return nil
}

func prepareStore(ctx context.Context, openedStore *store.Store) error {
	if openedStore == nil {
		return errors.New("Photos archive store is not open")
	}
	if _, err := openedStore.DB().ExecContext(ctx, Schema); err != nil {
		return fmt.Errorf("apply current Photos archive schema: %w", err)
	}
	return nil
}
