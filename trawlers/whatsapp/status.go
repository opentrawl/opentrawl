package wacrawl

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/whatsapp/internal/store"
	"github.com/opentrawl/opentrawl/trawlers/whatsapp/internal/whatsappdb"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/cache"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
)

func (c *Crawler) Status(ctx context.Context, req *trawlkit.Request) (*control.Status, error) {
	status := control.NewStatus("whatsapp", "Archive has not been synced.")
	status.State = "missing"
	status.SetupRequirements = whatsappSetupRequirements(ctx, c.cfg.Source)
	status.ConfigPath = req.Paths.Config
	status.DatabasePath = req.Paths.Archive
	if req.Store == nil {
		return &status, nil
	}
	st, err := store.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		status.State = "error"
		status.Summary = "Archive could not be read."
		status.Errors = []string{err.Error()}
		return &status, nil
	}
	archiveStatus, err := st.Status(ctx)
	if err != nil {
		status.State = "error"
		status.Summary = "Archive could not be inspected."
		status.Errors = []string{err.Error()}
		return &status, nil
	}
	status.DatabasePath = archiveStatus.DBPath
	status.LastSyncAt = contractTime(archiveStatus.LastImportAt)
	status.Counts = statusCounts(archiveStatus)
	switch {
	case archiveStatus.Messages == 0:
		status.State = "empty"
		if archiveStatus.LastImportAt.IsZero() {
			status.Summary = "Archive is empty; run trawl whatsapp sync to populate it."
		} else {
			status.Summary = "Archive contains no messages from the last sync."
		}
	default:
		status.State = "ok"
		status.Summary = "Recently synced."
	}
	return &status, nil
}

func whatsappSetupRequirements(ctx context.Context, configuredPath string) []control.SetupRequirement {
	const (
		pairingID = "pairing"
		accessID  = "full_disk_access"
	)
	openWhatsApp := []string{"/usr/bin/open", "-b", "net.whatsapp.WhatsApp"}
	source, discoverErr := whatsappdb.Discover(ctx, configuredPath)
	state := control.SetupStateUnavailable
	accessState := control.SetupStateUnavailable
	var canaryErr error
	if discoverErr == nil && source.Available && strings.TrimSpace(source.ChatDB) != "" {
		if _, err := os.Stat(source.ChatDB); err == nil {
			canaryErr = sourceCanary(ctx, source)
			switch {
			case canaryErr == nil:
				state = control.SetupStateReady
				accessState = control.SetupStateReady
			case isPermissionError(canaryErr):
				state = control.SetupStateReady
				accessState = control.SetupStateNeedsAction
			default:
				state = control.SetupStateUnavailable
				accessState = control.SetupStateUnavailable
			}
		} else if isPermissionError(err) {
			state = control.SetupStateReady
			accessState = control.SetupStateNeedsAction
		} else if !os.IsNotExist(err) {
			state = control.SetupStateUnavailable
			accessState = control.SetupStateUnavailable
		} else {
			state = control.SetupStateNeedsAction
			accessState = control.SetupStateNeedsAction
		}
	} else if discoverErr != nil && !isPermissionError(discoverErr) {
		state = control.SetupStateUnavailable
		accessState = control.SetupStateUnavailable
	} else {
		state = control.SetupStateNeedsAction
		accessState = control.SetupStateNeedsAction
	}
	return []control.SetupRequirement{
		control.NewSetupRequirement(
			pairingID,
			control.SetupKindPairing,
			state,
			"Open WhatsApp once so OpenTrawl can reuse its local session.",
			control.SetupActionRunCommand,
			openWhatsApp,
		),
		control.NewSetupRequirement(
			accessID,
			control.SetupKindFullDiskAccess,
			accessState,
			"OpenTrawl reads the local WhatsApp database.",
			control.SetupActionOpenFullDiskAccess,
			nil,
		),
	}
}

func statusCounts(status store.Status) []control.Count {
	counts := []control.Count{
		control.NewCount("messages", "messages", int64(status.Messages)),
		control.NewCount("media_messages", "media messages", int64(status.MediaMessages)),
		control.NewCount("chats", "chats", int64(status.Chats)),
		control.NewCount("unread_chats", "unread chats", int64(status.UnreadChats)),
		control.NewCount("unread_messages", "unread messages", int64(status.UnreadMessages)),
		control.NewCount("contacts", "contacts", int64(status.Contacts)),
		control.NewCount("groups", "groups", int64(status.Groups)),
		control.NewCount("participants", "participants", int64(status.Participants)),
	}
	if !status.OldestMessage.IsZero() {
		counts = append(counts, control.NewCount("since", "since", int64(status.OldestMessage.In(time.Local).Year())))
	}
	return counts
}

