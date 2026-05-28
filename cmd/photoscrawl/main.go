package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/joshp123/photoscrawl/internal/archive"
	"github.com/joshp123/photoscrawl/internal/photos"
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
	case "crawl":
		fs := flag.NewFlagSet("crawl", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		dbPath := fs.String("db", "", "photos.sqlite path")
		libraryPath := fs.String("library", "", "Photos Library.photoslibrary path")
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
		result, err := archive.Crawl(ctx, paths, archive.CrawlOptions{
			LibraryPath: *libraryPath,
			Provider:    photos.NewProvider(),
		})
		if err != nil {
			return err
		}
		return output.Write(os.Stdout, format, "crawl", result)
	case "classify":
		fs := flag.NewFlagSet("classify", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		dbPath := fs.String("db", "", "photos.sqlite path")
		all := fs.Bool("all", false, "classify all pending assets")
		limit := fs.Int("limit", 100, "max pending assets to classify")
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
		result, err := archive.Classify(ctx, paths, archive.ClassifyOptions{All: *all, Limit: *limit})
		if err != nil {
			return err
		}
		return output.Write(os.Stdout, format, "classify", result)
	case "search":
		fs := flag.NewFlagSet("search", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		dbPath := fs.String("db", "", "photos.sqlite path")
		query := fs.String("query", "", "search query")
		limit := fs.Int("limit", 20, "max results")
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
		result, err := archive.Search(ctx, paths, archive.SearchOptions{Query: *query, Limit: *limit})
		if err != nil {
			return err
		}
		return output.Write(os.Stdout, format, "search", result)
	case "open":
		fs := flag.NewFlagSet("open", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		dbPath := fs.String("db", "", "photos.sqlite path")
		id := fs.String("id", "", "asset id")
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
		result, err := archive.Open(ctx, paths, *id)
		if err != nil {
			return err
		}
		return output.Write(os.Stdout, format, "open", result)
	case "evidence":
		fs := flag.NewFlagSet("evidence", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		dbPath := fs.String("db", "", "photos.sqlite path")
		rowID := fs.String("row-id", "", "row id")
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
		result, err := archive.Evidence(ctx, paths, *rowID)
		if err != nil {
			return err
		}
		return output.Write(os.Stdout, format, "evidence", result)
	case "neighbors":
		return output.UsageError{Err: fmt.Errorf("%s is planned but not implemented in this POC slice", args[0])}
	default:
		return usage()
	}
}

func usage() error {
	return output.UsageError{Err: errors.New("usage: photoscrawl <init|status|crawl|classify|search|open|neighbors|evidence>")}
}
