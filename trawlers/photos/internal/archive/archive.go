package archive

import (
	"context"
	"database/sql"
	"fmt"
	"os"
)

type StatusResult struct {
	ArchiveExists                              bool
	Photos                                     int64
	LastSuccessfullyCompletedArchiveUpdateTime string
}

func Status(ctx context.Context, paths Paths) (StatusResult, error) {
	_, err := os.Stat(paths.Database)
	if err != nil {
		if os.IsNotExist(err) {
			return StatusResult{}, nil
		}
		return StatusResult{}, err
	}
	db, err := openExistingArchive(ctx, paths.Database)
	if err != nil {
		return StatusResult{}, err
	}
	defer func() { _ = db.Close() }()
	metrics, err := readStatusMetrics(ctx, db.DB())
	if err != nil {
		return StatusResult{}, err
	}
	return StatusResult{
		ArchiveExists: true,
		Photos:        metrics.PhotoCount,
		LastSuccessfullyCompletedArchiveUpdateTime: metrics.LastImportAt,
	}, nil
}

type statusMetrics struct {
	PhotoCount   int64
	LastImportAt string
}

func readStatusMetrics(ctx context.Context, db *sql.DB) (statusMetrics, error) {
	var metrics statusMetrics
	if err := db.QueryRowContext(ctx, `select count(*) from asset`).Scan(&metrics.PhotoCount); err != nil {
		return statusMetrics{}, fmt.Errorf("read photos status counts: %w", err)
	}
	lastImportAt, err := lastImportAt(ctx, db)
	if err != nil {
		return statusMetrics{}, err
	}
	metrics.LastImportAt = lastImportAt
	return metrics, nil
}

func lastImportAt(ctx context.Context, db *sql.DB) (string, error) {
	var last sql.NullString
	if err := db.QueryRowContext(ctx, `
select max(completed_at)
from crawl_snapshot
where completeness_state = 'complete'
`).Scan(&last); err != nil {
		return "", err
	}
	if last.Valid {
		return localRFC3339(last.String), nil
	}
	return "", nil
}
