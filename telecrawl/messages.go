package telecrawl

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/openclaw/crawlkit"
	"github.com/openclaw/crawlkit/flags"
	"github.com/openclaw/telecrawl/internal/store"
)

func (c *Crawler) runMessages(ctx context.Context, req *crawlkit.Request) error {
	r := c.handler(ctx, req)
	if len(req.Args) != 0 {
		return usageErr(errors.New("messages takes flags only"))
	}
	filter, err := c.messageFilter()
	if err != nil {
		return err
	}
	return r.withReadOnlyStore(func(st *store.Store) error {
		messages, err := st.Messages(r.ctx, filter)
		if err != nil {
			return err
		}
		total, err := st.CountMessages(r.ctx, filter)
		if err != nil {
			return err
		}
		shortRefs, err := st.ShortRefsFor(r.ctx, messageRefs(messages))
		if err != nil {
			return err
		}
		if r.json {
			return r.print(messageJSONEnvelope(messages, total, shortRefs))
		}
		return r.print(messagesEnvelope{Messages: messages, Total: total, ShortRefs: shortRefs})
	})
}

func (c *Crawler) messageFilter() (store.MessageFilter, error) {
	n, err := flags.Limit(c.messages.Limit, c.messages.LimitSet, c.messages.All)
	if err != nil {
		return store.MessageFilter{}, usageErr(err)
	}
	filter := store.MessageFilter{
		ChatJID:  strings.TrimSpace(c.messages.ChatJID),
		Sender:   strings.TrimSpace(c.messages.Sender),
		TopicID:  strings.TrimSpace(c.messages.TopicID),
		Who:      normalizeWords(c.messages.Who),
		Limit:    n,
		HasMedia: c.messages.HasMedia,
		Pinned:   c.messages.Pinned,
		Asc:      c.messages.Asc,
	}
	if filter.Who == "" && strings.TrimSpace(c.messages.Who) != "" {
		return filter, usageErr(errors.New("--who requires an identity"))
	}
	if c.messages.After != "" {
		t, err := parseDateFlag("--after", c.messages.After)
		if err != nil {
			return filter, usageErr(err)
		}
		filter.After = &t
	}
	if c.messages.Before != "" {
		t, err := parseDateFlag("--before", c.messages.Before)
		if err != nil {
			return filter, usageErr(err)
		}
		filter.Before = &t
	}
	if c.messages.FromMe && c.messages.FromThem {
		return filter, usageErr(errors.New("--from-me and --from-them conflict"))
	}
	if c.messages.FromMe || c.messages.FromThem {
		v := c.messages.FromMe
		filter.FromMe = &v
	}
	return filter, nil
}

func parseDateFlag(name, value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	t, err := flags.Date(value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s: %w", name, err)
	}
	if name == "--before" {
		if day, err := time.ParseInLocation("2006-01-02", value, time.Local); err == nil {
			return day.Add(24*time.Hour - time.Second).UTC(), nil
		}
	}
	return t, nil
}
