package gogcrawl

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/openclaw/crawlkit"
	cklog "github.com/openclaw/crawlkit/log"
	"github.com/openclaw/crawlkit/output"
	"github.com/opentrawl/opentrawl/gogcrawl/internal/archive"
	"github.com/opentrawl/opentrawl/gogcrawl/internal/gog"
)

func (c *Crawler) Sync(ctx context.Context, req *crawlkit.Request) (*crawlkit.SyncReport, error) {
	if c.syncMax < 0 {
		return nil, output.UsageError{Err: errors.New("sync --max must be 0 or greater")}
	}
	repo := c.backupRepo(req)
	progress := logProgress(req, cklog.ProgressOptions{Event: "sync_progress", Unit: "messages"})
	if err := reportProgress(req, progress, 0, 0, "starting sync"); err != nil {
		return nil, err
	}
	st, err := archive.Use(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return nil, commandErr("archive_open_failed", "cannot open the archive database", "remove the old archive and run trawl gogcrawl sync again", err)
	}
	startedAt := time.Now().UTC()
	if err := st.MarkSyncStarted(ctx, startedAt); err != nil {
		return nil, err
	}
	if status, err := c.gog.AuthStatus(ctx); err == nil {
		if err := st.SetOwnerAccount(ctx, status.AccountEmail); err != nil {
			return nil, err
		}
	} else {
		_ = logWarn(req, "owner_identity_unavailable", "account_email_unavailable")
	}
	if err := c.ensureBackupRepo(ctx, repo); err != nil {
		return nil, err
	}
	var done atomic.Int64
	if err := c.withHeartbeat(ctx, req, progress, &done, "backing up Gmail", func() error {
		logGogCommand(req, c.gog, backupGmailPushArgs(repo, c.syncQuery, c.syncMax)...)
		return c.gog.BackupGmailPush(ctx, gog.BackupPushRequest{Repo: repo, Query: c.syncQuery, Max: c.syncMax})
	}); err != nil {
		return nil, commandErr("gog_backup_failed", "Gmail backup failed", "run trawl gogcrawl doctor", err)
	}
	shards, err := archive.LoadBackupManifest(repo)
	if err != nil {
		return nil, commandErr("backup_manifest_failed", "backup manifest cannot be read", "run trawl gogcrawl sync again", err)
	}
	pending, err := st.PendingBackupShards(ctx, shards)
	if err != nil {
		return nil, err
	}
	result, err := c.ingestPendingShards(ctx, req, st, repo, pending, progress, &done)
	if err != nil {
		return nil, err
	}
	if rebuilt, messages, err := st.EnsureParticipants(ctx); err != nil {
		return nil, err
	} else if rebuilt {
		_ = logInfo(req, "participants_rebuilt", fmt.Sprintf("messages=%d", messages))
	}
	if rebuilt, refs, err := st.EnsureShortRefs(ctx); err != nil {
		return nil, err
	} else if rebuilt {
		_ = logInfo(req, "short_refs_rebuilt", fmt.Sprintf("refs=%d", refs))
	}
	if err := st.MarkSyncCompleted(ctx, time.Now().UTC()); err != nil {
		return nil, err
	}
	return &crawlkit.SyncReport{Added: int64(result.Inserted)}, nil
}

type syncResult struct {
	Seen     int
	Inserted int
	Labels   int
	Shards   int
}

func (c *Crawler) backupRepo(req *crawlkit.Request) string {
	if strings.TrimSpace(c.backupRepoPath) != "" {
		return strings.TrimSpace(c.backupRepoPath)
	}
	return filepath.Join(filepath.Dir(req.Paths.Archive), "backup")
}

