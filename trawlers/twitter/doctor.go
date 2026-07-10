package birdcrawl

import (
	"context"
	"errors"
	"strings"

	"github.com/opentrawl/opentrawl/birdcrawl/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit"
)

func (r *runtime) doctor(ctx context.Context) (*trawlkit.Doctor, error) {
	checks := []doctorCheck{}
	var status store.Status
	err := r.withReadOnlyStore(func(st *store.Store) error {
		checks = append(checks, r.dbIntegrityCheck(st))
		checks = append(checks, r.ftsParityCheck(st))
		var err error
		status, err = st.Status(r.ctx)
		if err != nil {
			return err
		}
		checks = append(checks, archiveReadinessCheck(status))
		return nil
	})
	if err != nil {
		schemaOutdated := errors.Is(err, store.ErrSchemaOutdated)
		integrityMessage, indexMessage, remedy := "archive database cannot be opened", "search index cannot be checked", "run trawl twitter import archive PATH."
		if schemaOutdated {
			integrityMessage, indexMessage, remedy = archiveSchemaUpgradeMessage, "search index cannot be checked until the archive upgrades", archiveSchemaUpgradeRemedy
		}
		checks = append(checks, doctorCheck{
			ID:      "database_integrity",
			State:   "missing",
			Message: integrityMessage,
			Remedy:  remedy,
		})
		checks = append(checks, doctorCheck{
			ID:      "search_index",
			State:   "missing",
			Message: indexMessage,
			Remedy:  remedy,
		})
		if schemaOutdated {
			checks = append(checks, archiveSchemaUpgradeCheck())
		} else {
			checks = append(checks, archiveReadinessCheck(store.Status{}))
		}
	}
	return &trawlkit.Doctor{Checks: trawlkitChecks(punctuateDoctorChecks(checks))}, nil
}

func (r *runtime) dbIntegrityCheck(st *store.Store) doctorCheck {
	result, err := st.Integrity(r.ctx)
	if err != nil || result != "ok" {
		return doctorCheck{ID: "database_integrity", State: "fail", Message: "database integrity check failed", Remedy: "Restore the archive from backup or re-run trawl twitter import archive PATH."}
	}
	return doctorCheck{ID: "database_integrity", State: "ok", Message: "database integrity check passed"}
}

func (r *runtime) ftsParityCheck(st *store.Store) doctorCheck {
	tweets, fts, err := st.FTSParity(r.ctx)
	if err != nil {
		return doctorCheck{ID: "search_index", State: "fail", Message: "search index cannot be read", Remedy: "Re-run trawl twitter import archive PATH to rebuild derived search state."}
	}
	if tweets != fts {
		return doctorCheck{ID: "search_index", State: "fail", Message: "search index does not cover every tweet", Remedy: "Re-run trawl twitter import archive PATH to rebuild derived search state."}
	}
	return doctorCheck{ID: "search_index", State: "ok", Message: "search index covers every tweet"}
}

func archiveReadinessCheck(status store.Status) doctorCheck {
	if !archiveReady(status) {
		return doctorCheck{ID: "archive_ready", State: "missing", Message: "no valid local X archive has been imported", Remedy: "run trawl twitter import archive PATH."}
	}
	return doctorCheck{ID: "archive_ready", State: "ok", Message: "local X archive is ready"}
}

func archiveSchemaUpgradeCheck() doctorCheck {
	return doctorCheck{ID: "archive_ready", State: "missing", Message: archiveSchemaUpgradeMessage, Remedy: archiveSchemaUpgradeRemedy}
}

func punctuateDoctorChecks(checks []doctorCheck) []doctorCheck {
	for i := range checks {
		checks[i].Message = withFullStop(checks[i].Message)
	}
	return checks
}

func trawlkitChecks(checks []doctorCheck) []trawlkit.Check {
	out := make([]trawlkit.Check, 0, len(checks))
	for _, check := range checks {
		out = append(out, trawlkit.Check{
			ID:      check.ID,
			State:   check.State,
			Message: check.Message,
			Remedy:  check.Remedy,
		})
	}
	return out
}

func withFullStop(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.HasSuffix(value, ".") {
		return value
	}
	return value + "."
}
