package cli

import (
	"errors"

	"github.com/opentrawl/opentrawl/calcrawl/internal/archive"
	"github.com/opentrawl/opentrawl/calcrawl/internal/calendarstore"
)

const fullDiskAccessRemedy = "grant Full Disk Access to your terminal or Trawl in System Settings > Privacy & Security > Full Disk Access"

type doctorOutput struct {
	Checks []doctorCheck `json:"checks"`
	Log    logTailOutput `json:"-"`
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
	fs, err := r.parseNoFlags("doctor", args)
	if err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("doctor takes no arguments"))
	}
	return r.print(doctorOutput{Checks: []doctorCheck{
		r.checkSourceStore(),
		r.checkArchivePresent(),
		r.checkArchiveSchema(),
	}, Log: r.logTail()})
}

func (r *runtime) checkSourceStore() doctorCheck {
	if err := calendarstore.CanaryRead(r.ctx, calendarstore.DefaultPath()); err != nil {
		return doctorCheck{
			ID:      "source_store",
			State:   "fail",
			Message: "cannot read the Calendar database",
			Remedy:  fullDiskAccessRemedy,
		}
	}
	return doctorCheck{ID: "source_store", State: "ok"}
}

func (r *runtime) checkArchivePresent() doctorCheck {
	if !archive.Exists(archive.DefaultPath()) {
		return doctorCheck{
			ID:      "archive",
			State:   "fail",
			Message: "archive has not been synced",
			Remedy:  "run: calcrawl sync",
		}
	}
	return doctorCheck{ID: "archive", State: "ok"}
}

func (r *runtime) checkArchiveSchema() doctorCheck {
	st, err := archive.OpenExisting(r.ctx, archive.DefaultPath())
	if err != nil {
		return doctorCheck{
			ID:      "schema",
			State:   "fail",
			Message: "archive schema is not current",
			Remedy:  "run: calcrawl sync",
		}
	}
	defer func() { _ = st.Close() }()
	if _, err := st.Status(r.ctx); err != nil {
		return doctorCheck{
			ID:      "schema",
			State:   "fail",
			Message: "archive schema could not be inspected",
			Remedy:  "run: calcrawl sync",
		}
	}
	return doctorCheck{ID: "schema", State: "ok"}
}
