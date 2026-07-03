package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/openclaw/crawlkit/output"
	"github.com/openclaw/photoscrawl/internal/archive"
	"github.com/openclaw/photoscrawl/internal/photos"
)

const (
	ollamaCloudBaseURL = "https://ollama.com/api"
	ollamaAPIKeyEnv    = "OLLAMA_API_KEY"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		if wantsJSON(os.Args[1:]) {
			if writeErr := writeError(os.Stdout, err); writeErr != nil {
				fmt.Fprintln(os.Stderr, writeErr)
			}
		} else {
			fmt.Fprintln(os.Stderr, humanError(err))
		}
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usage()
	}
	paths, err := archive.DefaultPaths()
	if err != nil {
		return err
	}
	switch args[0] {
	case "metadata":
		fs := flag.NewFlagSet("metadata", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		jsonFlag := fs.Bool("json", false, "write JSON")
		formatFlag := fs.String("format", "", "output format")
		if err := fs.Parse(args[1:]); err != nil {
			return output.UsageError{Err: err}
		}
		if fs.NArg() != 0 {
			return output.UsageError{Err: errors.New("metadata takes flags only")}
		}
		format, err := output.Resolve(*formatFlag, *jsonFlag)
		if err != nil {
			return err
		}
		return writeMetadata(os.Stdout, format, archive.ControlManifest(paths))
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
		return writeStatus(os.Stdout, format, status)
	case "doctor":
		fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		dbPath := fs.String("db", "", "photos.sqlite path")
		libraryPath := fs.String("library", "", "Photos Library.photoslibrary path")
		jsonFlag := fs.Bool("json", false, "write JSON")
		formatFlag := fs.String("format", "", "output format")
		if err := fs.Parse(args[1:]); err != nil {
			return output.UsageError{Err: err}
		}
		if fs.NArg() != 0 {
			return output.UsageError{Err: errors.New("doctor takes flags only")}
		}
		if *dbPath != "" {
			paths.Database = *dbPath
		}
		format, err := output.Resolve(*formatFlag, *jsonFlag)
		if err != nil {
			return err
		}
		result, err := archive.Doctor(ctx, paths, archive.DoctorOptions{LibraryPath: *libraryPath})
		if err != nil {
			return err
		}
		return writeDoctor(os.Stdout, format, result)
	case "sync":
		fs := flag.NewFlagSet("sync", flag.ContinueOnError)
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
		result, err := archive.Sync(ctx, paths, archive.SyncOptions{
			LibraryPath: *libraryPath,
			Provider:    photos.NewProvider(),
		})
		if err != nil {
			return err
		}
		return writeSync(os.Stdout, format, result)
	case "classify":
		fs := flag.NewFlagSet("classify", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		dbPath := fs.String("db", "", "photos.sqlite path")
		all := fs.Bool("all", false, "classify all pending assets")
		limit := fs.Int("limit", 100, "max pending assets to classify")
		model := fs.String("model", "", "Ollama-API vision model for content observations; local or cloud")
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
		result, err := archive.Classify(ctx, paths, archive.ClassifyOptions{
			All:         *all,
			Limit:       *limit,
			Model:       *model,
			ModelURL:    ollamaCloudBaseURL,
			ModelKeyEnv: ollamaAPIKeyEnv,
		})
		if err != nil {
			return err
		}
		return output.Write(os.Stdout, format, "classify", result)
	case "search":
		parsed, err := parseSearchCommand(args[1:])
		if err != nil {
			return err
		}
		if parsed.DBPath != "" {
			paths.Database = parsed.DBPath
		}
		result, err := archive.Search(ctx, paths, archive.SearchOptions{Query: parsed.Query, Limit: parsed.Limit, After: parsed.After, Before: parsed.Before})
		if err != nil {
			return err
		}
		return writeSearch(os.Stdout, parsed.Format, result)
	case "open":
		parsed, err := parseRefCommand("open", args[1:], false)
		if err != nil {
			return err
		}
		if parsed.DBPath != "" {
			paths.Database = parsed.DBPath
		}
		result, err := archive.Open(ctx, paths, parsed.Ref)
		if err != nil {
			return err
		}
		return writeOpen(os.Stdout, parsed.Format, result)
	case "neighbors":
		parsed, err := parseRefCommand("neighbors", args[1:], true)
		if err != nil {
			return err
		}
		if parsed.DBPath != "" {
			paths.Database = parsed.DBPath
		}
		result, err := archive.Neighbors(ctx, paths, archive.NeighborOptions{ID: parsed.Ref, Limit: parsed.Limit})
		if err != nil {
			return err
		}
		return writeNeighbors(os.Stdout, parsed.Format, result)
	default:
		return usage()
	}
}

func usage() error {
	return output.UsageError{Err: errors.New("usage: photoscrawl <metadata|status|doctor|sync|classify|search|open|neighbors>")}
}

func joinedQuery(flagValue string, args []string) string {
	parts := append([]string{strings.TrimSpace(flagValue)}, args...)
	return strings.TrimSpace(strings.Join(parts, " "))
}
