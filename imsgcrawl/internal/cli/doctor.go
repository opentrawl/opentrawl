package cli

import (
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"

	"github.com/openclaw/crawlkit/render"
	"github.com/openclaw/imsgcrawl/internal/archive"
	"github.com/openclaw/imsgcrawl/internal/messages"
)

const fullDiskAccessRemedy = "grant Full Disk Access to your terminal or Trawl in System Settings > Privacy & Security > Full Disk Access"

type doctorOutput struct {
	Checks  []doctorCheck         `json:"checks"`
	Log     *render.DoctorLogTail `json:"log,omitempty"`
	logTail render.LogTail
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
	fs := flag.NewFlagSet("imsgcrawl doctor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("doctor takes no arguments"))
	}
	checks := []doctorCheck{
		r.checkSourceStore(),
		r.checkArchive(),
		r.checkFullDiskAccess(),
	}
	rawLog := renderLogTail(r.readLogTail())
	return r.print(doctorOutput{Checks: checks, Log: render.DoctorLogTailOutput(rawLog), logTail: rawLog})
}

func (r *runtime) checkSourceStore() doctorCheck {
	if _, err := messages.Status(r.ctx, r.dbPath); err != nil {
		_ = r.logError("source_store_failed", worldMustChange(err, "cannot read the source database", "check the --db path and grant Full Disk Access if the Messages database is protected"))
		return doctorCheck{
			ID:      "source_store",
			State:   "fail",
			Message: "cannot read the source database",
			Remedy:  "check the --db path and grant Full Disk Access if the Messages database is protected",
		}
	}
	return doctorCheck{ID: "source_store", State: "ok"}
}

func (r *runtime) checkArchive() doctorCheck {
	if !archive.Exists(r.archivePath) {
		_ = r.logError("archive_missing", worldMustChange(nil, "archive.db has not been synced", "run imsgcrawl sync"))
		return doctorCheck{
			ID:      "archive",
			State:   "fail",
			Message: "archive.db has not been synced",
			Remedy:  "run imsgcrawl sync",
		}
	}
	st, err := archive.OpenExisting(r.ctx, r.archivePath)
	if err != nil {
		_ = r.logError("archive_open_failed", worldMustChange(err, "cannot read the archive database", "run imsgcrawl sync to rebuild the archive"))
		return doctorCheck{
			ID:      "archive",
			State:   "fail",
			Message: "cannot read the archive database",
			Remedy:  "run imsgcrawl sync to rebuild the archive",
		}
	}
	defer func() { _ = st.Close() }()
	if _, err := st.Status(r.ctx); err != nil {
		_ = r.logError("archive_status_failed", worldMustChange(err, "cannot inspect the archive database", "run imsgcrawl sync to rebuild the archive"))
		return doctorCheck{
			ID:      "archive",
			State:   "fail",
			Message: "cannot inspect the archive database",
			Remedy:  "run imsgcrawl sync to rebuild the archive",
		}
	}
	if _, err := st.Chats(r.ctx, 1); errors.Is(err, archive.ErrSchemaOutdated) {
		_ = r.logError("archive_schema_outdated", worldMustChange(err, err.Error(), "run imsgcrawl sync"))
		return doctorCheck{
			ID:      "archive",
			State:   "fail",
			Message: "archive schema predates this version",
			Remedy:  "run imsgcrawl sync to upgrade the archive schema",
		}
	}
	return doctorCheck{ID: "archive", State: "ok"}
}

func (r *runtime) checkFullDiskAccess() doctorCheck {
	dir := filepath.Dir(r.dbPath)
	if err := canReadDirectory(dir); err != nil {
		_ = r.logError("full_disk_access_failed", worldMustChange(err, "cannot read the Messages directory", fullDiskAccessRemedy))
		return doctorCheck{
			ID:      "full_disk_access",
			State:   "fail",
			Message: "cannot read the Messages directory",
			Remedy:  fullDiskAccessRemedy,
		}
	}
	return doctorCheck{ID: "full_disk_access", State: "ok"}
}

func canReadDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = f.Readdirnames(1)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}
