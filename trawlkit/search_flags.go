package trawlkit

import (
	"flag"
)

type searchFlagSpec struct {
	name  string
	usage string
}

var searchFlagSpecs = []searchFlagSpec{
	{name: "limit", usage: "Maximum number of results"},
	{name: "after", usage: "Show only results on or after this date"},
	{name: "before", usage: "Show only results on or before this date or time"},
	{name: "who", usage: "Show only results that involve this person"},
}

type searchFlagValues struct {
	limit  *int
	after  *string
	before *string
	who    *string
}

func defineSearchFlags(fs *flag.FlagSet, includeWho bool) searchFlagValues {
	var values searchFlagValues
	for _, spec := range searchFlagSpecs {
		if spec.name == "who" && !includeWho {
			continue
		}
		switch spec.name {
		case "limit":
			values.limit = fs.Int(spec.name, 20, spec.usage)
		case "after":
			values.after = fs.String(spec.name, "", spec.usage)
		case "before":
			values.before = fs.String(spec.name, "", spec.usage)
		case "who":
			values.who = fs.String(spec.name, "", spec.usage)
		}
	}
	return values
}

func runnerOwnedSearchFlagNames() map[string]struct{} {
	names := map[string]struct{}{}
	for _, spec := range searchFlagSpecs {
		names[spec.name] = struct{}{}
	}
	return names
}
