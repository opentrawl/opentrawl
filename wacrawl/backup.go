package wacrawl

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/openclaw/crawlkit"
	ckconfig "github.com/openclaw/crawlkit/config"
	ckflags "github.com/openclaw/crawlkit/flags"
	"github.com/openclaw/crawlkit/output"
	"github.com/openclaw/wacrawl/internal/backup"
	"github.com/openclaw/wacrawl/internal/store"
)

func (c *Crawler) bindBackupInitFlags(fs *flag.FlagSet) {
	c.bindBackupBaseFlags(fs)
	fs.BoolVar(&c.backupNoPush, "no-push", false, "commit locally without pushing")
}

func (c *Crawler) bindBackupPushFlags(fs *flag.FlagSet) {
	c.bindBackupBaseFlags(fs)
	fs.BoolVar(&c.backupNoPush, "no-push", false, "commit locally without pushing")
	fs.StringVar(&c.backupOpts.Tag, "tag", "", "tag the resulting backup commit")
	fs.BoolVar(&c.backupOpts.NoMedia, "no-media", false, "omit copied media files")
}

func (c *Crawler) bindBackupPullFlags(fs *flag.FlagSet) {
	c.bindBackupBaseFlags(fs)
	fs.StringVar(&c.backupOpts.Ref, "ref", "", "restore this Git ref")
	fs.BoolVar(&c.backupOpts.NoMedia, "no-media", false, "skip restoring media files")
}

func (c *Crawler) bindBackupStatusFlags(fs *flag.FlagSet) {
	c.bindBackupBaseFlags(fs)
}

func (c *Crawler) bindBackupSnapshotsFlags(fs *flag.FlagSet) {
	c.bindBackupBaseFlags(fs)
	c.backupLimit = newIntFlag(20)
	fs.Var(&c.backupLimit, "limit", "maximum snapshots")
}

func (c *Crawler) bindBackupBaseFlags(fs *flag.FlagSet) {
	c.backupOpts = backup.Options{Config: c.cfg.Backup, Push: true}
	c.backupNoPush = false
	fs.StringVar(&c.backupOpts.Repo, "repo", "", "local backup Git checkout")
	fs.StringVar(&c.backupOpts.Remote, "remote", "", "backup Git remote")
	fs.StringVar(&c.backupOpts.Identity, "identity", "", "local age identity")
	fs.Func("recipient", "age recipient allowed to decrypt backups", func(value string) error {
		c.backupOpts.Recipients = append(c.backupOpts.Recipients, value)
		return nil
	})
}

func (c *Crawler) runBackupInit(ctx context.Context, req *crawlkit.Request) error {
	if len(req.Args) != 0 {
		return usageErr(fmt.Errorf("backup init takes flags only"))
	}
	c.backupOpts.Push = !c.backupNoPush
	c.backupOpts.SaveConfig = func(cfg backup.Config) error {
		c.cfg.Backup = cfg
		return ckconfig.WriteTOML(req.Paths.Config, c.cfg, 0o600)
	}
	cfg, recipient, err := backup.Init(ctx, c.backupOpts)
	if err != nil {
		return err
	}
	if req.Format == output.JSON {
		return output.Write(req.Out, req.Format, "backup_init", map[string]any{"repo": cfg.Repo, "remote": cfg.Remote, "identity": cfg.Identity, "recipient": recipient})
	}
	_, err = fmt.Fprintf(req.Out, "repo=%s\nremote=%s\nidentity=%s\nrecipient=%s\n", cfg.Repo, cfg.Remote, cfg.Identity, recipient)
	return err
}

func (c *Crawler) runBackupPush(ctx context.Context, req *crawlkit.Request) error {
	if len(req.Args) != 0 {
		return usageErr(fmt.Errorf("backup push takes flags only"))
	}
	c.backupOpts.Push = !c.backupNoPush
	st, err := store.Use(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return err
	}
	result, err := backup.Push(ctx, st, c.backupOpts)
	if err != nil {
		return err
	}
	return writeBackupResult(req, result)
}

