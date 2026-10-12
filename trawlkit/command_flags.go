package trawlkit

import (
	"flag"
	"io"
	"regexp"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit/output"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

func parseBespokeFlags(command TrawlerCommand, args []string) ([]string, error) {
	fs := flag.NewFlagSet(command.TrawlerCommandName, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if command.RegisterTrawlerCommandFlags != nil {
		command.RegisterTrawlerCommandFlags(fs)
	}
	flagArgs, positional, err := bespokeFlagArgs(fs, args)
	if err != nil {
		return nil, err
	}
	if err := fs.Parse(flagArgs); err != nil {
		return nil, output.UsageError{Err: commandFailureFromGoFlagParseError(err)}
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
			return nil, nil, output.UsageError{Err: render.OptionNeedsValue(name)}
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

func splitFlagValue(arg string) (name, value string, inline bool) {
	name, value, inline = strings.Cut(arg, "=")
	return name, value, inline
}

// commandFailureFromGoFlagParseError words the standard flag package's
// explanation of a bad option as one of the library's sentences.
func commandFailureFromGoFlagParseError(goFlagParseError error) render.CommandFailure {
	goFlagParseExplanation := goFlagParseError.Error()
	if optionName, found := strings.CutPrefix(goFlagParseExplanation, "flag provided but not defined: "); found {
		return render.UnknownOption(optionName)
	}
	if optionName, found := strings.CutPrefix(goFlagParseExplanation, "flag needs an argument: "); found {
		return render.OptionNeedsValue(optionName)
	}
	if invalidValueMatch := goFlagInvalidOptionValueExplanation.FindStringSubmatch(goFlagParseExplanation); invalidValueMatch != nil {
		optionName := invalidValueMatch[1]
		if optionName == "limit" {
			return render.LimitMustBeWholeNumber()
		}
		return render.OptionValueIsNotValid(optionName)
	}
	return render.CommandLineRejectedByParser(goFlagParseExplanation)
}

// The flag package writes `invalid value "x" for flag -limit: parse error`
// and `invalid boolean value "x" for -from-me: parse error`.
var goFlagInvalidOptionValueExplanation = regexp.MustCompile(`^invalid (?:boolean )?value .* for (?:flag )?-+([^\s:]+): `)
