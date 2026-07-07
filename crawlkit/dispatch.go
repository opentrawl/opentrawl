package crawlkit

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"

	"github.com/openclaw/crawlkit/config"
	ckflags "github.com/openclaw/crawlkit/flags"
	"github.com/openclaw/crawlkit/output"
)

type globalOptions struct {
	json      bool
	version   bool
	help      bool
	verbosity int
	stateRoot string
	runID     string
	args      []string
}

type globalFlagKind int

const (
	globalFlagJSON globalFlagKind = iota
	globalFlagVerbose
	globalFlagVeryVerbose
	globalFlagVersion
	globalFlagHelp
	globalFlagStateRoot
	globalFlagRunID
)

type globalFlagSpec struct {
	tokens []string
	kind   globalFlagKind
}

var globalFlagSpecs = []globalFlagSpec{
	{tokens: []string{"--json"}, kind: globalFlagJSON},
	{tokens: []string{"-v", "--verbose"}, kind: globalFlagVerbose},
	{tokens: []string{"-vv"}, kind: globalFlagVeryVerbose},
	{tokens: []string{"--version"}, kind: globalFlagVersion},
	{tokens: []string{"-h", "--help", "-help"}, kind: globalFlagHelp},
	{tokens: []string{"--state-root"}, kind: globalFlagStateRoot},
	{tokens: []string{"--crawlkit-run-id"}, kind: globalFlagRunID},
}

type targetVerb struct {
	name      string
	tokens    []string
	args      []string
	mutates   bool
	timeout   time.Duration
	spine     *Verb
	bespoke   *Verb
	storeMode storeMode
}

type storeMode int

const (
	storeNone storeMode = iota
	storeOptional
	storeRead
	storeWrite
)

func parseGlobal(argv []string) (globalOptions, error) {
	var opts globalOptions
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		if arg == "--" {
			opts.args = append(opts.args, argv[i:]...)
			return opts, nil
		}
		name, value, inline := splitFlagValue(arg)
		spec, ok := globalFlagByToken(name)
		if !ok {
			opts.args = append(opts.args, arg)
			continue
		}
		switch spec.kind {
		case globalFlagJSON:
			if inline {
				opts.args = append(opts.args, arg)
				continue
			}
			opts.json = true
		case globalFlagVerbose:
			if inline {
				opts.args = append(opts.args, arg)
				continue
			}
			if opts.verbosity < 1 {
				opts.verbosity = 1
			}
		case globalFlagVeryVerbose:
			if inline {
				opts.args = append(opts.args, arg)
				continue
			}
			opts.verbosity = 2
		case globalFlagVersion:
			if inline {
				opts.args = append(opts.args, arg)
				continue
			}
			opts.version = true
		case globalFlagHelp:
			if inline {
				opts.args = append(opts.args, arg)
				continue
			}
			opts.help = true
		case globalFlagStateRoot:
			if !inline {
				i++
				if i >= len(argv) || strings.TrimSpace(argv[i]) == "" {
					return opts, usageError{err: errors.New("--state-root needs a path")}
				}
				value = argv[i]
			}
			opts.stateRoot = value
			if strings.TrimSpace(opts.stateRoot) == "" {
				return opts, usageError{err: errors.New("--state-root needs a path")}
			}
		case globalFlagRunID:
			if !inline {
				i++
				if i >= len(argv) || strings.TrimSpace(argv[i]) == "" {
					return opts, usageError{err: errors.New("--crawlkit-run-id needs a value")}
				}
				value = argv[i]
			}
			opts.runID = value
			if strings.TrimSpace(opts.runID) == "" {
				return opts, usageError{err: errors.New("--crawlkit-run-id needs a value")}
			}
		}
	}
	return opts, nil
}

func globalFlagByToken(token string) (globalFlagSpec, bool) {
	for _, spec := range globalFlagSpecs {
		for _, candidate := range spec.tokens {
			if token == candidate {
				return spec, true
			}
		}
	}
	return globalFlagSpec{}, false
}

func runnerOwnedGlobalFlagNames() map[string]struct{} {
	names := map[string]struct{}{}
	for _, spec := range globalFlagSpecs {
		for _, token := range spec.tokens {
			names[strings.TrimLeft(token, "-")] = struct{}{}
		}
	}
	return names
}

func selectSource(args []string, sources []Crawler) (Crawler, []string, error) {
	if len(sources) == 0 {
		return nil, nil, usageError{err: errors.New("no crawlers are registered")}
	}
	if len(sources) == 1 {
		source := sources[0]
		if len(args) > 0 && matchesSource(source.Info(), args[0]) {
			return source, args[1:], nil
		}
		return source, args, nil
	}
	if len(args) == 0 {
		return nil, nil, usageError{err: errors.New("source is required")}
	}
	var matches []Crawler
	for _, source := range sources {
		if matchesSource(source.Info(), args[0]) {
			matches = append(matches, source)
		}
	}
	switch len(matches) {
	case 0:
		return nil, nil, usageError{err: fmt.Errorf("unknown source %q", args[0])}
	case 1:
		return matches[0], args[1:], nil
	default:
		return nil, nil, usageError{err: fmt.Errorf("ambiguous source %q matches %s", args[0], sourceIDs(matches))}
	}
}

