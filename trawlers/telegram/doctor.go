package telecrawl

import (
	"context"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/telegramdesktop"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

func (c *Crawler) Doctor(ctx context.Context, req *trawlkit.Request) (*trawlkit.Doctor, error) {
	report := telegramdesktop.Probe(ctx, telegramdesktop.Options{Path: c.doctor.Path})
	checks := []trawlkit.Check{sourceStoreCheck(report)}
	checks = append(checks, archiveChecks(ctx, req)...)
	return &trawlkit.Doctor{Checks: checks}, nil
}

func (r *runtime) printDoctor(value doctorOutput) error {
	return render.WriteDoctor(r.stdout, doctorRenderChecks(value.Checks), value.logTail)
}

func sourceStoreCheck(report telegramdesktop.Report) trawlkit.Check {
	if selectedSourceReadable(report) {
		return trawlkit.Check{ID: "source_store", State: "ok", Message: selectedSourceReadableMessage(report)}
	}
	check := trawlkit.Check{
		ID:     "source_store",
		State:  "missing",
		Remedy: "Open the selected Telegram app, then run trawl telegram sync. OpenTrawl reuses its existing local session.",
	}
	switch {
	case !report.Exists || report.Store == "empty":
		check.Message = selectedSourceMessagePrefix(report) + ", but its local data store was not found."
	case report.Error != "":
		check.Message = selectedSourceMessagePrefix(report) + ", but its local data store cannot be read."
	default:
		check.Message = selectedSourceMessagePrefix(report) + ", but its local data store is not readable."
	}
	return check
}

func selectedSourceReadable(report telegramdesktop.Report) bool {
	if !report.Exists || !report.Accessible || report.Error != "" {
		return false
	}
	switch report.Product {
	case "telegram-macos":
		return report.Store == "telegram-macos-postbox"
	case "telegram-desktop":
		return report.Store == "tdesktop-binary"
	default:
		return true
	}
}

func selectedSourceReadableMessage(report telegramdesktop.Report) string {
	return selectedSourceMessagePrefix(report) + ". Its local data is readable."
}

func selectedSourceMessagePrefix(report telegramdesktop.Report) string {
	product := "Telegram Desktop"
	if report.Product == "telegram-macos" {
		product = "Telegram for macOS"
	}
	if report.Explicit {
		return product + " is selected from --path"
	}
	if report.Product == "telegram-macos" {
		return product + " is selected"
	}
	return product + " is selected because Telegram for macOS is not installed"
}

func archiveChecks(ctx context.Context, req *trawlkit.Request) []trawlkit.Check {
	if req.Store == nil {
		return []trawlkit.Check{{
			ID:      "archive",
			State:   "missing",
			Message: "Telegram archive has not been created.",
			Remedy:  "run trawl telegram sync to create the archive.",
		}}
	}
	st, err := store.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return []trawlkit.Check{{
			ID:      "archive",
			State:   "missing",
			Message: "Telegram archive cannot be read.",
			Remedy:  "run trawl telegram sync to rebuild the archive.",
		}}
	}
	defer func() { _ = st.Close() }()
	status, err := st.Status(ctx)
	if err != nil {
		return []trawlkit.Check{{
			ID:      "archive",
			State:   "missing",
			Message: "Telegram archive status cannot be read.",
			Remedy:  "run trawl telegram sync to rebuild the archive.",
		}}
	}
	if status.Messages == 0 {
		return []trawlkit.Check{{ID: "archive", State: "empty", Message: "Archive exists but has no messages.", Remedy: "run trawl telegram sync to fill the archive."}}
	}
	return []trawlkit.Check{
		{ID: "archive", State: "ok", Message: "Archive is readable."},
		trawlkitSyncRecencyCheck(status),
	}
}

func trawlkitSyncRecencyCheck(status store.Status) trawlkit.Check {
	check := trawlkit.Check{ID: "sync_recency", State: "ok", Message: "Recently synced."}
	switch {
	case status.LastImportAt.IsZero():
		check.State = "warn"
		check.Message = "Archive has never been synced."
		check.Remedy = "run trawl telegram sync"
	case time.Since(status.LastImportAt) > statusFreshFor:
		check.State = "warn"
		check.Message = "Archive sync is " + agePhrase(time.Since(status.LastImportAt)) + " old."
		check.Remedy = "run trawl telegram sync"
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
