package main

import (
	"context"
	"fmt"
	"io"
	"strings"
)

func runIssueLabel(args []string, stdout io.Writer, opts commandOptions) error {
	if len(args) == 0 || isHelp(args[0]) {
		_, err := fmt.Fprint(stdout, issueLabelHelp)
		return err
	}
	operation := args[0]
	if operation != "add" && operation != "remove" {
		return usageError{message: "linear issue label needs add or remove\n\nRun `linear issue label --help`."}
	}
	fs := newFlagSet("linear issue label " + operation)
	actor := fs.String("as", "", "Actor name to record in the request log")
	var labels stringList
	fs.Var(&labels, "label", "Label name; repeat for more labels")
	positionals, err := parseFlags(args[1:], fs)
	if err != nil {
		return helpOrUsage(err, stdout, issueLabelHelp)
	}
	if len(positionals) != 1 {
		return usageError{message: "linear issue label needs one issue identifier\n\nRun `linear issue label --help`."}
	}
	if strings.TrimSpace(*actor) == "" {
		return usageError{message: "--as is required for write commands"}
	}
	if len(cleanLabelNames(labels)) == 0 {
		return usageError{message: "--label is required"}
	}
	api, err := newLinearWriteAPI(opts.stderr, opts.verbosity)
	if err != nil {
		return err
	}
	defer func() { _ = api.Close() }()
	updated, err := api.ChangeIssueLabels(context.Background(), positionals[0], *actor, operation, labels)
	if err != nil {
		return err
	}
	return RenderUpdatedIssue(stdout, updated)
}

func runIssueRelation(args []string, stdout io.Writer, opts commandOptions) error {
	if len(args) == 0 || isHelp(args[0]) {
		_, err := fmt.Fprint(stdout, issueRelationHelp)
		return err
	}
	operation := args[0]
	if operation != "add" && operation != "remove" {
		return usageError{message: "linear issue relation needs add or remove\n\nRun `linear issue relation --help`."}
	}
	fs := newFlagSet("linear issue relation " + operation)
	actor := fs.String("as", "", "Actor name to record in the request log")
	blocks := fs.String("blocks", "", "Issue that this issue blocks")
	blockedBy := fs.String("blocked-by", "", "Issue that blocks the named issue")
	positionals, err := parseFlags(args[1:], fs)
	if err != nil {
		return helpOrUsage(err, stdout, issueRelationHelp)
	}
	if len(positionals) != 1 {
		return usageError{message: "linear issue relation needs one issue identifier\n\nRun `linear issue relation --help`."}
	}
	if strings.TrimSpace(*actor) == "" {
		return usageError{message: "--as is required for write commands"}
	}
	hasBlocks := setStringFlag(fs, "blocks", *blocks) != nil
	hasBlockedBy := setStringFlag(fs, "blocked-by", *blockedBy) != nil
	if hasBlocks == hasBlockedBy {
		return usageError{message: "set exactly one of --blocks or --blocked-by"}
	}
	other := *blocks
	direction := RelationBlocks
	if hasBlockedBy {
		other = *blockedBy
		direction = RelationBlockedBy
	}
	if strings.TrimSpace(other) == "" {
		return usageError{message: "relation issue identifier needs a value"}
	}
	api, err := newLinearWriteAPI(opts.stderr, opts.verbosity)
	if err != nil {
		return err
	}
	defer func() { _ = api.Close() }()
	updated, err := api.ChangeIssueRelation(context.Background(), positionals[0], other, *actor, operation, direction)
	if err != nil {
		return err
	}
	return RenderUpdatedIssue(stdout, updated)
}
