package cli

import (
	"errors"
	"time"

	"github.com/openclaw/crawlkit/control"
	"github.com/opentrawl/opentrawl/calcrawl/internal/archive"
	"github.com/opentrawl/opentrawl/calcrawl/internal/calendarstore"
)

const staleAfter = 24 * time.Hour

type statusText struct {
	control.Status
	Archive *archive.Status `json:"archive,omitempty"`
}

func (r *runtime) runStatus(args []string) error {
	if hasHelpFlag(args) {
		return printCommandUsage(r.stdout, []string{"status"})
	}
	fs, err := r.parseNoFlags("status", args)
	if err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("status takes no arguments"))
	}
	return r.print(r.status())
}

func (r *runtime) status() statusText {
	archivePath := archive.DefaultPath()
	sourcePath := calendarstore.DefaultPath()
	out := statusText{Status: control.NewStatus(archive.AppID, "Archive has not been synced.")}
	out.State = "missing"
	out.DatabasePath = archivePath
	out.Databases = statusDatabases(sourcePath, archivePath, nil)
	if !archive.Exists(archivePath) {
		return out
	}
	st, err := archive.OpenExisting(r.ctx, archivePath)
	if err != nil {
		out.State = "error"
		out.Summary = "Archive could not be read."
		out.Errors = []string{err.Error()}
		return out
	}
	defer func() { _ = st.Close() }()
	status, err := st.Status(r.ctx)
	if err != nil {
		out.State = "error"
		out.Summary = "Archive could not be inspected."
		out.Errors = []string{err.Error()}
		return out
	}
	out.Archive = &status
	out.DatabaseBytes = status.ArchiveBytes
	out.LastSyncAt = localRFC3339(status.LastSyncAt)
	out.Counts = statusCounts(status)
	out.Databases = statusDatabases(sourcePath, archivePath, out.Counts)
	out.Freshness = freshness(status.LastSyncAt)
	switch {
	case status.Events == 0:
		out.State = "empty"
		out.Summary = "Archive is empty."
	case isStale(status):
		out.State = "stale"
		out.Summary = "Archive is stale."
	default:
		out.State = "ok"
		out.Summary = "Archive is fresh."
	}
	return out
}

func statusCounts(status archive.Status) []control.Count {
	return []control.Count{
		control.NewCount("events", "events", status.Events),
		control.NewCount("calendars", "calendars", status.Calendars),
		control.NewCount("since", "since", archive.YearFromUnix(status.EarliestUnix)),
	}
}

func statusDatabases(sourcePath, archivePath string, counts []control.Count) []control.Database {
	return []control.Database{
		control.SQLiteDatabase("source", "Calendar.app store", "source", sourcePath, true, nil),
		control.SQLiteDatabase("archive", "Calendar archive", "archive", archivePath, false, counts),
	}
}

func isStale(status archive.Status) bool {
	lastSync, err := time.Parse(time.RFC3339Nano, status.LastSyncAt)
	if err != nil || time.Since(lastSync) > staleAfter {
		return true
	}
	sourceModified, err := calendarstore.ModifiedAt(calendarstore.DefaultPath())
	if err != nil {
		return true
	}
	syncedSource, err := time.Parse(time.RFC3339Nano, status.SourceModifiedAt)
	if err != nil {
		return true
	}
	return sourceModified.UTC().After(syncedSource.Add(time.Second))
}

func freshness(value string) *control.Freshness {
	if value == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil
	}
	state := "fresh"
	if time.Since(t) > staleAfter {
		state = "stale"
	}
	return &control.Freshness{
		Status:            state,
		AgeSeconds:        int64(time.Since(t).Seconds()),
		StaleAfterSeconds: int64(staleAfter.Seconds()),
	}
}

func localRFC3339(value string) string {
	if value == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return value
	}
	return t.Local().Format(time.RFC3339)
}
