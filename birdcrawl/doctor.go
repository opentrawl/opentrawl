package birdcrawl

import (
	"context"
	"errors"
	"strings"

	"github.com/openclaw/crawlkit"
	"github.com/opentrawl/opentrawl/birdcrawl/internal/store"
	"github.com/opentrawl/opentrawl/birdcrawl/internal/xapi"
)

func (r *runtime) doctor(ctx context.Context) (*crawlkit.Doctor, error) {
	checks := []doctorCheck{}
	cfg, err := loadBirdConfig(r.configPath)
	if err != nil {
		cfg = birdConfig{MonthlyBudgetMicros: defaultMonthlyBudgetUSDMicros}
	}
	var status store.Status
	err = r.withReadOnlyStore(func(st *store.Store) error {
		checks = append(checks, r.dbIntegrityCheck(st))
		checks = append(checks, r.ftsParityCheck(st))
		var err error
		status, err = st.Status(r.ctx)
		if err != nil {
			return err
		}
		checks = append(checks, dumpImportedCheck(status))
		checks = append(checks, stalenessCheck(status))
		return nil
	})
	if err != nil {
		integrityMessage, indexMessage, remedy := "archive database cannot be opened", "search index cannot be checked", "run trawl twitter import archive PATH."
		if errors.Is(err, store.ErrSchemaOutdated) {
			integrityMessage, indexMessage, remedy = "archive schema needs one sync to finish upgrading", "search index cannot be checked until the archive upgrades", "run trawl twitter sync."
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
		checks = append(checks, dumpImportedCheck(store.Status{}))
		checks = append(checks, stalenessCheck(store.Status{}))
	}
	checks = append(checks, credentialsPresentCheck())
	checks = append(checks, budgetHeadroomCheck(status, cfg))
	checks = append(checks, r.xAPIUserProbeCheck(cfg, status))
	return &crawlkit.Doctor{Checks: crawlkitChecks(punctuateDoctorChecks(checks))}, nil
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

func dumpImportedCheck(status store.Status) doctorCheck {
	if status.LastImportAt.IsZero() {
		return doctorCheck{ID: "dump_imported", State: "missing", Message: "no X archive dump has been imported", Remedy: "run trawl twitter import archive PATH."}
	}
	return doctorCheck{ID: "dump_imported", State: "ok", Message: "X archive dump has been imported"}
}

func stalenessCheck(status store.Status) doctorCheck {
	if status.Tweets == 0 {
		return doctorCheck{ID: "sync_recency", State: "missing", Message: "archive is empty", Remedy: "run trawl twitter import archive PATH."}
	}
	if status.LastLiveSync.IsZero() {
		return doctorCheck{ID: "sync_recency", State: "stale", Message: "live X API sync has not run", Remedy: "Set up X API credentials and run trawl twitter sync."}
	}
	return doctorCheck{ID: "sync_recency", State: "ok", Message: "live sync has run"}
}

func credentialsPresentCheck() doctorCheck {
	if xapi.CredentialsPresent(xapi.DefaultCredentialsPath()) {
		return doctorCheck{ID: "credentials_present", State: "ok", Message: "OAuth credentials file is present"}
	}
	return doctorCheck{ID: "credentials_present", State: "missing", Message: "OAuth credentials file is missing or incomplete", Remedy: "Create ~/.opentrawl/twitter/credentials.toml with OAuth user tokens and 0600 permissions."}
}

func budgetHeadroomCheck(status store.Status, cfg birdConfig) doctorCheck {
	remaining := cfg.MonthlyBudgetMicros - status.SpendMicros
	if remaining <= 0 {
		return doctorCheck{ID: "monthly_budget", State: "warn", Message: monthlyBudgetSpentMessage(status.SpendMonth), Remedy: "Raise monthly_budget_usd in config or wait for next month."}
	}
	return doctorCheck{ID: "monthly_budget", State: "ok", Message: "monthly X API budget has headroom"}
}

func (r *runtime) xAPIUserProbeCheck(cfg birdConfig, status store.Status) doctorCheck {
	if !xapi.CredentialsPresent(xapi.DefaultCredentialsPath()) {
		return doctorCheck{ID: "x_account_reachable", State: "skipped", Message: "skipped: credentials are missing (this is the one networked check)"}
	}
	if cfg.MonthlyBudgetMicros-status.SpendMicros <= xapi.PriceUserMicros {
		return doctorCheck{ID: "x_account_reachable", State: "skipped", Message: "skipped: monthly X API budget is spent", Remedy: "Raise monthly_budget_usd in config or wait for next month."}
	}
	client, err := xapi.New(xapi.Options{BaseURL: xapiBaseURL, HTTPClient: xapiHTTPClient})
	if err != nil {
		return doctorCheck{ID: "x_account_reachable", State: "fail", Message: "could not load OAuth credentials for the networked check", Remedy: "Check ~/.opentrawl/twitter/credentials.toml."}
	}
	_, _, err = client.Me(r.ctx)
	if err != nil {
		return doctorCheck{ID: "x_account_reachable", State: "fail", Message: "X did not accept the account probe (the one networked check)", Remedy: "Refresh the OAuth credentials and re-run trawl twitter doctor."}
	}
	return doctorCheck{ID: "x_account_reachable", State: "ok", Message: "X account is reachable (the one networked check)"}
}

func punctuateDoctorChecks(checks []doctorCheck) []doctorCheck {
	for i := range checks {
		checks[i].Message = withFullStop(checks[i].Message)
	}
	return checks
}

func crawlkitChecks(checks []doctorCheck) []crawlkit.Check {
	out := make([]crawlkit.Check, 0, len(checks))
	for _, check := range checks {
		out = append(out, crawlkit.Check{
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
