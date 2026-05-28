package archive

import (
	"context"
	"os"
	"path/filepath"

	"github.com/openclaw/crawlkit/control"
	"github.com/openclaw/crawlkit/store"
)

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

func Status(ctx context.Context, paths Paths) (control.Status, error) {
	status := control.NewStatus("photoscrawl", "photos archive")
	status.DatabasePath = paths.Database
	if info, err := os.Stat(paths.Database); err == nil {
		status.State = "ready"
		status.DatabaseBytes = info.Size()
		counts, err := counts(ctx, paths.Database)
		if err != nil {
			return control.Status{}, err
		}
		status.Counts = counts
		status.Databases = []control.Database{
			control.SQLiteDatabase("photos", "photos.sqlite", "primary", paths.Database, true, counts),
		}
		return status, nil
	}
	status.State = "missing"
	status.Summary = "photos.sqlite has not been initialized"
	return status, nil
}

func counts(ctx context.Context, dbPath string) ([]control.Count, error) {
	db, err := store.OpenReadOnly(ctx, dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	tables := []string{
		"source_library", "asset", "asset_resource", "album_membership",
		"location_observation", "visual_observation", "text_observation",
		"face_observation", "evidence_ref", "edge",
	}
	out := make([]control.Count, 0, len(tables))
	for _, table := range tables {
		var n int64
		if err := db.DB().QueryRowContext(ctx, "select count(*) from "+store.QuoteIdent(table)).Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, control.NewCount(table, table, n))
	}
	return out, nil
}