func (c *Crawler) Doctor(ctx context.Context, req *trawlkit.Request) (*trawlkit.Doctor, error) {
	source, discoverErr := whatsappdb.Discover(ctx, c.cfg.Source)
	canaryRan := source.Available && strings.TrimSpace(source.ChatDB) != ""
	var canaryErr error
	if canaryRan {
		canaryErr = sourceCanary(ctx, source)
	}
	checks := []trawlkit.Check{
		sourceStoreCheck(source, discoverErr, canaryErr),
		archiveCheck(ctx, req),
	}
	if canaryRan {
		if check, ok := fullDiskAccessCheck(canaryErr); ok {
			checks = append(checks, check)
		}
	}
	return &trawlkit.Doctor{Checks: checks}, nil
}

func sourceStoreCheck(source whatsappdb.Source, discoverErr, canaryErr error) trawlkit.Check {
	check := trawlkit.Check{ID: "source_store"}
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
		check.Remedy = "install WhatsApp Desktop, open it once, or set source in config.toml"
	case !source.Available:
		check.State = "missing"
		check.Message = "WhatsApp Desktop store was not found"
		check.Remedy = "install WhatsApp Desktop, open it once, or set source in config.toml"
	case chatDB == "":
		check.State = "missing"
		check.Message = "WhatsApp Desktop chat database was not found"
		check.Remedy = "open WhatsApp Desktop once, then run trawl whatsapp sync"
	case errors.Is(chatDBStatErr, os.ErrNotExist):
		check.State = "missing"
		check.Message = "WhatsApp Desktop chat database was not found"
		check.Remedy = "open WhatsApp Desktop once, then run trawl whatsapp sync"
	case chatDBStatErr != nil && !isPermissionError(chatDBStatErr):
		check.State = "fail"
		check.Message = chatDBStatErr.Error()
		check.Remedy = "check the WhatsApp Desktop store path, then run trawl whatsapp doctor again"
	case canaryErr != nil && !isPermissionError(canaryErr):
		check.State = "fail"
		check.Message = "cannot read WhatsApp Desktop database: " + canaryErr.Error()
		check.Remedy = "close WhatsApp Desktop if it is busy, then run trawl whatsapp doctor again"
	default:
		check.State = "ok"
		check.Message = "WhatsApp Desktop store found"
	}
	return check
}

func sourceCanary(ctx context.Context, source whatsappdb.Source) error {
	return probeSQLite(ctx, source.ChatDB)
}

func probeSQLite(ctx context.Context, dbPath string) error {
	if strings.TrimSpace(dbPath) == "" {
		return errors.New("db path is required")
	}
	root, err := os.MkdirTemp("", "wacrawl-source-canary-*")
	if err != nil {
		return fmt.Errorf("create sqlite canary snapshot dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(root) }()
	snapshot, err := cache.SnapshotSQLite(ctx, cache.SQLiteSnapshotOptions{
		SourcePath:     dbPath,
		DestinationDir: root,
		Name:           filepath.Base(dbPath),
	})
	if err != nil {
		return fmt.Errorf("snapshot sqlite: %w", err)
	}
	st, err := ckstore.OpenReadOnly(ctx, snapshot.Path)
	if err != nil {
		return fmt.Errorf("open sqlite: %w", err)
	}
	defer func() { _ = st.Close() }()
	var tables int
	return st.DB().QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master").Scan(&tables)
}

func archiveCheck(ctx context.Context, req *trawlkit.Request) trawlkit.Check {
	check := trawlkit.Check{ID: "archive"}
	switch {
	case strings.TrimSpace(req.Paths.Archive) == "":
		check.State = "fail"
		check.Message = "archive database path is empty"
		check.Remedy = "run trawl whatsapp sync with a valid state root"
	case req.Store == nil:
		check.State = "missing"
		check.Message = "archive database does not exist"
		check.Remedy = "run trawl whatsapp sync"
	default:
		if _, err := store.UseExisting(ctx, req.Store, req.Paths.Archive); err != nil {
			check.State = "error"
			check.Message = err.Error()
			check.Remedy = "move the corrupt archive aside, then run trawl whatsapp sync"
			return check
		}
		check.State = "ok"
		check.Message = "archive database opened"
	}
	return check
}

func fullDiskAccessCheck(canaryErr error) (trawlkit.Check, bool) {
	check := trawlkit.Check{ID: "full_disk_access"}
	switch {
	case canaryErr == nil:
		check.State = "ok"
		check.Message = "source database canary read succeeded"
		return check, true
	case isPermissionError(canaryErr):
		check.State = "fail"
		check.Message = "cannot read the WhatsApp Desktop database"
		check.Remedy = fullDiskAccessRemedy
		return check, true
	default:
		return trawlkit.Check{}, false
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

func contractTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
