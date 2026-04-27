package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/steipete/wacrawl/internal/backup"
	"github.com/steipete/wacrawl/internal/store"
)

func (a *app) runBackup(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usageErr(errors.New("backup requires init, push, pull, or status"))
	}
	switch args[0] {
	case "init":
		return a.runBackupInit(ctx, args[1:])
	case "push":
		return a.runBackupPush(ctx, args[1:])
	case "pull":
		return a.runBackupPull(ctx, args[1:])
	case "status":
		return a.runBackupStatus(ctx, args[1:])
	default:
		return usageErr(fmt.Errorf("unknown backup command %q", args[0]))
	}
}

func (a *app) runBackupInit(ctx context.Context, args []string) error {
	fs, opts, noPush := backupFlagSet("backup init")
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("backup init takes flags only"))
	}
	opts.Push = !*noPush
	cfg, recipient, err := backup.Init(ctx, *opts)
	if err != nil {
		return err
	}
	if a.json {
		return a.print(map[string]any{"repo": cfg.Repo, "remote": cfg.Remote, "identity": cfg.Identity, "recipient": recipient})
	}
	_, err = fmt.Fprintf(a.stdout, "repo=%s\nremote=%s\nidentity=%s\nrecipient=%s\n", cfg.Repo, cfg.Remote, cfg.Identity, recipient)
	return err
}

func (a *app) runBackupPush(ctx context.Context, args []string) error {
	fs, opts, noPush := backupFlagSet("backup push")
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("backup push takes flags only"))
	}
	opts.Push = !*noPush
	return a.withArchiveStore(ctx, func(st *store.Store) error {
		result, err := backup.Push(ctx, st, *opts)
		if err != nil {
			return err
		}
		return a.print(result)
	})
}

func (a *app) runBackupPull(ctx context.Context, args []string) error {
	fs, opts, _ := backupFlagSet("backup pull")
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("backup pull takes flags only"))
	}
	return a.withStore(ctx, func(st *store.Store) error {
		result, err := backup.Pull(ctx, st, *opts)
		if err != nil {
			return err
		}
		return a.print(result)
	})
}

func (a *app) runBackupStatus(ctx context.Context, args []string) error {
	fs, opts, _ := backupFlagSet("backup status")
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("backup status takes flags only"))
	}
	manifest, repo, err := backup.Status(ctx, *opts)
	if err != nil {
		return err
	}
	if a.json {
		return a.print(map[string]any{"repo": repo, "manifest": manifest})
	}
	if err := a.print(manifest); err != nil {
		return err
	}
	_, err = fmt.Fprintf(a.stdout, "repo=%s\n", repo)
	return err
}

func backupFlagSet(name string) (*flag.FlagSet, *backup.Options, *bool) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	opts := &backup.Options{}
	fs.StringVar(&opts.ConfigPath, "config", backup.DefaultConfigPath(), "")
	fs.StringVar(&opts.Repo, "repo", "", "")
	fs.StringVar(&opts.Remote, "remote", "", "")
	fs.StringVar(&opts.Identity, "identity", "", "")
	fs.Func("recipient", "", func(value string) error {
		opts.Recipients = append(opts.Recipients, value)
		return nil
	})
	noPush := fs.Bool("no-push", false, "")
	return fs, opts, noPush
}
