package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/alecthomas/kong"
	"github.com/opentrawl/opentrawl/conformance/internal/harness"
)

var Version = "dev"

type CLI struct {
	JSON        bool             `name:"json" help:"Write JSON report to stdout"`
	VersionFlag kong.VersionFlag `name:"version" help:"Print version and exit"`

	Crawler string `arg:"" name:"crawler" help:"Path to crawler binary"`
}

func Execute(args []string, stdout, stderr io.Writer) error {
	var root CLI
	parser, err := kong.New(&root,
		kong.Name("conformance"),
		kong.Description("Check a crawler binary against the OpenTrawl control contract."),
		kong.UsageOnError(),
		kong.Writers(stdout, stderr),
		kong.Vars{"version": Version},
	)
	if err != nil {
		return err
	}
	if _, err := parser.Parse(args); err != nil {
		return usageErr{err}
	}
	report := harness.Run(context.Background(), root.Crawler)
	if root.JSON {
		if err := writeJSON(stdout, report); err != nil {
			return err
		}
	} else if err := harness.WriteTable(stdout, report); err != nil {
		return err
	}
	if report.HasFailures() {
		return exitErr{code: 1}
	}
	return nil
}

func writeJSON(w io.Writer, report harness.Report) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exit exitErr
	if errors.As(err, &exit) {
		return exit.code
	}
	var usage usageErr
	if errors.As(err, &usage) {
		return 2
	}
	return 1
}

func ShouldPrintError(err error) bool {
	var exit exitErr
	return err != nil && !errors.As(err, &exit)
}

type exitErr struct {
	code int
}

func (e exitErr) Error() string {
	return fmt.Sprintf("exit %d", e.code)
}

type usageErr struct {
	error
}
