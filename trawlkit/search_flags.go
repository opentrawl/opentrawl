package trawlkit

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	ckflags "github.com/opentrawl/opentrawl/trawlkit/flags"
	"github.com/opentrawl/opentrawl/trawlkit/output"
)

func parseQuery(args []string) (Query, error) {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	searchFlags := defineSearchFlags(fs, true)
	flagArgs, positional, err := searchFlagArgs(args)
	if err != nil {
		return Query{}, err
	}
	if err := fs.Parse(flagArgs); err != nil {
		return Query{}, output.UsageError{Err: err}
	}
	limitSet := false
	whoSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "limit" {
			limitSet = true
		}
		if f.Name == "who" {
			whoSet = true
		}
	})
	resolvedLimit, err := ckflags.Limit(*searchFlags.limit, limitSet)
	if err != nil {
		return Query{}, output.UsageError{Err: err}
	}
	if whoSet && strings.TrimSpace(*searchFlags.who) == "" {
		return Query{}, output.UsageError{Err: errors.New("--who needs a person.")}
	}
	query := Query{Limit: resolvedLimit, Who: *searchFlags.who}
	if len(positional) > 0 {
		query.Text = strings.Join(positional, " ")
	}
	if query.After, err = parseDateFlag("--after", *searchFlags.after); err != nil {
		return Query{}, err
	}
	if query.Before, err = parseDateFlag("--before", *searchFlags.before); err != nil {
		return Query{}, err
	}
	if strings.TrimSpace(query.Text) == "" && strings.TrimSpace(query.Who) == "" && query.After.IsZero() && query.Before.IsZero() {
		return Query{}, usageError{err: errors.New("Search needs words, a person, or a date range.")}
	}
	return query, nil
}

func searchFlagArgs(args []string) ([]string, []string, error) {
	var flags []string
	var positional []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		name, value, inline := splitFlagValue(arg)
		if !knownSearchFlag(name) {
			if strings.HasPrefix(arg, "-") {
				flags = append(flags, arg)
			} else {
				positional = append(positional, arg)
			}
			continue
		}
		flags = append(flags, name)
		if inline {
			flags = append(flags, value)
			continue
		}
		i++
		if i >= len(args) {
			return nil, nil, output.UsageError{Err: fmt.Errorf("%s needs a value.", name)}
		}
		flags = append(flags, args[i])
	}
	return flags, positional, nil
}

func knownSearchFlag(name string) bool {
	if !strings.HasPrefix(name, "--") {
		return false
	}
	_, ok := searchFlagByName(strings.TrimLeft(name, "-"))
	return ok
}

type searchFlagSpec struct {
	name  string
	usage string
}

var searchFlagSpecs = []searchFlagSpec{
	{name: "limit", usage: "maximum results"},
	{name: "after", usage: "only results at or after this date"},
	{name: "before", usage: "only results on or before this date or time"},
	{name: "who", usage: "only results involving this person"},
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

func searchFlagByName(name string) (searchFlagSpec, bool) {
	for _, spec := range searchFlagSpecs {
		if name == spec.name {
			return spec, true
		}
	}
	return searchFlagSpec{}, false
}

func runnerOwnedSearchFlagNames() map[string]struct{} {
	names := map[string]struct{}{}
	for _, spec := range searchFlagSpecs {
		names[spec.name] = struct{}{}
	}
	return names
}

func parseDateFlag(name, raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	parseDate := ckflags.Date
	if name == "--before" {
		parseDate = ckflags.ParseDateOrTimeThroughEndOfEnteredPrecision
	}
	ts, err := parseDate(raw)
	if err != nil {
		return time.Time{}, output.UsageError{Err: fmt.Errorf("%s %w", name, err)}
	}
	return ts, nil
}
