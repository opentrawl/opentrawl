package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/gogcrawl/internal/archive"
	"github.com/opentrawl/opentrawl/gogcrawl/internal/gog"
)

type syncProgressEvent struct {
	Event string `json:"event"`
	Stage string `json:"stage"`
	Done  int    `json:"done"`
}

type syncCompleteEvent struct {
	Event       string `json:"event"`
	Stage       string `json:"stage"`
	Done        int    `json:"done"`
	Inserted    int    `json:"inserted"`
	Labels      int    `json:"labels"`
	Shards      int    `json:"shards"`
	ArchivePath string `json:"archive_path"`
	BackupRepo  string `json:"backup_repo"`
	SyncedAt    string `json:"synced_at"`
}

var syncValueFlags = map[string]bool{
	"query": true, "max": true,
}

func (r *runtime) runSync(args []string) error {
	if hasHelpFlag(args) {
		return printCommandUsage(r.stdout, []string{"sync"})
	}
	fs := flag.NewFlagSet("gogcrawl sync", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	query := fs.String("query", "", "")
	max := fs.Int("max", 0, "")
	flagArgs, positionals := splitFlagArgs(args, syncValueFlags)
	if len(positionals) != 0 {
		return usageErr(errors.New("sync takes no positional arguments"))
	}
	if err := fs.Parse(flagArgs); err != nil {
		return usageErr(err)
	}
	if *max < 0 {
		return usageErr(errors.New("sync --max must be 0 or greater"))
	}
	st, err := archive.Open(r.ctx, r.archivePath)
	if err != nil {
		return commandErr("archive_open_failed", "cannot open the archive database", "remove the old archive and run gogcrawl sync again", err)
	}
	defer func() { _ = st.Close() }()
	startedAt := time.Now().UTC()
	if err := st.MarkSyncStarted(r.ctx, startedAt); err != nil {
		return err
	}
	if err := r.ensureBackupRepo(); err != nil {
		return err
	}
	if err := r.gog.BackupGmailPush(r.ctx, gog.BackupPushRequest{Repo: r.backupRepoPath, Query: *query, Max: *max}); err != nil {
		return commandErr("gog_backup_failed", "Gmail backup failed", "run gogcrawl doctor", err)
	}
	shards, err := archive.LoadBackupManifest(r.backupRepoPath)
	if err != nil {
		return commandErr("backup_manifest_failed", "backup manifest cannot be read", "run gogcrawl sync again", err)
	}
	pending, err := st.PendingBackupShards(r.ctx, shards)
	if err != nil {
		return err
	}
	event, err := r.ingestPendingShards(st, pending)
	if err != nil {
		return err
	}
	completedAt := time.Now().UTC()
	if err := st.MarkSyncCompleted(r.ctx, completedAt); err != nil {
		return err
	}
	event.Event = "complete"
	event.Stage = "messages"
	event.ArchivePath = st.Path()
	event.BackupRepo = r.backupRepoPath
	event.SyncedAt = completedAt.Local().Format(time.RFC3339)
	if r.json {
		return json.NewEncoder(r.stdout).Encode(event)
	}
	return printSyncText(r.stdout, event)
}

func (r *runtime) ensureBackupRepo() error {
	if info, err := os.Stat(r.backupRepoPath); err != nil {
		if !os.IsNotExist(err) {
			return commandErr("backup_repo_failed", "backup repo cannot be inspected", "check --backup-repo", err)
		}
		if err := os.MkdirAll(filepath.Dir(r.backupRepoPath), 0o700); err != nil {
			return commandErr("backup_repo_failed", "backup repo parent cannot be created", "check --backup-repo", err)
		}
		if err := r.gog.BackupInit(r.ctx, r.backupRepoPath); err != nil {
			return commandErr("gog_backup_init_failed", "backup repo could not be initialised", "upgrade gogcli and run gogcrawl doctor", err)
		}
		// gog backup init configures its own default git remote; this
		// backup never leaves the machine, so drop any remote it set.
		if err := removeBackupRemotes(r.backupRepoPath); err != nil {
			return commandErr("backup_repo_failed", "backup repo remote could not be removed", "check --backup-repo", err)
		}
	} else if !info.IsDir() {
		return commandErr("backup_repo_failed", "backup repo path is not a directory", "choose a directory for --backup-repo", nil)
	}
	if hasRemote, err := backupRepoHasRemote(r.backupRepoPath); err != nil {
		return commandErr("backup_repo_failed", "backup repo config cannot be read", "check --backup-repo", err)
	} else if hasRemote {
		return commandErr("backup_repo_remote", "backup repo must not have a git remote", "use a gogcrawl-owned backup repo such as ~/.gogcrawl/backup", nil)
	}
	return nil
}

func removeBackupRemotes(repo string) error {
	out, err := exec.Command("git", "-C", repo, "remote").Output()
	if err != nil {
		return fmt.Errorf("list git remotes: %w", err)
	}
	for _, remote := range strings.Fields(string(out)) {
		if err := exec.Command("git", "-C", repo, "remote", "remove", remote).Run(); err != nil {
			return fmt.Errorf("remove git remote %s: %w", remote, err)
		}
	}
	return nil
}

func backupRepoHasRemote(repo string) (bool, error) {
	data, err := os.ReadFile(filepath.Join(repo, ".git", "config"))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), `[remote "`) {
			return true, nil
		}
	}
	return false, nil
}

func (r *runtime) ingestPendingShards(st *archive.Store, shards []archive.BackupShard) (syncCompleteEvent, error) {
	event := syncCompleteEvent{Shards: len(shards)}
	for _, shard := range shards {
		plaintext, err := r.gog.BackupCat(r.ctx, r.backupRepoPath, shard.Path)
		if err != nil {
			return event, commandErr("gog_backup_cat_failed", fmt.Sprintf("backup shard cannot be decrypted: %s", shard.Path), "run gogcrawl doctor", err)
		}
		result, err := st.IngestBackupShard(r.ctx, shard, plaintext)
		if err != nil {
			return event, err
		}
		event.Done += result.Seen
		event.Inserted += result.Inserted
		event.Labels += result.Labels
		if err := r.progress(event.Done); err != nil {
			return event, err
		}
	}
	return event, nil
}

func (r *runtime) progress(done int) error {
	if r.json {
		return json.NewEncoder(r.stdout).Encode(syncProgressEvent{Event: "progress", Stage: "messages", Done: done})
	}
	_, err := fmt.Fprintf(r.stderr, "gogcrawl: ingested %d messages\n", done)
	return err
}

func printSyncText(w io.Writer, event syncCompleteEvent) error {
	_, err := fmt.Fprintf(w, "Sync complete\n\nLocal archive:\n  Database: %s\n  Backup repo: %s\n  Synced: %s\n\nMessages:\n  Seen: %d\n  New: %d\n\nShards:\n  Ingested: %d\n  Labels: %d\n",
		event.ArchivePath, event.BackupRepo, event.SyncedAt, event.Done, event.Inserted, event.Shards, event.Labels)
	return err
}
