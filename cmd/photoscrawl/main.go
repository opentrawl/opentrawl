package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/joshp123/photoscrawl/internal/archive"
	"github.com/openclaw/crawlkit/output"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		if output.IsUsage(err) {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usage()
	}
	paths := archive.PathsFromEnv()
	switch args[0] {
	case "init":
		fs := flag.NewFlagSet("init", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		dbPath := fs.String("db", "", "photos.sqlite path")
		jsonFlag := fs.Bool("json", false, "write JSON")
		formatFlag := fs.String("format", "", "output format")
		if err := fs.Parse(args[1:]); err != nil {
			return output.UsageError{Err: err}
		}
		if *dbPath != "" {
			paths.Database = *dbPath
		}
		format, err := output.Resolve(*formatFlag, *jsonFlag)
		if err != nil {
			return err
		}
		result, err := archive.Init(ctx, paths)
		if err != nil {
			return err
		}
		return output.Write(os.Stdout, format, "init", result)
	case "status":
		fs := flag.NewFlagSet("status", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		dbPath := fs.String("db", "", "photos.sqlite path")
		jsonFlag := fs.Bool("json", false, "write JSON")
		formatFlag := fs.String("format", "", "output format")
		if err := fs.Parse(args[1:]); err != nil {
			return output.UsageError{Err: err}
		}
		if *dbPath != "" {
			paths.Database = *dbPath
		}
		format, err := output.Resolve(*formatFlag, *jsonFlag)
		if err != nil {
			return err
		}
		status, err := archive.Status(ctx, paths)
		if err != nil {
			return err
		}
		return output.Write(os.Stdout, format, "status", status)
	case "crawl", "classify", "search", "open", "neighbors", "evidence":
		return output.UsageError{Err: fmt.Errorf("%s is planned but not implemented in the seed", args[0])}
	default:
		return usage()
	}
}

func usage() error {
	return output.UsageError{Err: errors.New("usage: photoscrawl <init|status|crawl|classify|search|open|neighbors|evidence>")}
}
