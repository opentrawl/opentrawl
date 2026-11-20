package main

import (
	"os"

	"github.com/opentrawl/opentrawl/trawl/internal/cli"
)

var exit = os.Exit

func main() {
	exit(cli.RunCommandLine(os.Args))
}
