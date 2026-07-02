package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/openclaw/crawlkit/control"
	"github.com/openclaw/imsgcrawl/internal/archive"
	"github.com/openclaw/imsgcrawl/internal/messages"
)

type statusOutput struct {
	SchemaVersion string                 `json:"schema_version"`
	AppID         string                 `json:"app_id"`
	State         string                 `json:"state"`
	Summary       string                 `json:"summary"`
	Freshness     *statusFreshness       `json:"freshness,omitempty"`
	Source        *messages.StatusReport `json:"source,omitempty"`
	Archive       *archive.Status        `json:"archive,omitempty"`
	Counts        []control.Count        `json:"counts,omitempty"`
	Warnings      []string               `json:"warnings,omitempty"`
	Errors        []string               `json:"errors,omitempty"`
}

type statusFreshness struct {
	LastSync string `json:"last_sync"`
}

const statusStaleAfter = 7 * 24 * time.Hour

func (r *runtime) runSync(args []string) error {
	if hasHelpFlag(args) {
		return printCommandUsage(r.stdout, []string{"sync"})
	}
	fs := flag.NewFlagSet("imsgcrawl sync", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("sync takes no arguments"))
	}
	result, err := archive.Sync(r.ctx, r.archivePath, r.dbPath)
	if err != nil {
		return err
	}
	return r.print(result)
}

func (r *runtime) runStatus(args []string) error {
	if hasHelpFlag(args) {
		return printCommandUsage(r.stdout, []string{"status"})
	}
	fs := flag.NewFlagSet("imsgcrawl status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("status takes no arguments"))
	}
	out := statusOutput{
		SchemaVersion: control.SchemaVersion,
		AppID:         "imsgcrawl",
		State:         "ok",
		Summary:       "Archive is readable.",
	}
	archiveProblem := false
	sourceProblem := false
	archiveMissing := false
	source, err := messages.Status(r.ctx, r.dbPath)
	if err != nil {
		sourceProblem = true
		out.Errors = append(out.Errors, err.Error())
	} else {
		out.Source = &source
	}
	if archive.Exists(r.archivePath) {
		st, err := archive.OpenExisting(r.ctx, r.archivePath)
		if err != nil {
			archiveProblem = true
			out.Warnings = append(out.Warnings, "archive unreadable: "+err.Error())
		} else {
			defer func() { _ = st.Close() }()
			archiveStatus, err := st.Status(r.ctx)
			if err != nil {
				archiveProblem = true
				out.Warnings = append(out.Warnings, "archive status failed: "+err.Error())
			} else {
				out.Archive = &archiveStatus
				out.Counts = statusCounts(archiveStatus)
				setStatusFreshness(&out, archiveStatus.LastSyncAt)
			}
		}
	} else {
		archiveMissing = true
		out.Warnings = append(out.Warnings, "archive has not been synced")
	}
	setStatusState(&out, sourceProblem, archiveProblem, archiveMissing)
	return r.print(out)
}

func statusCounts(status archive.Status) []control.Count {
	since := int64(0)
	if status.EarliestMessageDate > 0 {
		since = int64(archive.AppleDateTime(status.EarliestMessageDate).Year())
	}
	return []control.Count{
		control.NewCount("messages", "messages", status.Messages),
		control.NewCount("chats", "chats", status.Chats),
		control.NewCount("since", "since", since),
	}
}

func setStatusFreshness(out *statusOutput, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	lastSync, err := time.Parse(time.RFC3339, value)
	if err != nil {
		out.Warnings = append(out.Warnings, "archive last_sync_at is invalid: "+err.Error())
		return
	}
	out.Freshness = &statusFreshness{LastSync: lastSync.Local().Format(time.RFC3339)}
	if time.Since(lastSync) > statusStaleAfter && out.State == "ok" {
		out.State = "stale"
		out.Summary = "Archive is stale."
	}
}

func setStatusState(out *statusOutput, sourceProblem, archiveProblem, archiveMissing bool) {
	switch {
	case archiveProblem:
		out.State = "error"
		out.Summary = "Archive could not be read."
	case archiveMissing:
		out.State = "missing"
		out.Summary = "Archive has not been synced."
	case sourceProblem:
		out.State = "error"
		out.Summary = "Messages source could not be read."
	case out.Archive != nil && out.Archive.Messages == 0:
		out.State = "empty"
		out.Summary = "Archive is empty."
	case out.Source != nil && out.Archive != nil && out.State == "ok":
		out.Summary = "Messages source and archive are readable."
	case out.Source == nil && out.Archive == nil:
		out.State = "error"
		out.Summary = "Messages source and archive are unavailable."
	}
}

