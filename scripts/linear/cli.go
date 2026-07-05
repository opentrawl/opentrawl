package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
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
	case "comment":
		return runComment(args[1:], stdin, stdout, opts)
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
		"comment":   commentHelp,
		"issue":     issueHelp,
		"issue new": issueNewHelp,
		"issues":    issuesHelp,
		"mcp":       mcpHelp,
	}
	key := strings.Join(args, " ")
	if help, ok := text[key]; ok {
		_, err := fmt.Fprint(stdout, help)
		return err
	}
	return usageError{message: fmt.Sprintf("unknown help topic %q", key)}
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
