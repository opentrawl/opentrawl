package cli

import (
	"errors"
	"time"

	"github.com/opentrawl/opentrawl/gogcrawl/internal/archive"
)

const statusFreshFor = 24 * time.Hour

type statusEnvelope struct {
	AppID        string              `json:"app_id"`
	State        string              `json:"state"`
	Summary      string              `json:"summary"`
	Freshness    *freshnessEnvelope  `json:"freshness,omitempty"`
	Counts       []countEnvelope     `json:"counts"`
	Auth         authEnvelope        `json:"auth"`
	DatabasePath string              `json:"database_path,omitempty"`
	Archive      *archiveStatusBlock `json:"archive,omitempty"`
	LastRun      *logRunEnvelope     `json:"last_run,omitempty"`
	RecentError  *logErrorEnvelope   `json:"recent_error,omitempty"`
}

type freshnessEnvelope struct {
	LastSync string `json:"last_sync,omitempty"`
}

type countEnvelope struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Value int64  `json:"value"`
}

type authEnvelope struct {
	Authorized bool `json:"authorized"`
}

type archiveStatusBlock struct {
	ArchivePath  string `json:"archive_path"`
	ArchiveBytes int64  `json:"archive_bytes,omitempty"`
	LastSyncAt   string `json:"last_sync_at,omitempty"`
	Messages     int64  `json:"messages"`
	Senders      int64  `json:"senders"`
	Since        int64  `json:"since"`
}

func (r *runtime) runStatus(args []string) error {
	if hasHelpFlag(args) {
		return printCommandUsage(r.stdout, []string{"status"})
	}
	if len(args) != 0 {
		return usageErr(errors.New("status takes no arguments"))
	}
	out := statusEnvelope{
		AppID:        "gogcrawl",
		State:        "missing",
		Summary:      "archive has not been synced",
		Counts:       statusCounts(archive.Status{}),
		Auth:         r.authEnvelope(),
		DatabasePath: r.archivePath,
	}
	out.LastRun, out.RecentError = r.logTail()
	if !archive.Exists(r.archivePath) {
		return r.print(out)
	}
	st, err := archive.OpenExisting(r.ctx, r.archivePath)
	if err != nil {
		out.State = "error"
		out.Summary = "archive database cannot be read"
		return r.print(out)
	}
	defer func() { _ = st.Close() }()
	status, err := st.Status(r.ctx)
	if err != nil {
		out.State = "error"
		out.Summary = "archive status cannot be read"
		return r.print(out)
	}
	out.Archive = &archiveStatusBlock{
		ArchivePath:  status.ArchivePath,
		ArchiveBytes: status.ArchiveBytes,
		LastSyncAt:   status.LastSyncAt,
		Messages:     status.Messages,
		Senders:      status.Senders,
		Since:        status.Since,
	}
	out.Counts = statusCounts(status)
	out.State, out.Summary = statusState(status)
	if status.LastSyncAt != "" {
		out.Freshness = &freshnessEnvelope{LastSync: status.LastSyncAt}
	}
	return r.print(out)
}

func statusState(status archive.Status) (string, string) {
	switch {
	case status.Messages == 0:
		return "empty", "archive is empty"
	case status.LastSyncAt == "":
		return "stale", "archive has no completed sync"
	}
	lastSync, err := time.Parse(time.RFC3339, status.LastSyncAt)
	if err != nil {
		return "error", "archive freshness timestamp cannot be read"
	}
	if time.Since(lastSync) > statusFreshFor {
		return "stale", "archive is stale"
	}
	return "ok", "archive is fresh"
}

func statusCounts(status archive.Status) []countEnvelope {
	return []countEnvelope{
		{ID: "messages", Label: "messages", Value: status.Messages},
		{ID: "senders", Label: "senders", Value: status.Senders},
		{ID: "since", Label: "since", Value: status.Since},
	}
}

func (r *runtime) authEnvelope() authEnvelope {
	status, err := r.gog.AuthStatus(r.ctx)
	if err != nil {
		return authEnvelope{Authorized: false}
	}
	return authEnvelope{Authorized: status.Authorized}
}
