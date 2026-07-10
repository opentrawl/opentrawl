package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

type stringList []string

type commandOptions struct {
	stderr    io.Writer
	verbosity int
}

func (s *stringList) String() string {
	return strings.Join(*s, ", ")
}

func (s *stringList) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func execute(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	var verbosity int
	var err error
	args, verbosity, err = parseGlobalFlags(args)
	if err != nil {
		return err
	}
	opts := commandOptions{stderr: stderr, verbosity: verbosity}
	if len(args) == 0 || isHelp(args[0]) {
		_, err := fmt.Fprint(stdout, rootHelp)
		if err != nil {
			return err
		}
		return helpShown{}
	}
	if args[0] == "help" {
		return showHelp(args[1:], stdout)
	}
	switch args[0] {
	case "ack":
		return runAck(args[1:], stdout, opts)
	case "comment":
		return runComment(args[1:], stdin, stdout, opts)
	case "inbox":
		return runInbox(args[1:], stdout, opts)
	case "issue":
		return runIssue(args[1:], stdout, opts)
	case "issues":
		return runIssues(args[1:], stdout, opts)
	case "mcp":
		if len(args) > 1 {
			if isHelp(args[1]) {
				_, err := fmt.Fprint(stdout, mcpHelp)
				if err != nil {
					return err
				}
				return helpShown{}
			}
			return usageError{message: "linear mcp does not take arguments\n\nRun `linear mcp --help`."}
		}
		return runMCP(stdin, stdout, stderr)
	default:
		return usageError{message: fmt.Sprintf("unknown command %q\n\nRun `linear --help`.", args[0])}
	}
}

func runInbox(args []string, stdout io.Writer, opts commandOptions) error {
	fs := newFlagSet("linear inbox")
	team := fs.String("team", "", "Linear team key")
	since := fs.String("since", "", "Duration window")
	all := fs.Bool("all", false, "List all comments")
	positionals, err := parseFlags(args, fs)
	if err != nil {
		return helpOrUsage(err, stdout, inboxHelp)
	}
	if len(positionals) > 0 {
		return usageError{message: "linear inbox does not take positional arguments\n\nRun `linear inbox --help`."}
	}
	inboxOpts := InboxOptions{Team: *team, Since: *since, All: *all}
	if _, err := parseInboxWindow(inboxOpts); err != nil {
		return usageError{message: err.Error()}
	}
	api, err := NewLinearAPI(opts.stderr, opts.verbosity)
	if err != nil {
		return err
	}
	defer func() {
		_ = api.Close()
	}()
	result, err := api.ListInbox(context.Background(), inboxOpts)
	if err != nil {
		return err
	}
	return RenderInbox(stdout, result)
}

func runAck(args []string, stdout io.Writer, opts commandOptions) error {
	if len(args) == 0 || isHelp(args[0]) {
		_, err := fmt.Fprint(stdout, ackHelp)
		if err != nil {
			return err
		}
		return helpShown{}
	}
	if strings.HasPrefix(args[0], "-") {
		return usageError{message: "linear ack needs a comment id\n\nRun `linear ack --help`."}
	}
	if len(args) > 1 {
		return usageError{message: "linear ack takes one comment id\n\nRun `linear ack --help`."}
	}
	api, err := NewLinearAPI(opts.stderr, opts.verbosity)
	if err != nil {
		return err
	}
	defer func() {
		_ = api.Close()
	}()
	result, err := api.AckComment(context.Background(), args[0])
	if err != nil {
		return err
	}
	return RenderAck(stdout, result)
}

func runComment(args []string, stdin io.Reader, stdout io.Writer, opts commandOptions) error {
	fs := newFlagSet("linear comment")
	actor := fs.String("as", "", "Display name to use in Linear")
	positionals, err := parseFlags(args, fs)
	if err != nil {
		return helpOrUsage(err, stdout, commentHelp)
	}
	if len(positionals) < 1 {
		return usageError{message: "linear comment needs an issue identifier\n\nRun `linear comment --help`."}
	}
	if strings.TrimSpace(*actor) == "" {
		return usageError{message: "--as is required for write commands"}
	}
	body := ""
	if len(positionals) > 1 {
		body = strings.Join(positionals[1:], " ")
	} else {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return fmt.Errorf("read comment body from stdin: %w", err)
		}
		body = string(data)
	}
	if strings.TrimSpace(body) == "" {
		return usageError{message: "comment body is required"}
	}
	api, err := NewLinearAPI(opts.stderr, opts.verbosity)
	if err != nil {
		return err
	}
	defer func() {
		_ = api.Close()
	}()
	created, err := api.CreateComment(context.Background(), positionals[0], *actor, body)
	if err != nil {
		return err
	}
	return RenderCreatedComment(stdout, created)
}

