package main

import (
	"context"
	"fmt"
	"os"

	"github.com/opentrawl/opentrawl/calcrawl/internal/cli"
)

func main() {
	if err := cli.Run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if !cli.ErrorPrinted(err) {
			_, _ = fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(cli.ExitCode(err))
	}
}
