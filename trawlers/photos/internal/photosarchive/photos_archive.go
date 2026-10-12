// Package photosarchive stores the Photos archive. Its tables are the state of
// every step: a step's work is the keys whose inputs exist and whose output
// row is missing.
package photosarchive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit/store"
)

type PhotosArchive struct {
	database *sql.DB
}

// UsePhotosArchive borrows the archive store opened by trawlkit and applies the
// current schema.
func UsePhotosArchive(ctx context.Context, openedArchiveStore *store.Store) (*PhotosArchive, error) {
	if openedArchiveStore == nil {
		return nil, errors.New("the Photos archive store is not open")
	}
	if _, err := openedArchiveStore.DB().ExecContext(ctx, photosArchiveSchema); err != nil {
		return nil, fmt.Errorf("apply Photos archive schema: %w", err)
	}
	return &PhotosArchive{database: openedArchiveStore.DB()}, nil
}

// UseExistingPhotosArchive borrows a read-only archive store without changing
// its schema. It returns false when no Photos update has written the archive.
func UseExistingPhotosArchive(ctx context.Context, openedArchiveStore *store.Store) (*PhotosArchive, bool, error) {
	if openedArchiveStore == nil {
		return nil, false, nil
	}
	var stepLastRunTableCount int
	if err := openedArchiveStore.DB().QueryRowContext(ctx,
		`select count(*) from sqlite_master where type = 'table' and name = 'photos_step_last_run'`,
	).Scan(&stepLastRunTableCount); err != nil {
		return nil, false, fmt.Errorf("read Photos archive tables: %w", err)
	}
	if stepLastRunTableCount == 0 {
		return nil, false, nil
	}
	return &PhotosArchive{database: openedArchiveStore.DB()}, true, nil
}

func formatArchiveTime(archiveTime time.Time) string {
	return archiveTime.UTC().Format(time.RFC3339Nano)
}

func parseArchiveTime(archiveTimeText string) (time.Time, error) {
	parsedTime, err := time.Parse(time.RFC3339Nano, archiveTimeText)
	if err != nil {
		return time.Time{}, fmt.Errorf("read archive time %q: %w", archiveTimeText, err)
	}
	return parsedTime, nil
}