func runIssue(args []string, stdout io.Writer, opts commandOptions) error {
	if len(args) == 0 || isHelp(args[0]) {
		_, err := fmt.Fprint(stdout, issueHelp)
		if err != nil {
			return err
		}
		return helpShown{}
	}
	if args[0] == "new" {
		return runIssueNew(args[1:], stdout, opts)
	}
	if args[0] == "state" {
		return runIssueState(args[1:], stdout, opts)
	}
	if args[0] == "update" {
		return runIssueUpdate(args[1:], stdout, opts)
	}
	if strings.HasPrefix(args[0], "-") {
		return usageError{message: "linear issue needs an issue identifier\n\nRun `linear issue --help`."}
	}
	if len(args) > 1 {
		return usageError{message: "linear issue takes one issue identifier\n\nRun `linear issue --help`."}
	}
	api, err := NewLinearAPI(opts.stderr, opts.verbosity)
	if err != nil {
		return err
	}
	defer func() {
		_ = api.Close()
	}()
	issue, err := api.GetIssue(context.Background(), args[0])
	if err != nil {
		return err
	}
	return RenderIssue(stdout, issue)
}

func runIssueNew(args []string, stdout io.Writer, opts commandOptions) error {
	fs := newFlagSet("linear issue new")
	team := fs.String("team", "", "Linear team key")
	title := fs.String("title", "", "Issue title")
	actor := fs.String("as", "", "Display name to use in Linear")
	description := fs.String("description", "", "Issue description")
	var labels stringList
	fs.Var(&labels, "label", "Label name; repeat for more labels")
	positionals, err := parseFlags(args, fs)
	if err != nil {
		return helpOrUsage(err, stdout, issueNewHelp)
	}
	if len(positionals) > 0 {
		return usageError{message: "linear issue new does not take positional arguments\n\nRun `linear issue new --help`."}
	}
	if strings.TrimSpace(*team) == "" {
		return usageError{message: "--team is required"}
	}
	if strings.TrimSpace(*title) == "" {
		return usageError{message: "--title is required"}
	}
	if strings.TrimSpace(*actor) == "" {
		return usageError{message: "--as is required for write commands"}
	}
	api, err := NewLinearAPI(opts.stderr, opts.verbosity)
	if err != nil {
		return err
	}
	defer func() {
		_ = api.Close()
	}()
	created, err := api.CreateIssue(context.Background(), *team, *title, *actor, *description, labels)
	if err != nil {
		return err
	}
	return RenderCreatedIssue(stdout, created)
}

func runIssueState(args []string, stdout io.Writer, opts commandOptions) error {
	fs := newFlagSet("linear issue state")
	state := fs.String("state", "", "Workflow state name")
	actor := fs.String("as", "", "Display name to use in Linear")
	positionals, err := parseFlags(args, fs)
	if err != nil {
		return helpOrUsage(err, stdout, issueStateHelp)
	}
	if len(positionals) < 1 {
		return usageError{message: "linear issue state needs an issue identifier\n\nRun `linear issue state --help`."}
	}
	if len(positionals) > 1 {
		return usageError{message: "linear issue state takes one issue identifier\n\nRun `linear issue state --help`."}
	}
	if strings.TrimSpace(*state) == "" {
		return usageError{message: "--state is required"}
	}
	if strings.TrimSpace(*actor) == "" {
		return usageError{message: "--as is required for write commands"}
	}
	api, err := NewLinearAPI(opts.stderr, opts.verbosity)
	if err != nil {
		return err
	}
	defer func() {
		_ = api.Close()
	}()
	updated, err := api.UpdateIssueState(context.Background(), positionals[0], *state, *actor)
	if err != nil {
		return err
	}
	return RenderUpdatedIssue(stdout, updated)
}

