package trawlkit

import (
	"flag"
	"fmt"
	"io"

	"github.com/opentrawl/opentrawl/trawlkit/control"
	ckflags "github.com/opentrawl/opentrawl/trawlkit/flags"
	"github.com/opentrawl/opentrawl/trawlkit/output"
)

const defaultChatLimit = 50

type chatFlagSpec struct {
	name  string
	usage string
}

// The chats verb owns exactly these flags, defined once for every surface.
var chatFlagSpecs = []chatFlagSpec{
	{name: "limit", usage: "maximum chats"},
	{name: "all", usage: "list every chat, ignoring --limit"},
	{name: "unread", usage: "only chats with unread messages"},
}

type chatFlagValues struct {
	limit  *int
	all    *bool
	unread *bool
}

func defineChatFlags(fs *flag.FlagSet) chatFlagValues {
	var values chatFlagValues
	for _, spec := range chatFlagSpecs {
		switch spec.name {
		case "limit":
			values.limit = fs.Int(spec.name, defaultChatLimit, spec.usage)
		case "all":
			values.all = fs.Bool(spec.name, false, spec.usage)
		case "unread":
			values.unread = fs.Bool(spec.name, false, spec.usage)
		}
	}
	return values
}

func parseChatQuery(args []string) (ChatQuery, error) {
	fs := flag.NewFlagSet("chats", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	values := defineChatFlags(fs)
	if err := fs.Parse(args); err != nil {
		return ChatQuery{}, output.UsageError{Err: err}
	}
	if fs.NArg() > 0 {
		return ChatQuery{}, output.UsageError{Err: fmt.Errorf("chats takes flags only, not %q", fs.Arg(0))}
	}
	limitSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "limit" {
			limitSet = true
		}
	})
	query := ChatQuery{All: *values.all, Unread: *values.unread}
	if query.All {
		// --all lists everything; the store reads a zero limit as no cap.
		return query, nil
	}
	limit, err := ckflags.Limit(*values.limit, limitSet)
	if err != nil {
		return ChatQuery{}, output.UsageError{Err: err}
	}
	query.Limit = limit
	return query, nil
}

func builtinChatFlags() []control.Flag {
	fs := flag.NewFlagSet("chats", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	defineChatFlags(fs)
	return flagsFromSet(fs)
}

func runnerOwnedChatFlagNames() map[string]struct{} {
	names := map[string]struct{}{}
	for _, spec := range chatFlagSpecs {
		names[spec.name] = struct{}{}
	}
	return names
}
