package main

import (
	"fmt"
	"io"
	"os"

	"github.com/opentrawl/opentrawl/trawl/internal/cli"
	"github.com/opentrawl/opentrawl/trawlkit"
)

var exit = os.Exit

func main() {
	exit(runWithTrawlInvocationDisplay(
		os.Args[1:],
		os.Stdout,
		os.Stderr,
		stableTrawlInvocationDisplay(os.Args[0]),
	))
}

func run(args []string, stdout, stderr io.Writer) int {
	return runWithTrawlInvocationDisplay(args, stdout, stderr, "./trawl")
}

func runWithTrawlInvocationDisplay(args []string, stdout, stderr io.Writer, trawlInvocationDisplay string) int {
	if len(args) > 0 && args[0] == trawlkit.HiddenWireSubcommand {
		return cli.ExecuteTrawlerWire(args)
	}
	if err := cli.ExecuteWithTrawlInvocationDisplay(args, stdout, stderr, trawlInvocationDisplay); err != nil {
		if cli.ShouldPrintError(err) {
			_, _ = fmt.Fprintln(stderr, err)
		}
		return cli.ExitCode(err)
	}
	return 0
}

func stableTrawlInvocationDisplay(argumentZero string) string {
	if argumentZero == "./trawl" {
		return "./trawl"
	}
	return "trawl"
}
