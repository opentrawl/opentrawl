package trawlkit

import (
	"flag"
	"fmt"
	"io"

	ckflags "github.com/opentrawl/opentrawl/trawlkit/flags"
	"github.com/opentrawl/opentrawl/trawlkit/output"
)

const defaultConversationLimit = 50

type conversationFlagSpec struct {
	name  string
	usage string
}

// The conversations command owns exactly these flags, defined once for every surface.
var conversationFlagSpecs = []conversationFlagSpec{
	{name: "limit", usage: "maximum conversations"},
	{name: "all", usage: "list every conversation, ignoring --limit"},
	{name: "unread", usage: "only conversations with unread messages"},
}

type conversationFlagValues struct {
	limit  *int
	all    *bool
	unread *bool
}

func defineConversationFlags(fs *flag.FlagSet) conversationFlagValues {
	var values conversationFlagValues
	for _, spec := range conversationFlagSpecs {
		switch spec.name {
		case "limit":
			values.limit = fs.Int(spec.name, defaultConversationLimit, spec.usage)
		case "all":
			values.all = fs.Bool(spec.name, false, spec.usage)
		case "unread":
			values.unread = fs.Bool(spec.name, false, spec.usage)
		}
	}
	return values
}

func parseConversationQuery(args []string) (ConversationQuery, error) {
	fs := flag.NewFlagSet("conversations", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	values := defineConversationFlags(fs)
	if err := fs.Parse(args); err != nil {
		return ConversationQuery{}, output.UsageError{Err: err}
	}
	if fs.NArg() > 0 {
		return ConversationQuery{}, output.UsageError{Err: fmt.Errorf("conversations takes flags only, not %q", fs.Arg(0))}
	}
	limitSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "limit" {
			limitSet = true
		}
	})
	query := ConversationQuery{All: *values.all, Unread: *values.unread}
	if query.All {
		// --all lists everything; the store reads a zero limit as no cap.
		return query, nil
	}
	limit, err := ckflags.Limit(*values.limit, limitSet)
	if err != nil {
		return ConversationQuery{}, output.UsageError{Err: err}
	}
	query.Limit = limit
	return query, nil
}

func runnerOwnedConversationFlagNames() map[string]struct{} {
	names := map[string]struct{}{}
	for _, spec := range conversationFlagSpecs {
		names[spec.name] = struct{}{}
	}
	return names
}