func (c *Crawler) ensureBackupRepo(ctx context.Context, repo string) error {
	if info, err := os.Stat(repo); err != nil {
		if !os.IsNotExist(err) {
			return commandErr("backup_repo_failed", "backup repo cannot be inspected", "check --backup-repo", err)
		}
		if err := os.MkdirAll(filepath.Dir(repo), 0o700); err != nil {
			return commandErr("backup_repo_failed", "backup repo parent cannot be created", "check --backup-repo", err)
		}
		if err := c.gog.BackupInit(ctx, repo); err != nil {
			return commandErr("gog_backup_init_failed", "backup repo could not be initialised", "upgrade gogcli and run trawl gogcrawl doctor", err)
		}
		if err := removeBackupRemotes(repo); err != nil {
			return commandErr("backup_repo_failed", "backup repo remote could not be removed", "check --backup-repo", err)
		}
	} else if !info.IsDir() {
		return commandErr("backup_repo_failed", "backup repo path is not a directory", "choose a directory for --backup-repo", nil)
	}
	if hasRemote, err := backupRepoHasRemote(repo); err != nil {
		return commandErr("backup_repo_failed", "backup repo config cannot be read", "check --backup-repo", err)
	} else if hasRemote {
		return commandErr("backup_repo_remote", "backup repo must not have a git remote", "use a gogcrawl-owned backup repo such as ~/.opentrawl/gogcrawl/backup", nil)
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

func (c *Crawler) ingestPendingShards(ctx context.Context, req *crawlkit.Request, st *archive.Store, repo string, shards []archive.BackupShard, progress *cklog.Progress, done *atomic.Int64) (syncResult, error) {
	out := syncResult{Shards: len(shards)}
	for _, shard := range shards {
		var plaintext []byte
		decryptStarted := time.Now()
		err := c.withHeartbeat(ctx, req, progress, done, "decrypting backup shard", func() error {
			var err error
			logGogCommand(req, c.gog, "backup", "cat", "--no-pull", "--repo", repo, shard.Path)
			plaintext, err = c.gog.BackupCat(ctx, repo, shard.Path)
			return err
		})
		decryptElapsed := time.Since(decryptStarted)
		if err != nil {
			return out, commandErr("gog_backup_cat_failed", fmt.Sprintf("backup shard cannot be decrypted: %s", shard.Path), "run trawl gogcrawl doctor", err)
		}
		ingestStarted := time.Now()
		result, err := st.IngestBackupShard(ctx, shard, plaintext)
		ingestElapsed := time.Since(ingestStarted)
		if err != nil {
			return out, err
		}
		logShardTimings(req, result, decryptElapsed, ingestElapsed)
		out.Seen += result.Seen
		done.Store(int64(out.Seen))
		out.Inserted += result.Inserted
		out.Labels += result.Labels
		if err := reportProgress(req, progress, int64(out.Seen), 0, "ingested backup shard"); err != nil {
			return out, err
		}
	}
	return out, nil
}

func (c *Crawler) withHeartbeat(ctx context.Context, req *crawlkit.Request, progress *cklog.Progress, done *atomic.Int64, message string, fn func() error) error {
	if err := reportProgress(req, progress, done.Load(), 0, message); err != nil {
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
				_ = reportProgress(req, progress, done.Load(), 0, message)
			case <-stop:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
	err := fn()
	close(stop)
	<-stopped
	return err
}

func reportProgress(req *crawlkit.Request, progress *cklog.Progress, done, total int64, message string) error {
	if req != nil && req.Progress != nil {
		req.Progress(crawlkit.Progress{Phase: "sync", Done: done, Total: total, Message: message})
	}
	if progress == nil {
		return nil
	}
	return progress.Report(done, message)
}

func logProgress(req *crawlkit.Request, opts cklog.ProgressOptions) *cklog.Progress {
	if req == nil || req.Log == nil {
		return nil
	}
	return req.Log.Progress(opts)
}

func logGogCommand(req *crawlkit.Request, client gog.Client, args ...string) {
	argv := append([]string{client.Binary}, args...)
	_ = logDebug(req, "subprocess_exec", "argv="+logQuote(strings.Join(argv, " ")))
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

func logShardTimings(req *crawlkit.Request, result archive.IngestResult, decryptElapsed, ingestElapsed time.Duration) {
	shard := result.Shard
	rows := result.Seen + result.Labels
	_ = logInfo(req, "shard_done", strings.Join([]string{
		"shard=" + logQuote(shard.Path),
		"kind=" + logQuote(string(shard.Kind)),
		"rows=" + strconv.Itoa(rows),
		"inserted=" + strconv.Itoa(result.Inserted),
		"labels=" + strconv.Itoa(result.Labels),
		"elapsed_ms=" + elapsedMS(decryptElapsed+ingestElapsed),
	}, " "))
	_ = logDebug(req, "shard_phase", strings.Join([]string{
		"shard=" + logQuote(shard.Path),
		"decrypt_ms=" + elapsedMS(decryptElapsed),
		"parse_ms=" + elapsedMS(result.ParseElapsed),
		"index_ms=" + elapsedMS(result.IndexElapsed),
	}, " "))
}

func logInfo(req *crawlkit.Request, event, message string) error {
	if req == nil || req.Log == nil {
		return nil
	}
	return req.Log.Info(event, message)
}

func logWarn(req *crawlkit.Request, event, message string) error {
	if req == nil || req.Log == nil {
		return nil
	}
	return req.Log.Warn(event, message)
}

func logDebug(req *crawlkit.Request, event, message string) error {
	if req == nil || req.Log == nil {
		return nil
	}
	return req.Log.Debug(event, message)
}

func logQuote(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if value == "" {
		return strconv.Quote("")
	}
	if strings.ContainsAny(value, " \t\r\n\"") {
		return strconv.Quote(value)
	}
	return value
}

func elapsedMS(value time.Duration) string {
	return strconv.FormatInt(value.Milliseconds(), 10)
}
