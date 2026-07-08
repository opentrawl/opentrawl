package crawlkit

import "strings"

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
}

func parseGlobal(argv []string) (globalOptions, error) {
	var opts globalOptions
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		if arg == "--" {
			opts.args = append(opts.args, argv[i:]...)
			return opts, nil
		}
		name, _, inline := splitFlagValue(arg)
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
