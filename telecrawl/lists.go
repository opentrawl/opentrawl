package telecrawl

import (
	"context"
	"errors"

	"github.com/openclaw/crawlkit"
	"github.com/openclaw/crawlkit/flags"
	"github.com/openclaw/telecrawl/internal/store"
)

func (c *Crawler) runChats(ctx context.Context, req *crawlkit.Request) error {
	r := c.handler(ctx, req)
	if len(req.Args) != 0 {
		return usageErr(errors.New("chats takes flags only"))
	}
	n, err := flags.Limit(c.chats.Limit, c.chats.LimitSet)
	if err != nil {
		return usageErr(err)
	}
	return r.withReadOnlyStore(func(st *store.Store) error {
		if c.chats.Folder != "" {
			chats, err := st.ChatsInFolder(r.ctx, c.chats.Folder, n)
			if err != nil {
				return err
			}
			total, err := st.CountChatsInFolder(r.ctx, c.chats.Folder)
			if err != nil {
				return err
			}
			if r.json {
				return r.print(chatJSONEnvelope(chats, total))
			}
			return r.print(chatsEnvelope{Chats: chats, Total: total})
		}
		chats, err := st.ListChats(r.ctx, n, c.chats.Unread)
		if err != nil {
			return err
		}
		total, err := st.CountChats(r.ctx, c.chats.Unread)
		if err != nil {
			return err
		}
		if r.json {
			return r.print(chatJSONEnvelope(chats, total))
		}
		return r.print(chatsEnvelope{Chats: chats, Total: total})
	})
}

func (c *Crawler) runFolders(ctx context.Context, req *crawlkit.Request) error {
	r := c.handler(ctx, req)
	if len(req.Args) != 0 {
		return usageErr(errors.New("folders takes flags only"))
	}
	return r.withReadOnlyStore(func(st *store.Store) error {
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

func (c *Crawler) runTopics(ctx context.Context, req *crawlkit.Request) error {
	r := c.handler(ctx, req)
	if len(req.Args) != 0 {
		return usageErr(errors.New("topics takes flags only"))
	}
	if c.topics.ChatID == "" {
		return usageErr(errors.New("topics requires --chat ID"))
	}
	n, err := flags.Limit(c.topics.Limit, c.topics.LimitSet)
	if err != nil {
		return usageErr(err)
	}
	return r.withReadOnlyStore(func(st *store.Store) error {
		topics, err := st.ListTopics(r.ctx, c.topics.ChatID, n)
		if err != nil {
			return err
		}
		if r.json {
			return r.print(topicJSONRows(topics))
		}
		total, err := st.CountTopics(r.ctx, c.topics.ChatID)
		if err != nil {
			return err
		}
		return r.print(topicsEnvelope{Topics: topics, Total: total, ChatID: c.topics.ChatID})
	})
}
