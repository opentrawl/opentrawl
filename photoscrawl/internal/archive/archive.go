package archive

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/openclaw/crawlkit/control"
	"github.com/openclaw/crawlkit/store"
)

type StatusResult struct {
	SchemaVersion string             `json:"schema_version"`
	AppID         string             `json:"app_id"`
	GeneratedAt   string             `json:"generated_at"`
	State         string             `json:"state"`
	Summary       string             `json:"summary"`
	DatabasePath  string             `json:"database_path,omitempty"`
	DatabaseBytes int64              `json:"database_bytes,omitempty"`
	LastImportAt  string             `json:"last_import_at,omitempty"`
	Counts        []control.Count    `json:"counts,omitempty"`
	Freshness     *StatusFreshness   `json:"freshness,omitempty"`
	Databases     []control.Database `json:"databases,omitempty"`
}

type StatusFreshness struct {
	LastSync string `json:"last_sync,omitempty"`
}

type InitResult struct {
	Database string `json:"database"`
}

func Init(ctx context.Context, paths Paths) (InitResult, error) {
	if err := os.MkdirAll(filepath.Dir(paths.Database), 0o700); err != nil {
		return InitResult{}, err
	}
	db, err := store.Open(ctx, store.Options{
		Path:          paths.Database,
		Schema:        Schema,
		SchemaVersion: SchemaVersion,
	})
	if err != nil {
		return InitResult{}, err
	}
	defer db.Close()
	return InitResult{Database: paths.Database}, nil
}

func Status(ctx context.Context, paths Paths) (StatusResult, error) {
	status := newStatus("photos archive")
	status.DatabasePath = paths.Database
	info, err := os.Stat(paths.Database)
	if err != nil {
		if os.IsNotExist(err) {
			status.State = "missing"
			status.Summary = "photos.sqlite has not been initialized"
			return status, nil
		}
		return StatusResult{}, err
	}
	status.DatabaseBytes = info.Size()
	metrics, err := readStatusMetrics(ctx, paths.Database)
	if err != nil {
		return StatusResult{}, err
	}
	if metrics.PhotoCount == 0 {
		status.State = "empty"
	} else {
		status.State = "ok"
	}
	status.Summary = statusSummary(metrics.PhotoCount)
	status.Counts = metrics.Counts
	status.LastImportAt = metrics.LastImportAt
	if metrics.LastImportAt != "" {
		status.Freshness = &StatusFreshness{LastSync: metrics.LastImportAt}
	}
	status.Databases = []control.Database{
		control.SQLiteDatabase("photos", "photos.sqlite", "primary", paths.Database, true, metrics.Counts),
	}
	return status, nil
}

func newStatus(summary string) StatusResult {
	base := control.NewStatus("photoscrawl", summary)
	return StatusResult{
		SchemaVersion: base.SchemaVersion,
		AppID:         base.AppID,
		GeneratedAt:   base.GeneratedAt,
		State:         base.State,
		Summary:       base.Summary,
	}
}

type statusMetrics struct {
	PhotoCount   int64
	LastImportAt string
	Counts       []control.Count
}

func readStatusMetrics(ctx context.Context, dbPath string) (statusMetrics, error) {
	db, err := store.OpenReadOnly(ctx, dbPath)
	if err != nil {
		return statusMetrics{}, err
	}
	defer db.Close()
	var metrics statusMetrics
	var since sql.NullInt64
	if err := db.DB().QueryRowContext(ctx, `
select count(*),
       min(case
         when creation_date glob '[0-9][0-9][0-9][0-9]*'
         then cast(substr(creation_date, 1, 4) as integer)
       end)
from asset
`).Scan(&metrics.PhotoCount, &since); err != nil {
		return statusMetrics{}, fmt.Errorf("read photos status counts: %w", err)
	}
	metrics.Counts = []control.Count{control.NewCount("photos", "photos", metrics.PhotoCount)}
	if since.Valid && since.Int64 > 0 {
		metrics.Counts = append(metrics.Counts, control.NewCount("since", "since", since.Int64))
	}
	lastImportAt, err := lastImportAt(ctx, db.DB())
	if err != nil {
		return statusMetrics{}, err
	}
	metrics.LastImportAt = lastImportAt
	return metrics, nil
}

func statusSummary(photos int64) string {
	switch photos {
	case 0:
		return "photos.sqlite is initialized but has no crawled photos"
	case 1:
		return "1 photo archived"
	default:
		return fmt.Sprintf("%d photos archived", photos)
	}
}

func lastImportAt(ctx context.Context, db *sql.DB) (string, error) {
	var last sql.NullString
	if err := db.QueryRowContext(ctx, `select max(completed_at) from crawl_snapshot`).Scan(&last); err != nil {
		return "", err
	}
	if last.Valid {
		return localRFC3339(last.String), nil
	}
	return "", nil
}
