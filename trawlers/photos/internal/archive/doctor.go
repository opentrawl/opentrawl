package archive

import (
	"context"
	"os"

	"github.com/openclaw/crawlkit/store"
)

const (
	fullDiskAccessRemedy = "grant Full Disk Access to your terminal or Trawl in System Settings > Privacy & Security > Full Disk Access"
	archiveRemedy        = "run trawl photos sync"
)

type DoctorOptions struct {
	LibraryPath string
}

type DoctorResult struct {
	Checks []DoctorCheck `json:"checks"`
}

type DoctorCheck struct {
	ID      string `json:"id"`
	State   string `json:"state"`
	Message string `json:"message,omitempty"`
	Remedy  string `json:"remedy,omitempty"`
}

func Doctor(ctx context.Context, paths Paths, opts DoctorOptions) (DoctorResult, error) {
	checks := sourceStoreChecks(opts.LibraryPath)
	checks = append(checks, archiveCheck(ctx, paths))
	return DoctorResult{Checks: checks}, nil
}

func archiveCheck(ctx context.Context, paths Paths) DoctorCheck {
	if info, err := os.Stat(paths.Database); err != nil {
		if os.IsNotExist(err) {
			return DoctorCheck{
				ID:      "archive",
				State:   "missing",
				Message: "photos.db has not been initialised",
				Remedy:  archiveRemedy,
			}
		}
		return DoctorCheck{
			ID:      "archive",
			State:   "fail",
			Message: "cannot access photos.db archive",
			Remedy:  archiveRemedy,
		}
	} else if info.IsDir() {
		return DoctorCheck{
			ID:      "archive",
			State:   "fail",
			Message: "photos.db archive path is a directory",
			Remedy:  archiveRemedy,
		}
	}

	db, err := store.OpenReadOnly(ctx, paths.Database)
	if err != nil {
		return DoctorCheck{
			ID:      "archive",
			State:   "fail",
			Message: "cannot open photos.db archive",
			Remedy:  archiveRemedy,
		}
	}
	defer func() { _ = db.Close() }()

	var photos int64
	if err := db.DB().QueryRowContext(ctx, `select count(*) from asset`).Scan(&photos); err != nil {
		return DoctorCheck{
			ID:      "archive",
			State:   "fail",
			Message: "photos.db archive is not intact",
			Remedy:  archiveRemedy,
		}
	}
	return DoctorCheck{ID: "archive", State: "ok"}
}
