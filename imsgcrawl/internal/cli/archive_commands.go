package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/openclaw/crawlkit/control"
	"github.com/openclaw/crawlkit/flags"
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
	Log           *logTailOutput         `json:"log,omitempty"`
}

type statusFreshness struct {
	LastSync string `json:"last_sync"`
}

type syncProgressEvent struct {
	Event string `json:"event"`
	Stage string `json:"stage"`
	Done  int    `json:"done"`
	Total int    `json:"total,omitempty"`
}

type syncCompleteEvent struct {
	Event      string          `json:"event"`
	State      string          `json:"state"`
	Counts     []control.Count `json:"counts"`
	FinishedAt string          `json:"finished_at"`
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
	enc := json.NewEncoder(r.stdout)
	if r.json {
		if err := enc.Encode(syncProgressEvent{Event: "progress", Stage: "messages", Done: 0}); err != nil {
			return err
		}
	}
	progress := r.progress("sync_progress", "stage", 0)
	if err := progress.Report(0, "sync started"); err != nil {
		return err
	}
	stopHeartbeat := r.startHeartbeat(progress, "sync still running")
	result, err := archive.Sync(r.ctx, r.archivePath, r.dbPath)
	stopHeartbeat()
	if err != nil {
		return err
	}
	r.logSyncTimings(result)
	if err := progress.Report(int64(result.Messages), "sync complete"); err != nil {
		return err
	}
	if r.json {
		return printSyncJSONL(enc, result)
	}
	return r.print(result)
}

func (r *runtime) logSyncTimings(result archive.SyncResult) {
	_ = r.logInfo("sync_done", strings.Join([]string{
		"messages=" + strconv.Itoa(result.Messages),
		"chats=" + strconv.Itoa(result.Chats),
		"participants=" + strconv.Itoa(result.Participants),
		"elapsed_ms=" + elapsedMS(result.TotalElapsed),
	}, " "))
	_ = r.logDebug("sync_phase", strings.Join([]string{
		"source=" + logQuote("messages"),
		"extract_ms=" + elapsedMS(result.ExtractElapsed),
		"contacts_ms=" + elapsedMS(result.ContactsElapsed),
		"map_ms=" + elapsedMS(result.MapElapsed),
		"write_ms=" + elapsedMS(result.WriteElapsed),
	}, " "))
}

func printSyncJSONL(enc *json.Encoder, result archive.SyncResult) error {
	if err := enc.Encode(syncProgressEvent{Event: "progress", Stage: "messages", Done: result.Messages, Total: result.Messages}); err != nil {
		return err
	}
	return enc.Encode(syncCompleteEvent{
		Event:      "complete",
		State:      "ok",
		Counts:     syncResultCounts(result),
		FinishedAt: result.SyncedAt,
	})
}

func syncResultCounts(result archive.SyncResult) []control.Count {
	return []control.Count{
		control.NewCount("messages", "messages", int64(result.Messages)),
		control.NewCount("chats", "chats", int64(result.Chats)),
		control.NewCount("participants", "participants", int64(result.Participants)),
	}
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
		SchemaVersion: control.StatusSchemaVersion,
		AppID:         "imsgcrawl",
		State:         "ok",
		Summary:       "Archive is readable.",
	}
	archiveProblem := false
	sourceProblem := false
	archiveMissing := false
	source, err := messages.Status(r.ctx, r.dbPath)
	if err != nil {
		_ = r.logError("source_status_failed", worldMustChange(err, "messages source could not be read", "check the --db path and grant Full Disk Access if the Messages database is protected"))
		sourceProblem = true
		out.Errors = append(out.Errors, err.Error())
	} else {
		out.Source = &source
	}
	if archive.Exists(r.archivePath) {
		st, err := archive.OpenExisting(r.ctx, r.archivePath)
		if err != nil {
			_ = r.logError("archive_open_failed", worldMustChange(err, "archive could not be read", "run imsgcrawl sync"))
			archiveProblem = true
			out.Warnings = append(out.Warnings, "archive unreadable: "+err.Error())
		} else {
			defer func() { _ = st.Close() }()
			archiveStatus, err := st.Status(r.ctx)
			if err != nil {
				_ = r.logError("archive_status_failed", worldMustChange(err, "archive status could not be read", "run imsgcrawl sync"))
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
	out.Log = r.readLogTail()
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
		control.NewCount("named_contacts", "named contacts", status.NamedContacts),
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
	rows, err := flags.Limit(*limit, flagPassed(fs, "limit"), *all)
	if err != nil {
		return usageErr(err)
	}
	*limit = rows
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
	rows, err := flags.Limit(*limit, flagPassed(fs, "limit"), *all)
	if err != nil {
		return usageErr(err)
	}
	*limit = rows
	return r.withArchive(func(st *archive.Store) error {
		rows, err := st.Messages(r.ctx, *chatID, *limit, *asc)
		if err != nil {
			return err
		}
		chat, err := st.Chat(r.ctx, *chatID)
		if errors.Is(err, archive.ErrChatNotFound) {
			return r.contractError("not_found", fmt.Sprintf("chat %s was not found", *chatID), "run imsgcrawl chats and use a current chat id")
		}
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

func (r *runtime) withArchive(fn func(*archive.Store) error) error {
	st, err := archive.OpenExisting(r.ctx, r.archivePath)
	if err != nil {
		return worldMustChange(err, "open archive failed; run imsgcrawl sync first", "run imsgcrawl sync")
	}
	defer func() { _ = st.Close() }()
	if err := fn(st); errors.Is(err, archive.ErrSchemaOutdated) {
		return worldMustChange(err, err.Error(), "run imsgcrawl sync")
	} else {
		return err
	}
}

func (r *runtime) withWritableArchive(fn func(*archive.Store) error) error {
	if !archive.Exists(r.archivePath) {
		return worldMustChange(nil, "open archive failed; run imsgcrawl sync first", "run imsgcrawl sync")
	}
	st, err := archive.OpenForDerivedRepair(r.ctx, r.archivePath)
	if err != nil {
		return worldMustChange(err, "open archive failed; run imsgcrawl sync first", "run imsgcrawl sync")
	}
	defer func() { _ = st.Close() }()
	if err := fn(st); errors.Is(err, archive.ErrSchemaOutdated) {
		return worldMustChange(err, err.Error(), "run imsgcrawl sync")
	} else {
		return err
	}
}
