package main

import (
	"fmt"
	"io"
	"os"
)

var exit = os.Exit

func main() {
	exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	err := execute(args, stdin, stdout, stderr)
	if err != nil && shouldPrintError(err) {
		_, _ = fmt.Fprintln(stderr, err)
	}
	return exitCode(err)
}
