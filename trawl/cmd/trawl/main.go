package main

import (
	"fmt"
	"io"
	"os"

	"github.com/openclaw/crawlkit"
	"github.com/opentrawl/opentrawl/trawl/internal/cli"
)

var exit = os.Exit

func main() {
	exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == crawlkit.HiddenWireSubcommand {
		return cli.ExecuteCrawlerWire(args)
	}
	if err := cli.Execute(args, stdout, stderr); err != nil {
		if cli.ShouldPrintError(err) {
			_, _ = fmt.Fprintln(stderr, err)
		}
		return cli.ExitCode(err)
	}
	return 0
}
