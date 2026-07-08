package archive

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	"github.com/openclaw/crawlkit/control"
	"github.com/openclaw/crawlkit/render"
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

func Status(ctx context.Context, paths Paths) (StatusResult, error) {
	status := newStatus("photos archive")
	status.DatabasePath = paths.Database
	info, err := os.Stat(paths.Database)
	if err != nil {
		if os.IsNotExist(err) {
			status.State = "missing"
			status.Summary = "photos.db has not been initialised"
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
		control.SQLiteDatabase("photos", "photos.db", "primary", paths.Database, true, metrics.Counts),
	}
	return status, nil
}

func newStatus(summary string) StatusResult {
	base := control.NewStatus("photos", summary)
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
	defer func() { _ = db.Close() }()
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
	queuedForClassify, queuedNeedsDownload, classificationQueuePending, err := readQueueCounts(ctx, db.DB())
	if err != nil {
		return statusMetrics{}, err
	}
	metrics.Counts = append(metrics.Counts,
		control.NewCount("queued_for_classify", "queued for classify", queuedForClassify),
		control.NewCount("queued_needs_download", "queued needs download", queuedNeedsDownload),
		control.NewCount("classification_queue_pending", "classification queue pending", classificationQueuePending),
	)
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
		return "photos.db is initialised but has no crawled photos"
	case 1:
		return "1 photo archived"
	default:
		return fmt.Sprintf("%s photos archived", render.FormatInteger(photos))
	}
}

func readQueueCounts(ctx context.Context, db *sql.DB) (queuedForClassify, queuedNeedsDownload, classificationQueuePending int64, err error) {
	err = db.QueryRowContext(ctx, `
select
  coalesce(sum(case when state in ('pending', 'metadata_classified', 'place_pending') then 1 else 0 end), 0),
  coalesce(sum(case when state in ('pending', 'metadata_classified', 'place_pending') and needs_download <> 0 then 1 else 0 end), 0),
  coalesce(sum(case when state = 'pending' then 1 else 0 end), 0)
from classification_queue
`).Scan(&queuedForClassify, &queuedNeedsDownload, &classificationQueuePending)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("read classification queue counts: %w", err)
	}
	return queuedForClassify, queuedNeedsDownload, classificationQueuePending, nil
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
