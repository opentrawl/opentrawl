package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	cklog "github.com/openclaw/crawlkit/log"
	"github.com/opentrawl/opentrawl/gogcrawl/internal/archive"
	"github.com/opentrawl/opentrawl/gogcrawl/internal/gog"
)

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
	progress := r.log.Progress(cklog.ProgressOptions{Event: "sync_progress", Unit: "messages"})
	if err := progress.Report(0, "starting sync"); err != nil {
		return err
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
	if status, err := r.gog.AuthStatus(r.ctx); err == nil {
		if err := st.SetOwnerAccount(r.ctx, status.AccountEmail); err != nil {
			return err
		}
	} else {
		_ = r.log.Warn("owner_identity_unavailable", "account_email_unavailable")
	}
	if err := r.ensureBackupRepo(); err != nil {
		return err
	}
	var done atomic.Int64
	if err := r.withHeartbeat(progress, &done, "backing up Gmail", func() error {
		r.logGogCommand(backupGmailPushArgs(r.backupRepoPath, *query, *max)...)
		return r.gog.BackupGmailPush(r.ctx, gog.BackupPushRequest{Repo: r.backupRepoPath, Query: *query, Max: *max})
	}); err != nil {
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
	event, err := r.ingestPendingShards(st, pending, progress, &done)
	if err != nil {
		return err
	}
	if rebuilt, messages, err := st.EnsureParticipants(r.ctx); err != nil {
		return err
	} else if rebuilt {
		_ = r.log.Info("participants_rebuilt", fmt.Sprintf("messages=%d", messages))
	}
	if rebuilt, refs, err := st.EnsureShortRefs(r.ctx); err != nil {
		return err
	} else if rebuilt {
		_ = r.log.Info("short_refs_rebuilt", fmt.Sprintf("refs=%d", refs))
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
	configPath := filepath.Join(repo, ".git", "config")
	data, err := os.ReadFile(configPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read backup repo config: %w", err)
	}
	cleaned := removeRemoteConfigSections(string(data))
	if cleaned == string(data) {
		return nil
	}
	return os.WriteFile(configPath, []byte(cleaned), 0o600)
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

func removeRemoteConfigSections(config string) string {
	lines := strings.Split(config, "\n")
	out := make([]string, 0, len(lines))
	inRemote := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			inRemote = strings.HasPrefix(trimmed, `[remote "`)
		}
		if inRemote {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func (r *runtime) ingestPendingShards(st *archive.Store, shards []archive.BackupShard, progress *cklog.Progress, done *atomic.Int64) (syncCompleteEvent, error) {
	event := syncCompleteEvent{Shards: len(shards)}
	for _, shard := range shards {
		var plaintext []byte
		decryptStarted := time.Now()
		err := r.withHeartbeat(progress, done, "decrypting backup shard", func() error {
			var err error
			r.logGogCommand("backup", "cat", "--no-pull", "--repo", r.backupRepoPath, shard.Path)
			plaintext, err = r.gog.BackupCat(r.ctx, r.backupRepoPath, shard.Path)
			return err
		})
		decryptElapsed := time.Since(decryptStarted)
		if err != nil {
			return event, commandErr("gog_backup_cat_failed", fmt.Sprintf("backup shard cannot be decrypted: %s", shard.Path), "run gogcrawl doctor", err)
		}
		ingestStarted := time.Now()
		result, err := st.IngestBackupShard(r.ctx, shard, plaintext)
		ingestElapsed := time.Since(ingestStarted)
		if err != nil {
			return event, err
		}
		r.logShardTimings(result, decryptElapsed, ingestElapsed)
		event.Done += result.Seen
		done.Store(int64(event.Done))
		event.Inserted += result.Inserted
		event.Labels += result.Labels
		if err := progress.Report(int64(event.Done), "ingested backup shard"); err != nil {
			return event, err
		}
	}
	return event, nil
}

func (r *runtime) logGogCommand(args ...string) {
	argv := append([]string{r.gog.Binary}, args...)
	r.logDebug("subprocess_exec", "argv="+logQuote(strings.Join(argv, " ")))
}

func backupGmailPushArgs(repo, query string, max int) []string {
	args := []string{"backup", "gmail", "push", "--no-push", "--gmail-cache", "--repo", repo}
	if query := strings.TrimSpace(query); query != "" {
		args = append(args, "--query", query)
	}
	if max > 0 {
		args = append(args, "--max", strconv.Itoa(max))
	}
	return args
}

func (r *runtime) logShardTimings(result archive.IngestResult, decryptElapsed, ingestElapsed time.Duration) {
	shard := result.Shard
	rows := result.Seen + result.Labels
	r.logInfo("shard_done", strings.Join([]string{
		"shard=" + logQuote(shard.Path),
		"kind=" + logQuote(string(shard.Kind)),
		"rows=" + strconv.Itoa(rows),
		"inserted=" + strconv.Itoa(result.Inserted),
		"labels=" + strconv.Itoa(result.Labels),
		"elapsed_ms=" + elapsedMS(decryptElapsed+ingestElapsed),
	}, " "))
	r.logDebug("shard_phase", strings.Join([]string{
		"shard=" + logQuote(shard.Path),
		"decrypt_ms=" + elapsedMS(decryptElapsed),
		"parse_ms=" + elapsedMS(result.ParseElapsed),
		"index_ms=" + elapsedMS(result.IndexElapsed),
	}, " "))
}

func (r *runtime) withHeartbeat(progress *cklog.Progress, done *atomic.Int64, message string, fn func() error) error {
	if err := progress.Report(done.Load(), message); err != nil {
		return err
	}
	stop := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				_ = progress.Report(done.Load(), message)
			case <-stop:
				return
			}
		}
	}()
	err := fn()
	close(stop)
	<-stopped
	return err
}

func printSyncText(w io.Writer, event syncCompleteEvent) error {
	_, err := fmt.Fprintf(w, "Sync complete\n\nLocal archive:\n  Database: %s\n  Backup repo: %s\n  Synced: %s\n\nMessages:\n  Seen: %d\n  New: %d\n\nShards:\n  Ingested: %d\n  Labels: %d\n",
		event.ArchivePath, event.BackupRepo, event.SyncedAt, event.Done, event.Inserted, event.Shards, event.Labels)
	return err
}