func runIssueUpdate(args []string, stdout io.Writer, opts commandOptions) error {
	fs := newFlagSet("linear issue update")
	actor := fs.String("as", "", "Actor name to record in the request log")
	descriptionFile := fs.String("description-file", "", "File containing the replacement issue description")
	priority := fs.String("priority", "", "Replacement issue priority")
	project := fs.String("project", "", "Replacement issue project")
	positionals, err := parseFlags(args, fs)
	if err != nil {
		return helpOrUsage(err, stdout, issueUpdateHelp)
	}
	if len(positionals) < 1 {
		return usageError{message: "linear issue update needs an issue identifier\n\nRun `linear issue update --help`."}
	}
	if len(positionals) > 1 {
		return usageError{message: "linear issue update takes one issue identifier\n\nRun `linear issue update --help`."}
	}
	if strings.TrimSpace(*actor) == "" {
		return usageError{message: "--as is required for write commands"}
	}
	options := IssueUpdateOptions{
		Priority: setStringFlag(fs, "priority", *priority),
		Project:  setStringFlag(fs, "project", *project),
	}
	if path := setStringFlag(fs, "description-file", *descriptionFile); path != nil {
		if strings.TrimSpace(*path) == "" {
			return usageError{message: "--description-file needs a path"}
		}
		data, err := os.ReadFile(*path)
		if err != nil {
			return fmt.Errorf("read issue description: %w", err)
		}
		description := string(data)
		options.Description = &description
	}
	if options.empty() {
		return usageError{message: "set at least one of --description-file, --priority or --project"}
	}
	api, err := NewLinearAPI(opts.stderr, opts.verbosity)
	if err != nil {
		return err
	}
	defer func() {
		_ = api.Close()
	}()
	updated, err := api.UpdateIssue(context.Background(), positionals[0], *actor, options)
	if err != nil {
		return err
	}
	return RenderUpdatedIssue(stdout, updated)
}

func runIssues(args []string, stdout io.Writer, opts commandOptions) error {
	fs := newFlagSet("linear issues")
	team := fs.String("team", "", "Linear team key")
	state := fs.String("state", "", "State name")
	positionals, err := parseFlags(args, fs)
	if err != nil {
		return helpOrUsage(err, stdout, issuesHelp)
	}
	if len(positionals) > 0 {
		return usageError{message: "linear issues does not take positional arguments\n\nRun `linear issues --help`."}
	}
	api, err := NewLinearAPI(opts.stderr, opts.verbosity)
	if err != nil {
		return err
	}
	defer func() {
		_ = api.Close()
	}()
	result, err := api.ListIssues(context.Background(), *team, *state)
	if err != nil {
		return err
	}
	return RenderIssues(stdout, result)
}

func showHelp(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		_, err := fmt.Fprint(stdout, rootHelp)
		return err
	}
	text := map[string]string{
		"ack":          ackHelp,
		"comment":      commentHelp,
		"inbox":        inboxHelp,
		"issue":        issueHelp,
		"issue new":    issueNewHelp,
		"issue state":  issueStateHelp,
		"issue update": issueUpdateHelp,
		"issues":       issuesHelp,
		"mcp":          mcpHelp,
	}
	key := strings.Join(args, " ")
	if help, ok := text[key]; ok {
		_, err := fmt.Fprint(stdout, help)
		return err
	}
	return usageError{message: fmt.Sprintf("unknown help topic %q", key)}
}

func setStringFlag(fs *flag.FlagSet, name, value string) *string {
	set := false
	fs.Visit(func(flag *flag.Flag) {
		if flag.Name == name {
			set = true
		}
	})
	if !set {
		return nil
	}
	return &value
}

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func parseGlobalFlags(args []string) ([]string, int, error) {
	verbosity := 0
	for len(args) > 0 {
		switch args[0] {
		case "-v":
			if verbosity < 1 {
				verbosity = 1
			}
			args = args[1:]
		case "-vv":
			verbosity = 2
			args = args[1:]
		default:
			return args, verbosity, nil
		}
	}
	return args, verbosity, nil
}

func parseFlags(args []string, fs *flag.FlagSet) ([]string, error) {
	var positionals []string
	for len(args) > 0 {
		arg := args[0]
		if arg == "--" {
			positionals = append(positionals, args[1:]...)
			break
		}
		if !isFlagToken(arg) {
			positionals = append(positionals, arg)
			args = args[1:]
			continue
		}
		if isHelp(arg) {
			return nil, flag.ErrHelp
		}
		before := len(args)
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == before {
			return nil, fmt.Errorf("could not parse flag %s", arg)
		}
	}
	return positionals, nil
}

func isFlagToken(arg string) bool {
	return strings.HasPrefix(arg, "-") && arg != "-"
}

func helpOrUsage(err error, stdout io.Writer, help string) error {
	if err == flag.ErrHelp {
		_, printErr := fmt.Fprint(stdout, help)
		if printErr != nil {
			return printErr
		}
		return helpShown{}
	}
	return usageError{message: err.Error()}
}

func isHelp(arg string) bool {
	return arg == "-h" || arg == "--help"
}

func renderString(fn func(io.Writer) error) (string, error) {
	var buf bytes.Buffer
	if err := fn(&buf); err != nil {
		return "", err
	}
	return buf.String(), nil
}
