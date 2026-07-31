package trawlkit

import "flag"

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

func runnerOwnedConversationFlagNames() map[string]struct{} {
	names := map[string]struct{}{}
	for _, spec := range conversationFlagSpecs {
		names[spec.name] = struct{}{}
	}
	return names
}
