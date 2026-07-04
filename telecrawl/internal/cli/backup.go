package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/openclaw/telecrawl/internal/backup"
	"github.com/openclaw/telecrawl/internal/store"
)

func (r *runtime) runBackup(args []string) error {
	if len(args) == 0 {
		return usageErr(errors.New("backup needs subcommand: init, push, pull, status, snapshots"))
	}
	switch args[0] {
	case "init":
		return r.backupInit(args[1:])
	case "push":
		return r.backupPush(args[1:])
	case "pull":
		return r.backupPull(args[1:])
	case "status":
		return r.backupStatus(args[1:])
	case "snapshots":
		return r.backupSnapshots(args[1:])
	default:
		return usageErr(fmt.Errorf("unknown backup command %q", args[0]))
	}
}

func backupFlags(name string) (*flag.FlagSet, *backup.Options, *bool) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	opts := &backup.Options{}
	fs.StringVar(&opts.ConfigPath, "config", backup.DefaultConfigPath(), "")
	fs.StringVar(&opts.Repo, "repo", "", "")
	fs.StringVar(&opts.Remote, "remote", "", "")
	fs.StringVar(&opts.Identity, "identity", "", "")
	fs.StringVar(&opts.Ref, "ref", "", "")
	fs.StringVar(&opts.Tag, "tag", "", "")
	fs.IntVar(&opts.Limit, "limit", 20, "")
	fs.Func("recipient", "", func(value string) error {
		opts.Recipients = append(opts.Recipients, value)
		return nil
	})
	noPush := fs.Bool("no-push", false, "")
	return fs, opts, noPush
}

func (r *runtime) backupInit(args []string) error {
	fs, opts, noPush := backupFlags("telecrawl backup init")
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	opts.Push = !*noPush
	cfg, recipient, err := backup.Init(r.ctx, *opts)
	if err != nil {
		return err
	}
	return r.print(backupInitOutput{Repo: cfg.Repo, Remote: cfg.Remote, Identity: cfg.Identity, Recipient: recipient})
}

func (r *runtime) backupPush(args []string) error {
	fs, opts, noPush := backupFlags("telecrawl backup push")
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	opts.Push = !*noPush
	return r.withStore(func(st *store.Store) error {
		result, err := backup.Push(r.ctx, st, *opts)
		if err != nil {
			return err
		}
		return r.print(result)
	})
}

func (r *runtime) backupPull(args []string) error {
	fs, opts, _ := backupFlags("telecrawl backup pull")
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	return r.withStore(func(st *store.Store) error {
		result, err := backup.Pull(r.ctx, st, *opts)
		if err != nil {
			return err
		}
		return r.print(result)
	})
}

func (r *runtime) backupStatus(args []string) error {
	fs, opts, _ := backupFlags("telecrawl backup status")
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	manifest, repo, err := backup.Status(r.ctx, *opts)
	if err != nil {
		return err
	}
	return r.print(backupStatusOutput{Repo: repo, Manifest: manifest})
}

func (r *runtime) backupSnapshots(args []string) error {
	fs, opts, _ := backupFlags("telecrawl backup snapshots")
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("backup snapshots takes flags only"))
	}
	if opts.Limit < 1 {
		return usageErr(errors.New("backup snapshots --limit must be greater than zero"))
	}
	snapshots, repo, err := backup.Snapshots(r.ctx, *opts)
	if err != nil {
		return err
	}
	if r.json {
		return r.print(map[string]any{"repo": repo, "snapshots": snapshots})
	}
	return r.print(snapshots)
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
