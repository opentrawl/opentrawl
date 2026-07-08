package telecrawl

import (
	"context"
	"errors"

	"github.com/openclaw/crawlkit"
	"github.com/openclaw/crawlkit/flags"
	"github.com/openclaw/telecrawl/internal/backup"
	"github.com/openclaw/telecrawl/internal/store"
)

func (c *Crawler) backupInit(ctx context.Context, req *crawlkit.Request) error {
	r := c.handler(ctx, req)
	if len(req.Args) != 0 {
		return usageErr(errors.New("backup init takes flags only"))
	}
	opts := c.backupOptions(req)
	opts.Push = !c.backup.NoPush
	cfg, recipient, err := backup.Init(ctx, opts)
	if err != nil {
		return err
	}
	return r.print(backupInitOutput{Repo: cfg.Repo, Remote: cfg.Remote, Identity: cfg.Identity, Recipient: recipient})
}

func (c *Crawler) backupPush(ctx context.Context, req *crawlkit.Request) error {
	r := c.handler(ctx, req)
	if len(req.Args) != 0 {
		return usageErr(errors.New("backup push takes flags only"))
	}
	opts := c.backupOptions(req)
	opts.Push = !c.backup.NoPush
	return r.withStore(func(st *store.Store) error {
		result, err := backup.Push(ctx, st, opts)
		if err != nil {
			return err
		}
		return r.print(result)
	})
}

func (c *Crawler) backupPull(ctx context.Context, req *crawlkit.Request) error {
	r := c.handler(ctx, req)
	if len(req.Args) != 0 {
		return usageErr(errors.New("backup pull takes flags only"))
	}
	opts := c.backupOptions(req)
	return r.withStore(func(st *store.Store) error {
		result, err := backup.Pull(ctx, st, opts)
		if err != nil {
			return err
		}
		return r.print(result)
	})
}

func (c *Crawler) backupStatus(ctx context.Context, req *crawlkit.Request) error {
	r := c.handler(ctx, req)
	if len(req.Args) != 0 {
		return usageErr(errors.New("backup status takes flags only"))
	}
	opts := c.backupOptions(req)
	manifest, repo, err := backup.Status(ctx, opts)
	if err != nil {
		return err
	}
	return r.print(backupStatusOutput{Repo: repo, Manifest: manifest})
}

func (c *Crawler) backupSnapshots(ctx context.Context, req *crawlkit.Request) error {
	r := c.handler(ctx, req)
	if len(req.Args) != 0 {
		return usageErr(errors.New("backup snapshots takes flags only"))
	}
	// Snapshots list from git history, which crawlkit bounds with a positive
	// -n; there is no unlimited walk, so this verb takes --limit (one contract)
	// but not --all.
	opts := c.backupOptions(req)
	n, err := flags.Limit(c.backup.Limit, c.backup.LimitSet, false)
	if err != nil {
		return usageErr(err)
	}
	opts.Limit = n
	snapshots, repo, err := backup.Snapshots(ctx, opts)
	if err != nil {
		return err
	}
	if r.json {
		return r.print(map[string]any{"repo": repo, "snapshots": snapshots})
	}
	return r.print(snapshots)
}

func (c *Crawler) backupOptions(req *crawlkit.Request) backup.Options {
	return backup.Options{
		ConfigPath: req.Paths.Config,
		Repo:       c.backup.Repo,
		Remote:     c.backup.Remote,
		Identity:   c.backup.Identity,
		Recipients: append([]string(nil), c.backup.Recipients...),
		Ref:        c.backup.Ref,
		Tag:        c.backup.Tag,
		Limit:      c.backup.Limit,
	}
}

type backupInitOutput struct {
	Repo      string `json:"repo"`
	Remote    string `json:"remote"`
	Identity  string `json:"identity"`
	Recipient string `json:"recipient"`
}

type backupStatusOutput struct {
	Repo     string          `json:"repo"`
	Manifest backup.Manifest `json:"manifest"`
}
