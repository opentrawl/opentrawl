package telecrawl

import (
	"context"
	"errors"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/flags"
)

// Chats implements trawlkit.ChatLister. Telegram stores a real per-chat
// unread count, so the plain list and the --unread filter both come from the
// store; the kit owns the verb, flags, JSON and table.
func (c *Crawler) Chats(ctx context.Context, req *trawlkit.Request, q trawlkit.ChatQuery) ([]trawlkit.Chat, error) {
	limit := q.Limit
	if q.All {
		limit = 0
	}
	r := c.handler(ctx, req)
	var out []trawlkit.Chat
	err := r.withReadOnlyStore(func(st *store.Store) error {
		rows, err := st.ListChats(r.ctx, limit, q.Unread)
		if err != nil {
			return err
		}
		out = make([]trawlkit.Chat, 0, len(rows))
		for _, chat := range rows {
			unread := int64(chat.UnreadCount)
			out = append(out, trawlkit.Chat{
				// Telegram stores "user", "group" and "channel"; only a one-to-one
				// "user" chat is a dm, so channels and groups are both groups.
				ID:           chat.JID,
				Title:        chatName(chat),
				Group:        chat.Kind != "user",
				LastActivity: chat.LastMessageAt,
				Unread:       &unread,
			})
		}
		return nil
	})
	return out, err
}

func (c *Crawler) runFolders(ctx context.Context, req *trawlkit.Request) error {
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

func (c *Crawler) runTopics(ctx context.Context, req *trawlkit.Request) error {
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
