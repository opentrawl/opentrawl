package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
)

func runInitiative(args []string, stdout io.Writer, opts commandOptions) error {
	if len(args) == 0 || isHelp(args[0]) {
		_, err := fmt.Fprint(stdout, initiativeHelp)
		if err != nil {
			return err
		}
		return helpShown{}
	}
	if args[0] == "update" {
		return runInitiativeUpdate(args[1:], stdout, opts)
	}
	if strings.HasPrefix(args[0], "-") {
		return usageError{message: "linear initiative needs an initiative name or id\n\nRun `linear initiative --help`."}
	}
	if len(args) > 1 {
		return usageError{message: "linear initiative takes one initiative name or id\n\nRun `linear initiative --help`."}
	}
	api, err := newLinearAPI(opts.stderr, opts.verbosity)
	if err != nil {
		return err
	}
	defer func() { _ = api.Close() }()
	initiative, err := api.GetInitiative(context.Background(), args[0])
	if err != nil {
		return err
	}
	return RenderInitiative(stdout, initiative)
}

func runInitiativeUpdate(args []string, stdout io.Writer, opts commandOptions) error {
	fs := newFlagSet("linear initiative update")
	actor := fs.String("as", "", "Actor name to record in the request log")
	summary := fs.String("summary", "", "Replacement initiative summary")
	descriptionFile := fs.String("description-file", "", "File containing the replacement initiative description")
	positionals, err := parseFlags(args, fs)
	if err != nil {
		return helpOrUsage(err, stdout, initiativeUpdateHelp)
	}
	if len(positionals) < 1 {
		return usageError{message: "linear initiative update needs an initiative name or id\n\nRun `linear initiative update --help`."}
	}
	if len(positionals) > 1 {
		return usageError{message: "linear initiative update takes one initiative name or id\n\nRun `linear initiative update --help`."}
	}
	if strings.TrimSpace(*actor) == "" {
		return usageError{message: "--as is required for write commands"}
	}
	options := InitiativeUpdateOptions{Summary: setStringFlag(fs, "summary", *summary)}
	if path := setStringFlag(fs, "description-file", *descriptionFile); path != nil {
		if strings.TrimSpace(*path) == "" {
			return usageError{message: "--description-file needs a path"}
		}
		data, err := os.ReadFile(*path)
		if err != nil {
			return fmt.Errorf("read initiative description: %w", err)
		}
		description := string(data)
		options.Description = &description
	}
	if options.Summary == nil && options.Description == nil {
		return usageError{message: "set at least one of --summary or --description-file"}
	}
	api, err := newLinearWriteAPI(opts.stderr, opts.verbosity)
	if err != nil {
		return err
	}
	defer func() { _ = api.Close() }()
	initiative, err := api.UpdateInitiative(context.Background(), positionals[0], *actor, options)
	if err != nil {
		return err
	}
	return RenderInitiative(stdout, initiative)
}
