package cli

import (
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"strings"

	"github.com/openclaw/crawlkit/render"
	"github.com/openclaw/wacrawl/internal/whatsappdb"
)

func (a *app) runDoctor(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	source := fs.String("source", a.source, "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printCommandUsage(a.stdout, "doctor")
			return nil
		}
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("doctor takes flags only"))
	}
	src, discoverErr := whatsappdb.Discover(ctx, *source)
	canaryRan := src.Available && strings.TrimSpace(src.ChatDB) != ""
	var canaryErr error
	if canaryRan {
		canaryErr = sourceCanary(ctx, src)
	}
	checks := []doctorCheck{
		sourceStoreCheck(src, discoverErr, canaryErr),
		a.archiveCheck(ctx),
	}
	if canaryRan {
		check, ok := fullDiskAccessCheck(canaryErr)
		if ok {
			checks = append(checks, check)
		}
	}
	logTail := a.logTail()
	return a.print(doctorEnvelope{Checks: checks, LastRun: logTail.LastRun, Error: logTail.Error})
}

type doctorEnvelope struct {
	Checks  []doctorCheck     `json:"checks"`
	LastRun *logRunEnvelope   `json:"last_run,omitempty"`
	Error   *logErrorEnvelope `json:"recent_error,omitempty"`
}

type doctorCheck struct {
	ID      string `json:"id"`
	State   string `json:"state"`
	Message string `json:"message,omitempty"`
	Remedy  string `json:"remedy,omitempty"`
}

func sourceStoreCheck(source whatsappdb.Source, discoverErr, canaryErr error) doctorCheck {
	check := doctorCheck{ID: "source_store"}
	chatDB := strings.TrimSpace(source.ChatDB)
	var chatDBStatErr error
	if chatDB != "" {
		_, chatDBStatErr = os.Stat(chatDB)
	}
	switch {
	case discoverErr != nil && isPermissionError(discoverErr):
		check.State = "ok"
		check.Message = "WhatsApp Desktop store path found"
	case discoverErr != nil:
		check.State = "fail"
		check.Message = discoverErr.Error()
		check.Remedy = "install WhatsApp Desktop, open it once, or pass --source PATH"
	case !source.Available:
		check.State = "missing"
		check.Message = "WhatsApp Desktop store was not found"
		check.Remedy = "install WhatsApp Desktop, open it once, or pass --source PATH"
	case chatDB == "":
		check.State = "missing"
		check.Message = "WhatsApp Desktop chat database was not found"
		check.Remedy = "open WhatsApp Desktop once, then run wacrawl sync"
	case errors.Is(chatDBStatErr, os.ErrNotExist):
		check.State = "missing"
		check.Message = "WhatsApp Desktop chat database was not found"
		check.Remedy = "open WhatsApp Desktop once, then run wacrawl sync"
	case chatDBStatErr != nil && !isPermissionError(chatDBStatErr):
		check.State = "fail"
		check.Message = chatDBStatErr.Error()
		check.Remedy = "check the WhatsApp Desktop store path, then run wacrawl doctor again"
	case canaryErr != nil && !isPermissionError(canaryErr):
		check.State = "fail"
		check.Message = "cannot read WhatsApp Desktop database: " + canaryErr.Error()
		check.Remedy = "close WhatsApp Desktop if it is busy, then run wacrawl doctor again"
	default:
		check.State = "ok"
		check.Message = "WhatsApp Desktop store found"
	}
	return check
}

func sourceCanary(ctx context.Context, source whatsappdb.Source) error {
	_, err := queryReadOnlySQL(ctx, source.ChatDB, "SELECT count(*) AS tables FROM sqlite_master")
	return err
}

func (a *app) archiveCheck(ctx context.Context) doctorCheck {
	check := doctorCheck{ID: "archive"}
	info, err := os.Stat(a.dbPath)
	switch {
	case strings.TrimSpace(a.dbPath) == "":
		check.State = "fail"
		check.Message = "archive database path is empty"
		check.Remedy = "pass --db PATH or run wacrawl sync with the default archive path"
	case err != nil && errors.Is(err, os.ErrNotExist):
		check.State = "missing"
		check.Message = "archive database does not exist"
		check.Remedy = "run wacrawl sync"
	case err != nil:
		check.State = "error"
		check.Message = err.Error()
		check.Remedy = "check the archive path, then run wacrawl sync"
	case info.IsDir():
		check.State = "fail"
		check.Message = "archive path is a directory"
		check.Remedy = "pass --db PATH pointing at a SQLite database, then run wacrawl sync"
	default:
		if _, err := queryReadOnlySQL(ctx, a.dbPath, "SELECT count(*) AS tables FROM sqlite_master"); err != nil {
			check.State = "error"
			check.Message = err.Error()
			check.Remedy = "move the corrupt archive aside, then run wacrawl sync"
			return check
		}
		check.State = "ok"
		check.Message = "archive database opened"
	}
	return check
}

func fullDiskAccessCheck(canaryErr error) (doctorCheck, bool) {
	check := doctorCheck{ID: "full_disk_access"}
	switch {
	case canaryErr == nil:
		check.State = "ok"
		check.Message = "source database canary read succeeded"
		return check, true
	case isPermissionError(canaryErr):
		check.State = "fail"
		check.Message = "cannot read the WhatsApp Desktop database"
		check.Remedy = "grant Full Disk Access to your terminal or Trawl in System Settings > Privacy & Security > Full Disk Access"
		return check, true
	default:
		return doctorCheck{}, false
	}
}

func isPermissionError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrPermission) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "permission denied") ||
		strings.Contains(message, "operation not permitted") ||
		strings.Contains(message, "not authorized") ||
		strings.Contains(message, "authorization denied")
}

func (a *app) printDoctor(envelope doctorEnvelope) error {
	checks := make([]render.Check, 0, len(envelope.Checks))
	for _, check := range envelope.Checks {
		checks = append(checks, render.Check{
			Name:    doctorCheckLabel(check.ID),
			State:   render.CheckState(check.State),
			Message: check.Message,
			Remedy:  check.Remedy,
		})
	}
	return render.WriteDoctor(a.stdout, checks, renderLogTail(logTailEnvelope{LastRun: envelope.LastRun, Error: envelope.Error}))
}

func doctorCheckLabel(id string) string {
	switch id {
	case "source_store":
		return "source store"
	case "full_disk_access":
		return "full disk access"
	default:
		return strings.ReplaceAll(id, "_", " ")
	}
}
