package cli

import (
	"context"
	"errors"
	"flag"
	"io"

	"github.com/openclaw/wacrawl/internal/store"
)

func (a *app) runUnread(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("unread", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	limit := fs.Int("limit", 50, "")
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
	return a.withReadStore(ctx, func(st *store.Store) error {
		chats, err := st.ListUnreadChats(ctx, *limit)
		if err != nil {
			return err
		}
		return a.print(chats)
	})
}