func matchesSource(info Info, token string) bool {
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}
	if token == info.ID || token == info.Surface {
		return true
	}
	for _, alias := range info.Aliases {
		if token == strings.TrimSpace(alias) {
			return true
		}
	}
	return false
}

func sourceIDs(sources []Crawler) string {
	ids := make([]string, 0, len(sources))
	for _, source := range sources {
		ids = append(ids, firstText(source.Info().ID, source.Info().Surface))
	}
	return strings.Join(ids, ", ")
}

func (r runner) dispatch(ctx context.Context, source Crawler, args []string, globals globalOptions, format output.Format, wireChild bool) executionResult {
	verb, err := resolveVerb(source, args)
	if err != nil {
		return executionResult{err: err}
	}
	if verb.mutates && !wireChild {
		return r.runChild(ctx, source, verb, globals, format)
	}
	return r.runInProcess(ctx, source, verb, globals, format, wireChild)
}

func resolveVerb(source Crawler, args []string) (targetVerb, error) {
	if len(args) == 0 {
		return targetVerb{}, usageError{err: errors.New("verb is required")}
	}
	name := args[0]
	rest := args[1:]
	if name == "contacts" && len(rest) > 0 && rest[0] == "export" {
		name = "contacts_export"
		rest = rest[1:]
	}
	switch name {
	case "metadata":
		spine, err := supportedSpineVerbDeclarations(source)
		if err != nil {
			return targetVerb{}, err
		}
		return targetVerb{name: name, args: rest, spine: spineDeclaration(spine, name), storeMode: storeNone}, nil
	case "status", "doctor":
		spine, err := supportedSpineVerbDeclarations(source)
		if err != nil {
			return targetVerb{}, err
		}
		return targetVerb{name: name, args: rest, spine: spineDeclaration(spine, name), storeMode: storeOptional}, nil
	case "sync":
		if _, ok := source.(Syncer); !ok {
			return targetVerb{}, usageError{err: errors.New("source does not support sync")}
		}
		spine, err := supportedSpineVerbDeclarations(source)
		if err != nil {
			return targetVerb{}, err
		}
		return targetVerb{name: name, args: rest, mutates: true, spine: spineDeclaration(spine, name), storeMode: storeWrite}, nil
	case "search":
		if _, ok := source.(Searcher); !ok {
			return targetVerb{}, usageError{err: errors.New("source does not support search")}
		}
		spine, err := supportedSpineVerbDeclarations(source)
		if err != nil {
			return targetVerb{}, err
		}
		return targetVerb{name: name, args: rest, spine: spineDeclaration(spine, name), storeMode: storeRead}, nil
	case "open":
		if _, ok := source.(Opener); !ok {
			return targetVerb{}, usageError{err: errors.New("source does not support open")}
		}
		spine, err := supportedSpineVerbDeclarations(source)
		if err != nil {
			return targetVerb{}, err
		}
		return targetVerb{name: name, args: rest, spine: spineDeclaration(spine, name), storeMode: storeRead}, nil
	case "who":
		if _, ok := source.(WhoMatcher); !ok {
			return targetVerb{}, usageError{err: errors.New("source does not support who")}
		}
		spine, err := supportedSpineVerbDeclarations(source)
		if err != nil {
			return targetVerb{}, err
		}
		return targetVerb{name: name, args: rest, spine: spineDeclaration(spine, name), storeMode: storeRead}, nil
	case "contacts_export":
		if _, ok := source.(ContactExporter); !ok {
			return targetVerb{}, usageError{err: errors.New("source does not support contacts export")}
		}
		spine, err := supportedSpineVerbDeclarations(source)
		if err != nil {
			return targetVerb{}, err
		}
		return targetVerb{name: name, args: rest, spine: spineDeclaration(spine, name), storeMode: storeRead}, nil
	}
	for _, verb := range source.Verbs() {
		if matched, verbRest := matchBespokeVerb(verb, args); matched {
			v := verb
			mode := storeRead
			if verb.Mutates {
				mode = storeWrite
			}
			return targetVerb{name: commandKey(verb.Name), tokens: strings.Fields(verb.Name), args: verbRest, mutates: verb.Mutates, timeout: verb.Timeout, bespoke: &v, storeMode: mode}, nil
		}
	}
	return targetVerb{}, usageError{err: fmt.Errorf("unknown verb %q", name)}
}

