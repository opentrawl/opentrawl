package main

import (
	"fmt"
	"io"
	"os"

	"github.com/openclaw/clawdex/internal/cli"
	ckoutput "github.com/openclaw/crawlkit/output"
)

var exit = os.Exit

func main() {
	exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if err := cli.Execute(args, stdout, stderr); err != nil {
		// A rendered error already wrote the JSON envelope to stdout; only
		// plain-text errors go to stderr.
		if !ckoutput.IsRendered(err) {
			_, _ = fmt.Fprintln(stderr, err)
		}
		return cli.ExitCode(err)
	}
	return 0
}
