package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/openclaw/telecrawl/internal/store"
)

func (r *runtime) runMessages(args []string) error {
	filter, err := r.messageFilter("telecrawl messages", args, false, defaultMessageLimit)
	if err != nil {
		return err
	}
	return r.withStore(func(st *store.Store) error {
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

var messageFilterValueFlags = map[string]bool{
	"chat": true, "sender": true, "topic": true, "who": true,
	"limit": true, "after": true, "before": true,
}

func splitFlagArgs(args []string) (flags, positionals []string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if strings.HasPrefix(arg, "-") {
			flags = append(flags, arg)
			name := strings.TrimLeft(arg, "-")
			if !strings.Contains(name, "=") && messageFilterValueFlags[name] && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		positionals = append(positionals, arg)
	}
	return flags, positionals
}

func (r *runtime) messageFilter(name string, args []string, requireQuery bool, defaultLimit int) (store.MessageFilter, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var filter store.MessageFilter
	fs.StringVar(&filter.ChatJID, "chat", "", "")
	fs.StringVar(&filter.Sender, "sender", "", "")
	fs.StringVar(&filter.TopicID, "topic", "", "")
	if requireQuery {
		fs.StringVar(&filter.Who, "who", "", "")
	}
	fs.IntVar(&filter.Limit, "limit", defaultLimit, "")
	after := fs.String("after", "", "")
	before := fs.String("before", "", "")
	fromMe := fs.Bool("from-me", false, "")
	fromThem := fs.Bool("from-them", false, "")
	fs.BoolVar(&filter.HasMedia, "media", false, "")
	fs.BoolVar(&filter.Pinned, "pinned", false, "")
	fs.BoolVar(&filter.Asc, "asc", false, "")
	flagTokens, positionals := splitFlagArgs(args)
	if err := fs.Parse(flagTokens); err != nil {
		return filter, usageErr(err)
	}
	whoProvided := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "who" {
			whoProvided = true
		}
	})
	if requireQuery {
		if whoProvided {
			filter.Who = normalizeCLIWords(filter.Who)
			if filter.Who == "" {
				return filter, usageErr(errors.New("--who requires an identity"))
			}
		}
		filterOnly := whoProvided || strings.TrimSpace(*after) != "" || strings.TrimSpace(*before) != ""
		switch {
		case len(positionals) == 0 && !filterOnly:
			return filter, usageErr(errors.New("search takes a query unless --who, --after, or --before is set\n\n" + commandUsage([]string{"search"})))
		case len(positionals) > 1:
			return filter, usageErr(errors.New("search takes at most one query"))
		case len(positionals) == 1:
			filter.Query = positionals[0]
		}
	} else if len(positionals) != 0 {
		return filter, usageErr(errors.New("messages takes flags only"))
	}
	if *after != "" {
		t, err := parseDate(*after)
		if err != nil {
			return filter, usageErr(err)
		}
		filter.After = &t
	}
	if *before != "" {
		t, err := parseDate(*before)
		if err != nil {
			return filter, usageErr(err)
		}
		filter.Before = &t
	}
	if *fromMe && *fromThem {
		return filter, usageErr(errors.New("--from-me and --from-them conflict"))
	}
	if *fromMe || *fromThem {
		v := *fromMe
		filter.FromMe = &v
	}
	return filter, nil
}

func normalizeCLIWords(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func parseDate(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02", value); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("invalid date %q", value)
}
