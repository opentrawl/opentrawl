package cli

import (
	"context"
	"errors"
	"flag"
	"io"

	"github.com/openclaw/crawlkit/flags"
	"github.com/openclaw/wacrawl/internal/store"
)

func (a *app) runUnread(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("unread", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	limit := fs.Int("limit", 50, "")
	all := fs.Bool("all", false, "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printCommandUsage(a.stdout, "unread")
			return nil
		}
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("unread takes flags only"))
	}
	n, err := flags.Limit(*limit, flagWasProvided(fs, "limit"), *all)
	if err != nil {
		return usageErr(err)
	}
	return a.withReadStore(ctx, func(st *store.Store) error {
		chats, err := st.ListUnreadChats(ctx, n)
		if err != nil {
			return err
		}
		total, err := st.CountUnreadChats(ctx)
		if err != nil {
			return err
		}
		return a.print(newChatsEnvelope(chats, total, true, "wacrawl unread --all"))
	})
}
