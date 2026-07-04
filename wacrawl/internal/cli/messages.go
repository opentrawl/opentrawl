package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/openclaw/wacrawl/internal/store"
)

type messageListOutput struct {
	Query     string          `json:"query,omitempty"`
	Returned  int             `json:"returned"`
	Limit     int             `json:"limit"`
	Truncated bool            `json:"truncated"`
	Messages  []store.Message `json:"results"`
}

func newMessageListOutput(query string, limit int, messages []store.Message) messageListOutput {
	if messages == nil {
		messages = []store.Message{}
	}
	return messageListOutput{
		Query:     query,
		Returned:  len(messages),
		Limit:     limit,
		Truncated: limit > 0 && len(messages) == limit,
		Messages:  messages,
	}
}

func (a *app) runMessages(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("messages", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	filter := bindMessageFlags(fs)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printCommandUsage(a.stdout, "messages")
			return nil
		}
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("messages takes flags only"))
	}
	resolved, err := filter.resolve()
	if err != nil {
		return usageErr(err)
	}
	return a.withReadStore(ctx, func(st *store.Store) error {
		msgs, err := st.Messages(ctx, resolved)
		if err != nil {
			return err
		}
		return a.print(newMessageListOutput("", resolved.Limit, msgs))
	})
}

type messageFlags struct {
	chat     *string
	sender   *string
	limit    *int
	after    *string
	before   *string
	fromMe   *bool
	fromThem *bool
	hasMedia *bool
	asc      *bool
}

func bindMessageFlags(fs *flag.FlagSet) messageFlags {
	return messageFlags{
		chat:     fs.String("chat", "", ""),
		sender:   fs.String("sender", "", ""),
		limit:    fs.Int("limit", defaultMessageLimit, ""),
		after:    fs.String("after", "", ""),
		before:   fs.String("before", "", ""),
		fromMe:   fs.Bool("from-me", false, ""),
		fromThem: fs.Bool("from-them", false, ""),
		hasMedia: fs.Bool("has-media", false, ""),
		asc:      fs.Bool("asc", false, ""),
	}
}

func (f messageFlags) resolve() (store.MessageFilter, error) {
	if *f.fromMe && *f.fromThem {
		return store.MessageFilter{}, errors.New("--from-me and --from-them are mutually exclusive")
	}
	if *f.limit < 1 || *f.limit > maxMessageLimit {
		return store.MessageFilter{}, fmt.Errorf("--limit must be between 1 and %d", maxMessageLimit)
	}
	out := store.MessageFilter{
		ChatJID:  *f.chat,
		Sender:   *f.sender,
		Limit:    *f.limit,
		HasMedia: *f.hasMedia,
		Asc:      *f.asc,
	}
	if *f.fromMe {
		v := true
		out.FromMe = &v
	}
	if *f.fromThem {
		v := false
		out.FromMe = &v
	}
	if strings.TrimSpace(*f.after) != "" {
		t, err := parseTime(*f.after)
		if err != nil {
			return store.MessageFilter{}, err
		}
		out.After = &t
	}
	if strings.TrimSpace(*f.before) != "" {
		t, err := parseTime(*f.before)
		if err != nil {
			return store.MessageFilter{}, err
		}
		out.Before = &t
	}
	return out, nil
}

func (a *app) printMessages(messages []store.Message, truncated bool, limit int) error {
	for _, m := range messages {
		body := firstNonEmpty(messageSnippet(m), messageText(m))
		if _, err := fmt.Fprintf(a.stdout, "[%s] %s / %s / %s\n%s\n\n", formatTime(m.Timestamp), m.ChatName, firstNonEmpty(m.SenderName, m.SenderJID), m.MessageID, body); err != nil {
			return err
		}
	}
	if truncated {
		_, err := fmt.Fprintf(a.stdout, "showing %d of possibly more; narrow with --limit, --after, --before, or --chat\n", limit)
		return err
	}
	return nil
}
