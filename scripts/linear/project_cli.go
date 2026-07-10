package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
)

func runProject(args []string, stdout io.Writer, opts commandOptions) error {
	if len(args) == 0 || isHelp(args[0]) {
		_, err := fmt.Fprint(stdout, projectHelp)
		if err != nil {
			return err
		}
		return helpShown{}
	}
	if args[0] == "update" {
		return runProjectUpdate(args[1:], stdout, opts)
	}
	if args[0] == "milestone" {
		return runProjectMilestone(args[1:], stdout, opts)
	}
	if strings.HasPrefix(args[0], "-") {
		return usageError{message: "linear project needs a project name or slug\n\nRun `linear project --help`."}
	}
	if len(args) > 1 {
		return usageError{message: "linear project takes one project name or slug\n\nRun `linear project --help`."}
	}
	api, err := newLinearAPI(opts.stderr, opts.verbosity)
	if err != nil {
		return err
	}
	defer func() { _ = api.Close() }()
	project, err := api.GetProject(context.Background(), args[0])
	if err != nil {
		return err
	}
	return RenderProject(stdout, project)
}

func runProjectUpdate(args []string, stdout io.Writer, opts commandOptions) error {
	fs := newFlagSet("linear project update")
	actor := fs.String("as", "", "Actor name to record in the request log")
	summary := fs.String("summary", "", "Replacement project summary")
	descriptionFile := fs.String("description-file", "", "File containing the replacement project description")
	status := fs.String("status", "", "Replacement project status")
	priority := fs.String("priority", "", "Replacement project priority")
	positionals, err := parseFlags(args, fs)
	if err != nil {
		return helpOrUsage(err, stdout, projectUpdateHelp)
	}
	if len(positionals) < 1 {
		return usageError{message: "linear project update needs a project name or slug\n\nRun `linear project update --help`."}
	}
	if len(positionals) > 1 {
		return usageError{message: "linear project update takes one project name or slug\n\nRun `linear project update --help`."}
	}
	if strings.TrimSpace(*actor) == "" {
		return usageError{message: "--as is required for write commands"}
	}
	options := ProjectUpdateOptions{Summary: setStringFlag(fs, "summary", *summary), Status: setStringFlag(fs, "status", *status), Priority: setStringFlag(fs, "priority", *priority)}
	if path := setStringFlag(fs, "description-file", *descriptionFile); path != nil {
		if strings.TrimSpace(*path) == "" {
			return usageError{message: "--description-file needs a path"}
		}
		data, err := os.ReadFile(*path)
		if err != nil {
			return fmt.Errorf("read project description: %w", err)
		}
		description := string(data)
		options.Description = &description
	}
	if options.empty() {
		return usageError{message: "set at least one of --summary, --description-file, --status or --priority"}
	}
	api, err := newLinearAPI(opts.stderr, opts.verbosity)
	if err != nil {
		return err
	}
	defer func() { _ = api.Close() }()
	project, err := api.UpdateProject(context.Background(), positionals[0], *actor, options)
	if err != nil {
		return err
	}
	return RenderProject(stdout, project)
}

func runProjectMilestone(args []string, stdout io.Writer, opts commandOptions) error {
	if len(args) == 0 || isHelp(args[0]) {
		_, err := fmt.Fprint(stdout, projectMilestoneHelp)
		if err != nil {
			return err
		}
		return helpShown{}
	}
	if args[0] != "ensure" {
		return usageError{message: "linear project milestone supports ensure\n\nRun `linear project milestone --help`."}
	}
	fs := newFlagSet("linear project milestone ensure")
	actor := fs.String("as", "", "Actor name to record in the request log")
	name := fs.String("name", "", "Milestone name")
	descriptionFile := fs.String("description-file", "", "File containing the replacement milestone description")
	positionals, err := parseFlags(args[1:], fs)
	if err != nil {
		return helpOrUsage(err, stdout, projectMilestoneEnsureHelp)
	}
	if len(positionals) < 1 {
		return usageError{message: "linear project milestone ensure needs a project name or slug\n\nRun `linear project milestone ensure --help`."}
	}
	if len(positionals) > 1 {
		return usageError{message: "linear project milestone ensure takes one project name or slug\n\nRun `linear project milestone ensure --help`."}
	}
	if strings.TrimSpace(*actor) == "" {
		return usageError{message: "--as is required for write commands"}
	}
	if strings.TrimSpace(*name) == "" {
		return usageError{message: "--name is required"}
	}
	options := ProjectMilestoneOptions{Name: *name}
	if path := setStringFlag(fs, "description-file", *descriptionFile); path != nil {
		if strings.TrimSpace(*path) == "" {
			return usageError{message: "--description-file needs a path"}
		}
		data, err := os.ReadFile(*path)
		if err != nil {
			return fmt.Errorf("read milestone description: %w", err)
		}
		description := string(data)
		options.Description = &description
	}
	api, err := newLinearAPI(opts.stderr, opts.verbosity)
	if err != nil {
		return err
	}
	defer func() { _ = api.Close() }()
	result, err := api.EnsureProjectMilestone(context.Background(), positionals[0], *actor, options)
	if err != nil {
		return err
	}
	return RenderEnsuredProjectMilestone(stdout, result)
}