func (r *runtime) runChats(args []string) error {
	if hasHelpFlag(args) {
		return printCommandUsage(r.stdout, []string{"chats"})
	}
	fs := flag.NewFlagSet("imsgcrawl chats", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	limit := fs.Int("limit", defaultChatLimit, "")
	all := fs.Bool("all", false, "")
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("chats takes flags only"))
	}
	if *limit <= 0 {
		return usageErr(errors.New("chats --limit must be positive"))
	}
	if *all && flagPassed(fs, "limit") {
		return usageErr(errors.New("use either --all or --limit"))
	}
	if *all {
		*limit = 0
	}
	return r.withArchive(func(st *archive.Store) error {
		chats, err := st.Chats(r.ctx, *limit)
		if err != nil {
			return err
		}
		total, err := st.CountChats(r.ctx)
		if err != nil {
			return err
		}
		return r.print(chatListOutput{
			listHeader: newListHeader("chats", len(chats), total, *limit),
			Items:      chats,
		})
	})
}

func (r *runtime) runMessages(args []string) error {
	if hasHelpFlag(args) {
		return printCommandUsage(r.stdout, []string{"messages"})
	}
	fs := flag.NewFlagSet("imsgcrawl messages", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	chatID := fs.String("chat", "", "")
	limit := fs.Int("limit", defaultMessageLimit, "")
	all := fs.Bool("all", false, "")
	asc := fs.Bool("asc", false, "")
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("messages takes flags only"))
	}
	if strings.TrimSpace(*chatID) == "" {
		return usageErr(errors.New("messages requires --chat"))
	}
	if *limit <= 0 {
		return usageErr(errors.New("messages --limit must be positive"))
	}
	if *limit > maxListLimit {
		return usageErr(fmt.Errorf("messages --limit must be %d or less", maxListLimit))
	}
	if *all && flagPassed(fs, "limit") {
		return usageErr(errors.New("use either --all or --limit"))
	}
	if *all {
		*limit = 0
	}
	return r.withArchive(func(st *archive.Store) error {
		rows, err := st.Messages(r.ctx, *chatID, *limit, *asc)
		if err != nil {
			return err
		}
		chat, err := st.Chat(r.ctx, *chatID)
		if err != nil {
			return err
		}
		total, err := st.CountMessages(r.ctx, *chatID)
		if err != nil {
			return err
		}
		order := "newest-first"
		if *asc {
			order = "oldest-first"
		}
		return r.print(messageListOutput{
			listHeader: newListHeader("messages", len(rows), total, *limit),
			ChatID:     *chatID,
			Chat:       &chat,
			Order:      order,
			Items:      rows,
		})
	})
}

func (r *runtime) runSearch(args []string) error {
	if hasHelpFlag(args) {
		return printCommandUsage(r.stdout, []string{"search"})
	}
	fs := flag.NewFlagSet("imsgcrawl search", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	limit := fs.Int("limit", defaultSearchLimit, "")
	all := fs.Bool("all", false, "")
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	query := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if query == "" {
		return usageErr(errors.New("search query is required"))
	}
	if *limit <= 0 {
		return usageErr(errors.New("search --limit must be positive"))
	}
	if *limit > maxListLimit {
		return usageErr(fmt.Errorf("search --limit must be %d or less", maxListLimit))
	}
	if *all && flagPassed(fs, "limit") {
		return usageErr(errors.New("use either --all or --limit"))
	}
	if *all {
		*limit = 0
	}
	return r.withArchive(func(st *archive.Store) error {
		results, err := st.Search(r.ctx, query, *limit)
		if err != nil {
			return err
		}
		total, err := st.CountSearch(r.ctx, query)
		if err != nil {
			return err
		}
		return r.print(searchListOutput{
			listHeader: newListHeader("search", len(results), total, *limit),
			Query:      query,
			Items:      results,
		})
	})
}

func (r *runtime) withArchive(fn func(*archive.Store) error) error {
	st, err := archive.OpenExisting(r.ctx, r.archivePath)
	if err != nil {
		return fmt.Errorf("open archive: %w; run imsgcrawl sync first", err)
	}
	defer func() { _ = st.Close() }()
	return fn(st)
}
