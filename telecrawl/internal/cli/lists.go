package cli

import (
	"errors"
	"flag"
	"io"

	"github.com/openclaw/telecrawl/internal/store"
)

func (r *runtime) runChats(args []string) error {
	fs := flag.NewFlagSet("telecrawl chats", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	limit := fs.Int("limit", 50, "")
	unread := fs.Bool("unread", false, "")
	folder := fs.String("folder", "", "")
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	return r.withStore(func(st *store.Store) error {
		if *folder != "" {
			chats, err := st.ChatsInFolder(r.ctx, *folder, *limit)
			if err != nil {
				return err
			}
			total, err := st.CountChatsInFolder(r.ctx, *folder)
			if err != nil {
				return err
			}
			if r.json {
				return r.print(chatJSONEnvelope(chats, total))
			}
			return r.print(chatsEnvelope{Chats: chats, Total: total})
		}
		chats, err := st.ListChats(r.ctx, *limit, *unread)
		if err != nil {
			return err
		}
		total, err := st.CountChats(r.ctx, *unread)
		if err != nil {
			return err
		}
		if r.json {
			return r.print(chatJSONEnvelope(chats, total))
		}
		return r.print(chatsEnvelope{Chats: chats, Total: total})
	})
}

func (r *runtime) runFolders(args []string) error {
	fs := flag.NewFlagSet("telecrawl folders", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("folders takes flags only"))
	}
	return r.withStore(func(st *store.Store) error {
		folders, err := st.ListFolders(r.ctx)
		if err != nil {
			return err
		}
		if r.json {
			return r.print(folderJSONRows(folders))
		}
		return r.print(foldersEnvelope{Folders: folders})
	})
}

func (r *runtime) runTopics(args []string) error {
	fs := flag.NewFlagSet("telecrawl topics", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	chat := fs.String("chat", "", "")
	limit := fs.Int("limit", 100, "")
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("topics takes flags only"))
	}
	if *chat == "" {
		return usageErr(errors.New("topics requires --chat ID"))
	}
	return r.withStore(func(st *store.Store) error {
		topics, err := st.ListTopics(r.ctx, *chat, *limit)
		if err != nil {
			return err
		}
		if r.json {
			return r.print(topicJSONRows(topics))
		}
		return r.print(topicsEnvelope{Topics: topics})
	})
}
