package cli

import (
	"errors"

	"github.com/opentrawl/opentrawl/gogcrawl/internal/archive"
)

type doctorOutput struct {
	Checks []doctorCheck `json:"checks"`
}

type doctorCheck struct {
	ID      string `json:"id"`
	State   string `json:"state"`
	Message string `json:"message,omitempty"`
	Remedy  string `json:"remedy,omitempty"`
}

func (r *runtime) runDoctor(args []string) error {
	if hasHelpFlag(args) {
		return printCommandUsage(r.stdout, []string{"doctor"})
	}
	if len(args) != 0 {
		return usageErr(errors.New("doctor takes no arguments"))
	}
	return r.print(doctorOutput{Checks: []doctorCheck{
		r.checkGogBinary(),
		r.checkGogAuth(),
		r.checkArchive(),
	}})
}

func (r *runtime) checkGogBinary() doctorCheck {
	if _, err := r.gog.LookPath(); err != nil {
		return doctorCheck{
			ID:      "gog_binary",
			State:   "fail",
			Message: "gog is not on PATH",
			Remedy:  "install gog and make sure your shell can run gog",
		}
	}
	return doctorCheck{ID: "gog_binary", State: "ok"}
}

func (r *runtime) checkGogAuth() doctorCheck {
	status, err := r.gog.AuthStatus(r.ctx)
	if err != nil {
		return doctorCheck{
			ID:      "gog_auth",
			State:   "fail",
			Message: "gog auth check failed",
			Remedy:  "run gog login <email>",
		}
	}
	if !status.FoundAccount {
		return doctorCheck{
			ID:      "gog_auth",
			State:   "fail",
			Message: "gog has no stored account",
			Remedy:  "run gog login <email>",
		}
	}
	if !status.Authorized {
		return doctorCheck{
			ID:      "gog_auth",
			State:   "fail",
			Message: "gog has no valid stored account",
			Remedy:  "run gog login <email>",
		}
	}
	return doctorCheck{ID: "gog_auth", State: "ok"}
}

func (r *runtime) checkArchive() doctorCheck {
	if !archive.Exists(r.archivePath) {
		return doctorCheck{
			ID:      "archive",
			State:   "fail",
			Message: "archive database has not been synced",
			Remedy:  "run gogcrawl sync",
		}
	}
	st, err := archive.OpenExisting(r.ctx, r.archivePath)
	if err != nil {
		remedy := "run gogcrawl sync to rebuild the archive"
		if errors.Is(err, archive.ErrSchemaMismatch) {
			remedy = "run gogcrawl sync to upgrade the archive schema"
		}
		return doctorCheck{
			ID:      "archive",
			State:   "fail",
			Message: "archive database cannot be read",
			Remedy:  remedy,
		}
	}
	defer func() { _ = st.Close() }()
	if _, err := st.Status(r.ctx); err != nil {
		return doctorCheck{
			ID:      "archive",
			State:   "fail",
			Message: "archive status cannot be read",
			Remedy:  "run gogcrawl sync to rebuild the archive",
		}
	}
	return doctorCheck{ID: "archive", State: "ok"}
}