func (c *Crawler) runBackupPull(ctx context.Context, req *crawlkit.Request) error {
	if len(req.Args) != 0 {
		return usageErr(fmt.Errorf("backup pull takes flags only"))
	}
	st, err := store.Use(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return err
	}
	result, err := backup.Pull(ctx, st, c.backupOpts)
	if err != nil {
		return err
	}
	return writeBackupResult(req, result)
}

func (c *Crawler) runBackupStatus(ctx context.Context, req *crawlkit.Request) error {
	if len(req.Args) != 0 {
		return usageErr(fmt.Errorf("backup status takes flags only"))
	}
	manifest, repo, err := backup.Status(ctx, c.backupOpts)
	if err != nil {
		return err
	}
	if req.Format == output.JSON {
		return output.Write(req.Out, req.Format, "backup_status", map[string]any{"repo": repo, "manifest": manifest})
	}
	if err := writeBackupManifest(req, manifest); err != nil {
		return err
	}
	_, err = fmt.Fprintf(req.Out, "repo=%s\n", repo)
	return err
}

func (c *Crawler) runBackupSnapshots(ctx context.Context, req *crawlkit.Request) error {
	if len(req.Args) != 0 {
		return usageErr(fmt.Errorf("backup snapshots takes flags only"))
	}
	n, err := ckflags.Limit(c.backupLimit.value, c.backupLimit.set, false)
	if err != nil {
		return usageErr(err)
	}
	c.backupOpts.Limit = n
	snapshots, repo, err := backup.Snapshots(ctx, c.backupOpts)
	if err != nil {
		return err
	}
	if req.Format == output.JSON {
		return output.Write(req.Out, req.Format, "backup_snapshots", map[string]any{"repo": repo, "snapshots": snapshots})
	}
	return writeBackupSnapshots(req, snapshots)
}

func writeBackupResult(req *crawlkit.Request, result backup.Result) error {
	if req.Format == output.JSON {
		return output.Write(req.Out, req.Format, "backup", result)
	}
	if _, err := fmt.Fprintf(req.Out, "repo=%s\nchanged=%t\nencrypted=%t\nshards=%d\nmessages=%d\nmedia_files=%d\n", result.Repo, result.Changed, result.Encrypted, result.Shards, result.Messages, result.MediaFiles); err != nil {
		return err
	}
	if result.Ref != "" {
		if _, err := fmt.Fprintf(req.Out, "ref=%s\n", result.Ref); err != nil {
			return err
		}
	}
	if result.Tag != "" {
		if _, err := fmt.Fprintf(req.Out, "tag=%s\n", result.Tag); err != nil {
			return err
		}
	}
	return nil
}

func writeBackupSnapshots(req *crawlkit.Request, snapshots []backup.Snapshot) error {
	if len(snapshots) == 0 {
		_, err := fmt.Fprintln(req.Out, "No backup snapshots found.")
		return err
	}
	tw := tabwriter.NewWriter(req.Out, 2, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "REF\tEXPORTED\tMESSAGES\tMEDIA\tSHARDS\tTAGS")
	for _, snapshot := range snapshots {
		ref := snapshot.Ref
		if len(ref) > 12 {
			ref = ref[:12]
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\t%s\n", ref, formatTime(snapshot.Exported), snapshot.Counts.Messages, snapshot.Counts.MediaFiles, snapshot.Shards, strings.Join(snapshot.Tags, ","))
	}
	return tw.Flush()
}

func writeBackupManifest(req *crawlkit.Request, manifest backup.Manifest) error {
	_, err := fmt.Fprintf(req.Out, "encrypted=%t\nshards=%d\nmessages=%d\nmedia_files=%d\nexported=%s\n", manifest.Encrypted, len(manifest.Shards), manifest.Counts.Messages, len(manifest.Files), formatTime(manifest.Exported))
	return err
}