func (verb targetVerb) childArgs() []string {
	if len(verb.tokens) > 0 {
		return append([]string(nil), verb.tokens...)
	}
	if verb.name == "contacts_export" {
		return []string{"contacts", "export"}
	}
	return []string{verb.name}
}

func matchBespokeVerb(verb Verb, args []string) (bool, []string) {
	parts := strings.Fields(verb.Name)
	if len(parts) == 0 || len(args) < len(parts) {
		return false, nil
	}
	for i, part := range parts {
		if args[i] != part {
			return false, nil
		}
	}
	return true, append([]string(nil), args[len(parts):]...)
}

func parseBespokeFlags(verb Verb, args []string) ([]string, error) {
	fs := flag.NewFlagSet(verb.Name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if verb.Flags != nil {
		verb.Flags(fs)
	}
	flagArgs, positional, err := bespokeFlagArgs(fs, args)
	if err != nil {
		return nil, err
	}
	if err := fs.Parse(flagArgs); err != nil {
		return nil, output.UsageError{Err: err}
	}
	return append([]string(nil), positional...), nil
}

func bespokeFlagArgs(fs *flag.FlagSet, args []string) ([]string, []string, error) {
	var flagArgs []string
	var positional []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		name, value, inline := splitFlagValue(arg)
		flagName := strings.TrimLeft(name, "-")
		fl := fs.Lookup(flagName)
		if fl == nil {
			if strings.HasPrefix(arg, "-") {
				flagArgs = append(flagArgs, arg)
			} else {
				positional = append(positional, arg)
			}
			continue
		}
		if inline {
			flagArgs = append(flagArgs, name+"="+value)
			continue
		}
		flagArgs = append(flagArgs, name)
		if isBoolFlag(fl) {
			continue
		}
		i++
		if i >= len(args) {
			return nil, nil, output.UsageError{Err: fmt.Errorf("flag needs an argument: %s", name)}
		}
		flagArgs = append(flagArgs, args[i])
	}
	return flagArgs, positional, nil
}

func isBoolFlag(f *flag.Flag) bool {
	type boolFlag interface {
		IsBoolFlag() bool
	}
	value, ok := f.Value.(boolFlag)
	return ok && value.IsBoolFlag()
}

func loadConfig(info Info, stateRoot string) error {
	if info.Config == nil {
		return nil
	}
	rv := reflect.ValueOf(info.Config)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return ConfigFieldError{Field: "config", Fix: "pass a pointer to the crawler config struct"}
	}
	paths, err := resolveSourcePaths(stateRoot, info.ID)
	if err != nil {
		return err
	}
	exists, err := pathExists(paths.Config)
	if err != nil {
		return fmt.Errorf("stat config: %w", err)
	}
	if exists {
		if err := config.LoadTOML(paths.Config, info.Config); err != nil {
			return fmt.Errorf("load config: %w", err)
		}
	}
	if validator, ok := info.Config.(ConfigValidator); ok {
		if err := validator.Validate(); err != nil {
			return err
		}
	}
	return nil
}

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
	resolvedLimit, err := ckflags.Limit(*searchFlags.limit, limitSet, *searchFlags.all)
	if err != nil {
		return Query{}, output.UsageError{Err: err}
	}
	if whoSet && strings.TrimSpace(*searchFlags.who) == "" {
		return Query{}, output.UsageError{Err: errors.New("search --who requires an identity")}
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
		return Query{}, usageError{err: errors.New("search needs a query or filter")}
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
		if name == "--all" {
			if inline {
				flags = append(flags, name+"="+value)
			} else {
				flags = append(flags, name)
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
			return nil, nil, output.UsageError{Err: fmt.Errorf("flag needs an argument: %s", name)}
		}
		flags = append(flags, args[i])
	}
	return flags, positional, nil
}

func splitFlagValue(arg string) (name, value string, inline bool) {
	name, value, inline = strings.Cut(arg, "=")
	return name, value, inline
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
	{name: "all", usage: "return every result"},
	{name: "limit", usage: "maximum results"},
	{name: "after", usage: "only results at or after this date"},
	{name: "before", usage: "only results before this date"},
	{name: "who", usage: "only results involving this person"},
}

type searchFlagValues struct {
	all    *bool
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
		case "all":
			values.all = fs.Bool(spec.name, false, spec.usage)
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
	ts, err := ckflags.Date(raw)
	if err != nil {
		return time.Time{}, output.UsageError{Err: fmt.Errorf("%s: %w", name, err)}
	}
	if name == "--before" {
		if day, err := time.ParseInLocation("2006-01-02", raw, time.Local); err == nil {
			return day.Add(24*time.Hour - time.Second).UTC(), nil
		}
	}
	return ts, nil
}
