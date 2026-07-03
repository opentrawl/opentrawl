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
	"github.com/openclaw/photoscrawl/internal/evalcard"
	"github.com/openclaw/photoscrawl/internal/photos"
	"github.com/openclaw/photoscrawl/internal/place"
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
	case "place-context":
		fs := flag.NewFlagSet("place-context", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		inputPath := fs.String("input", "-", "JSON place input path, or stdin")
		radius := fs.Float64("radius", 150, "nearby POI search radius in meters")
		jsonFlag := fs.Bool("json", false, "write JSON")
		formatFlag := fs.String("format", "", "output format")
		if err := fs.Parse(args[1:]); err != nil {
			return output.UsageError{Err: err}
		}
		format, err := output.Resolve(*formatFlag, *jsonFlag)
		if err != nil {
			return err
		}
		result, err := place.Run(ctx, place.Options{
			InputPath:    *inputPath,
			RadiusMeters: *radius,
			CacheDir:     paths.PlaceContextCacheDir(),
		})
		if err != nil {
			return err
		}
		return output.Write(os.Stdout, format, "place_context", result)
	case "place-card":
		fs := flag.NewFlagSet("place-card", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		inputPath := fs.String("input", "-", "JSON place-context result path, or stdin")
		if err := fs.Parse(args[1:]); err != nil {
			return output.UsageError{Err: err}
		}
		result, err := place.LoadResult(*inputPath)
		if err != nil {
			return err
		}
		fmt.Fprintln(os.Stdout, place.RenderCard(result))
		return nil
	case "place-backfill":
		fs := flag.NewFlagSet("place-backfill", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		dbPath := fs.String("db", "", "photos.sqlite path")
		outDir := fs.String("out", "", "private place backfill output directory")
		jsonFlag := fs.Bool("json", false, "write JSON")
		formatFlag := fs.String("format", "", "output format")
		if err := fs.Parse(args[1:]); err != nil {
			return output.UsageError{Err: err}
		}
		if *dbPath != "" {
			paths.Database = *dbPath
		}
		if *outDir == "" {
			*outDir = paths.PlaceBackfillDir()
		}
		format, err := output.Resolve(*formatFlag, *jsonFlag)
		if err != nil {
			return err
		}
		result, err := place.Backfill(ctx, place.BackfillOptions{
			DatabasePath: paths.Database,
			OutputDir:    *outDir,
		})
		if err != nil {
			return err
		}
		return output.Write(os.Stdout, format, "place_backfill", result)
	case "eval-card":
		fs := flag.NewFlagSet("eval-card", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		libraryPath := fs.String("library", "", "Photos Library.photoslibrary path")
		outDir := fs.String("out", "", "private eval output directory")
		cacheDir := fs.String("cache-dir", "", "private original cache directory")
		promptPath := fs.String("prompt", "", "photo-card prompt file")
		models := fs.String("models", "", "comma-separated Ollama models")
		ollamaURL := fs.String("ollama-url", "", "Ollama generate URL or base URL")
		allowICloud := fs.Bool("allow-icloud-downloads", false, "allow PhotoKit to download missing originals")
		limit := fs.Int("limit", 15, "max images to prepare")
		concurrency := fs.Int("concurrency", 4, "max concurrent model calls")
		sample := fs.String("sample", "latest", "sample mode: latest or random")
		seed := fs.Uint64("seed", 1, "random sample seed")
		jsonFlag := fs.Bool("json", false, "write JSON")
		formatFlag := fs.String("format", "", "output format")
		if err := fs.Parse(args[1:]); err != nil {
			return output.UsageError{Err: err}
		}
		format, err := output.Resolve(*formatFlag, *jsonFlag)
		if err != nil {
			return err
		}
		result, err := evalcard.Run(ctx, evalcard.Options{
			LibraryPath:          *libraryPath,
			OutputDir:            *outDir,
			CacheDir:             *cacheDir,
			DefaultOutputRoot:    paths.EvalRootDir(),
			DefaultCacheDir:      paths.OriginalsCacheDir(),
			PromptPath:           *promptPath,
			Models:               splitList(*models),
			OllamaGenerateURL:    *ollamaURL,
			OllamaAPIKeyEnv:      "OLLAMA_API_KEY",
			Limit:                *limit,
			Concurrency:          *concurrency,
			Sample:               *sample,
			Seed:                 *seed,
			AllowICloudDownloads: *allowICloud,
			Provider:             photos.NewProvider(),
		})
		if err != nil {
			return err
		}
		return output.Write(os.Stdout, format, "eval_card", result)
	default:
		return usage()
	}
}

func usage() error {
	return output.UsageError{Err: errors.New("usage: photoscrawl <metadata|status|doctor|sync|classify|search|open|neighbors|place-context|place-card|place-backfill|eval-card>")}
}

func splitList(value string) []string {
	out := []string{}
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func joinedQuery(flagValue string, args []string) string {
	parts := append([]string{strings.TrimSpace(flagValue)}, args...)
	return strings.TrimSpace(strings.Join(parts, " "))
}
