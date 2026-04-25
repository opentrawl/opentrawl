package main

import (
	"context"
	"fmt"
	"os"

	"github.com/steipete/wacrawl/internal/cli"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if err := cli.Run(context.Background(), args, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return cli.ExitCode(err)
	}
	return 0
}
