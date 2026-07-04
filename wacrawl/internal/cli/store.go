package cli

import (
	"context"
	"errors"
	"os"

	"github.com/openclaw/wacrawl/internal/store"
)

func (a *app) withStore(ctx context.Context, fn func(*store.Store) error) error {
	st, err := store.Open(ctx, a.dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	return fn(st)
}

var errNoArchive = errors.New("no archive yet; run wacrawl sync to create it")

// withReadStore opens the archive read-only so read commands cannot
// change the archive file, per the reads-never-mutate contract rule.
func (a *app) withReadStore(ctx context.Context, fn func(*store.Store) error) error {
	st, err := store.OpenReadOnly(ctx, a.dbPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return worldMustChange(errNoArchive, "run wacrawl sync")
		}
		return err
	}
	defer func() { _ = st.Close() }()
	return fn(st)
}

func (a *app) withExistingStore(ctx context.Context, fn func(*store.Store) error) error {
	if _, err := os.Stat(a.dbPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return worldMustChange(errNoArchive, "run wacrawl sync")
		}
		return err
	}
	return a.withStore(ctx, fn)
}
