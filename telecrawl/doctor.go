package telecrawl

import (
	"context"
	"strings"
	"time"

	"github.com/openclaw/crawlkit"
	"github.com/openclaw/crawlkit/render"
	"github.com/openclaw/telecrawl/internal/store"
	"github.com/openclaw/telecrawl/internal/telegramdesktop"
)

func (c *Crawler) Doctor(ctx context.Context, req *crawlkit.Request) (*crawlkit.Doctor, error) {
	report := telegramdesktop.Probe(ctx, telegramdesktop.Options{Path: c.doctor.Path})
	checks := []crawlkit.Check{sourceStoreCheck(report)}
	checks = append(checks, archiveChecks(ctx, req)...)
	return &crawlkit.Doctor{Checks: checks}, nil
}

func (r *runtime) printDoctor(value doctorOutput) error {
	return render.WriteDoctor(r.stdout, doctorRenderChecks(value.Checks), value.logTail)
}

func sourceStoreCheck(report telegramdesktop.Report) crawlkit.Check {
	if report.Exists && report.Accessible && report.Error == "" {
		return crawlkit.Check{ID: "source_store", State: "ok", Message: "Telegram source data is readable."}
	}
	check := crawlkit.Check{
		ID:     "source_store",
		State:  "missing",
		Remedy: "Install or open Telegram Desktop, or pass --path to a readable Telegram data directory.",
	}
	switch {
	case !report.Exists:
		check.Message = "Telegram source data was not found."
	case report.Error != "":
		check.Message = "Telegram source data could not be read."
	default:
		check.Message = "Telegram source data is not readable."
	}
	return check
}

func archiveChecks(ctx context.Context, req *crawlkit.Request) []crawlkit.Check {
	if req.Store == nil {
		return []crawlkit.Check{{
			ID:      "archive",
			State:   "missing",
			Message: "telecrawl archive has not been created.",
			Remedy:  "Run telecrawl sync to create the archive.",
		}}
	}
	st, err := store.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return []crawlkit.Check{{
			ID:      "archive",
			State:   "missing",
			Message: "telecrawl archive cannot be read.",
			Remedy:  "Run telecrawl sync to rebuild the archive.",
		}}
	}
	defer func() { _ = st.Close() }()
	status, err := st.Status(ctx)
	if err != nil {
		return []crawlkit.Check{{
			ID:      "archive",
			State:   "missing",
			Message: "telecrawl archive status cannot be read.",
			Remedy:  "Run telecrawl sync to rebuild the archive.",
		}}
	}
	if status.Messages == 0 {
		return []crawlkit.Check{{ID: "archive", State: "empty", Message: "Archive exists but has no messages.", Remedy: "Run telecrawl sync to fill the archive."}}
	}
	return []crawlkit.Check{
		{ID: "archive", State: "ok", Message: "Archive is readable."},
		crawlkitSyncRecencyCheck(status),
	}
}

func crawlkitSyncRecencyCheck(status store.Status) crawlkit.Check {
	check := crawlkit.Check{ID: "sync_recency", State: "ok", Message: "Archive sync is fresh."}
	switch {
	case status.LastImportAt.IsZero():
		check.State = "warn"
		check.Message = "Archive has never been synced."
		check.Remedy = "run telecrawl sync"
	case time.Since(status.LastImportAt) > statusFreshFor:
		check.State = "warn"
		check.Message = "Archive sync is " + agePhrase(time.Since(status.LastImportAt)) + " old."
		check.Remedy = "run telecrawl sync"
	}
	return check
}

func doctorRenderChecks(checks []doctorCheck) []render.Check {
	out := make([]render.Check, 0, len(checks))
	for _, check := range checks {
		name := strings.TrimSpace(check.ID)
		if name == "" {
			name = strings.TrimSpace(check.Label)
		}
		out = append(out, render.Check{
			Name:    name,
			State:   render.CheckState(check.State),
			Message: check.Message,
			Remedy:  check.Remedy,
		})
	}
	return out
}
