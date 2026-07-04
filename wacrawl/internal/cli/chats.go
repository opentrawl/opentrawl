package cli

import (
	"context"
	"errors"
	"flag"
	"io"

	"github.com/openclaw/wacrawl/internal/store"
)

func (a *app) runChats(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("chats", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	limit := fs.Int("limit", 50, "")
	unread := fs.Bool("unread", false, "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printCommandUsage(a.stdout, "chats")
			return nil
		}
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("chats takes flags only"))
	}
	return a.withReadStore(ctx, func(st *store.Store) error {
		var (
			chats []store.Chat
			err   error
		)
		if *unread {
			chats, err = st.ListUnreadChats(ctx, *limit)
		} else {
			chats, err = st.ListChats(ctx, *limit)
		}
		if err != nil {
			return err
		}
		return a.print(chats)
	})
}
