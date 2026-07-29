package trawlkit

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit/output"
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
			return nil, nil, output.UsageError{Err: fmt.Errorf("%s needs a value.", name)}
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
