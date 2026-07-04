package main

import (
	"context"
	"fmt"
	"os"

	"github.com/openclaw/crawlkit/output"
	"github.com/opentrawl/opentrawl/gogcrawl/internal/cli"
)

func main() {
	if err := cli.Run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if !output.IsRendered(err) {
			_, _ = fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(cli.ExitCode(err))
	}
}
